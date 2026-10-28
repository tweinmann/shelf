package cli

import (
	"strings"
	"testing"

	"github.com/tweinmann/shelf/internal/cluster"
)

func TestAppStatusVerification(t *testing.T) {
	t.Parallel()
	unsigned := cluster.Verification{
		Reason: "the deploy artifact is not signed by the cluster's signer: no matching signatures were found",
		Source: "https://github.com/acme/hello", Commit: "42a964eb0b4bd7248305d5f22233d6dbabcd56f3",
	}
	tests := []struct {
		name   string
		v      *cluster.Verification
		want   []string
		absent []string
	}{
		{
			name: "verified",
			want: []string{
				"  source    https://github.com/acme/hello at 42a964eb0b4b\n",
				"  signer    keyless, issuer " + cluster.DefaultSignerIssuer,
				"  verified  yes\n",
				"  scan      trivy 0.75.0, 2026-10-28T12:00:00Z\n",
				"    web  1 critical, 26 high\n",
				"  exposure  allowed\n",
			},
		},
		{
			name: "unsigned",
			v:    &unsigned,
			want: []string{
				"  verified  no: the deploy artifact is not signed by the cluster's signer",
				"            push again with the current reusable workflow",
				"  exposure  refused until it is verified\n",
			},
			absent: []string{"  scan "},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t).public()
			h.cluster.states = []cluster.AppState{{Name: "hello", Phase: cluster.PhaseReady, Revision: "main@sha256:abc"}}
			if tt.v != nil {
				h.cluster.verifications = map[string]cluster.Verification{"hello": *tt.v}
			}
			stdout, stderr, code := h.run(t, "app", "status", "hello")
			if code != 0 {
				t.Fatalf("exit code %d: %s", code, stderr)
			}
			if tt.v != nil {
				goldenJSON(t, "appstatus-unverified", runJSON(t, h, 0, "app", "status", "hello"))
			}
			for _, want := range tt.want {
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout lacks %q:\n%s", want, stdout)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(stdout, absent) {
					t.Errorf("stdout has %q:\n%s", absent, stdout)
				}
			}
		})
	}
}

// TestExposureGate: only a verified app gets a tunnel it does not have yet, and the refusal
// comes before anything is asked of Cloudflare. The app itself is deployed either way.
func TestExposureGate(t *testing.T) {
	t.Parallel()
	noScan := cluster.Verification{Reason: "the deploy artifact has no scan (dev.shelf.scan)"}
	unsigned := cluster.Verification{Reason: "the deploy artifact is not signed by the cluster's signer: no matching signatures were found"}

	t.Run("verified app gets its tunnel after the rollout", func(t *testing.T) {
		t.Parallel()
		h := cloudflareHarness(t)
		stdout := h.mustRun(t, "app", "add", "greeter", greeterArtifact, "--cloudflare", "home", "--domain", "example.com")
		if len(h.cluster.added) != 2 || h.cluster.added[0].Cloudflare != "" || h.cluster.added[0].TunnelID != "" {
			t.Fatalf("the app was not deployed privately first: %+v", h.cluster.added)
		}
		if last := h.lastAdded(t); last.Cloudflare != "home" || last.TunnelID == "" || len(h.api.created) != 1 {
			t.Errorf("the verified app got no tunnel: %+v, created %v", last, h.api.created)
		}
		if !strings.Contains(stdout, "exposure: once greeter runs a verified deploy artifact") ||
			!strings.Contains(stdout, "verified: signed by the cluster's signer, every image scanned") {
			t.Errorf("stdout:\n%s", stdout)
		}
	})

	t.Run("new app without a scan stays private", func(t *testing.T) {
		t.Parallel()
		h := cloudflareHarness(t)
		h.cluster.verifications = map[string]cluster.Verification{"greeter": noScan}
		_, stderr, code := h.run(t, "app", "add", "greeter", greeterArtifact, "--cloudflare", "home", "--domain", "example.com")
		if code != 1 || !strings.Contains(stderr, "greeter is not exposed: it does not run a verified deploy artifact: the deploy artifact has no scan") ||
			!strings.Contains(stderr, "stays inside the cluster") {
			t.Fatalf("exit code %d, stderr:\n%s", code, stderr)
		}
		if len(h.cluster.added) != 1 || h.lastAdded(t).Cloudflare != "" {
			t.Errorf("added %+v", h.cluster.added)
		}
		if len(h.api.created) != 0 || len(h.api.zoneFor) != 0 || len(h.api.records) != 0 {
			t.Errorf("Cloudflare was asked: created %v, zones %v, records %v", h.api.created, h.api.zoneFor, h.api.records)
		}
	})

	t.Run("quick tunnel refused for an unsigned app", func(t *testing.T) {
		t.Parallel()
		h := cloudflareHarness(t)
		h.mustRun(t, "app", "add", "greeter", greeterArtifact)
		h.cluster.verifications = map[string]cluster.Verification{"greeter": unsigned}
		_, stderr, code := h.run(t, "app", "credentials", "greeter", "--quick")
		if code != 1 || !strings.Contains(stderr, "not signed by the cluster's signer") || !strings.Contains(stderr, "id-token: write") {
			t.Fatalf("exit code %d, stderr:\n%s", code, stderr)
		}
		if h.lastAdded(t).Quick {
			t.Error("the unsigned app got a quick tunnel")
		}
	})

	t.Run("an exposed app keeps its tunnel", func(t *testing.T) {
		t.Parallel()
		h := cloudflareHarness(t)
		h.mustRun(t, "app", "add", "greeter", greeterArtifact, "--quick")
		h.cluster.verifications = map[string]cluster.Verification{"greeter": noScan}
		h.mustRun(t, "app", "add", "greeter", greeterArtifact)
		if !h.lastAdded(t).Quick {
			t.Error("re-adding an exposed app took its quick tunnel away")
		}
	})
}
