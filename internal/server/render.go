package server

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/hostcfg"
	"github.com/tweinmann/shelf/internal/ops"
)

var (
	errAlreadyClaimed = errors.New("this instance is already set up; log in instead")
	errWrongToken     = errors.New("that is not the setup code this instance printed")
)

// pageNames are the templates that go with the layout, one file each.
var pageNames = []string{
	"dashboard.html", "app.html", "new.html", "connections.html", "job.html", "busy.html",
	"login.html", "setup.html", "error.html",
}

func parsePages(now func() time.Time) (map[string]*template.Template, error) {
	pages := map[string]*template.Template{}
	for _, name := range pageNames {
		t, err := template.New("layout.html").Funcs(funcs(now)).ParseFS(ui, "ui/layout.html", "ui/"+name)
		if err != nil {
			return nil, err
		}
		pages[name] = t
	}
	return pages, nil
}

func funcs(now func() time.Time) template.FuncMap {
	return template.FuncMap{
		"ago":      func(t time.Time) string { return ago(now(), t) },
		"short":    shortRevision,
		"hasError": func(p cluster.Phase) bool { return p == cluster.PhaseFailed },
	}
}

// ago says how long ago something happened, in the words a person would use.
func ago(now, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < 0:
		return "just now"
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute") + " ago"
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour") + " ago"
	default:
		return plural(int(d.Hours()/24), "day") + " ago"
	}
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// shortRevision keeps a revision readable: main@sha256:9f86d0818… instead of all 64 characters.
func shortRevision(revision string) string {
	name, digest, found := strings.Cut(revision, "@")
	if !found {
		return revision
	}
	algorithm, hex, found := strings.Cut(digest, ":")
	if !found || len(hex) <= 12 {
		return revision
	}
	return name + "@" + algorithm + ":" + hex[:12] + "…"
}

// base is what every page shows.
type base struct {
	Title string
	// Version is the shelf version in the footer.
	Version string
	// Authenticated decides whether the header offers a way out.
	Authenticated bool
	// CSRF is the token every form of a logged-in page carries.
	CSRF string
}

func (s *Server) base(r *http.Request, title string) base {
	b := base{Title: title, Version: s.version}
	if session, ok := sessionOf(r); ok {
		b.Authenticated = true
		b.CSRF = csrfToken(session)
	}
	return b
}

// render writes a page. It renders into a buffer first, so that a template error becomes an
// error page instead of half a page with a 200 on it.
func (s *Server) render(w http.ResponseWriter, page string, status int, data any) {
	t, ok := s.pages[page]
	if !ok {
		http.Error(w, "unknown page "+page, http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		http.Error(w, "rendering "+page+": "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// errorView is a page that explains a refusal.
type errorView struct {
	base
	Status  int
	Message string
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, status int, message string) {
	s.render(w, "error.html", status, errorView{
		base:    s.base(r, http.StatusText(status)),
		Status:  status,
		Message: message,
	})
}

// dashboardView is the app list with the state of the cluster above it.
type dashboardView struct {
	base
	Status ops.Status
	Apps   []ops.App
	// Error is why the apps could not be listed; the status above them is still shown.
	Error string
	// Running is the change that is going on, so that every page can link to it.
	Running *Job
}

// appView is one app with the chain that produced it.
type appView struct {
	base
	App       ops.App
	Status    ops.Status
	Diagnosis cluster.Diagnosis
	// Trouble is the first stage that is not ready, or nil.
	Trouble *cluster.Stage
	// SecretNames are the names of the app's generated secrets, always shown.
	SecretNames []string
	// Secrets are their values, only after the password was entered again.
	Secrets map[string]string
	// SecretError is why the values are not shown.
	SecretError string
	// Connections are what the access form offers.
	Connections ops.Connections
	Running     *Job
}

// newAppView is the form that registers an app. It offers the connections to choose from; the
// tokens behind them are defined on the connections page only.
type newAppView struct {
	base
	Status      ops.Status
	Connections ops.Connections
	Name        string
	Artifact    string
	Insecure    bool
	Domain      string
	Registry    string
	Cloudflare  string
	Error       string
}

// jobView is the page that watches one change.
type jobView struct {
	base
	Job *Job
	// Log is everything reported so far, as text.
	Log string
	// From is how many events the log above already holds; the browser asks for the rest.
	From int
}

// busyView explains that another change is running.
type busyView struct {
	base
	Running *Job
}

// loginView is the password page.
type loginView struct {
	base
	Error string
}

// setupView is the page that claims the instance.
type setupView struct {
	base
	Error string
	// Token keeps what was typed, so a wrong password does not mean typing the code again.
	Token string
}

// csrfToken derives a form token from the session. It is deterministic, so it needs no storage,
// and it is not the session identifier, so a page that ends up in a log does not carry a login.
func csrfToken(session hostcfg.Session) string {
	sum := sha256.Sum256([]byte("shelf-csrf:" + session.ID))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// checkCSRF compares the token in the form with the one of this session.
func checkCSRF(r *http.Request) bool {
	session, ok := sessionOf(r)
	if !ok {
		return false
	}
	got := r.PostFormValue("csrf")
	return subtle.ConstantTimeCompare([]byte(got), []byte(csrfToken(session))) == 1
}
