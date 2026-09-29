package cluster

import (
	"context"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"

	"github.com/tweinmann/shelf/internal/progress"
)

// An app that is reachable from the internet has its own Cloudflare tunnel, in the Cloudflare
// account the app was given. The platform runs cloudflared for it in the app namespace; these
// are the names of what that takes.
const (
	// CloudflaredName is the Deployment and the ConfigMap of cloudflared in the app namespace.
	CloudflaredName = "cloudflared"
	// tunnelCredentialsKey is the key of credentials.json in the tunnel Secret.
	tunnelCredentialsKey = "credentials.json"
)

var deploymentGVK = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}

// TunnelSecretName is the Secret in SystemNamespace with the credentials of an app's tunnel. The
// platform copies it into the app namespace for cloudflared; the Cloudflare API token that made
// the tunnel never enters the cluster.
func TunnelSecretName(app string) string { return "tunnel-" + app }

// TunnelSecret returns the Secret with the credentials of an app's tunnel.
func TunnelSecret(app string, credentials []byte) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      TunnelSecretName(app),
			"namespace": SystemNamespace,
			"labels":    map[string]any{AppLabel: app, WatchLabel: "Enabled"},
		},
		"type": "Opaque",
		"data": map[string]any{
			tunnelCredentialsKey: base64.StdEncoding.EncodeToString(credentials),
		},
	}}
}

// tunnelCredentials reads credentials.json back out of a tunnel Secret.
func tunnelCredentials(obj *unstructured.Unstructured) ([]byte, error) {
	data, _, _ := unstructured.NestedStringMap(obj.Object, "data")
	encoded, ok := data[tunnelCredentialsKey]
	if !ok {
		return nil, nil
	}
	credentials, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("secret %s/%s: %w", obj.GetNamespace(), obj.GetName(), err)
	}
	return credentials, nil
}

// waitForTunnel waits until cloudflared in the app namespace runs for the given tunnel, or,
// without one, until it is gone. Closing matters as much as opening: Cloudflare refuses to delete
// a tunnel that still has connections, and shelf deletes the old tunnel right after this.
func (c *client) waitForTunnel(ctx context.Context, rep progress.Reporter, app, tunnel string) error {
	deployment := ref{gvk: deploymentGVK, namespace: app, name: CloudflaredName}
	if tunnel == "" {
		obj, err := c.get(ctx, deployment)
		if err != nil || obj == nil {
			return err
		}
		return c.step(ctx, rep, "the tunnel to close", func(ctx context.Context) (string, error) {
			return "", wait.PollUntilContextCancel(ctx, pollInterval, true, func(ctx context.Context) (bool, error) {
				obj, err := c.get(ctx, deployment)
				return obj == nil, err
			})
		})
	}
	config := ref{gvk: configMapGVK, namespace: app, name: CloudflaredName}
	return c.step(ctx, rep, "the tunnel", func(ctx context.Context) (string, error) {
		// The platform rewrites the configuration first; a Deployment that is ready from the
		// tunnel before must not pass.
		err := c.waitFor(ctx, config, func(obj *unstructured.Unstructured) (bool, string, error) {
			data, _, _ := unstructured.NestedStringMap(obj.Object, "data")
			return strings.Contains(data["config.yaml"], tunnel), "waiting for the platform to configure cloudflared", nil
		})
		if err != nil {
			return "", err
		}
		return "", c.waitFor(ctx, deployment, current)
	})
}

// AppNames returns the apps of this cluster, by the providers `shelf app add` created.
func AppNames(ctx context.Context, cfg *rest.Config) ([]string, error) {
	c, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	return c.appNames(ctx)
}

func (c *client) appNames(ctx context.Context) ([]string, error) {
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

// ClusterSettings returns the settings `shelf init cluster` stored in the cluster.
func ClusterSettings(ctx context.Context, cfg *rest.Config) (Settings, error) {
	c, err := newClient(cfg)
	if err != nil {
		return Settings{}, err
	}
	return c.settings(ctx)
}

// applyReport applies an object and reports what happened to it.
func (c *client) applyReport(ctx context.Context, rep progress.Reporter, obj *unstructured.Unstructured) error {
	action, err := c.apply(ctx, obj)
	if err != nil {
		return err
	}
	rep.Report(progress.Applied(describe(obj), string(action), ""))
	return nil
}
