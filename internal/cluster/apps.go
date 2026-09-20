package cluster

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"

	"github.com/tweinmann/shelf/internal/progress"
)

// AppLabel marks the objects shelf creates for an app in SystemNamespace.
const AppLabel = "shelf.dev/app"

var (
	providerGVK    = schema.GroupVersionKind{Group: "fluxcd.controlplane.io", Version: "v1", Kind: "ResourceSetInputProvider"}
	helmReleaseGVK = schema.GroupVersionKind{Group: "helm.toolkit.fluxcd.io", Version: "v2", Kind: "HelmRelease"}
	secretGVK      = schema.GroupVersionKind{Version: "v1", Kind: "Secret"}
	namespaceGVK   = schema.GroupVersionKind{Version: "v1", Kind: "Namespace"}
)

// Names of the objects the platform ResourceSet generates in an app namespace.
const (
	deployName = "deploy"
	chartName  = "shelf-app"
)

// AppSecretName is the Secret in SystemNamespace holding an app's secret values.
func AppSecretName(app string) string { return "app-" + app }

// AppOptions configure AddApp.
type AppOptions struct {
	Name     string
	Artifact Artifact
	// Insecure allows a deploy artifact registry without TLS.
	Insecure bool
	// Secrets are all secret values of the app; they replace the stored ones.
	Secrets map[string]string
	Timeout time.Duration
	// Report receives what happens; nil reports nothing.
	Report progress.Reporter
}

// AppSecret returns the Secret with an app's secret values.
func AppSecret(app string, values map[string]string) *unstructured.Unstructured {
	data := map[string]any{}
	for k, v := range values {
		data[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      AppSecretName(app),
			"namespace": SystemNamespace,
			"labels": map[string]any{
				AppLabel:   app,
				WatchLabel: "Enabled",
			},
		},
		"type": "Opaque",
		"data": data,
	}}
}

// AppProvider returns the ResourceSetInputProvider that makes the platform deploy an app.
func AppProvider(o AppOptions) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "fluxcd.controlplane.io/v1",
		"kind":       "ResourceSetInputProvider",
		"metadata": map[string]any{
			"name":      o.Name,
			"namespace": SystemNamespace,
			"labels":    map[string]any{AppLabel: o.Name},
		},
		"spec": map[string]any{
			"type": "Static",
			"defaultValues": map[string]any{
				"name":     o.Name,
				"url":      o.Artifact.URL,
				"tag":      o.Artifact.Tag,
				"insecure": o.Insecure,
			},
		},
	}}
}

// AppSecrets returns the stored secret values of an app; none if the app has no Secret yet.
func AppSecrets(ctx context.Context, cfg *rest.Config, app string) (map[string]string, error) {
	c, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	obj, err := c.get(ctx, ref{gvk: secretGVK, namespace: SystemNamespace, name: AppSecretName(app)})
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	if obj == nil {
		return values, nil
	}
	data, _, _ := unstructured.NestedStringMap(obj.Object, "data")
	for k, v := range data {
		decoded, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("secret %s/%s, key %s: %w", SystemNamespace, AppSecretName(app), k, err)
		}
		values[k] = string(decoded)
	}
	return values, nil
}

// AddApp stores the app's secrets and provider, then waits until Flux has deployed the current
// artifact and the HelmRelease is ready. Running it again updates both and waits again.
func AddApp(ctx context.Context, cfg *rest.Config, o AppOptions) error {
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	c, err := newClient(cfg)
	if err != nil {
		return err
	}
	rep := progress.OrDiscard(o.Report)

	for _, obj := range []*unstructured.Unstructured{AppSecret(o.Name, o.Secrets), AppProvider(o)} {
		if err := c.applyReport(ctx, rep, obj); err != nil {
			return err
		}
	}

	return c.rollOut(ctx, rep, o.Name, o.Artifact)
}

// Redeploy asks Flux to fetch the app's artifact again and to roll out what it finds, and waits
// until it runs. It changes nothing: it is the answer to "the tag moved" and to "try that
// again", and the artifact reference it uses is the one the app is registered with.
func Redeploy(ctx context.Context, cfg *rest.Config, app string, timeout time.Duration,
	report progress.Reporter) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c, err := newClient(cfg)
	if err != nil {
		return err
	}
	provider, err := c.get(ctx, ref{gvk: providerGVK, namespace: SystemNamespace, name: app})
	if err != nil {
		return err
	}
	if provider == nil {
		return fmt.Errorf("app %s does not exist", app)
	}
	values, _, _ := unstructured.NestedMap(provider.Object, "spec", "defaultValues")
	url, _ := values["url"].(string)
	tag, _ := values["tag"].(string)
	return c.rollOut(ctx, progress.OrDiscard(report), app, Artifact{URL: url, Tag: tag})
}

// rollOut waits for the chain from the deploy artifact to the running release, asking each
// controller to act now instead of at its next interval. It is the same wait whether the app
// was just registered, changed, or only asked to try again.
func (c *client) rollOut(ctx context.Context, rep progress.Reporter, name string, artifact Artifact) error {
	repo := ref{gvk: ociRepositoryGVK, namespace: name, name: deployName}
	var revision string
	err := c.step(ctx, rep, "the deploy artifact", func(ctx context.Context) (string, error) {
		// The ResourceSet may still be creating the OCIRepository, or updating its URL.
		err := c.waitFor(ctx, repo, func(obj *unstructured.Unstructured) (bool, string, error) {
			url, _, _ := unstructured.NestedString(obj.Object, "spec", "url")
			tag, _, _ := unstructured.NestedString(obj.Object, "spec", "ref", "tag")
			return url == artifact.URL && tag == artifact.Tag, "waiting for the platform to create the app", nil
		})
		if err != nil {
			return "", err
		}
		obj, err := c.reconcileAndWait(ctx, repo, failOnAuthError(readyCondition))
		if err != nil {
			return "", err
		}
		revision, _, _ = unstructured.NestedString(obj.Object, "status", "artifact", "revision")
		return revision, nil
	})
	if err != nil {
		return err
	}

	ks := ref{gvk: kustomizationGVK, namespace: name, name: deployName}
	err = c.step(ctx, rep, "the app values", func(ctx context.Context) (string, error) {
		return "", c.waitFor(ctx, ks, appliedRevision(revision))
	})
	if err != nil {
		return err
	}

	chart := ref{gvk: ociRepositoryGVK, namespace: name, name: chartName}
	release := ref{gvk: helmReleaseGVK, namespace: name, name: name}
	return c.step(ctx, rep, "the app", func(ctx context.Context) (string, error) {
		if err := c.waitFor(ctx, chart, readyCondition); err != nil {
			return "", err
		}
		obj, err := c.reconcileAndWait(ctx, release, readyCondition)
		if err != nil {
			return "", err
		}
		_, msg, _ := readyCondition(obj)
		return msg, nil
	})
}

// RemoveApp deletes the app's provider, waits until the platform has removed the app namespace
// with everything in it, and deletes the app's Secret. It reports whether anything existed.
func RemoveApp(ctx context.Context, cfg *rest.Config, app string, timeout time.Duration,
	report progress.Reporter) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c, err := newClient(cfg)
	if err != nil {
		return false, err
	}
	rep := progress.OrDiscard(report)
	found := false
	provider := ref{gvk: providerGVK, namespace: SystemNamespace, name: app}
	deleted, err := c.delete(ctx, provider)
	if err != nil {
		return false, err
	}
	if deleted {
		found = true
		rep.Report(progress.Applied(provider.String(), "deleted", ""))
	}

	ns := ref{gvk: namespaceGVK, name: app}
	err = c.step(ctx, rep, "namespace "+app+" to be deleted", func(ctx context.Context) (string, error) {
		return "", wait.PollUntilContextCancel(ctx, pollInterval, true, func(ctx context.Context) (bool, error) {
			obj, err := c.get(ctx, ns)
			if err != nil {
				return false, err
			}
			if obj != nil {
				found = true
				// Only the platform may delete the namespace, and only if it created it.
				if labels := obj.GetLabels(); labels[AppLabel] != app {
					return false, fmt.Errorf("namespace %s exists but does not belong to app %s", app, app)
				}
			}
			return obj == nil, nil
		})
	})
	if err != nil {
		if ctx.Err() != nil {
			return found, fmt.Errorf("namespace %s is still there; check the HelmRelease and ResourceSet in the cluster", app)
		}
		return found, err
	}

	secret := ref{gvk: secretGVK, namespace: SystemNamespace, name: AppSecretName(app)}
	deleted, err = c.delete(ctx, secret)
	if err != nil {
		return found, err
	}
	if deleted {
		found = true
		rep.Report(progress.Applied(secret.String(), "deleted", ""))
	}
	return found, nil
}

// reconcileAndWait asks the controller of a Flux object to reconcile now and waits until it has
// handled that request and is ready. It returns the object in that state.
func (c *client) reconcileAndWait(ctx context.Context, r ref, ready readyFunc) (*unstructured.Unstructured, error) {
	token := time.Now().UTC().Format(time.RFC3339Nano)
	if err := c.annotate(ctx, r, reconcileAnnotation, token); err != nil {
		return nil, err
	}
	var last *unstructured.Unstructured
	err := c.waitFor(ctx, r, func(obj *unstructured.Unstructured) (bool, string, error) {
		handled, _, _ := unstructured.NestedString(obj.Object, "status", "lastHandledReconcileAt")
		if handled != token {
			return false, "waiting for Flux to handle the change", nil
		}
		last = obj
		return ready(obj)
	})
	return last, err
}

// delete removes an object in the foreground and reports whether it existed.
func (c *client) delete(ctx context.Context, r ref) (bool, error) {
	ri, err := c.resource(r.gvk, r.namespace)
	if err != nil {
		return false, err
	}
	policy := metav1.DeletePropagationForeground
	err = ri.Delete(ctx, r.name, metav1.DeleteOptions{PropagationPolicy: &policy})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("deleting %s: %w", r, err)
	}
	return true, nil
}
