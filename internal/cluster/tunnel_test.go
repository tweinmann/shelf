package cluster

import (
	"os"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestParseQuickTunnel(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		body    string
		want    string
		wantErr bool
	}{
		"address":    {body: `{"hostname":"some-words.trycloudflare.com"}`, want: "https://some-words.trycloudflare.com/"},
		"none yet":   {body: `{"hostname":""}`},
		"not json":   {body: `404 page not found`, wantErr: true},
		"extra keys": {body: `{"hostname":"a.trycloudflare.com","other":1}`, want: "https://a.trycloudflare.com/"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := parseQuickTunnel([]byte(tt.body))
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("got %q, %v; want %q, error %t", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

// TestIsQuick tells the cloudflared of a quick tunnel from that of a named one, which is what
// the wait after a change from one to the other depends on.
func TestIsQuick(t *testing.T) {
	t.Parallel()
	deployment := func(args ...any) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
				"containers": []any{map[string]any{"name": "cloudflared", "args": args}},
			}}},
		}}
	}
	if !isQuick(deployment("tunnel", "--no-autoupdate", "--url", "http://traefik")) {
		t.Error("a quick tunnel is not recognised")
	}
	if isQuick(deployment("tunnel", "--config", "/etc/cloudflared/config/config.yaml", "run")) {
		t.Error("a named tunnel is taken for a quick one")
	}
}

// TestPlatformRunsQuickTunnels keeps checkQuickTunnels and the platform in step: the check looks
// for the input in the ResourceSet that ships with this shelf.
func TestPlatformRunsQuickTunnels(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../platform/apps/resourceset.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), quickInput) {
		t.Errorf("platform/apps/resourceset.yaml does not refer to %s", quickInput)
	}
}
