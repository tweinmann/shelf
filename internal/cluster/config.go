package cluster

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var configMapGVK = schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}

const (
	// SystemNamespace holds the app providers, their secrets and the registry credential.
	SystemNamespace = "shelf-system"
	// ConfigName is the ConfigMap in FluxNamespace whose keys Flux substitutes into the
	// platform manifests.
	ConfigName = "shelf-config"
	// RegistrySecretName is the registry credential in SystemNamespace; the platform copies it
	// into every app namespace.
	RegistrySecretName = "registry"
	// RegistryHost is the registry the credential is for.
	RegistryHost = "ghcr.io"
	// WatchLabel makes the platform ResourceSet copy a Secret into the app namespaces as soon
	// as it changes, instead of at its next interval.
	WatchLabel = "reconcile.fluxcd.io/watch"
)

// Settings are the per-cluster values the platform needs.
type Settings struct {
	// Domain: apps are reachable at <app><HostSuffix>.<domain>.
	Domain string
	// HostSuffix separates clusters that share a zone, e.g. "-dev". Cloudflare's free
	// certificate covers one level of subdomain, so the suffix goes into the app label rather
	// than into another level.
	HostSuffix string
	// TunnelTarget is what external-dns points the DNS records at,
	// <tunnel-uuid>.cfargotunnel.com. Empty until `shelf init expose` ran.
	TunnelTarget string
	// Chart is the shelf-app chart every app is installed with.
	Chart Artifact
	// InsecureRegistry allows platform and chart registries without TLS.
	InsecureRegistry bool
}

// RegistryAuth is a registry login. The token must be a classic PAT with read:packages.
type RegistryAuth struct {
	Username string
	Token    string
}

// ConfigObjects returns the system namespace and the ConfigMap with the cluster settings.
func ConfigObjects(s Settings) []*unstructured.Unstructured {
	return []*unstructured.Unstructured{
		{Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Namespace",
			// Server-side apply drops a field manager that owns no fields, and the next apply
			// then counts as a change. The label gives shelf a field to own.
			"metadata": map[string]any{
				"name":   SystemNamespace,
				"labels": map[string]any{"app.kubernetes.io/managed-by": FieldManager},
			},
		}},
		{Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata":   map[string]any{"name": ConfigName, "namespace": FluxNamespace},
			"data": map[string]any{
				"SHELF_DOMAIN":            s.Domain,
				"SHELF_HOST_SUFFIX":       s.HostSuffix,
				"SHELF_TUNNEL_TARGET":     s.TunnelTarget,
				"SHELF_CHART_URL":         s.Chart.URL,
				"SHELF_CHART_TAG":         s.Chart.Tag,
				"SHELF_INSECURE_REGISTRY": strconv.FormatBool(s.InsecureRegistry),
			},
		}},
	}
}

// settings returns the cluster settings stored in the cluster, or zero values if there are none.
func (c *client) settings(ctx context.Context) (Settings, error) {
	obj, err := c.get(ctx, ref{gvk: configMapGVK, namespace: FluxNamespace, name: ConfigName})
	if err != nil || obj == nil {
		return Settings{}, err
	}
	data, _, _ := unstructured.NestedStringMap(obj.Object, "data")
	return ParseSettings(data), nil
}

// ParseSettings reads back what ConfigObjects wrote. Every command that writes the settings has
// to read all of them first, or it would reset the ones it does not know about.
func ParseSettings(data map[string]string) Settings {
	insecure, _ := strconv.ParseBool(data["SHELF_INSECURE_REGISTRY"])
	return Settings{
		Domain:           data["SHELF_DOMAIN"],
		HostSuffix:       data["SHELF_HOST_SUFFIX"],
		TunnelTarget:     data["SHELF_TUNNEL_TARGET"],
		Chart:            Artifact{URL: data["SHELF_CHART_URL"], Tag: data["SHELF_CHART_TAG"]},
		InsecureRegistry: insecure,
	}
}

// RegistrySecret returns the registry credential. Without auth it holds no login, which still
// lets apps with public images run.
func RegistrySecret(auth *RegistryAuth) (*unstructured.Unstructured, error) {
	auths := map[string]any{}
	if auth != nil {
		auths[RegistryHost] = map[string]string{
			"username": auth.Username,
			"password": auth.Token,
			"auth":     base64.StdEncoding.EncodeToString([]byte(auth.Username + ":" + auth.Token)),
		}
	}
	config, err := json.Marshal(map[string]any{"auths": auths})
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      RegistrySecretName,
			"namespace": SystemNamespace,
			"labels":    map[string]any{WatchLabel: "Enabled"},
		},
		"type": "kubernetes.io/dockerconfigjson",
		"data": map[string]any{
			".dockerconfigjson": base64.StdEncoding.EncodeToString(config),
		},
	}}, nil
}
