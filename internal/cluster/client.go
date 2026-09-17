package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/cli-utils/pkg/kstatus/status"
)

// Action is what server-side apply did to an object.
type Action string

const (
	Created    Action = "created"
	Configured Action = "configured"
	Unchanged  Action = "unchanged"
)

// classify compares the resourceVersion before (empty: the object did not exist) and after an
// apply. A server-side apply that changes nothing does not write, so the version stays.
func classify(before, after string) Action {
	switch before {
	case "":
		return Created
	case after:
		return Unchanged
	default:
		return Configured
	}
}

// pollInterval is how often waits look at the cluster.
var pollInterval = 2 * time.Second

type client struct {
	dyn    dynamic.Interface
	mapper *restmapper.DeferredDiscoveryRESTMapper
}

func newClient(cfg *rest.Config) (*client, error) {
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &client{dyn: dyn, mapper: restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(disc))}, nil
}

func (c *client) resource(gvk schema.GroupVersionKind, namespace string) (dynamic.ResourceInterface, error) {
	mapping, err := c.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, err
	}
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		return c.dyn.Resource(mapping.Resource).Namespace(namespace), nil
	}
	return c.dyn.Resource(mapping.Resource), nil
}

// apply creates or updates obj with server-side apply, taking over conflicting fields.
func (c *client) apply(ctx context.Context, obj *unstructured.Unstructured) (Action, error) {
	ri, err := c.resource(obj.GroupVersionKind(), obj.GetNamespace())
	if err != nil {
		return "", fmt.Errorf("%s: %w", describe(obj), err)
	}
	before := ""
	existing, err := ri.Get(ctx, obj.GetName(), metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
	case err != nil:
		return "", fmt.Errorf("%s: %w", describe(obj), err)
	default:
		before = existing.GetResourceVersion()
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return "", err
	}
	force := true
	after, err := ri.Patch(ctx, obj.GetName(), types.ApplyPatchType, data,
		metav1.PatchOptions{FieldManager: FieldManager, Force: &force})
	if err != nil {
		return "", fmt.Errorf("applying %s: %w", describe(obj), err)
	}
	return classify(before, after.GetResourceVersion()), nil
}

// ref identifies an object to wait for.
type ref struct {
	gvk       schema.GroupVersionKind
	namespace string
	name      string
}

func refOf(obj *unstructured.Unstructured) ref {
	return ref{gvk: obj.GroupVersionKind(), namespace: obj.GetNamespace(), name: obj.GetName()}
}

func (r ref) String() string {
	if r.namespace == "" {
		return r.gvk.Kind + " " + r.name
	}
	return r.gvk.Kind + " " + r.namespace + "/" + r.name
}

func describe(obj *unstructured.Unstructured) string { return refOf(obj).String() }

// readyFunc decides whether an object is ready and explains why not.
type readyFunc func(obj *unstructured.Unstructured) (bool, string, error)

// current is ready when kstatus reports the object as reconciled: the controller has observed
// the latest generation and reports success (Ready, Available, Established, ...).
func current(obj *unstructured.Unstructured) (bool, string, error) {
	res, err := status.Compute(obj)
	if err != nil {
		return false, "", err
	}
	return res.Status == status.CurrentStatus, res.Message, nil
}

// conditions returns status.conditions.
func conditions(obj *unstructured.Unstructured) []map[string]any {
	items, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	var out []map[string]any
	for _, item := range items {
		if cond, ok := item.(map[string]any); ok {
			out = append(out, cond)
		}
	}
	return out
}

// readyCondition is ready when the Ready condition is True for the current generation. Flux
// and the Flux Operator report readiness this way. Unlike kstatus it does not treat an object
// without status as ready, which a freshly created custom resource is. A Stalled condition
// means Flux gave up until something changes, so it ends the wait with an error.
func readyCondition(obj *unstructured.Unstructured) (bool, string, error) {
	observed, found, _ := unstructured.NestedInt64(obj.Object, "status", "observedGeneration")
	var ready map[string]any
	for _, cond := range conditions(obj) {
		switch cond["type"] {
		case "Stalled":
			if cond["status"] == "True" {
				msg, _ := cond["message"].(string)
				return false, msg, fmt.Errorf("%s is stalled: %s", describe(obj), msg)
			}
		case "Ready":
			ready = cond
		}
	}
	if ready == nil {
		return false, "no Ready condition yet", nil
	}
	msg, _ := ready["message"].(string)
	if !found {
		if g, ok := ready["observedGeneration"].(int64); ok {
			observed, found = g, true
		}
	}
	if !found || observed != obj.GetGeneration() {
		return false, "waiting for the controller to observe the latest change", nil
	}
	return ready["status"] == "True", msg, nil
}

// waitFor polls until ready reports true or the context ends. A missing object counts as not
// ready yet, because controllers create some of the objects shelf waits for.
func (c *client) waitFor(ctx context.Context, r ref, ready readyFunc) error {
	last := "not found"
	err := wait.PollUntilContextCancel(ctx, pollInterval, true, func(ctx context.Context) (bool, error) {
		ri, err := c.resource(r.gvk, r.namespace)
		if meta.IsNoMatchError(err) {
			c.mapper.Reset()
			return false, nil
		}
		if err != nil {
			return false, err
		}
		obj, err := ri.Get(ctx, r.name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		ok, msg, err := ready(obj)
		if msg != "" {
			last = msg
		}
		return ok, err
	})
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%s is not ready: %s", r, last)
		}
		return fmt.Errorf("waiting for %s: %w", r, err)
	}
	return nil
}

// annotate sets one annotation with a merge patch, leaving field ownership alone.
func (c *client) annotate(ctx context.Context, r ref, key, value string) error {
	ri, err := c.resource(r.gvk, r.namespace)
	if err != nil {
		return err
	}
	patch, err := json.Marshal(map[string]any{
		"metadata": map[string]any{"annotations": map[string]string{key: value}},
	})
	if err != nil {
		return err
	}
	if _, err := ri.Patch(ctx, r.name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("annotating %s: %w", r, err)
	}
	return nil
}

// get returns an object, or nil if it does not exist.
func (c *client) get(ctx context.Context, r ref) (*unstructured.Unstructured, error) {
	ri, err := c.resource(r.gvk, r.namespace)
	if err != nil {
		return nil, err
	}
	obj, err := ri.Get(ctx, r.name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	return obj, err
}
