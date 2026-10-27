package cluster

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func serviceWithPort(port int64, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{"ports": []any{
		map[string]any{"name": name, "port": port, "targetPort": name},
	}}}}
}

func endpointSlice(portName string, port int64, endpoints ...map[string]any) *unstructured.Unstructured {
	items := make([]any, 0, len(endpoints))
	for _, e := range endpoints {
		items = append(items, e)
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"ports":     []any{map[string]any{"name": portName, "port": port}},
		"endpoints": items,
	}}
}

func endpoint(pod string, ready bool) map[string]any {
	return map[string]any{
		"conditions": map[string]any{"ready": ready},
		"targetRef":  map[string]any{"kind": "Pod", "name": pod},
	}
}

func TestTraefikTarget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		svc    *unstructured.Unstructured
		slices []*unstructured.Unstructured
		pod    string
		port   int
		err    string
	}{
		{name: "named target port", svc: serviceWithPort(80, "web"),
			slices: []*unstructured.Unstructured{endpointSlice("web", 8000, endpoint("traefik-1", true))},
			pod:    "traefik-1", port: 8000},
		{name: "unnamed port", svc: serviceWithPort(80, ""),
			slices: []*unstructured.Unstructured{endpointSlice("", 8000, endpoint("traefik-1", true))},
			pod:    "traefik-1", port: 8000},
		{name: "skips a pod that is not ready", svc: serviceWithPort(80, "web"),
			slices: []*unstructured.Unstructured{endpointSlice("web", 8000,
				endpoint("traefik-old", false), endpoint("traefik-new", true))},
			pod: "traefik-new", port: 8000},
		{name: "no ready pod", svc: serviceWithPort(80, "web"),
			slices: []*unstructured.Unstructured{endpointSlice("web", 8000, endpoint("traefik-1", false))},
			err:    "no Traefik pod is ready"},
		{name: "no endpoints", svc: serviceWithPort(80, "web"), err: "no Traefik pod is ready"},
		{name: "service without port 80", svc: serviceWithPort(443, "websecure"), err: "has no port 80"},
		{name: "slice for another port", svc: serviceWithPort(80, "web"),
			slices: []*unstructured.Unstructured{endpointSlice("websecure", 8443, endpoint("traefik-1", true))},
			err:    "no Traefik pod is ready"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pod, port, err := traefikTarget(tt.svc, tt.slices)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("error %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil || pod != tt.pod || port != tt.port {
				t.Errorf("got %s:%d %v, want %s:%d", pod, port, err, tt.pod, tt.port)
			}
		})
	}
}
