package ops_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/ops"
)

// fakeTraefik answers like Traefik in front of an app: it reports the Host and headers it got,
// redirects /login to the app's own host, and switches /ws to an echo protocol.
func fakeTraefik(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.Redirect(w, r, "http://hello-dev.example.com/login/form?next=%2F", http.StatusFound)
		case "/elsewhere":
			http.Redirect(w, r, "https://accounts.example.org/", http.StatusFound)
		case "/ws":
			conn, buf, err := http.NewResponseController(w).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			fmt.Fprint(buf, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n")
			_ = buf.Flush()
			line, _ := buf.ReadString('\n')
			fmt.Fprint(buf, "echo: "+line)
			_ = buf.Flush()
		default:
			fmt.Fprintf(w, "host=%s path=%s fwd-host=%s fwd-proto=%s", r.Host, r.URL.Path,
				r.Header.Get("X-Forwarded-Host"), r.Header.Get("X-Forwarded-Proto"))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// forwards hands out forwards to Traefik and counts them. Each one is a relay on a port of its
// own that end closes with every connection through it, as a broken port-forward does.
type forwards struct {
	traefik string
	mu      sync.Mutex
	made    int
	relay   net.Listener
	conns   []net.Conn
	done    chan struct{}
	broke   bool
}

func (f *forwards) open(context.Context) (*cluster.Forward, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.broke {
		return nil, errors.New("no Traefik pod is ready")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	f.made++
	f.relay, f.done = ln, make(chan struct{})
	go func() {
		for {
			in, err := ln.Accept()
			if err != nil {
				return
			}
			out, err := net.Dial("tcp", f.traefik)
			if err != nil {
				in.Close()
				return
			}
			f.mu.Lock()
			f.conns = append(f.conns, in, out)
			f.mu.Unlock()
			go func() { _, _ = io.Copy(out, in); out.Close() }()
			go func() { _, _ = io.Copy(in, out); in.Close() }()
		}
	}()
	return &cluster.Forward{Addr: ln.Addr().String(), Done: f.done}, nil
}

func (f *forwards) end() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.relay.Close()
	for _, c := range f.conns {
		c.Close()
	}
	f.conns = nil
	close(f.done)
}

func proxy(t *testing.T, f *forwards) (*httptest.Server, string) {
	t.Helper()
	p := ops.NewAppProxy("hello-dev.example.com", f.open)
	// The local server stands for the listener shelf app open binds; its address is the one
	// the browser sees.
	local := httptest.NewUnstartedServer(nil)
	local.Config.Handler = p.Handler(local.Listener.Addr().String())
	local.Start()
	t.Cleanup(local.Close)
	t.Cleanup(p.Close)
	return local, local.Listener.Addr().String()
}

func get(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func TestAppProxySetsTheAppsHost(t *testing.T) {
	t.Parallel()
	f := &forwards{traefik: fakeTraefik(t).Listener.Addr().String()}
	local, addr := proxy(t, f)
	_, body := get(t, local.URL+"/api/x")
	want := "host=hello-dev.example.com path=/api/x fwd-host=" + addr + " fwd-proto=http"
	if body != want {
		t.Errorf("Traefik got %q, want %q", body, want)
	}
}

func TestAppProxyRewritesRedirects(t *testing.T) {
	t.Parallel()
	f := &forwards{traefik: fakeTraefik(t).Listener.Addr().String()}
	local, addr := proxy(t, f)
	resp, _ := get(t, local.URL+"/login")
	if got, want := resp.Header.Get("Location"), "http://"+addr+"/login/form?next=%2F"; got != want {
		t.Errorf("Location %q, want %q", got, want)
	}
	resp, _ = get(t, local.URL+"/elsewhere")
	if got := resp.Header.Get("Location"); got != "https://accounts.example.org/" {
		t.Errorf("a redirect to another host was changed to %q", got)
	}
}

func TestAppProxyPassesUpgrades(t *testing.T) {
	t.Parallel()
	f := &forwards{traefik: fakeTraefik(t).Listener.Addr().String()}
	_, addr := proxy(t, f)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprint(conn, "GET /ws HTTP/1.1\r\nHost: "+addr+"\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n")
	r := bufio.NewReader(conn)
	resp, err := http.ReadResponse(r, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %v %v", resp, err)
	}
	fmt.Fprint(conn, "hello\n")
	if line, _ := r.ReadString('\n'); line != "echo: hello\n" {
		t.Errorf("after the upgrade: %q", line)
	}
}

func TestAppProxyReconnects(t *testing.T) {
	t.Parallel()
	f := &forwards{traefik: fakeTraefik(t).Listener.Addr().String()}
	local, _ := proxy(t, f)
	get(t, local.URL+"/")
	f.end()
	if _, body := get(t, local.URL+"/again"); !strings.Contains(body, "path=/again") {
		t.Errorf("after the forward ended: %q", body)
	}
	f.mu.Lock()
	made := f.made
	f.mu.Unlock()
	if made != 2 {
		t.Errorf("opened %d forwards, want 2", made)
	}

	f.end()
	f.mu.Lock()
	f.broke = true
	f.mu.Unlock()
	resp, body := get(t, local.URL+"/")
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(body, "no Traefik pod is ready") {
		t.Errorf("without a forward: %d %q", resp.StatusCode, body)
	}
}
