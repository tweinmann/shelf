package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tweinmann/shelf/internal/github"
)

// fakeGitHub answers like the GitHub packages API of the user "o".
type fakeGitHub struct {
	// token is the token the last client was made with.
	token string
	err   error
}

func (f *fakeGitHub) Packages(context.Context) ([]github.Package, error) {
	return []github.Package{
		{Name: "shop", Owner: "O", Updated: time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)},
		{Name: "greeter/web", Owner: "O"},
		{Name: "greeter", Owner: "O", Updated: time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)},
	}, f.err
}

func (f *fakeGitHub) Tags(_ context.Context, pkg string) ([]github.Tag, error) {
	if pkg != "greeter" {
		return nil, errors.New("Not Found (HTTP 404)")
	}
	day := func(d int) time.Time { return time.Date(2026, 9, d, 8, 0, 0, 0, time.UTC) }
	return []github.Tag{
		{Name: "main", Created: day(3)}, {Name: "sha-333", Created: day(3)},
		{Name: "sha256-abc.sig", Created: day(3)}, {Name: "sha-111", Created: day(1)},
	}, f.err
}

func TestConnectionPackages(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.defineRegistry(t, "ghcr", "o", "ghcr-secret-token")

	stdout := h.mustRun(t, "connection", "packages", "ghcr")
	want := "greeter  oci://ghcr.io/o/greeter  2026-09-01 08:00\n" +
		"shop     oci://ghcr.io/o/shop     2026-09-02 08:00\n"
	if stdout != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", stdout, want)
	}
	if h.gh.token != "ghcr-secret-token" {
		t.Errorf("the list was read with %q, not the connection's token", h.gh.token)
	}

	if _, stderr, code := h.run(t, "connection", "packages", "missing"); code == 0 ||
		!strings.Contains(stderr, "there is no registry connection missing") {
		t.Errorf("missing connection: exit code %d, stderr %q", code, stderr)
	}
	h.gh.err = errors.New("Bad credentials (HTTP 401)")
	if _, stderr, code := h.run(t, "connection", "packages", "ghcr"); code == 0 || !strings.Contains(stderr, "Bad credentials") {
		t.Errorf("refused token: exit code %d, stderr %q", code, stderr)
	}
}

func TestConnectionZones(t *testing.T) {
	t.Parallel()
	h := cloudflareHarness(t)
	if stdout := h.mustRun(t, "connection", "zones", "tobile"); stdout != "example.com\nshop.ch\n" {
		t.Errorf("stdout %q", stdout)
	}
	if _, stderr, code := h.run(t, "connection", "zones", "other"); code == 0 ||
		!strings.Contains(stderr, "no Cloudflare connection other") {
		t.Errorf("missing connection: exit code %d, stderr %q", code, stderr)
	}
}

func TestAppTags(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.app = appWithSecrets("greeter")
	h.defineRegistry(t, "ghcr", "o", "ghcr-secret-token")
	h.defineRegistry(t, "other", "someone", "ghcr-secret-token")
	h.mustRun(t, "app", "add", "greeter", greeterArtifact, "--registry", "ghcr")
	h.mustRun(t, "app", "add", "plain", greeterArtifact)
	h.mustRun(t, "app", "add", "foreign", greeterArtifact, "--registry", "other")

	stdout := h.mustRun(t, "app", "tags", "greeter")
	want := "* main     2026-09-03 08:00\n" +
		"  sha-333  2026-09-03 08:00\n" +
		"  sha-111  2026-09-01 08:00\n"
	if stdout != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", stdout, want)
	}

	if _, stderr, code := h.run(t, "app", "tags", "plain"); code == 0 ||
		!strings.Contains(stderr, "pulls without a registry connection") {
		t.Errorf("app without a connection: exit code %d, stderr %q", code, stderr)
	}
	if _, stderr, code := h.run(t, "app", "tags", "foreign"); code == 0 ||
		!strings.Contains(stderr, "belongs to o, and the connection lists the packages of someone only") {
		t.Errorf("artifact of another user: exit code %d, stderr %q", code, stderr)
	}
	if _, stderr, code := h.run(t, "app", "tags", "nothing"); code == 0 || !strings.Contains(stderr, "app nothing does not exist") {
		t.Errorf("missing app: exit code %d, stderr %q", code, stderr)
	}
}
