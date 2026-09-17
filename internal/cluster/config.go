package cluster

import (
	"encoding/base64"
	"encoding/json"
	"strconv"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

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
)

// Settings are the per-cluster values the platform needs.
type Settings struct {
	// Domain: apps are reachable at <app>.<domain>.
	Domain string
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
				"SHELF_CHART_URL":         s.Chart.URL,
				"SHELF_CHART_TAG":         s.Chart.Tag,
				"SHELF_INSECURE_REGISTRY": strconv.FormatBool(s.InsecureRegistry),
			},
		}},
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
		"metadata":   map[string]any{"name": RegistrySecretName, "namespace": SystemNamespace},
		"type":       "kubernetes.io/dockerconfigjson",
		"data": map[string]any{
			".dockerconfigjson": base64.StdEncoding.EncodeToString(config),
		},
	}}, nil
}
