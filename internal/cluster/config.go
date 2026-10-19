package cluster

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var configMapGVK = schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}

const (
	// SystemNamespace holds the app providers and their secrets.
	SystemNamespace = "shelf-system"
	// ConfigName is the ConfigMap in FluxNamespace whose keys Flux substitutes into the
	// platform manifests.
	ConfigName = "shelf-config"
	// RegistryHost is the registry a registry connection is for.
	RegistryHost = "ghcr.io"
	// WatchLabel makes the platform ResourceSet copy a Secret into the app namespaces as soon
	// as it changes, instead of at its next interval.
	WatchLabel = "reconcile.fluxcd.io/watch"
)

// InternalDomain is where an app without a domain of its own answers, inside the cluster only:
// <app>.shelf.internal. The name is reserved for private use, so it never gets a link, and it
// carries no host suffix, because it never leaves the cluster.
const InternalDomain = "shelf.internal"

// Settings are the per-cluster values the platform needs. A cluster has no domain: a domain
// belongs to an app, which answers at <app><HostSuffix>.<domain> with one and at
// <app>.shelf.internal without.
type Settings struct {
	// HostSuffix separates clusters that share a zone, e.g. "-dev". Cloudflare's free
	// certificate covers one level of subdomain, so the suffix goes into the app label rather
	// than into another level.
	HostSuffix string
	// Chart is the shelf-app chart every app is installed with.
	Chart Artifact
	// InsecureRegistry allows platform and chart registries without TLS.
	InsecureRegistry bool
	// LegacyDomain is the domain a cluster had before clusters had none, read back and never
	// written. `shelf init cluster` gives it to the apps that answered under it.
	LegacyDomain string
}

// Installed reports whether `shelf init cluster` has run against the cluster.
func (s Settings) Installed() bool { return s.Chart.URL != "" }

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
				"SHELF_HOST_SUFFIX":       s.HostSuffix,
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
		HostSuffix:       data["SHELF_HOST_SUFFIX"],
		Chart:            Artifact{URL: data["SHELF_CHART_URL"], Tag: data["SHELF_CHART_TAG"]},
		InsecureRegistry: insecure,
		LegacyDomain:     data[legacyDomainKey],
	}
}

// legacyDomainKey held the cluster's domain until clusters had none.
const legacyDomainKey = "SHELF_DOMAIN"

// registryLogin reads the login back out of a registry Secret, or nil when it holds none.
func registryLogin(obj *unstructured.Unstructured) (*RegistryAuth, error) {
	encoded, found, err := unstructured.NestedString(obj.Object, "data", ".dockerconfigjson")
	if err != nil || !found {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("Secret %s/%s: %w", obj.GetNamespace(), obj.GetName(), err)
	}
	var config struct {
		Auths map[string]struct{ Username, Password string } `json:"auths"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("Secret %s/%s: %w", obj.GetNamespace(), obj.GetName(), err)
	}
	entry, ok := config.Auths[RegistryHost]
	if !ok || entry.Username == "" || entry.Password == "" {
		return nil, nil
	}
	return &RegistryAuth{Username: entry.Username, Token: entry.Password}, nil
}
