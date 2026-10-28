package cli

import (
	"slices"
	"strings"
	"testing"
)

// TestLANExceptions covers the operator's decision to let one app reach private addresses: it is
// kept until it is changed, replaced as a whole, taken away with --deny-lan, and refused for
// anything that is not a private range or would reach the cluster's pods.
func TestLANExceptions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.app = appWithSecrets("hello")
	h.mustRun(t, "app", "add", "hello", helloArtifact, "--insecure-registry")
	if added := h.lastAdded(t); len(added.LAN) != 0 {
		t.Errorf("a new app reaches %v, want no private range", added.LAN)
	}

	stdout := h.mustRun(t, "app", "credentials", "hello", "--allow-lan", "192.168.1.10/32")
	if added := h.lastAdded(t); !slices.Equal(added.LAN, []string{"192.168.1.10/32"}) {
		t.Errorf("LAN %v", added.LAN)
	}
	if !strings.Contains(stdout, "LAN: may reach 192.168.1.10/32; its previews never do") {
		t.Errorf("stdout:\n%s", stdout)
	}

	// A deploy without the flags keeps the exception, as it keeps the connections.
	h.mustRun(t, "app", "add", "hello", helloArtifact, "--insecure-registry")
	if added := h.lastAdded(t); !slices.Equal(added.LAN, []string{"192.168.1.10/32"}) {
		t.Errorf("a redeploy changed the LAN to %v", added.LAN)
	}

	// Given again, the list is replaced, in canonical form and without repeats.
	h.mustRun(t, "app", "credentials", "hello", "--allow-lan", "192.168.2.7/24", "--allow-lan", "192.168.2.0/24", "--allow-lan", "100.64.1.2/32")
	if added := h.lastAdded(t); !slices.Equal(added.LAN, []string{"192.168.2.0/24", "100.64.1.2/32"}) {
		t.Errorf("LAN %v", added.LAN)
	}

	stdout = h.mustRun(t, "app", "credentials", "hello", "--deny-lan")
	if added := h.lastAdded(t); len(added.LAN) != 0 || !strings.Contains(stdout, "LAN: none") {
		t.Errorf("LAN %v after --deny-lan; stdout:\n%s", added.LAN, stdout)
	}

	before := len(h.cluster.added)
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"--allow-lan", "8.8.8.0/24"}, "8.8.8.0/24 is not a private range"},
		{[]string{"--allow-lan", "nas.local"}, `"nas.local" is not a CIDR; a LAN exception must be a private CIDR`},
		{[]string{"--allow-lan", "10.42.0.0/16"}, "10.42.0.0/16 overlaps 10.42.0.0/24, where the cluster runs its pods; it would reach other apps"},
		{[]string{"--allow-lan", "192.168.1.0/24", "--deny-lan"}, "cannot be given and taken away at once"},
	} {
		_, stderr, code := h.run(t, append([]string{"app", "credentials", "hello"}, tt.args...)...)
		if code == 0 || !strings.Contains(stderr, tt.want) {
			t.Errorf("%v: exit %d, stderr %q, want %q", tt.args, code, stderr, tt.want)
		}
	}
	if len(h.cluster.added) != before {
		t.Error("a refused LAN exception changed the app")
	}
}
