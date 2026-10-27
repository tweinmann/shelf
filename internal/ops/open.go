package ops

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"

	"github.com/tweinmann/shelf/internal/cluster"
)

// AppProxy carries requests from this machine to one app through a port-forward to Traefik. It
// sets the Host header to the app's own host, so Traefik routes them as it routes a tunnel's:
// the same routes, the same stripPrefix. Nothing about the app changes; it is not an exposure.
type AppProxy struct {
	// Host is the app's own host, <app><suffix>.<domain> or <app>.shelf.internal.
	Host string

	forward func(context.Context) (*cluster.Forward, error)
	mu      sync.Mutex
	current *cluster.Forward
	// replaced is called when a new forward takes over, to drop connections to the old one.
	replaced func()
}

// OpenApp checks that the app exists and opens the first forward, so that a cluster that
// cannot forward fails before anything listens.
func (o *Ops) OpenApp(ctx context.Context, name string) (*AppProxy, error) {
	settings, err := o.Cluster.Settings(ctx, o.config())
	if err != nil {
		return nil, err
	}
	states, err := o.Cluster.AppStates(ctx, o.config())
	if err != nil {
		return nil, err
	}
	for _, state := range states {
		if state.Name != name {
			continue
		}
		// The app's own host, never a quick tunnel's random name: that is the name cloudflared
		// sets too, and the one the app's Ingress answers to.
		p := &AppProxy{
			Host:    AppHost(name, settings.HostSuffix, state.Domain),
			forward: func(ctx context.Context) (*cluster.Forward, error) { return o.Cluster.ForwardTraefik(ctx, o.config()) },
		}
		if _, err := p.addr(ctx); err != nil {
			return nil, err
		}
		return p, nil
	}
	return nil, &NotFoundError{Name: name}
}

// NewAppProxy is an AppProxy over forwards made by forward, for tests.
func NewAppProxy(host string, forward func(context.Context) (*cluster.Forward, error)) *AppProxy {
	return &AppProxy{Host: host, forward: forward}
}

// Close ends the current forward.
func (p *AppProxy) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.current != nil {
		p.current.Close()
		p.current = nil
	}
}

// addr is the local end of a working forward, opening a new one when the last one ended, as it
// does when the Traefik pod is replaced.
func (p *AppProxy) addr(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.current != nil {
		select {
		case <-p.current.Done:
			p.current = nil
		default:
			return p.current.Addr, nil
		}
	}
	f, err := p.forward(ctx)
	if err != nil {
		return "", err
	}
	p.current = f
	if p.replaced != nil {
		p.replaced()
	}
	return f.Addr, nil
}

// Handler serves the app to a browser that reaches it at local, e.g. 127.0.0.1:8081.
func (p *AppProxy) Handler(local string) http.Handler {
	dialer := &net.Dialer{}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		addr, err := p.addr(ctx)
		if err != nil {
			return nil, err
		}
		return dialer.DialContext(ctx, network, addr)
	}
	p.mu.Lock()
	p.replaced = transport.CloseIdleConnections
	p.mu.Unlock()
	return &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme, r.Out.URL.Host = "http", p.Host
			r.Out.Host = p.Host
			// X-Forwarded-Host is where the browser is, so an app that builds links from it
			// sends the browser back here.
			r.SetXForwarded()
		},
		ModifyResponse: func(resp *http.Response) error {
			location := resp.Header.Get("Location")
			if location == "" {
				return nil
			}
			u, err := url.Parse(location)
			if err != nil || u.Hostname() != p.Host {
				return nil
			}
			u.Scheme, u.Host = "http", local
			resp.Header.Set("Location", u.String())
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, fmt.Sprintf("shelf cannot reach the cluster's Traefik: %v", err), http.StatusBadGateway)
		},
	}
}
