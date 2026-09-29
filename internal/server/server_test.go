package server_test

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/hostcfg"
	"github.com/tweinmann/shelf/internal/ops"
	"github.com/tweinmann/shelf/internal/progress"
	"github.com/tweinmann/shelf/internal/server"
	"github.com/tweinmann/shelf/internal/testutil"
)

// now is the clock every test renders against, so that "17 minutes ago" is the same tomorrow.
var now = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

const digest = "sha256:00677bbefb02ca2056a9eb0741e12735e38067d6f155938034a4ccb612450db7"

// fakePlatform answers like a cluster that is not there.
type fakePlatform struct {
	status    ops.Status
	apps      []ops.App
	diagnoses map[string]cluster.Diagnosis
	secrets   map[string]string
	listErr   error

	mu sync.Mutex
	// calls records the changes that were asked for, in order.
	calls []string
	// block holds a change until it is closed, so a test can look at one that is running.
	block chan struct{}
	// fail is the error every change returns.
	fail error
	// access is the access the last change gave an app.
	access ops.Access
	// connections are what the connections page lists; saved records what was saved.
	connections ops.Connections
	saved       []string
}

func (f *fakePlatform) Connections(context.Context) (ops.Connections, error) {
	return f.connections, nil
}

// Zones, Packages and Tags answer for the connections "cf" and "ghcr"; any other name is one
// whose list cannot be read.
func (f *fakePlatform) Zones(_ context.Context, name string) ([]string, error) {
	if name != "cf" {
		return nil, errors.New("this machine holds no Cloudflare connection " + name)
	}
	return []string{"example.com", "shop.ch"}, nil
}

func (f *fakePlatform) Packages(_ context.Context, name string) ([]ops.Package, error) {
	if name != "ghcr" {
		return nil, errors.New("Bad credentials (HTTP 401)")
	}
	return []ops.Package{{Name: "greeter", Artifact: "oci://ghcr.io/owner/greeter", Updated: now}}, nil
}

func (f *fakePlatform) Tags(_ context.Context, name, artifact string) ([]ops.Tag, error) {
	if name != "ghcr" || artifact != "oci://ghcr.io/owner/greeter" {
		return nil, errors.New("no tags for " + artifact)
	}
	return []ops.Tag{{Name: "main", Created: now}, {Name: "sha-905acce", Created: now.Add(-time.Hour)}}, nil
}

func (f *fakePlatform) SaveRegistryConnection(_ context.Context, name string, auth cluster.RegistryAuth,
	rep progress.Reporter) error {
	f.mu.Lock()
	f.saved = append(f.saved, "registry "+name+" "+auth.Username+" "+auth.Token)
	f.mu.Unlock()
	return f.change("save registry "+name, rep)
}

func (f *fakePlatform) SaveCloudflareConnection(_ context.Context, conn hostcfg.Cloudflare, rep progress.Reporter) error {
	f.mu.Lock()
	f.saved = append(f.saved, "cloudflare "+conn.Name+" "+conn.Token+" "+conn.Account)
	f.mu.Unlock()
	return f.change("save cloudflare "+conn.Name, rep)
}

func (f *fakePlatform) RemoveConnection(_ context.Context, kind, name string, rep progress.Reporter) error {
	return f.change("remove "+kind+" "+name, rep)
}

func (f *fakePlatform) savedConnections() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.saved...)
}

func (f *fakePlatform) Secrets(_ context.Context, _ string) (map[string]string, error) {
	return f.secrets, nil
}

// change records a call, reports two lines, and waits if the test wants it to.
func (f *fakePlatform) change(call string, rep progress.Reporter) error {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	block := f.block
	f.mu.Unlock()
	rep.Report(progress.Applied("ResourceSetInputProvider shelf-system/greeter", "created", ""))
	rep.Report(progress.Step("the app"))
	if block != nil {
		<-block
	}
	if f.fail != nil {
		rep.Report(progress.StepFailed("the app"))
		return f.fail
	}
	rep.Report(progress.StepDone("the app", 12*time.Second, "Helm install succeeded"))
	return nil
}

func (f *fakePlatform) made() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakePlatform) AddApp(_ context.Context, o ops.AddOptions, rep progress.Reporter) error {
	f.mu.Lock()
	f.access = o.Access
	f.mu.Unlock()
	return f.change("add "+o.Name+" "+o.Artifact.String(), rep)
}

func (f *fakePlatform) SetAccess(_ context.Context, name string, access ops.Access, _ time.Duration,
	rep progress.Reporter) error {
	f.mu.Lock()
	f.access = access
	f.mu.Unlock()
	return f.change("access "+name, rep)
}

func (f *fakePlatform) lastAccess() ops.Access {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.access
}

func (f *fakePlatform) RemoveApp(_ context.Context, name string, _ time.Duration, rep progress.Reporter) error {
	return f.change("remove "+name, rep)
}

func (f *fakePlatform) Redeploy(_ context.Context, name string, _ time.Duration, rep progress.Reporter) error {
	return f.change("redeploy "+name, rep)
}

func (f *fakePlatform) Status(context.Context) ops.Status { return f.status }

func (f *fakePlatform) Apps(context.Context) ([]ops.App, error) {
	return f.apps, f.listErr
}

func (f *fakePlatform) App(_ context.Context, name string) (ops.App, error) {
	if f.listErr != nil {
		return ops.App{}, f.listErr
	}
	for _, a := range f.apps {
		if a.Name == name {
			return a, nil
		}
	}
	return ops.App{}, &ops.NotFoundError{Name: name}
}

func (f *fakePlatform) Diagnose(_ context.Context, name string) (cluster.Diagnosis, error) {
	d, ok := f.diagnoses[name]
	if !ok {
		return cluster.Diagnosis{}, &ops.NotFoundError{Name: name}
	}
	return d, nil
}

// runningPlatform is a healthy cluster with one working and one broken app.
func runningPlatform() *fakePlatform {
	settings := cluster.Settings{Domain: "example.com", HostSuffix: "-dev"}
	return &fakePlatform{
		status: ops.Status{
			Context: "k3d-shelf-dev", Server: "https://127.0.0.1:6445",
			Reachable: true, Installed: true, Settings: settings,
			Hosts: "<app>-dev.example.com", Public: true,
		},
		apps: []ops.App{
			{
				AppState: cluster.AppState{
					Name:     "greeter",
					Artifact: cluster.Artifact{URL: "oci://ghcr.io/tweinmann/greeter", Tag: "main"},
					Revision: "main@" + digest,
					Deployed: now.Add(-17 * time.Minute),
					Phase:    cluster.PhaseReady,
					Tunnel:   "t-1", Registry: "ghcr", Cloudflare: "tobile",
				},
				Host:              "greeter-dev.example.com",
				Public:            true,
				URL:               "https://greeter-dev.example.com/",
				CloudflareAccount: "acc-1",
				Components: []ops.Component{
					{
						Component: cluster.Component{Name: "check", Phase: cluster.PhaseWorking},
						Internal:  true,
					},
					{
						Component: cluster.Component{Name: "db", Phase: cluster.PhaseReady,
							Ports: []cluster.Port{{Name: "main", Number: 5432}}},
						Address:  "db:5432",
						Internal: true,
					},
					{
						Component: cluster.Component{Name: "web", Phase: cluster.PhaseReady,
							Ports: []cluster.Port{{Name: "main", Number: 80}}, Path: "/"},
						Address: "greeter-dev.example.com/",
						URL:     "https://greeter-dev.example.com/",
					},
				},
			},
			{
				AppState: cluster.AppState{
					Name:     "shop",
					Artifact: cluster.Artifact{URL: "oci://ghcr.io/tweinmann/shop", Tag: "no-such-tag"},
					Revision: "main@" + digest,
					Deployed: now.Add(-3 * time.Hour),
					Phase:    cluster.PhaseFailed,
					Stage:    "the deploy artifact",
					Reason:   "MANIFEST_UNKNOWN: manifest unknown",
					Domain:   "shop.ch",
					Tunnel:   "t-2", Cloudflare: "club",
				},
				Host:              "shop-dev.shop.ch",
				Public:            true,
				CloudflareMissing: true,
				URL:               "https://shop-dev.shop.ch/",
				Components: []ops.Component{
					{
						Component: cluster.Component{Name: "web", Phase: cluster.PhaseReady,
							Ports: []cluster.Port{{Name: "http", Number: 8080}}, Path: "/"},
						Address: "shop-dev.shop.ch/",
						URL:     "https://shop-dev.shop.ch/",
					},
				},
			},
		},
		connections: ops.Connections{
			Registry: []ops.RegistryConnection{
				{RegistryConnection: cluster.RegistryConnection{Name: "ghcr", Username: "tobi"}, Apps: []string{"greeter"}},
				{RegistryConnection: cluster.RegistryConnection{Name: "spare", Username: "tobi"}},
			},
			Cloudflare: []ops.CloudflareConnection{
				{Name: "club", Apps: []string{"shop"}, Missing: true},
				{Name: "tobile", Account: "acc-1", Apps: []string{"greeter"}},
			},
		},
		diagnoses: map[string]cluster.Diagnosis{
			"greeter": {Name: "greeter", Stages: []cluster.Stage{
				{Name: "the deploy artifact", Object: "OCIRepository greeter/deploy", Phase: cluster.PhaseReady,
					Reason: "Succeeded", Message: "stored artifact for digest 'main@" + digest + "'"},
				{Name: "the app values", Object: "Kustomization greeter/deploy", Phase: cluster.PhaseReady,
					Reason: "ReconciliationSucceeded", Message: "Applied revision: main@" + digest},
				{Name: "the chart", Object: "OCIRepository greeter/shelf-app", Phase: cluster.PhaseReady,
					Reason: "Succeeded", Message: "stored artifact for digest '0.1.0@sha256:8f5bfec4'"},
				{Name: "the app", Object: "HelmRelease greeter/greeter", Phase: cluster.PhaseReady,
					Reason: "InstallSucceeded", Message: "Helm install succeeded"},
				{Name: "the pods", Object: "Pods", Phase: cluster.PhaseReady, Message: "2 running"},
			}},
			"shop": {Name: "shop", Stages: []cluster.Stage{
				{Name: "the deploy artifact", Object: "OCIRepository shop/deploy", Phase: cluster.PhaseFailed,
					Reason: "OCIArtifactPullFailed", Message: "MANIFEST_UNKNOWN: manifest unknown",
					Hint: "shelf cannot read the deploy artifact from the registry"},
				{Name: "the app values", Object: "Kustomization shop/deploy", Phase: cluster.PhaseReady,
					Reason: "ReconciliationSucceeded", Message: "Applied revision: main@" + digest},
				{Name: "the chart", Object: "OCIRepository shop/shelf-app", Phase: cluster.PhaseReady,
					Reason: "Succeeded", Message: "stored artifact for digest '0.1.0@sha256:8f5bfec4'"},
				{Name: "the app", Object: "HelmRelease shop/shop", Phase: cluster.PhaseReady,
					Reason: "InstallSucceeded", Message: "Helm install succeeded"},
				{Name: "the pods", Object: "Pods", Phase: cluster.PhaseReady, Message: "1 running"},
			}},
		},
	}
}

// quickPlatform adds two apps with quick tunnels to runningPlatform: one whose cloudflared
// reported its address, and one whose tunnel has none yet.
func quickPlatform() *fakePlatform {
	p := runningPlatform()
	p.apps = append(p.apps,
		ops.App{
			AppState: cluster.AppState{
				Name:     "notes",
				Artifact: cluster.Artifact{URL: "oci://ghcr.io/tweinmann/notes", Tag: "main"},
				Revision: "main@" + digest,
				Deployed: now.Add(-5 * time.Minute),
				Phase:    cluster.PhaseReady,
				Quick:    true, QuickURL: "https://some-random-words.trycloudflare.com/",
			},
			Host:   "some-random-words.trycloudflare.com",
			Public: true,
			URL:    "https://some-random-words.trycloudflare.com/",
			Components: []ops.Component{
				{
					Component: cluster.Component{Name: "web", Phase: cluster.PhaseReady,
						Ports: []cluster.Port{{Name: "main", Number: 80}}, Path: "/"},
					Address: "some-random-words.trycloudflare.com/",
					URL:     "https://some-random-words.trycloudflare.com/",
				},
			},
		},
		ops.App{
			AppState: cluster.AppState{
				Name:     "draft",
				Artifact: cluster.Artifact{URL: "oci://ghcr.io/tweinmann/draft", Tag: "main"},
				Phase:    cluster.PhaseWorking,
				Quick:    true,
			},
			Host:   "draft-dev.example.com",
			Public: true,
		},
	)
	p.diagnoses["notes"] = p.diagnoses["greeter"]
	return p
}

// testServer builds a server with its own directory and a fixed clock.
func testServer(t *testing.T, platform server.Platform) (*server.Server, http.Handler) {
	t.Helper()
	store, err := hostcfg.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return serverWith(t, store, platform)
}

func serverWith(t *testing.T, store *hostcfg.Store, platform server.Platform) (*server.Server, http.Handler) {
	t.Helper()
	jobs := 0
	srv, err := server.New(server.Options{
		Platform: platform,
		Store:    store,
		Version:  "v0.3.0",
		Now:      func() time.Time { return now },
		NewID: func() string {
			jobs++
			return fmt.Sprintf("job-%d", jobs)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv, srv.Handler()
}

const password = "a-good-password"

// testSession is the login the tests below start with.
const testSession = "test-session"

// testHash is the password hash, computed once for the whole package. Hashing is expensive on
// purpose — 600 000 rounds — and a test that only needs to be logged in should not pay for it
// again. The claim and the login themselves are tested through the pages.
var testHash = sync.OnceValue(func() string {
	hash, err := hostcfg.HashPassword(password)
	if err != nil {
		panic(err)
	}
	return hash
})

// claimed returns a server that already has an owner and an open session.
func claimed(t *testing.T, platform server.Platform) (http.Handler, *http.Cookie) {
	t.Helper()
	store, err := hostcfg.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAdmin(hostcfg.Admin{
		Password: testHash(),
		Sessions: []hostcfg.Session{{ID: testSession, Created: now, Expires: now.Add(24 * time.Hour)}},
	}); err != nil {
		t.Fatal(err)
	}
	_, h := serverWith(t, store, platform)
	return h, &http.Cookie{Name: "shelf_session", Value: testSession}
}

// claimedThroughThePage goes the way a person goes: the setup code, then the password.
func claimedThroughThePage(t *testing.T, platform server.Platform) (http.Handler, *http.Cookie) {
	t.Helper()
	srv, h := testServer(t, platform)
	form := url.Values{"token": {srv.SetupToken()}, "password": {password}, "confirm": {password}}
	rec := post(h, "/setup", form, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("claim: %d\n%s", rec.Code, rec.Body)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("claim set %d cookies", len(cookies))
	}
	return h, cookies[0]
}

func get(h http.Handler, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func post(h http.Handler, path string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// sessionValue hides the token that changes on every run, so that a page can be compared.
var sessionValue = regexp.MustCompile(`name="csrf" value="[^"]*"`)

func golden(t *testing.T, name string, rec *httptest.ResponseRecorder) {
	t.Helper()
	body := sessionValue.ReplaceAll(rec.Body.Bytes(), []byte(`name="csrf" value="CSRF"`))
	testutil.Golden(t, "testdata/"+name+".html", body)
}

// TestPages renders every page of the admin UI. A page is a string, so the diff of a golden
// file is what a change to the interface looks like in review.
func TestPages(t *testing.T) {
	t.Parallel()
	running := runningPlatform()

	t.Run("dashboard", func(t *testing.T) {
		t.Parallel()
		h, cookie := claimed(t, running)
		rec := get(h, "/", cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		golden(t, "dashboard", rec)
	})

	t.Run("app", func(t *testing.T) {
		t.Parallel()
		h, cookie := claimed(t, running)
		golden(t, "app-ready", get(h, "/apps/greeter", cookie))
		golden(t, "app-failed", get(h, "/apps/shop", cookie))
	})

	t.Run("add form", func(t *testing.T) {
		t.Parallel()
		h, cookie := claimed(t, running)
		golden(t, "new", get(h, "/apps/new", cookie))
	})

	t.Run("quick tunnels", func(t *testing.T) {
		t.Parallel()
		h, cookie := claimed(t, quickPlatform())
		golden(t, "dashboard-quick", get(h, "/", cookie))
		golden(t, "app-quick", get(h, "/apps/notes", cookie))
	})

	t.Run("connections", func(t *testing.T) {
		t.Parallel()
		h, cookie := claimed(t, running)
		golden(t, "connections", get(h, "/connections", cookie))
	})

	// An app that was just added has no values in the cluster yet, so shelf cannot say what it is
	// made of. The page says so rather than claiming the app has no components.
	t.Run("components not known yet", func(t *testing.T) {
		t.Parallel()
		fresh := runningPlatform()
		fresh.apps[0].Components = nil
		h, cookie := claimed(t, fresh)
		body := get(h, "/apps/greeter", cookie).Body.String()
		if !strings.Contains(body, "does not know yet what this app is made of") {
			t.Errorf("the page does not say why the components are missing:\n%s", body)
		}
	})

	t.Run("cluster unreachable", func(t *testing.T) {
		t.Parallel()
		down := &fakePlatform{status: ops.Status{
			Context: "k3d-shelf-dev", Server: "https://127.0.0.1:6445",
			Error: "dial tcp 127.0.0.1:6445: connect: connection refused",
		}}
		h, cookie := claimed(t, down)
		rec := get(h, "/", cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("a cluster that is down is a page, not an error: %d", rec.Code)
		}
		golden(t, "dashboard-unreachable", rec)
	})

	// A development cluster answers under a reserved name such as dev.local. No record can ever
	// point there, so the page says that instead of offering a link that cannot work.
	t.Run("reserved domain", func(t *testing.T) {
		t.Parallel()
		local := &fakePlatform{
			status: ops.Status{
				Context: "k3d-shelf-dev", Server: "https://127.0.0.1:6445",
				Reachable: true, Installed: true,
				Settings: cluster.Settings{Domain: "dev.local"},
				Hosts:    "<app>.dev.local",
			},
			apps: []ops.App{{
				AppState: cluster.AppState{
					Name:     "greeter",
					Artifact: cluster.Artifact{URL: "oci://ghcr.io/tweinmann/greeter", Tag: "main"},
					Revision: "main@" + digest,
					Deployed: now.Add(-17 * time.Minute),
					Phase:    cluster.PhaseReady,
				},
				Host: "greeter.dev.local",
				Components: []ops.Component{{
					Component: cluster.Component{Name: "web", Phase: cluster.PhaseReady,
						Ports: []cluster.Port{{Name: "main", Number: 80}}, Path: "/"},
					Address: "greeter.dev.local/",
				}},
			}},
		}
		h, cookie := claimed(t, local)
		rec := get(h, "/", cookie)
		if strings.Contains(rec.Body.String(), `href="https://greeter.dev.local`) {
			t.Error("the page offers a link to a name that cannot exist")
		}
		if page := get(h, "/apps/greeter", cookie); strings.Contains(page.Body.String(), `href="https://greeter.dev.local`) {
			t.Error("a component offers a link to a name that cannot exist")
		}
		golden(t, "dashboard-reserved-domain", rec)
	})

	t.Run("platform not installed", func(t *testing.T) {
		t.Parallel()
		fresh := &fakePlatform{status: ops.Status{
			Context: "k3d-shelf-dev", Server: "https://127.0.0.1:6445", Reachable: true,
		}}
		h, cookie := claimed(t, fresh)
		golden(t, "dashboard-not-installed", get(h, "/", cookie))
	})

	t.Run("apps cannot be listed", func(t *testing.T) {
		t.Parallel()
		broken := runningPlatform()
		broken.listErr = errors.New("listing OCIRepository: the server could not find the requested resource")
		broken.apps = nil
		h, cookie := claimed(t, broken)
		golden(t, "dashboard-list-error", get(h, "/", cookie))
	})

	t.Run("setup", func(t *testing.T) {
		t.Parallel()
		_, h := testServer(t, running)
		golden(t, "setup", get(h, "/setup", nil))
	})

	t.Run("setup with a wrong code", func(t *testing.T) {
		t.Parallel()
		_, h := testServer(t, running)
		form := url.Values{"token": {"WRONG"}, "password": {password}, "confirm": {password}}
		rec := post(h, "/setup", form, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status %d", rec.Code)
		}
		golden(t, "setup-error", rec)
	})

	t.Run("login", func(t *testing.T) {
		t.Parallel()
		h, _ := claimed(t, running)
		golden(t, "login", get(h, "/login", nil))
	})

	t.Run("login with a wrong password", func(t *testing.T) {
		t.Parallel()
		h, _ := claimed(t, running)
		rec := post(h, "/login", url.Values{"password": {"nope"}}, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status %d", rec.Code)
		}
		golden(t, "login-error", rec)
	})

	t.Run("unknown app", func(t *testing.T) {
		t.Parallel()
		h, cookie := claimed(t, running)
		rec := get(h, "/apps/nope", cookie)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status %d", rec.Code)
		}
		golden(t, "error", rec)
	})
}

// TestUnauthenticated checks the rule the whole admin UI rests on: without a session, nothing
// but the way to get one.
func TestUnauthenticated(t *testing.T) {
	t.Parallel()
	h, _ := claimed(t, runningPlatform())

	for _, path := range []string{"/", "/apps/greeter"} {
		rec := get(h, path, nil)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
			t.Errorf("%s: %d %s, want a redirect to /login", path, rec.Code, rec.Header().Get("Location"))
		}
		if strings.Contains(rec.Body.String(), "greeter") {
			t.Errorf("%s leaked the app list", path)
		}
	}
	// An API request gets a status code; a redirect to an HTML page is useless to a script.
	if rec := get(h, "/api/status", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("/api/status: %d", rec.Code)
	}
	// The health check has to work without a session; a supervisor has no cookie.
	if rec := get(h, "/healthz", nil); rec.Code != http.StatusOK {
		t.Errorf("/healthz: %d", rec.Code)
	}
	// A session that is not one is refused, and the cookie is cleared.
	rec := get(h, "/", &http.Cookie{Name: "shelf_session", Value: "made-up"})
	if rec.Code != http.StatusSeeOther {
		t.Errorf("made-up session: %d", rec.Code)
	}
}

// TestUnclaimed checks that a fresh instance shows one page only, and that the page cannot be
// used without the code the installer printed.
func TestUnclaimed(t *testing.T) {
	t.Parallel()
	srv, h := testServer(t, runningPlatform())
	if srv.SetupToken() == "" {
		t.Fatal("a fresh instance has no setup token")
	}

	for _, path := range []string{"/", "/apps/greeter", "/login"} {
		rec := get(h, path, nil)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/setup" {
			t.Errorf("%s: %d %s, want a redirect to /setup", path, rec.Code, rec.Header().Get("Location"))
		}
	}

	tests := map[string]url.Values{
		"wrong code":            {"token": {"WRONG"}, "password": {password}, "confirm": {password}},
		"no code":               {"password": {password}, "confirm": {password}},
		"passwords differ":      {"token": {srv.SetupToken()}, "password": {password}, "confirm": {"other"}},
		"password is too short": {"token": {srv.SetupToken()}, "password": {"short"}, "confirm": {"short"}},
	}
	for name, form := range tests {
		t.Run(name, func(t *testing.T) {
			rec := post(h, "/setup", form, nil)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status %d, want 401", rec.Code)
			}
			if srv.SetupToken() == "" {
				t.Error("the instance was claimed anyway")
			}
		})
	}

	// The right code claims it, and the code is spent.
	form := url.Values{"token": {srv.SetupToken()}, "password": {password}, "confirm": {password}}
	if rec := post(h, "/setup", form, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("claim: %d", rec.Code)
	}
	if srv.SetupToken() != "" {
		t.Error("the setup token is still valid after the claim")
	}
	if rec := post(h, "/setup", form, nil); rec.Header().Get("Location") != "/login" {
		t.Errorf("a second claim: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

// TestLoginAndLogout covers the way in and the way out.
func TestLoginAndLogout(t *testing.T) {
	t.Parallel()
	h, cookie := claimedThroughThePage(t, runningPlatform())

	if rec := get(h, "/", cookie); rec.Code != http.StatusOK {
		t.Fatalf("the session from the claim does not work: %d", rec.Code)
	}

	// Logging out needs the form token of that session, or a page from another site could do it.
	if rec := post(h, "/logout", url.Values{"csrf": {"wrong"}}, cookie); rec.Code != http.StatusForbidden {
		t.Errorf("logout without the token: %d", rec.Code)
	}
	if rec := get(h, "/", cookie); rec.Code != http.StatusOK {
		t.Fatalf("the session ended anyway: %d", rec.Code)
	}

	token := csrfFrom(t, get(h, "/", cookie).Body.String())
	if rec := post(h, "/logout", url.Values{"csrf": {token}}, cookie); rec.Code != http.StatusSeeOther {
		t.Fatalf("logout: %d", rec.Code)
	}
	if rec := get(h, "/", cookie); rec.Code != http.StatusSeeOther {
		t.Error("the session still works after logging out")
	}

	// Logging in again with the password opens a new session.
	rec := post(h, "/login", url.Values{"password": {password}}, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login: %d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Value == "" {
		t.Fatalf("login set %+v", cookies)
	}
	if !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Errorf("the session cookie is not protected: %+v", cookies[0])
	}
	if rec := get(h, "/", cookies[0]); rec.Code != http.StatusOK {
		t.Errorf("the new session does not work: %d", rec.Code)
	}
}

// TestLoginBackoff checks that guessing the password gets slower.
func TestLoginBackoff(t *testing.T) {
	t.Parallel()
	h, _ := claimed(t, runningPlatform())
	for range 3 {
		post(h, "/login", url.Values{"password": {"nope"}}, nil)
	}
	// The clock stands still in tests, so the next attempt is refused whatever it carries.
	rec := post(h, "/login", url.Values{"password": {password}}, nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status %d, want 429", rec.Code)
	}
}

// TestSessionsSurviveARestart checks that an update of shelf does not log everyone out: the
// sessions are in the admin file, not in memory.
func TestSessionsSurviveARestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := hostcfg.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	options := server.Options{
		Platform: runningPlatform(), Store: store, Version: "v0.3.0",
		Now: func() time.Time { return now },
	}
	first, err := server.New(options)
	if err != nil {
		t.Fatal(err)
	}
	h := first.Handler()
	form := url.Values{"token": {first.SetupToken()}, "password": {password}, "confirm": {password}}
	cookies := post(h, "/setup", form, nil).Result().Cookies()

	restarted, err := server.New(options)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.SetupToken() != "" {
		t.Error("the restarted server offers to claim an instance that has an owner")
	}
	if rec := get(restarted.Handler(), "/", cookies[0]); rec.Code != http.StatusOK {
		t.Errorf("the session did not survive: %d", rec.Code)
	}
}

// TestAPIStatus checks the JSON the pages read.
func TestAPIStatus(t *testing.T) {
	t.Parallel()
	h, cookie := claimed(t, runningPlatform())
	rec := get(h, "/api/status", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("content type %q", got)
	}
	for _, want := range []string{`"greeter"`, `"Failed"`, `"tunnel": "t-1"`, `"registry": "ghcr"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body lacks %s:\n%s", want, rec.Body)
		}
	}
}

// TestChoicesAPI checks the lists a form fetches once a connection is chosen: the items, or the
// reason they cannot be read, and never without a session.
func TestChoicesAPI(t *testing.T) {
	t.Parallel()
	h, cookie := claimed(t, runningPlatform())
	tests := []struct {
		path string
		code int
		want string
	}{
		{"/api/connections/registry/ghcr/packages", http.StatusOK,
			`{"items":[{"name":"greeter","artifact":"oci://ghcr.io/owner/greeter","updated":"2026-09-20T12:00:00Z"}]}`},
		{"/api/connections/registry/other/packages", http.StatusBadGateway,
			`{"items":[],"error":"Bad credentials (HTTP 401)"}`},
		{"/api/connections/registry/ghcr/tags?artifact=" + url.QueryEscape("oci://ghcr.io/owner/greeter"), http.StatusOK,
			`{"items":[{"name":"main","created":"2026-09-20T12:00:00Z"},{"name":"sha-905acce","created":"2026-09-20T11:00:00Z"}]}`},
		{"/api/connections/registry/ghcr/tags", http.StatusBadGateway, `{"items":[],"error":"no tags for "}`},
		{"/api/connections/cloudflare/cf/zones", http.StatusOK, `{"items":["example.com","shop.ch"]}`},
		{"/api/connections/cloudflare/gone/zones", http.StatusBadGateway,
			`{"items":[],"error":"this machine holds no Cloudflare connection gone"}`},
	}
	for _, tt := range tests {
		rec := get(h, tt.path, cookie)
		if rec.Code != tt.code || strings.TrimSpace(rec.Body.String()) != tt.want {
			t.Errorf("%s: %d %s\nwant %d %s", tt.path, rec.Code, rec.Body, tt.code, tt.want)
		}
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Errorf("%s: content type %q", tt.path, got)
		}
		if rec := get(h, tt.path, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without a session: %d", tt.path, rec.Code)
		}
	}
}

// TestCrossOriginPost checks that a form on another site cannot act as the logged-in user.
func TestCrossOriginPost(t *testing.T) {
	t.Parallel()
	h, cookie := claimed(t, runningPlatform())
	r := httptest.NewRequest(http.MethodPost, "/logout", nil)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403", rec.Code)
	}
}

func csrfFrom(t *testing.T, body string) string {
	t.Helper()
	m := regexp.MustCompile(`name="csrf" value="([^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("the page has no form token")
	}
	return m[1]
}

// TestChangeApps walks the changes the admin UI can make: add an app, point it at another tag,
// deploy it again, and remove it. Each one starts a job and sends the browser to its page.
func TestChangeApps(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		path     string
		form     url.Values
		wantCall string
	}{
		{
			name: "add", path: "/apps",
			form:     url.Values{"name": {"greeter"}, "artifact": {"oci://ghcr.io/o/greeter:main"}},
			wantCall: "add greeter oci://ghcr.io/o/greeter:main",
		},
		{
			name: "deploy another tag", path: "/apps/greeter/deploy",
			form:     url.Values{"artifact": {"oci://ghcr.io/o/greeter:sha-905acce"}},
			wantCall: "add greeter oci://ghcr.io/o/greeter:sha-905acce",
		},
		{
			name: "deploy again", path: "/apps/greeter/redeploy",
			form: url.Values{}, wantCall: "redeploy greeter",
		},
		{
			name: "change the access", path: "/apps/greeter/access",
			form: url.Values{"domain": {"greeter.ch"}, "registry": {"ghcr"}}, wantCall: "access greeter",
		},
		{
			name: "remove", path: "/apps/greeter/delete",
			form: url.Values{"confirm": {"greeter"}}, wantCall: "remove greeter",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			platform := runningPlatform()
			h, cookie := claimed(t, platform)
			form := withCSRF(t, h, cookie, tt.form)

			rec := post(h, tt.path, form, cookie)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("status %d\n%s", rec.Code, rec.Body)
			}
			if got := rec.Header().Get("Location"); got != "/jobs/job-1" {
				t.Errorf("went to %q, want the job page", got)
			}
			waitForJob(t, h, cookie, "job-1")
			if calls := platform.made(); len(calls) != 1 || calls[0] != tt.wantCall {
				t.Errorf("calls %v, want %q", calls, tt.wantCall)
			}
		})
	}
}

// TestAccessForms checks that the connections chosen in a form reach the operation: a name
// chooses one, "none" takes it away.
func TestAccessForms(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		path string
		form url.Values
		want ops.Access
	}{
		{
			name: "add with connections", path: "/apps",
			form: url.Values{"name": {"greeter"}, "artifact": {"oci://ghcr.io/o/greeter:main"},
				"domain": {"greeter.ch"}, "registry": {"ghcr"}, "cloudflare": {"tobile"}},
			want: ops.Access{Domain: "greeter.ch", Registry: "ghcr", Cloudflare: "tobile"},
		},
		{
			name: "add without", path: "/apps",
			form: url.Values{"name": {"greeter"}, "artifact": {"oci://ghcr.io/o/greeter:main"},
				"registry": {""}, "cloudflare": {""}},
			want: ops.Access{RemoveRegistry: true, Quick: true},
		},
		{
			name: "add a private app", path: "/apps",
			form: url.Values{"name": {"greeter"}, "artifact": {"oci://ghcr.io/o/greeter:main"},
				"registry": {""}, "cloudflare": {"@private"}},
			want: ops.Access{RemoveRegistry: true, Private: true},
		},
		{
			name: "move an app to a quick tunnel", path: "/apps/greeter/access",
			form: url.Values{"registry": {"ghcr"}, "cloudflare": {""}},
			want: ops.Access{Registry: "ghcr", Quick: true},
		},
		{
			name: "take an app off the internet", path: "/apps/greeter/access",
			form: url.Values{"registry": {"ghcr"}, "cloudflare": {"@private"}},
			want: ops.Access{Registry: "ghcr", Private: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			platform := runningPlatform()
			h, cookie := claimed(t, platform)
			rec := post(h, tt.path, withCSRF(t, h, cookie, tt.form), cookie)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("status %d\n%s", rec.Code, rec.Body)
			}
			waitForJob(t, h, cookie, "job-1")
			if got := platform.lastAccess(); got != tt.want {
				t.Errorf("access %+v, want %+v", got, tt.want)
			}
		})
	}

	t.Run("an invalid domain", func(t *testing.T) {
		t.Parallel()
		platform := runningPlatform()
		h, cookie := claimed(t, platform)
		form := url.Values{"name": {"greeter"}, "artifact": {"oci://ghcr.io/o/greeter:main"},
			"domain": {"Not A Domain"}, "registry": {"ghcr"}}
		rec := post(h, "/apps", withCSRF(t, h, cookie, form), cookie)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "the domain must be a DNS name") {
			t.Fatalf("status %d\n%s", rec.Code, rec.Body)
		}
		if !strings.Contains(rec.Body.String(), `<option value="ghcr" selected>`) {
			t.Error("the refused form lost the chosen connection")
		}
		if calls := platform.made(); len(calls) != 0 {
			t.Errorf("something changed anyway: %v", calls)
		}
	})
}

// TestConnectionForms checks the only forms with token fields: the token reaches the operation,
// runs as a job, and never comes back, not even on a form that was refused.
func TestConnectionForms(t *testing.T) {
	t.Parallel()
	const token = "not-a-real-token"
	tests := []struct {
		name      string
		path      string
		form      url.Values
		wantCall  string
		wantSaved string
		wantErr   string
	}{
		{
			name: "a registry connection", path: "/connections/registry",
			form:     url.Values{"name": {"ghcr"}, "username": {"tobi"}, "token": {token}},
			wantCall: "save registry ghcr", wantSaved: "registry ghcr tobi " + token,
		},
		{
			name: "a Cloudflare connection", path: "/connections/cloudflare",
			form:     url.Values{"name": {"tobile"}, "token": {token}, "account": {"acc-1"}},
			wantCall: "save cloudflare tobile", wantSaved: "cloudflare tobile " + token + " acc-1",
		},
		{
			name: "remove", path: "/connections/cloudflare/tobile/delete",
			form: url.Values{}, wantCall: "remove cloudflare tobile",
		},
		{
			name: "a registry connection without a user", path: "/connections/registry",
			form:    url.Values{"name": {"ghcr"}, "username": {""}, "token": {token}},
			wantErr: "a registry connection needs a user name and a token",
		},
		{
			name: "an invalid name", path: "/connections/cloudflare",
			form:    url.Values{"name": {"To_bile"}, "token": {token}, "account": {"acc-1"}},
			wantErr: "must be a DNS label",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			platform := runningPlatform()
			h, cookie := claimed(t, platform)
			rec := post(h, tt.path, withCSRF(t, h, cookie, tt.form), cookie)
			if tt.wantErr != "" {
				body := rec.Body.String()
				if rec.Code != http.StatusBadRequest || !strings.Contains(body, tt.wantErr) {
					t.Fatalf("status %d, want 400 with %q\n%s", rec.Code, tt.wantErr, body)
				}
				if strings.Contains(body, token) {
					t.Errorf("the token was sent back:\n%s", body)
				}
				if calls := platform.made(); len(calls) != 0 {
					t.Errorf("something changed anyway: %v", calls)
				}
				return
			}
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("status %d\n%s", rec.Code, rec.Body)
			}
			waitForJob(t, h, cookie, "job-1")
			if calls := platform.made(); len(calls) != 1 || calls[0] != tt.wantCall {
				t.Errorf("calls %v, want %q", calls, tt.wantCall)
			}
			if tt.wantSaved != "" {
				if saved := platform.savedConnections(); len(saved) != 1 || saved[0] != tt.wantSaved {
					t.Errorf("saved %v, want %q", saved, tt.wantSaved)
				}
			}
			if job := get(h, "/jobs/job-1", cookie).Body.String(); strings.Contains(job, token) {
				t.Errorf("the token is on the job page:\n%s", job)
			}
		})
	}
}

// TestChangesNeedTheFormToken checks that a page from another site cannot change anything, even
// with the session cookie in hand.
func TestChangesNeedTheFormToken(t *testing.T) {
	t.Parallel()
	platform := runningPlatform()
	h, cookie := claimed(t, platform)
	paths := map[string]url.Values{
		"/apps":                             {"name": {"greeter"}, "artifact": {"oci://ghcr.io/o/greeter:main"}},
		"/apps/greeter/deploy":              {"artifact": {"oci://ghcr.io/o/greeter:main"}},
		"/apps/greeter/redeploy":            {},
		"/apps/greeter/access":              {"domain": {"greeter.ch"}},
		"/connections/registry":             {"name": {"ghcr"}, "username": {"tobi"}, "token": {"t"}},
		"/connections/cloudflare":           {"name": {"tobile"}, "token": {"t"}},
		"/connections/registry/ghcr/delete": {},
		"/apps/greeter/delete":              {"confirm": {"greeter"}},
		"/apps/greeter/secrets":             {"password": {password}},
	}
	for path, form := range paths {
		form.Set("csrf", "not-the-token")
		if rec := post(h, path, form, cookie); rec.Code != http.StatusForbidden {
			t.Errorf("%s: %d, want 403", path, rec.Code)
		}
	}
	if calls := platform.made(); len(calls) != 0 {
		t.Errorf("something changed anyway: %v", calls)
	}
}

// TestRemoveNeedsTheName checks the one action that destroys data.
func TestRemoveNeedsTheName(t *testing.T) {
	t.Parallel()
	platform := runningPlatform()
	h, cookie := claimed(t, platform)

	for _, confirm := range []string{"", "Greeter", "shop", " greeter"} {
		form := withCSRF(t, h, cookie, url.Values{"confirm": {confirm}})
		rec := post(h, "/apps/greeter/delete", form, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("confirm %q: status %d, want 400", confirm, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "type its name") {
			t.Errorf("confirm %q: the page does not say what to do:\n%s", confirm, rec.Body)
		}
	}
	if calls := platform.made(); len(calls) != 0 {
		t.Errorf("the app was removed anyway: %v", calls)
	}
}

// TestOneChangeAtATime checks that a second change is refused while one is running, and that
// the refusal links to the one that is in the way.
func TestOneChangeAtATime(t *testing.T) {
	t.Parallel()
	platform := runningPlatform()
	platform.block = make(chan struct{})
	h, cookie := claimed(t, platform)

	first := post(h, "/apps/greeter/redeploy", withCSRF(t, h, cookie, url.Values{}), cookie)
	if first.Code != http.StatusSeeOther {
		t.Fatalf("first change: %d", first.Code)
	}

	second := post(h, "/apps/shop/redeploy", withCSRF(t, h, cookie, url.Values{}), cookie)
	if second.Code != http.StatusConflict {
		t.Fatalf("second change: %d, want 409", second.Code)
	}
	if !strings.Contains(second.Body.String(), `href="/jobs/job-1"`) {
		t.Errorf("the refusal does not link to the running change:\n%s", second.Body)
	}
	golden(t, "busy", second)

	close(platform.block)
	waitForJob(t, h, cookie, "job-1")
	if calls := platform.made(); len(calls) != 1 {
		t.Errorf("calls %v; only the first change may run", calls)
	}

	// Once it is done, the next one is allowed.
	third := post(h, "/apps/shop/redeploy", withCSRF(t, h, cookie, url.Values{}), cookie)
	if third.Code != http.StatusSeeOther {
		t.Fatalf("third change: %d", third.Code)
	}
}

// TestJobPageSurvivesAReload is the reason the log lives in the job and not only in the stream.
func TestJobPageSurvivesAReload(t *testing.T) {
	t.Parallel()
	platform := runningPlatform()
	platform.block = make(chan struct{})
	h, cookie := claimed(t, platform)
	post(h, "/apps/greeter/redeploy", withCSRF(t, h, cookie, url.Values{}), cookie)

	waitForLog(t, h, cookie, "job-1", "waiting for the app ...")
	running := get(h, "/jobs/job-1", cookie)
	if running.Code != http.StatusOK {
		t.Fatalf("status %d", running.Code)
	}
	body := running.Body.String()
	for _, want := range []string{
		"ResourceSetInputProvider shelf-system/greeter: created", "waiting for the app ...", "running",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q:\n%s", want, body)
		}
	}
	golden(t, "job-running", running)

	close(platform.block)
	waitForJob(t, h, cookie, "job-1")
	done := get(h, "/jobs/job-1", cookie)
	if !strings.Contains(done.Body.String(), "done after 12s: Helm install succeeded") {
		t.Errorf("the finished page lacks the end of the log:\n%s", done.Body)
	}
	golden(t, "job-done", done)
}

// TestJobLogIsTheSameAsTheCommandLine checks the promise that the two do not drift: the log in
// the browser is the text the command line prints, event for event.
func TestJobLogIsTheSameAsTheCommandLine(t *testing.T) {
	t.Parallel()
	platform := runningPlatform()
	h, cookie := claimed(t, platform)
	post(h, "/apps/greeter/redeploy", withCSRF(t, h, cookie, url.Values{}), cookie)
	waitForJob(t, h, cookie, "job-1")

	var want strings.Builder
	writer := progress.Writer(&want)
	writer.Report(progress.Applied("ResourceSetInputProvider shelf-system/greeter", "created", ""))
	writer.Report(progress.Step("the app"))
	writer.Report(progress.StepDone("the app", 12*time.Second, "Helm install succeeded"))

	body := get(h, "/jobs/job-1", cookie).Body.String()
	if !strings.Contains(body, template.HTMLEscapeString(want.String())) {
		t.Errorf("the log is not what the command line prints.\nwant:\n%s\ngot:\n%s", want.String(), body)
	}
}

// TestJobEvents checks the stream the page attaches to: it replays what was missed and then
// follows, and it ends when the change ends.
func TestJobEvents(t *testing.T) {
	t.Parallel()
	platform := runningPlatform()
	platform.block = make(chan struct{})
	h, cookie := claimed(t, platform)
	post(h, "/apps/greeter/redeploy", withCSRF(t, h, cookie, url.Values{}), cookie)

	// Attach after the two events the page already rendered.
	waitForLog(t, h, cookie, "job-1", "waiting for the app ...")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/job-1/events?from=2", nil)
	req.AddCookie(cookie)
	streamed := make(chan struct{})
	go func() {
		h.ServeHTTP(rec, req)
		close(streamed)
	}()

	close(platform.block)
	select {
	case <-streamed:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end with the change")
	}

	body := rec.Body.String()
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("content type %q", got)
	}
	for _, want := range []string{
		"event: line", `"text":"done after 12s: Helm install succeeded\n"`, "event: end", "id: 2",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the stream lacks %q:\n%s", want, body)
		}
	}
	// The two events the page already had must not come again.
	if strings.Contains(body, "created") {
		t.Errorf("the stream repeats what the page already showed:\n%s", body)
	}
}

// TestFailedChange checks that a change that fails says so on its page.
func TestFailedChange(t *testing.T) {
	t.Parallel()
	platform := runningPlatform()
	platform.fail = errors.New("the app is not ready: no chart values")
	h, cookie := claimed(t, platform)
	post(h, "/apps/greeter/redeploy", withCSRF(t, h, cookie, url.Values{}), cookie)
	waitForJob(t, h, cookie, "job-1")

	body := get(h, "/jobs/job-1", cookie).Body.String()
	for _, want := range []string{"failed", "no chart values"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q:\n%s", want, body)
		}
	}
	golden(t, "job-failed", get(h, "/jobs/job-1", cookie))
}

// TestSecrets checks that the generated values are shown only after the password, and that
// asking for them is recorded.
func TestSecrets(t *testing.T) {
	t.Parallel()
	platform := runningPlatform()
	platform.secrets = map[string]string{"db-password": "NOT-A-REAL-SECRET"}
	h, cookie := claimed(t, platform)

	page := get(h, "/apps/greeter", cookie)
	if !strings.Contains(page.Body.String(), "db-password") {
		t.Error("the page does not list the secret names")
	}
	if strings.Contains(page.Body.String(), "NOT-A-REAL-SECRET") {
		t.Fatal("the page shows a secret value without asking for the password")
	}

	wrong := post(h, "/apps/greeter/secrets", withCSRF(t, h, cookie, url.Values{"password": {"nope"}}), cookie)
	if wrong.Code != http.StatusUnauthorized || strings.Contains(wrong.Body.String(), "NOT-A-REAL-SECRET") {
		t.Fatalf("a wrong password revealed the value: %d\n%s", wrong.Code, wrong.Body)
	}

	right := post(h, "/apps/greeter/secrets", withCSRF(t, h, cookie, url.Values{"password": {password}}), cookie)
	if right.Code != http.StatusOK || !strings.Contains(right.Body.String(), "NOT-A-REAL-SECRET") {
		t.Fatalf("the value was not shown: %d\n%s", right.Code, right.Body)
	}
}

// withCSRF adds the form token of the session to a form.
func withCSRF(t *testing.T, h http.Handler, cookie *http.Cookie, form url.Values) url.Values {
	t.Helper()
	form.Set("csrf", csrfFrom(t, get(h, "/", cookie).Body.String()))
	return form
}

// waitForLog waits until a change has reported a piece of text, so that a test can act on a
// job that is halfway through.
func waitForLog(t *testing.T, h http.Handler, cookie *http.Cookie, id, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(get(h, "/jobs/"+id, cookie).Body.String(), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s never reported %q", id, want)
}

// waitForJob waits until a change is no longer running.
func waitForJob(t *testing.T, h http.Handler, cookie *http.Cookie, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !strings.Contains(get(h, "/jobs/"+id, cookie).Body.String(), `id="job-state">running`) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s is still running", id)
}
