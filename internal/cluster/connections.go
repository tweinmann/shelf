package cluster

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"

	"github.com/tweinmann/shelf/internal/progress"
)

// A registry connection is a login for RegistryHost that the user names once and assigns to any
// number of apps. It lives in the cluster, because the cluster pulls with it: the platform copies
// it into the namespace of every app that uses it, and a new token reaches all of them at once.
const (
	// ConnectionLabel marks a connection in SystemNamespace; its value is the kind.
	ConnectionLabel = "shelf.dev/connection"
	// ConnectionRegistry is the kind of a registry connection.
	ConnectionRegistry = "registry"
	// AnonymousRegistrySecretName holds no login. It is what an app without a registry
	// connection pulls with, so the platform always has something to copy.
	AnonymousRegistrySecretName = "registry-anonymous"
	registryConnectionPrefix    = "connection-registry-"
)

// RegistryConnectionSecretName is the Secret in SystemNamespace that holds a registry connection.
func RegistryConnectionSecretName(name string) string { return registryConnectionPrefix + name }

// RegistryConnection is a registry connection as it may be shown: the token is not part of it.
type RegistryConnection struct {
	Name     string `json:"name"`
	Username string `json:"username"`
}

// dockerConfigSecret returns a registry login in SystemNamespace that the platform watches.
func dockerConfigSecret(name string, labels map[string]any, auth *RegistryAuth) (*unstructured.Unstructured, error) {
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
	labels[WatchLabel] = "Enabled"
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      name,
			"namespace": SystemNamespace,
			"labels":    labels,
		},
		"type": "kubernetes.io/dockerconfigjson",
		"data": map[string]any{
			".dockerconfigjson": base64.StdEncoding.EncodeToString(config),
		},
	}}, nil
}

// RegistryConnectionSecret returns the Secret of a registry connection.
func RegistryConnectionSecret(name string, auth RegistryAuth) (*unstructured.Unstructured, error) {
	return dockerConfigSecret(RegistryConnectionSecretName(name),
		map[string]any{ConnectionLabel: ConnectionRegistry}, &auth)
}

// AnonymousRegistrySecret returns the Secret an app without a registry connection pulls with.
func AnonymousRegistrySecret() (*unstructured.Unstructured, error) {
	return dockerConfigSecret(AnonymousRegistrySecretName, map[string]any{}, nil)
}

// RegistryConnections returns the registry connections of this cluster, by name.
func RegistryConnections(ctx context.Context, cfg *rest.Config) ([]RegistryConnection, error) {
	c, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	secrets, err := c.list(ctx, secretGVK, SystemNamespace, ConnectionLabel+"="+ConnectionRegistry)
	if err != nil {
		return nil, err
	}
	out := make([]RegistryConnection, 0, len(secrets))
	for _, s := range secrets {
		name, ok := strings.CutPrefix(s.GetName(), registryConnectionPrefix)
		if !ok {
			continue
		}
		conn := RegistryConnection{Name: name}
		// A Secret that cannot be read shows without a user; saving it again repairs it.
		if login, err := registryLogin(s); err == nil && login != nil {
			conn.Username = login.Username
		}
		out = append(out, conn)
	}
	slices.SortFunc(out, func(a, b RegistryConnection) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// ReadRegistryConnection returns the login of a registry connection, or nil if there is none of
// that name.
func ReadRegistryConnection(ctx context.Context, cfg *rest.Config, name string) (*RegistryAuth, error) {
	c, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	obj, err := c.get(ctx, ref{gvk: secretGVK, namespace: SystemNamespace, name: RegistryConnectionSecretName(name)})
	if err != nil || obj == nil {
		return nil, err
	}
	return registryLogin(obj)
}

// SaveRegistryConnection creates or replaces a registry connection. It never reports the token.
func SaveRegistryConnection(ctx context.Context, cfg *rest.Config, name string, auth RegistryAuth,
	report progress.Reporter) error {
	c, err := newClient(cfg)
	if err != nil {
		return err
	}
	secret, err := RegistryConnectionSecret(name, auth)
	if err != nil {
		return err
	}
	action, err := c.apply(ctx, secret)
	if err != nil {
		return err
	}
	progress.OrDiscard(report).Report(progress.Applied(describe(secret), string(action),
		"for "+auth.Username+"@"+RegistryHost))
	return nil
}

// DeleteRegistryConnection removes a registry connection and reports whether it existed. Whether
// an app still uses it is the caller's question.
func DeleteRegistryConnection(ctx context.Context, cfg *rest.Config, name string,
	report progress.Reporter) (bool, error) {
	c, err := newClient(cfg)
	if err != nil {
		return false, err
	}
	r := ref{gvk: secretGVK, namespace: SystemNamespace, name: RegistryConnectionSecretName(name)}
	deleted, err := c.delete(ctx, r)
	if err != nil {
		return false, err
	}
	if deleted {
		progress.OrDiscard(report).Report(progress.Applied(r.String(), "deleted", ""))
	}
	return deleted, nil
}
