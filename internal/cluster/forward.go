package cluster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

var (
	serviceGVK       = schema.GroupVersionKind{Version: "v1", Kind: "Service"}
	endpointSliceGVK = schema.GroupVersionKind{Group: "discovery.k8s.io", Version: "v1", Kind: "EndpointSlice"}
)

const (
	// TraefikNamespace and traefikService are where the platform runs Traefik.
	TraefikNamespace = "traefik"
	traefikService   = "traefik"
	// traefikPort is the Service port every tunnel's cloudflared talks to.
	traefikPort = 80
)

// Forward is a port-forward through the API server to a Traefik pod, listening on a loopback
// port of this machine. It ends when the pod goes away or the connection breaks; Done says so.
type Forward struct {
	// Addr is the local end, 127.0.0.1:<port>.
	Addr string
	Done <-chan struct{}
	stop chan struct{}
}

// Close ends the forward.
func (f *Forward) Close() {
	if f.stop == nil {
		return
	}
	select {
	case <-f.stop:
	default:
		close(f.stop)
	}
}

// ForwardTraefik opens a port-forward to a ready Traefik pod, the same entry point a tunnel
// reaches through the Service, on a free loopback port. It talks to the API server only.
func ForwardTraefik(ctx context.Context, cfg *rest.Config) (*Forward, error) {
	c, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	svc, err := c.get(ctx, ref{gvk: serviceGVK, namespace: TraefikNamespace, name: traefikService})
	if err != nil {
		return nil, err
	}
	if svc == nil {
		return nil, errors.New("the cluster has no Traefik service; run `shelf init cluster`")
	}
	slices, err := c.list(ctx, endpointSliceGVK, TraefikNamespace, "kubernetes.io/service-name="+traefikService)
	if err != nil {
		return nil, err
	}
	pod, port, err := traefikTarget(svc, slices)
	if err != nil {
		return nil, err
	}
	dialer, err := portForwardDialer(cfg, TraefikNamespace, pod)
	if err != nil {
		return nil, err
	}
	stop, ready := make(chan struct{}), make(chan struct{})
	fw, err := portforward.NewOnAddresses(dialer, []string{"127.0.0.1"}, []string{"0:" + strconv.Itoa(port)},
		stop, ready, io.Discard, io.Discard)
	if err != nil {
		return nil, err
	}
	done := make(chan struct{})
	failed := make(chan error, 1)
	go func() {
		defer close(done)
		failed <- fw.ForwardPorts()
	}()
	f := &Forward{Done: done, stop: stop}
	select {
	case <-ready:
	case err := <-failed:
		return nil, fmt.Errorf("forwarding to pod %s/%s: %w", TraefikNamespace, pod, err)
	case <-ctx.Done():
		f.Close()
		return nil, ctx.Err()
	}
	ports, err := fw.GetPorts()
	if err != nil || len(ports) == 0 {
		f.Close()
		return nil, fmt.Errorf("forwarding to pod %s/%s: no local port", TraefikNamespace, pod)
	}
	f.Addr = "127.0.0.1:" + strconv.Itoa(int(ports[0].Local))
	return f, nil
}

// traefikTarget picks a ready pod behind the Traefik Service and the container port its port
// 80 leads to, as the Service itself would.
func traefikTarget(svc *unstructured.Unstructured, slices []*unstructured.Unstructured) (string, int, error) {
	ports, _, _ := unstructured.NestedSlice(svc.Object, "spec", "ports")
	name, found := "", false
	for _, item := range ports {
		p, _ := item.(map[string]any)
		if number, _ := p["port"].(int64); number == traefikPort {
			name, _ = p["name"].(string)
			found = true
		}
	}
	if !found {
		return "", 0, fmt.Errorf("the Traefik service has no port %d", traefikPort)
	}
	for _, slice := range slices {
		port := 0
		items, _, _ := unstructured.NestedSlice(slice.Object, "ports")
		for _, item := range items {
			p, _ := item.(map[string]any)
			if n, _ := p["name"].(string); n == name {
				number, _ := p["port"].(int64)
				port = int(number)
			}
		}
		if port == 0 {
			continue
		}
		endpoints, _, _ := unstructured.NestedSlice(slice.Object, "endpoints")
		for _, item := range endpoints {
			e, _ := item.(map[string]any)
			if ready, found, _ := unstructured.NestedBool(e, "conditions", "ready"); found && !ready {
				continue
			}
			if kind, _, _ := unstructured.NestedString(e, "targetRef", "kind"); kind != "" && kind != "Pod" {
				continue
			}
			if pod, _, _ := unstructured.NestedString(e, "targetRef", "name"); pod != "" {
				return pod, port, nil
			}
		}
	}
	return "", 0, errors.New("no Traefik pod is ready; `shelf app status` and the pods in namespace traefik say why")
}

// portForwardDialer connects to a pod's portforward subresource, over WebSocket where the API
// server supports it and over SPDY otherwise, as kubectl does.
func portForwardDialer(cfg *rest.Config, namespace, pod string) (httpstream.Dialer, error) {
	host, err := url.Parse(cfg.Host)
	if err != nil {
		return nil, err
	}
	if host.Scheme == "" {
		host.Scheme = "https"
	}
	host.Path = path.Join(host.Path, "/api/v1/namespaces", namespace, "pods", pod, "portforward")
	transport, upgrader, err := spdy.RoundTripperFor(cfg)
	if err != nil {
		return nil, err
	}
	viaSPDY := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, host)
	viaWebSocket, err := portforward.NewSPDYOverWebsocketDialer(host, cfg)
	if err != nil {
		return nil, err
	}
	return portforward.NewFallbackDialer(viaWebSocket, viaSPDY, func(err error) bool {
		return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
	}), nil
}
