package cluster

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/tweinmann/shelf/internal/schema"
)

const (
	// PreviewsLabel marks the provider of an app's previews, with the app's name. It is not
	// AppLabel: the ResourceSet apps would make an app of it.
	PreviewsLabel = "shelf.dev/previews"
	// PreviewLabel marks a preview's namespace, with the branch slug.
	PreviewLabel = "shelf.dev/preview"
	// previewInterval is how often the platform looks for new branch tags, and how often a
	// preview's deploy artifact is checked: an agent waits for both.
	previewInterval = "30s"
)

// PreviewProviderName is the provider of an app's previews. "--" cannot occur in an app's name,
// so this cannot be another app's provider.
func PreviewProviderName(app string) string { return app + "--previews" }

// PreviewNamespace is where the preview of a branch slug runs: <app>--<slug>.
func PreviewNamespace(app, slug string) string { return app + "--" + slug }

// PreviewProvider returns the provider that makes the platform run a preview for each branch
// tag of the app's deploy artifact, at most o.Previews of them, read with the app's registry
// login.
func PreviewProvider(o AppOptions) *unstructured.Unstructured {
	spec := map[string]any{
		"type": "OCIArtifactTag",
		"url":  o.Artifact.URL,
		"filter": map[string]any{
			"includeTag": "^" + schema.BranchTagPrefix,
			"limit":      int64(o.Previews),
		},
		"defaultValues": map[string]any{
			"app":      o.Name,
			"url":      o.Artifact.URL,
			"insecure": o.Insecure,
			"registry": o.Registry,
		},
	}
	if o.Insecure {
		spec["insecure"] = true
	}
	if o.Registry != "" {
		spec["secretRef"] = map[string]any{"name": RegistryConnectionSecretName(o.Registry)}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "fluxcd.controlplane.io/v1",
		"kind":       "ResourceSetInputProvider",
		"metadata": map[string]any{
			"name":        PreviewProviderName(o.Name),
			"namespace":   SystemNamespace,
			"labels":      map[string]any{PreviewsLabel: o.Name},
			"annotations": map[string]any{"fluxcd.controlplane.io/reconcileEvery": previewInterval},
		},
		"spec": spec,
	}}
}

// previewLimit is the number of previews a provider allows, 0 for none.
func previewLimit(provider *unstructured.Unstructured) int {
	if provider == nil {
		return 0
	}
	limit, found, _ := unstructured.NestedInt64(provider.Object, "spec", "filter", "limit")
	if !found {
		return 0
	}
	return int(limit)
}

// Preview is one preview of an app as it runs in the cluster.
type Preview struct {
	AppState
	// App is the app this is a preview of; AppState.Name is the preview's namespace.
	App string `json:"app"`
	// Slug is the branch slug, the part of the name after "--".
	Slug string `json:"slug"`
}

// previewLimits returns, per app, how many previews it may have; an app without previews is
// not in the map.
func (c *client) previewLimits(ctx context.Context) (map[string]int, error) {
	providers, err := c.list(ctx, providerGVK, SystemNamespace, PreviewsLabel)
	if err != nil {
		return nil, err
	}
	limits := map[string]int{}
	for _, p := range providers {
		if app := p.GetLabels()[PreviewsLabel]; p.GetName() == PreviewProviderName(app) {
			limits[app] = previewLimit(p)
		}
	}
	return limits, nil
}

// previewState is what a preview's objects say about it: its artifact as the source reads it,
// and its phase as the diagnosis finds it.
func previewState(name string, o *appObjects) AppState {
	state := AppState{Name: name}
	if o.source != nil {
		state.Artifact.URL, _, _ = unstructured.NestedString(o.source.Object, "spec", "url")
		state.Artifact.Tag, _, _ = unstructured.NestedString(o.source.Object, "spec", "ref", "tag")
		state.Revision, _, _ = unstructured.NestedString(o.source.Object, "status", "artifact", "revision")
		state.Commit = artifactCommit(o.source)
	}
	if o.release != nil {
		state.Deployed = lastDeployed(o.release)
	}
	applyDiagnosis(&state, diagnose(name, o))
	return state
}

// previewNamespaces adds the namespaces of previews to the objects of the apps, so that a
// preview is diagnosed, read and waited for like an app, by its namespace.
func (c *client) previewNamespaces(ctx context.Context, apps map[string]*appObjects) error {
	namespaces, err := c.list(ctx, namespaceGVK, "", PreviewLabel)
	if err != nil {
		return err
	}
	for _, ns := range namespaces {
		labels := ns.GetLabels()
		app, slug := labels[AppLabel], labels[PreviewLabel]
		if app == "" || ns.GetName() != PreviewNamespace(app, slug) {
			continue
		}
		apps[ns.GetName()] = &appObjects{previewOf: app, slug: slug}
	}
	return nil
}

// PlatformNeeds is what an operation needs the platform in the cluster to do.
type PlatformNeeds struct {
	// Quick: run quick tunnels.
	Quick bool
	// Previews: run previews.
	Previews bool
}

// previewsMissing is the refusal for a platform that cannot run previews.
func previewsMissing() error {
	return fmt.Errorf("the platform in this cluster predates previews; install the platform of " +
		"this shelf with `shelf init cluster` first, or leave out --previews")
}
