package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tweinmann/shelf/internal/cluster"
)

// openHarness is a cluster with the app hello and a Traefik that reports the Host it got.
func openHarness(t *testing.T) *harness {
	t.Helper()
	traefik := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "host=%s path=%s", r.Host, r.URL.Path)
	}))
	t.Cleanup(traefik.Close)
	h := newHarness(t).public()
	quick := helloState(cluster.PhaseReady)
	quick.Quick, quick.QuickURL = true, "https://random-words.trycloudflare.com"
	h.cluster.states = []cluster.AppState{quick}
	h.cluster.traefik = traefik.Listener.Addr().String()
	return h
}

func TestAppOpen(t *testing.T) {
	t.Parallel()
	h := openHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var opened, body string
	h.browser = func(url string) error {
		// The browser is opened once the address answers; what it sees is checked, then the
		// command is stopped as Ctrl-C stops it.
		defer cancel()
		opened = url
		resp, err := http.Get(url + "api/x")
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		body = string(b)
		return nil
	}
	stdout, stderr, code := h.runContext(t, ctx, "", "app", "open", "hello", "--browser")
	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr)
	}
	if !strings.HasPrefix(opened, "http://127.0.0.1:") || !strings.Contains(stdout, "hello (hello.shelf.internal) is at "+opened) {
		t.Errorf("opened %q, stdout %q", opened, stdout)
	}
	// The app's own host, not the quick tunnel's name: that is what its Ingress answers to.
	if body != "host=hello.shelf.internal path=/api/x" {
		t.Errorf("Traefik got %q", body)
	}
	if len(h.cluster.added)+len(h.cluster.removed)+len(h.cluster.redeployed) != 0 {
		t.Errorf("opening changed the app")
	}
}

func TestAppOpenWithoutBrowser(t *testing.T) {
	t.Parallel()
	h := openHarness(t)
	called := false
	h.browser = func(string) error { called = true; return nil }
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	stdout, stderr, code := h.runContext(t, ctx, "", "app", "open", "hello")
	if code != 0 || called {
		t.Errorf("exit code %d, browser started %v: %s", code, called, stderr)
	}
	if !strings.Contains(stdout, "is at http://127.0.0.1:") {
		t.Errorf("stdout %q", stdout)
	}
}

func TestAppOpenPort(t *testing.T) {
	t.Parallel()
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port
	_, stderr, code := openHarness(t).run(t, "app", "open", "hello", "--port", fmt.Sprint(port))
	if code != 1 || !strings.Contains(stderr, fmt.Sprintf("cannot listen on 127.0.0.1:%d", port)) {
		t.Errorf("port in use: exit code %d, stderr %q", code, stderr)
	}

	h := openHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	free, _ := net.Listen("tcp", "127.0.0.1:0")
	port = free.Addr().(*net.TCPAddr).Port
	free.Close()
	stdout, _, _ := h.runContext(t, ctx, "", "app", "open", "hello", "--port", fmt.Sprint(port))
	if !strings.Contains(stdout, fmt.Sprintf("http://127.0.0.1:%d/", port)) {
		t.Errorf("chosen port: %q", stdout)
	}
}

func TestAppOpenRefusals(t *testing.T) {
	t.Parallel()
	_, stderr, code := openHarness(t).run(t, "app", "open", "nope")
	if code != 1 || !strings.Contains(stderr, "app nope does not exist") {
		t.Errorf("unknown app: exit code %d, stderr %q", code, stderr)
	}
	h := openHarness(t)
	h.cluster.traefik = ""
	if _, stderr, code := h.run(t, "app", "open", "hello"); code != 1 || !strings.Contains(stderr, "no Traefik pod is ready") {
		t.Errorf("no Traefik: exit code %d, stderr %q", code, stderr)
	}
	if _, stderr, code := openHarness(t).run(t, "app", "open", "hello", "--address", "0.0.0.0"); code != 1 ||
		!strings.Contains(stderr, "unknown flag") {
		t.Errorf("another address: exit code %d, stderr %q", code, stderr)
	}
}
