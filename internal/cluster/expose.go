package cluster

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
)

// Names of the objects that expose a cluster. They all live in SystemNamespace, next to the
// platform components that read them.
const (
	// ExposeLabel marks the provider that switches the exposure on.
	ExposeLabel = "shelf.dev/expose"
	// ExposeName is the provider, and the name of the Cloudflare tunnel's secret.
	ExposeName = "expose"
	// TunnelSecretName holds the credentials cloudflared uses.
	TunnelSecretName = "tunnel"
	// CloudflaredName is the deployment that keeps the tunnel open.
	CloudflaredName = "cloudflared"
)

var deploymentGVK = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}

// ExposeOptions configure Expose.
type ExposeOptions struct {
	// TunnelID is the Cloudflare tunnel; DNS records point at <TunnelID>.cfargotunnel.com.
	TunnelID string
	// Credentials is the credentials.json of that tunnel, or nil to keep the stored one.
	Credentials []byte
	Timeout     time.Duration
	Out         io.Writer
}

// secret returns an opaque Secret in SystemNamespace that the platform watches.
func secret(name string, data map[string]string) *unstructured.Unstructured {
	encoded := map[string]any{}
	for k, v := range data {
		encoded[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      name,
			"namespace": SystemNamespace,
			"labels":    map[string]any{WatchLabel: "Enabled"},
		},
		"type": "Opaque",
		"data": encoded,
	}}
}

// ExposeProvider returns the provider that makes the platform run cloudflared and external-dns.
func ExposeProvider(o ExposeOptions) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "fluxcd.controlplane.io/v1",
		"kind":       "ResourceSetInputProvider",
		"metadata": map[string]any{
			"name":      ExposeName,
			"namespace": SystemNamespace,
			"labels":    map[string]any{ExposeLabel: "true"},
		},
		"spec": map[string]any{
			"type": "Static",
			"defaultValues": map[string]any{
				"tunnel": o.TunnelID,
			},
		},
	}}
}

// Expose stores the tunnel credentials and the API token, switches the platform's exposure on
// and waits until cloudflared and external-dns run. It also records the tunnel target in the
// cluster settings, so the chart annotates every ingress with it.
func Expose(ctx context.Context, cfg *rest.Config, o ExposeOptions) error {
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	c, err := newClient(cfg)
	if err != nil {
		return err
	}
	out := o.Out

	if o.Credentials != nil {
		if err := c.applyReport(ctx, out, secret(TunnelSecretName,
			map[string]string{"credentials.json": string(o.Credentials)})); err != nil {
			return err
		}
	}
	if err := c.applyReport(ctx, out, ExposeProvider(o)); err != nil {
		return err
	}

	settings, err := c.settings(ctx)
	if err != nil {
		return err
	}
	settings.TunnelTarget = o.TunnelID + ".cfargotunnel.com"
	for _, obj := range ConfigObjects(settings) {
		if err := c.applyReport(ctx, out, obj); err != nil {
			return err
		}
	}
	// Every app's ingress is annotated with the tunnel target, which the platform substitutes
	// from the settings; without this the apps would follow only at the next interval.
	err = c.step(ctx, out, "the platform", func(ctx context.Context) (string, error) {
		_, err := c.reconcileAndWait(ctx, platformSync, readyCondition)
		return "", err
	})
	if err != nil {
		return err
	}

	return c.step(ctx, out, "the tunnel", func(ctx context.Context) (string, error) {
		return "", c.waitFor(ctx, ref{gvk: deploymentGVK, namespace: SystemNamespace, name: CloudflaredName}, current)
	})
}

// AppNames returns the apps of this cluster, by the providers `shelf app add` created.
func AppNames(ctx context.Context, cfg *rest.Config) ([]string, error) {
	c, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	ri, err := c.resource(providerGVK, SystemNamespace)
	if err != nil {
		return nil, err
	}
	list, err := ri.List(ctx, metav1.ListOptions{LabelSelector: AppLabel})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.GetName())
	}
	slices.Sort(names)
	return names, nil
}

// TunnelCredentials returns the stored credentials.json of the tunnel, or nil if there are none.
func TunnelCredentials(ctx context.Context, cfg *rest.Config) ([]byte, error) {
	c, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	obj, err := c.get(ctx, ref{gvk: secretGVK, namespace: SystemNamespace, name: TunnelSecretName})
	if err != nil || obj == nil {
		return nil, err
	}
	data, _, _ := unstructured.NestedStringMap(obj.Object, "data")
	encoded, ok := data["credentials.json"]
	if !ok {
		return nil, nil
	}
	credentials, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("secret %s/%s: %w", SystemNamespace, TunnelSecretName, err)
	}
	return credentials, nil
}

// ClusterSettings returns the settings `shelf init cluster` stored in the cluster.
func ClusterSettings(ctx context.Context, cfg *rest.Config) (Settings, error) {
	c, err := newClient(cfg)
	if err != nil {
		return Settings{}, err
	}
	return c.settings(ctx)
}

// applyReport applies an object and prints what happened to it.
func (c *client) applyReport(ctx context.Context, out io.Writer, obj *unstructured.Unstructured) error {
	action, err := c.apply(ctx, obj)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: %s\n", describe(obj), action)
	return nil
}
