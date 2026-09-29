package cluster

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// helloValues is what the deploy artifact of examples/hello leaves in the cluster: a routed
// component, one that only listens inside, and one with no port at all.
const helloValues = `apiVersion: shelf.dev/v1alpha1
name: hello
components:
  web:
    image: traefik/whoami:v1.11.0@sha256:1f4e
    ports:
      main: 80
    route:
      path: /
  check:
    image: postgres:16@sha256:2a7b
    command: [sh, -c]
    args: [sleep 30]
  db:
    image: postgres:16@sha256:2a7b
    ports:
      main: 5432
`

func valuesMap(doc string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": "hello-values", "namespace": "hello"},
		"data":       map[string]any{"app.yaml": doc},
	}}
}

// componentPod is a pod of one component, as the chart labels it.
func componentPod(component string, ready bool) *unstructured.Unstructured {
	p := pod("Running", container(ready, nil))
	p.SetLabels(map[string]string{AppLabel: "hello", ComponentLabel: component})
	return p
}

func TestComponents(t *testing.T) {
	t.Parallel()
	pods := []*unstructured.Unstructured{
		componentPod("web", true),
		componentPod("web", true),
		componentPod("db", true),
		// A pod of another app in the same list must not count towards a component here.
		pod("Running", container(false, nil)),
	}

	got, err := components(valuesMap(helloValues), pods)
	if err != nil {
		t.Fatal(err)
	}
	want := []Component{
		{Name: "check", Phase: PhaseMissing},
		{Name: "db", Ports: []Port{{Name: "main", Number: 5432}}, Phase: PhaseReady},
		{Name: "web", Ports: []Port{{Name: "main", Number: 80}}, Path: "/", Phase: PhaseReady},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d components, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Name != w.Name || g.Path != w.Path || g.Phase != w.Phase || len(g.Ports) != len(w.Ports) {
			t.Errorf("component %d: got %+v, want %+v", i, g, w)
			continue
		}
		for j, p := range w.Ports {
			if g.Ports[j] != p {
				t.Errorf("%s port %d: got %+v, want %+v", w.Name, j, g.Ports[j], p)
			}
		}
	}
}

func TestComponentPhase(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		pods []*unstructured.Unstructured
		want Phase
	}{
		"no pods": {want: PhaseMissing},
		"all running": {
			pods: []*unstructured.Unstructured{componentPod("web", true), componentPod("web", true)},
			want: PhaseReady,
		},
		"one still starting": {
			pods: []*unstructured.Unstructured{componentPod("web", true), componentPod("web", false)},
			want: PhaseWorking,
		},
		"one stuck decides": {
			pods: []*unstructured.Unstructured{
				componentPod("web", true),
				func() *unstructured.Unstructured {
					p := pod("Pending", container(false, waiting("CrashLoopBackOff", "back-off")))
					p.SetLabels(map[string]string{ComponentLabel: "web"})
					return p
				}(),
			},
			want: PhaseFailed,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := componentPhase(tt.pods); got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

// An app that has just been added has no values in the cluster yet. That is a state to show, not
// an error to raise.
func TestComponentsWithoutValues(t *testing.T) {
	t.Parallel()
	empty := valuesMap("")
	unstructured.RemoveNestedField(empty.Object, "data")

	got, err := components(empty, nil)
	if err != nil || got != nil {
		t.Errorf("got %+v, %v; want no components and no error", got, err)
	}
}
