// Package server is the admin UI: the web interface that shows what the platform is doing and,
// from Phase 8 on, changes it. It talks to a Platform, never to a cluster directly, so its
// tests need neither network nor Kubernetes.
//
// The pages are rendered here with html/template rather than handed to a JavaScript
// application. A rendered page is a string, and a string can be compared with a golden file,
// which is how the rest of this repository tests what it produces.
package server

import (
	"context"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"time"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/hostcfg"
	"github.com/tweinmann/shelf/internal/ops"
	"github.com/tweinmann/shelf/internal/progress"
)

//go:embed ui
var ui embed.FS

// clusterTimeout bounds every read from the cluster, so that a page never hangs on an API
// server that does not answer. A dashboard is most useful exactly when something is broken.
const clusterTimeout = 8 * time.Second

// Platform is what the admin UI needs from shelf. *ops.Ops implements it; tests implement it
// with fixed answers. Everything below Diagnose changes the cluster and runs as a job.
type Platform interface {
	Status(ctx context.Context) ops.Status
	Apps(ctx context.Context) ([]ops.App, error)
	App(ctx context.Context, name string) (ops.App, error)
	Diagnose(ctx context.Context, name string) (cluster.Diagnosis, error)
	Secrets(ctx context.Context, name string) (map[string]string, error)

	AddApp(ctx context.Context, o ops.AddOptions, rep progress.Reporter) error
	RemoveApp(ctx context.Context, name string, timeout time.Duration, rep progress.Reporter) error
	Redeploy(ctx context.Context, name string, timeout time.Duration, rep progress.Reporter) error
}

// Options configure a server.
type Options struct {
	// Platform answers what the pages show.
	Platform Platform
	// Store holds the password and the sessions.
	Store *hostcfg.Store
	// Version is the shelf version, shown in the footer.
	Version string
	// Now is the clock. Nil means time.Now; tests set it so that "3 minutes ago" is stable.
	Now func() time.Time
	// Secure marks the session cookie as HTTPS-only. It is false on a LAN without TLS.
	Secure bool
	// NewID makes the identifier of a job. Nil means a random one; tests set it so that a
	// rendered page is the same every time.
	NewID func() string
}

// Server is the admin UI.
type Server struct {
	platform Platform
	store    *hostcfg.Store
	version  string
	now      func() time.Time
	secure   bool

	admin  *adminState
	logins *backoff
	jobs   *jobs
	pages  map[string]*template.Template
}

// New returns a server. It reads the admin state once; from then on the state in memory is
// authoritative and every change is written through.
func New(o Options) (*Server, error) {
	if o.Platform == nil {
		return nil, errors.New("no platform")
	}
	if o.Store == nil {
		return nil, errors.New("no shelf directory")
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	admin, err := newAdminState(o.Store, now)
	if err != nil {
		return nil, err
	}
	pages, err := parsePages(now)
	if err != nil {
		return nil, err
	}
	newID := o.NewID
	if newID == nil {
		newID = hostcfg.NewToken
	}
	return &Server{
		platform: o.Platform,
		store:    o.Store,
		version:  o.Version,
		now:      now,
		secure:   o.Secure,
		admin:    admin,
		logins:   newBackoff(now),
		jobs:     newJobs(now, newID),
		pages:    pages,
	}, nil
}

// SetupToken returns the token that claims this instance, or an empty string once it is
// claimed. The command that starts the server prints it.
func (s *Server) SetupToken() string { return s.admin.setupToken() }

// Handler returns the whole admin UI.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public: the two pages that lead to a session, the health check and the stylesheet the
	// login page needs to look like a page at all.
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.Handle("GET /static/", http.FileServerFS(staticFS()))
	mux.HandleFunc("GET /setup", s.setupForm)
	mux.HandleFunc("POST /setup", s.setup)
	mux.HandleFunc("GET /login", s.loginForm)
	mux.HandleFunc("POST /login", s.login)

	mux.Handle("POST /logout", s.guard(http.HandlerFunc(s.logout)))
	mux.Handle("GET /{$}", s.guard(http.HandlerFunc(s.dashboard)))
	mux.Handle("GET /apps/new", s.guard(http.HandlerFunc(s.newAppForm)))
	mux.Handle("POST /apps", s.guard(http.HandlerFunc(s.addApp)))
	mux.Handle("GET /apps/{name}", s.guard(http.HandlerFunc(s.appPage)))
	mux.Handle("POST /apps/{name}/deploy", s.guard(http.HandlerFunc(s.deployApp)))
	mux.Handle("POST /apps/{name}/redeploy", s.guard(http.HandlerFunc(s.redeployApp)))
	mux.Handle("POST /apps/{name}/delete", s.guard(http.HandlerFunc(s.deleteApp)))
	mux.Handle("POST /apps/{name}/secrets", s.guard(http.HandlerFunc(s.revealSecrets)))
	mux.Handle("GET /jobs/{id}", s.guard(http.HandlerFunc(s.jobPage)))
	mux.Handle("GET /api/jobs/{id}/events", s.guard(http.HandlerFunc(s.jobEvents)))
	mux.Handle("GET /api/status", s.guard(http.HandlerFunc(s.apiStatus)))

	// Cross-origin protection refuses a state-changing request that a different site sent, by
	// looking at Sec-Fetch-Site and Origin. It is the outermost layer, so it also covers a
	// route that is added later and forgets its own token check.
	return http.NewCrossOriginProtection().Handler(noStore(mux))
}

// staticFS serves the embedded assets at /static/.
func staticFS() fs.FS {
	sub, err := fs.Sub(ui, "ui")
	if err != nil {
		panic(err) // the directory is embedded above; it exists.
	}
	return sub
}

// noStore keeps pages out of the browser cache: they show the state of a cluster, and a page
// from the back button that claims an app is healthy would be a lie.
func noStore(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

// withTimeout bounds a cluster read.
func withTimeout(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), clusterTimeout)
}
