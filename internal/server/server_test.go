package server_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/hostcfg"
	"github.com/tweinmann/shelf/internal/ops"
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
	listErr   error
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
	settings := cluster.Settings{Domain: "example.com", HostSuffix: "-dev", TunnelTarget: "t-1.cfargotunnel.com"}
	return &fakePlatform{
		status: ops.Status{
			Context: "k3d-shelf-dev", Server: "https://127.0.0.1:6445",
			Reachable: true, Installed: true, Settings: settings,
			Hosts: "<app>-dev.example.com", Exposed: true,
		},
		apps: []ops.App{
			{
				AppState: cluster.AppState{
					Name:     "greeter",
					Artifact: cluster.Artifact{URL: "oci://ghcr.io/tweinmann/greeter", Tag: "main"},
					Revision: "main@" + digest,
					Deployed: now.Add(-17 * time.Minute),
					Phase:    cluster.PhaseReady,
				},
				Host: "greeter-dev.example.com",
				URL:  "https://greeter-dev.example.com/",
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
				},
				Host: "shop-dev.example.com",
				URL:  "https://shop-dev.example.com/",
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

// testServer builds a server with its own directory and a fixed clock.
func testServer(t *testing.T, platform server.Platform) (*server.Server, http.Handler) {
	t.Helper()
	store, err := hostcfg.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.New(server.Options{
		Platform: platform,
		Store:    store,
		Version:  "v0.3.0",
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv, srv.Handler()
}

const password = "a-good-password"

// claimed returns a server that has an owner, and a cookie that is logged in.
func claimed(t *testing.T, platform server.Platform) (http.Handler, *http.Cookie) {
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
	h, cookie := claimed(t, runningPlatform())

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
	for _, want := range []string{`"greeter"`, `"Failed"`, `"exposed": true`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body lacks %s:\n%s", want, rec.Body)
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
