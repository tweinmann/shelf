package server

import (
	"crypto/subtle"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/tweinmann/shelf/internal/hostcfg"
)

const (
	sessionCookie = "shelf_session"
	// sessionLife is how long a login lasts without being used. Every request that arrives
	// with a valid session pushes it back, so someone who uses the UI stays logged in.
	sessionLife = 30 * 24 * time.Hour
	// sessionRefresh is how much of the life has to be gone before the expiry is written
	// again, so that a page load does not mean a file write.
	sessionRefresh = 24 * time.Hour
)

// adminState is the admin file, kept in memory and written through on every change. Requests
// are served concurrently, so everything goes through the mutex.
type adminState struct {
	store *hostcfg.Store
	now   func() time.Time

	mu    sync.Mutex
	admin hostcfg.Admin
}

func newAdminState(store *hostcfg.Store, now func() time.Time) (*adminState, error) {
	admin, err := store.Admin()
	if err != nil {
		return nil, err
	}
	s := &adminState{store: store, now: now, admin: admin}
	// An instance that has never been claimed gets a token to claim it with. It is written
	// once and stays until it is used, so that a restart does not invalidate the token the
	// installer printed.
	if !admin.Claimed() && admin.SetupToken == "" {
		s.admin.SetupToken = hostcfg.NewToken()
		if err := store.SaveAdmin(s.admin); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (a *adminState) setupToken() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.admin.SetupToken
}

func (a *adminState) claimed() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.admin.Claimed()
}

// claim sets the first password. It checks the setup token, which is what keeps a stranger on
// the same network from taking the instance before its owner does.
func (a *adminState) claim(token, password string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.admin.Claimed() {
		return errAlreadyClaimed
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(a.admin.SetupToken)) != 1 {
		return errWrongToken
	}
	hash, err := hostcfg.HashPassword(password)
	if err != nil {
		return err
	}
	a.admin.Password = hash
	a.admin.SetupToken = ""
	return a.store.SaveAdmin(a.admin)
}

// checkPassword reports whether the password is the admin password.
func (a *adminState) checkPassword(password string) bool {
	a.mu.Lock()
	hash := a.admin.Password
	a.mu.Unlock()
	if hash == "" {
		return false
	}
	return hostcfg.CheckPassword(hash, password)
}

// newSession opens a login and returns its identifier.
func (a *adminState) newSession() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	id := hostcfg.NewToken()
	a.admin.Sessions = append(a.expired(now), hostcfg.Session{
		ID: id, Created: now, Expires: now.Add(sessionLife),
	})
	if err := a.store.SaveAdmin(a.admin); err != nil {
		return "", err
	}
	return id, nil
}

// session returns the open session with this identifier, and pushes its expiry back.
func (a *adminState) session(id string) (hostcfg.Session, bool) {
	if id == "" {
		return hostcfg.Session{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for i, s := range a.admin.Sessions {
		if subtle.ConstantTimeCompare([]byte(s.ID), []byte(id)) != 1 {
			continue
		}
		if !now.Before(s.Expires) {
			return hostcfg.Session{}, false
		}
		if s.Expires.Sub(now) < sessionLife-sessionRefresh {
			a.admin.Sessions[i].Expires = now.Add(sessionLife)
			// A failed write costs the sliding expiry, not the session.
			_ = a.store.SaveAdmin(a.admin)
		}
		return a.admin.Sessions[i], true
	}
	return hostcfg.Session{}, false
}

// endSession closes one login.
func (a *adminState) endSession(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.admin.Sessions = slices.DeleteFunc(a.expired(a.now()), func(s hostcfg.Session) bool {
		return s.ID == id
	})
	_ = a.store.SaveAdmin(a.admin)
}

// expired returns the sessions that are still valid; it is called with the mutex held.
func (a *adminState) expired(now time.Time) []hostcfg.Session {
	return slices.DeleteFunc(a.admin.Sessions, func(s hostcfg.Session) bool {
		return !now.Before(s.Expires)
	})
}

// backoff slows down password guessing from one address. It is deliberately simple: this is a
// machine on a home network with one password, not a public login.
type backoff struct {
	now func() time.Time

	mu    sync.Mutex
	state map[string]*attempt
}

type attempt struct {
	failures int
	next     time.Time
}

func newBackoff(now func() time.Time) *backoff {
	return &backoff{now: now, state: map[string]*attempt{}}
}

// maxDelay caps the wait, so that a locked-out owner is not locked out for good.
const maxDelay = 5 * time.Minute

// allow reports whether an address may try again now.
func (b *backoff) allow(addr string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	a := b.state[addr]
	return a == nil || !b.now().Before(a.next)
}

func (b *backoff) failed(addr string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	a := b.state[addr]
	if a == nil {
		a = &attempt{}
		b.state[addr] = a
	}
	a.failures++
	delay := time.Duration(1<<min(a.failures, 10)) * time.Second
	a.next = b.now().Add(min(delay, maxDelay))
}

func (b *backoff) succeeded(addr string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.state, addr)
}

// clientAddr is who is asking. There is no proxy in front of shelf, so the connection is the
// only thing to go by — a forwarded-for header would be free to invent.
func clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// guard lets a request through only with an open session. Before the instance is claimed it
// sends every request to the setup page instead, so a fresh server shows one thing only.
func (s *Server) guard(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.admin.claimed() {
			s.redirect(w, r, "/setup")
			return
		}
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			s.redirect(w, r, "/login")
			return
		}
		session, ok := s.admin.session(cookie.Value)
		if !ok {
			s.clearSession(w)
			s.redirect(w, r, "/login")
			return
		}
		h.ServeHTTP(w, requestWithSession(r, session))
	})
}

// redirect sends a browser to a page and answers an API request with a status code, because
// a redirect to an HTML login page is useless to a script.
func (s *Server) redirect(w http.ResponseWriter, r *http.Request, to string) {
	if isAPI(r) {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func isAPI(r *http.Request) bool {
	return len(r.URL.Path) >= len("/api/") && r.URL.Path[:len("/api/")] == "/api/"
}

func (s *Server) setSession(w http.ResponseWriter, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		MaxAge:   int(sessionLife.Seconds()),
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}
