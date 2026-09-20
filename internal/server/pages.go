package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tweinmann/shelf/internal/hostcfg"
	"github.com/tweinmann/shelf/internal/ops"
)

// sessionKey carries the open session through the request context.
type ctxKey int

const sessionKey ctxKey = iota

func requestWithSession(r *http.Request, s hostcfg.Session) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), sessionKey, s))
}

func sessionOf(r *http.Request) (hostcfg.Session, bool) {
	s, ok := r.Context().Value(sessionKey).(hostcfg.Session)
	return s, ok
}

// dashboard shows the cluster and the apps in it.
func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r)
	defer cancel()

	view := dashboardView{base: s.base(r, "Apps")}
	view.Status = s.platform.Status(ctx)
	if view.Status.Reachable {
		apps, err := s.platform.Apps(ctx)
		if err != nil {
			view.Error = err.Error()
		}
		view.Apps = apps
	}
	s.render(w, "dashboard.html", http.StatusOK, view)
}

// appPage shows one app and why it is, or is not, running.
func (s *Server) appPage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r)
	defer cancel()
	name := r.PathValue("name")
	if err := ops.CheckAppName(name); err != nil {
		s.fail(w, r, http.StatusNotFound, err.Error())
		return
	}

	app, err := s.platform.App(ctx, name)
	var notFound *ops.NotFoundError
	switch {
	case errors.As(err, &notFound):
		s.fail(w, r, http.StatusNotFound, notFound.Error())
		return
	case err != nil:
		s.fail(w, r, http.StatusBadGateway, err.Error())
		return
	}
	view := appView{base: s.base(r, app.Name), App: app, Status: s.platform.Status(ctx)}
	diagnosis, err := s.platform.Diagnose(ctx, name)
	if err != nil {
		s.fail(w, r, http.StatusBadGateway, err.Error())
		return
	}
	view.Diagnosis = diagnosis
	view.Trouble = diagnosis.Trouble()
	s.render(w, "app.html", http.StatusOK, view)
}

// apiStatus is the snapshot the dashboard reads; it exists for the pages of the admin UI and
// is not a stable interface.
func (s *Server) apiStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r)
	defer cancel()
	body := struct {
		Status ops.Status `json:"status"`
		Apps   []ops.App  `json:"apps"`
		Error  string     `json:"error,omitempty"`
	}{Status: s.platform.Status(ctx)}
	if body.Status.Reachable {
		apps, err := s.platform.Apps(ctx)
		if err != nil {
			body.Error = err.Error()
		}
		body.Apps = apps
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(body)
}

// setupForm is the page a fresh instance shows, and the only one it shows.
func (s *Server) setupForm(w http.ResponseWriter, r *http.Request) {
	if s.admin.claimed() {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, "setup.html", http.StatusOK, setupView{base: s.base(r, "Set up shelf")})
}

// setup claims the instance with the printed code and the first password.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	token := r.PostFormValue("token")
	password := r.PostFormValue("password")
	view := setupView{base: s.base(r, "Set up shelf"), Token: token}

	switch err := s.claimAndLogIn(w, token, password, r.PostFormValue("confirm")); {
	case err == nil:
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	case errors.Is(err, errAlreadyClaimed):
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	default:
		view.Error = err.Error()
		s.render(w, "setup.html", http.StatusUnauthorized, view)
	}
}

func (s *Server) claimAndLogIn(w http.ResponseWriter, token, password, confirm string) error {
	if password != confirm {
		return errors.New("the two passwords are not the same")
	}
	if err := s.admin.claim(token, password); err != nil {
		return err
	}
	id, err := s.admin.newSession()
	if err != nil {
		return err
	}
	s.setSession(w, id)
	return nil
}

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	if !s.admin.claimed() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	s.render(w, "login.html", http.StatusOK, loginView{base: s.base(r, "Log in")})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.admin.claimed() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	addr := clientAddr(r)
	view := loginView{base: s.base(r, "Log in")}
	if !s.logins.allow(addr) {
		view.Error = "too many attempts; wait a moment and try again"
		s.render(w, "login.html", http.StatusTooManyRequests, view)
		return
	}
	if !s.admin.checkPassword(r.PostFormValue("password")) {
		s.logins.failed(addr)
		view.Error = "wrong password"
		s.render(w, "login.html", http.StatusUnauthorized, view)
		return
	}
	s.logins.succeeded(addr)
	id, err := s.admin.newSession()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	s.setSession(w, id)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if !checkCSRF(r) {
		s.fail(w, r, http.StatusForbidden, "this form is out of date; load the page again")
		return
	}
	if session, ok := sessionOf(r); ok {
		s.admin.endSession(session.ID)
	}
	s.clearSession(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
