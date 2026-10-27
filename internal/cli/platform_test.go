package cli

import (
	"os"
	"strings"
	"testing"
)

// TestAppAddWithoutPlatform covers a cluster that init cluster never installed the platform into:
// nothing would ever deploy the app, so the add fails before it reads the artifact, writes a
// secret or touches Cloudflare.
func TestAppAddWithoutPlatform(t *testing.T) {
	t.Parallel()
	h := cloudflareHarness(t)
	h.app = appWithSecrets("greeter", "token")
	h.cluster.noPlatform = true

	_, stderr, code := h.run(t, "app", "add", "greeter", greeterArtifact,
		"--cloudflare", "home", "--domain", "example.com")
	if code == 0 || !strings.Contains(stderr, "no shelf platform") || !strings.Contains(stderr, "shelf init cluster") {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	if len(h.cluster.added) != 0 {
		t.Error("the app was registered")
	}
	if h.fetched != "" {
		t.Errorf("the artifact %s was read", h.fetched)
	}
	if n := len(h.api.created) + len(h.api.deleted) + len(h.api.records) + len(h.api.deletedRecords); n != 0 {
		t.Errorf("%d changes in Cloudflare", n)
	}
	if _, err := os.Stat(h.backup.Path("greeter")); !os.IsNotExist(err) {
		t.Errorf("secret backup: %v", err)
	}
}

func TestAppRedeployWithoutPlatform(t *testing.T) {
	t.Parallel()
	h := cloudflareHarness(t)
	h.mustRun(t, "app", "add", "greeter", greeterArtifact)
	h.cluster.noPlatform = true

	_, stderr, code := h.run(t, "app", "redeploy", "greeter")
	if code == 0 || !strings.Contains(stderr, "no shelf platform") {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	if len(h.cluster.redeployed) != 0 {
		t.Error("the redeploy reached the cluster")
	}
}

// TestQuickTunnelOnOldPlatform covers a platform older than quick tunnels: only an app that asks
// for one is refused, before anything changes, and the hint names a flag that exists.
func TestQuickTunnelOnOldPlatform(t *testing.T) {
	t.Parallel()
	h := cloudflareHarness(t)
	h.mustRun(t, "app", "add", "greeter", greeterArtifact, "--cloudflare", "home", "--domain", "example.com")
	h.cluster.oldPlatform = true
	added, created, records := len(h.cluster.added), len(h.api.created), len(h.api.records)

	_, stderr, code := h.run(t, "app", "add", "notes", greeterArtifact, "--quick")
	if code == 0 || !strings.Contains(stderr, "does not run quick tunnels yet") ||
		!strings.Contains(stderr, "without --quick") || strings.Contains(stderr, "--private") {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}

	// Switching an app with a named tunnel leaves the tunnel and its record alone.
	_, stderr, code = h.run(t, "app", "credentials", "greeter", "--quick")
	if code == 0 || !strings.Contains(stderr, "does not run quick tunnels yet") {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	if len(h.cluster.added) != added || len(h.api.created) != created || len(h.api.records) != records ||
		len(h.api.deleted)+len(h.api.deletedRecords) != 0 {
		t.Error("the refused changes reached the cluster or Cloudflare")
	}

	// An app without a quick tunnel is deployed as before.
	h.mustRun(t, "app", "add", "notes", greeterArtifact)
}
