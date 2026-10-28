package cluster

import (
	"context"
	"fmt"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

var nodeGVK = schema.GroupVersionKind{Version: "v1", Kind: "Node"}

// NodePodCIDRs returns the address ranges the cluster gives its pods, from every node. A LAN
// exception inside one of them would let an app reach the pods of other apps.
func NodePodCIDRs(ctx context.Context, cfg *rest.Config) ([]string, error) {
	c, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	nodes, err := c.list(ctx, nodeGVK, "", "")
	if err != nil {
		return nil, err
	}
	return podCIDRs(nodes), nil
}

// podCIDRs reads spec.podCIDRs of the nodes, and spec.podCIDR of a node that has only that.
func podCIDRs(nodes []*unstructured.Unstructured) []string {
	var out []string
	for _, n := range nodes {
		cidrs, _, _ := unstructured.NestedStringSlice(n.Object, "spec", "podCIDRs")
		if len(cidrs) == 0 {
			if one, _, _ := unstructured.NestedString(n.Object, "spec", "podCIDR"); one != "" {
				cidrs = []string{one}
			}
		}
		out = append(out, cidrs...)
	}
	return out
}

// Pod Security labels the platform sets on app and preview namespaces.
const (
	podSecurityEnforce = "pod-security.kubernetes.io/enforce"
	// restrictedLevel is the level the namespaces warn about, and the one violations are
	// reported against.
	restrictedLevel = "restricted"
)

// Isolation is how an app or preview is fenced in.
type Isolation struct {
	// PodSecurity is the level its namespace enforces, empty for a namespace from a platform
	// before Pod Security.
	PodSecurity string
	// Restricted are the pods that would violate the level restricted, which the namespace warns
	// about but admits.
	Restricted []Violation
	// LAN are the private ranges it may reach, none for a preview.
	LAN []string
}

// Violation is one pod, or several alike, that breaks a Pod Security level, and why.
type Violation struct {
	Pod    string
	Reason string
}

// AppIsolation reads how an app or a preview (by its namespace) is fenced in. It asks the API
// server which pods would violate restricted with a dry run of that label: the API server answers
// with a warning per pod and keeps nothing.
func AppIsolation(ctx context.Context, cfg *rest.Config, namespace string, lan []string) (Isolation, error) {
	c, err := newClient(cfg)
	if err != nil {
		return Isolation{}, err
	}
	ns, err := c.get(ctx, ref{gvk: namespaceGVK, name: namespace})
	if err != nil {
		return Isolation{}, err
	}
	if ns == nil {
		return Isolation{}, &NotFoundError{Name: namespace}
	}
	iso := Isolation{PodSecurity: ns.GetLabels()[podSecurityEnforce], LAN: lan}
	if iso.PodSecurity == "" || iso.PodSecurity == restrictedLevel {
		return iso, nil
	}
	warnings := &warningCollector{}
	dry := rest.CopyConfig(cfg)
	dry.WarningHandler = warnings
	dyn, err := dynamic.NewForConfig(dry)
	if err != nil {
		return Isolation{}, err
	}
	patch := fmt.Sprintf(`{"metadata":{"labels":{%q:%q}}}`, podSecurityEnforce, restrictedLevel)
	if _, err := dyn.Resource(corev1.SchemeGroupVersion.WithResource("namespaces")).Patch(ctx, namespace,
		types.MergePatchType, []byte(patch), metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}}); err != nil {
		return Isolation{}, fmt.Errorf("checking the pods of %s against Pod Security %s: %w", namespace, restrictedLevel, err)
	}
	iso.Restricted = violations(warnings.messages)
	return iso, nil
}

// warningCollector keeps the warnings of the API server's responses.
type warningCollector struct {
	mu       sync.Mutex
	messages []string
}

func (w *warningCollector) HandleWarningHeader(_ int, _ string, message string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.messages = append(w.messages, message)
}

// violations reads the warnings of a Pod Security label change. The first one says the level is
// violated at all; each further one is "<pod>: <what>", where pods that break it alike are named
// together, as "web-5d8f (and 2 other pods)".
func violations(warnings []string) []Violation {
	var out []Violation
	for _, w := range warnings {
		pod, reason, ok := strings.Cut(w, ": ")
		if !ok || strings.HasPrefix(w, "existing pods in namespace") || strings.Contains(pod, " violate") {
			continue
		}
		out = append(out, Violation{Pod: pod, Reason: reason})
	}
	return out
}
