package cluster

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"

	"github.com/tweinmann/shelf/internal/render"
)

// Port is one port a component listens on.
type Port struct {
	Name   string `json:"name"`
	Number int    `json:"number"`
}

// Component is one part of an app: a container with its ports, and the path it answers under if
// it has a route. Components without one are reachable from their siblings only.
type Component struct {
	Name string `json:"name"`
	// Ports are what the component listens on, sorted by name.
	Ports []Port `json:"ports,omitempty"`
	// Path is the path the component answers under, empty when it has no route.
	Path string `json:"path,omitempty"`
	// Phase is the state of this component's pods.
	Phase Phase `json:"phase"`
}

// AppComponents returns what one app is made of.
//
// It reads the values ConfigMap the deploy artifact wrote, because that is the document the
// chart renders from: it names every component, while the Ingresses would name only those that
// answer from outside. An app whose values have not been applied yet has nothing to show, which
// is a state, not an error — the diagnosis of the app says why.
func AppComponents(ctx context.Context, cfg *rest.Config, app string) ([]Component, error) {
	c, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	values, err := c.get(ctx, ref{gvk: configMapGVK, namespace: app, name: render.ConfigMapName})
	if err != nil {
		return nil, err
	}
	if values == nil {
		return nil, nil
	}
	pods, err := c.list(ctx, podGVK, app, AppLabel)
	if err != nil {
		return nil, err
	}
	return components(values, pods)
}

// components turns the values document and the app's pods into the component list.
func components(values *unstructured.Unstructured, pods []*unstructured.Unstructured) ([]Component, error) {
	doc, found, err := unstructured.NestedString(values.Object, "data", render.ConfigMapKey)
	if err != nil || !found {
		return nil, err
	}
	app, err := render.ParseApp([]byte(doc))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", describe(values), err)
	}

	byComponent := map[string][]*unstructured.Unstructured{}
	for _, pod := range pods {
		if name := pod.GetLabels()[ComponentLabel]; name != "" {
			byComponent[name] = append(byComponent[name], pod)
		}
	}

	out := make([]Component, 0, len(app.Components))
	for _, name := range slices.Sorted(maps.Keys(app.Components)) {
		spec := app.Components[name]
		c := Component{Name: name, Phase: componentPhase(byComponent[name])}
		ports := spec.PortNames()
		for _, port := range slices.Sorted(maps.Keys(ports)) {
			c.Ports = append(c.Ports, Port{Name: port, Number: ports[port]})
		}
		if spec.Route != nil {
			c.Path = spec.Route.Path
		}
		out = append(out, c)
	}
	return out, nil
}

// componentPhase folds the pods of one component into a single state, the way podStage judges
// the pods of a whole app: one that will not recover by itself decides, otherwise one that is
// still starting, otherwise ready.
func componentPhase(pods []*unstructured.Unstructured) Phase {
	if len(pods) == 0 {
		return PhaseMissing
	}
	phase := PhaseReady
	for _, pod := range pods {
		switch p, _, _ := podTrouble(pod); p {
		case PhaseFailed:
			return PhaseFailed
		case PhaseWorking:
			phase = PhaseWorking
		}
	}
	return phase
}
