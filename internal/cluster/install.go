package cluster

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
)

// Options configure Install.
type Options struct {
	Platform Artifact
	Settings Settings
	// Registry replaces the stored registry credential when set. When nil, an existing
	// credential is kept, and an empty one is created if there is none.
	Registry *RegistryAuth
	// Timeout bounds the whole installation, including all waits.
	Timeout time.Duration
	// Out receives progress messages.
	Out io.Writer
}

var (
	ociRepositoryGVK = schema.GroupVersionKind{Group: "source.toolkit.fluxcd.io", Version: "v1", Kind: "OCIRepository"}
	kustomizationGVK = schema.GroupVersionKind{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Kind: "Kustomization"}
)

// Install installs or updates the Flux Operator and the FluxInstance, and waits until Flux has
// applied the platform artifact and everything in it is ready. It is idempotent: a second run
// reports every object as unchanged.
func Install(ctx context.Context, cfg *rest.Config, opts Options) error {
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	c, err := newClient(cfg)
	if err != nil {
		return err
	}
	out := opts.Out

	objs, err := FluxOperatorObjects()
	if err != nil {
		return err
	}
	first, rest := installOrder(objs)
	actions := map[Action]int{}
	if err := c.applyAll(ctx, first, actions); err != nil {
		return err
	}
	for _, obj := range first {
		if err := c.waitFor(ctx, refOf(obj), current); err != nil {
			return err
		}
	}
	// The CRDs are new API types; forget what discovery knew before.
	c.mapper.Reset()
	if err := c.applyAll(ctx, rest, actions); err != nil {
		return err
	}
	fmt.Fprintf(out, "Flux Operator %s: %d objects, %s\n", FluxOperatorVersion, len(objs), summarize(actions))
	err = c.step(ctx, out, "the operator", func(ctx context.Context) (string, error) {
		for _, obj := range rest {
			if obj.GetKind() == "Deployment" {
				if err := c.waitFor(ctx, refOf(obj), current); err != nil {
					return "", err
				}
			}
		}
		return "", nil
	})
	if err != nil {
		return err
	}

	settings := opts.Settings
	if settings.TunnelTarget == "" {
		// `shelf init expose` writes the tunnel target; a later `init cluster` keeps it.
		current, err := c.settings(ctx)
		if err != nil {
			return err
		}
		settings.TunnelTarget = current.TunnelTarget
	}
	for _, obj := range ConfigObjects(settings) {
		action, err := c.apply(ctx, obj)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s: %s\n", describe(obj), action)
	}
	if err := c.ensureRegistrySecret(ctx, out, opts.Registry); err != nil {
		return err
	}

	instance := FluxInstance(opts.Platform, opts.Settings.InsecureRegistry)
	action, err := c.apply(ctx, instance)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Flux %s (%s): %s\n", FluxVersion, describe(instance), action)
	err = c.step(ctx, out, "Flux", func(ctx context.Context) (string, error) {
		return "", c.waitFor(ctx, refOf(instance), readyCondition)
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Platform %s\n", opts.Platform)
	return c.step(ctx, out, "the platform", func(ctx context.Context) (string, error) {
		return c.waitForPlatform(ctx, opts.Platform)
	})
}

// ensureRegistrySecret writes the registry credential if one is given or none exists yet. It
// never prints the credential.
func (c *client) ensureRegistrySecret(ctx context.Context, out io.Writer, auth *RegistryAuth) error {
	secret, err := RegistrySecret(auth)
	if err != nil {
		return err
	}
	if auth == nil {
		existing, err := c.get(ctx, refOf(secret))
		if err != nil {
			return err
		}
		if existing != nil {
			fmt.Fprintf(out, "%s: kept (no GHCR_TOKEN given)\n", describe(secret))
			return nil
		}
	}
	action, err := c.apply(ctx, secret)
	if err != nil {
		return err
	}
	login := "without a login"
	if auth != nil {
		login = "for " + auth.Username + "@" + RegistryHost
	}
	fmt.Fprintf(out, "%s: %s, %s\n", describe(secret), action, login)
	return nil
}

func (c *client) applyAll(ctx context.Context, objs []*unstructured.Unstructured, actions map[Action]int) error {
	for _, obj := range objs {
		action, err := c.apply(ctx, obj)
		if err != nil {
			return err
		}
		actions[action]++
	}
	return nil
}

func summarize(actions map[Action]int) string {
	var parts []string
	for _, a := range slices.Sorted(maps.Keys(actions)) {
		parts = append(parts, fmt.Sprintf("%d %s", actions[a], a))
	}
	return strings.Join(parts, ", ")
}

// step runs a wait and reports how long it took and, if given, a detail.
func (c *client) step(ctx context.Context, out io.Writer, what string, wait func(context.Context) (string, error)) error {
	fmt.Fprintf(out, "  waiting for %s ... ", what)
	start := time.Now()
	detail, err := wait(ctx)
	if err != nil {
		fmt.Fprintln(out, "failed")
		return err
	}
	elapsed := time.Since(start).Round(time.Second)
	if detail != "" {
		fmt.Fprintf(out, "done after %s: %s\n", elapsed, detail)
	} else {
		fmt.Fprintf(out, "done after %s\n", elapsed)
	}
	return nil
}

// reconcileAnnotation asks a Flux controller to reconcile now; it confirms in
// status.lastHandledReconcileAt. The flux CLI uses the same mechanism.
const reconcileAnnotation = "reconcile.fluxcd.io/requestedAt"

// waitForPlatform waits until the sync OCIRepository points at p and has fetched it, and the
// sync Kustomization has applied exactly that revision and everything in it is ready.
//
// A tag can move (the dev registry reuses "dev"), so shelf requests a reconciliation of the
// OCIRepository and waits until Flux handled that request. Checking the applied revision keeps
// a Kustomization that is still ready from an earlier artifact from passing.
func (c *client) waitForPlatform(ctx context.Context, p Artifact) (string, error) {
	repo := ref{gvk: ociRepositoryGVK, namespace: FluxNamespace, name: PlatformSyncName}
	err := c.waitFor(ctx, repo, func(obj *unstructured.Unstructured) (bool, string, error) {
		url, _, _ := unstructured.NestedString(obj.Object, "spec", "url")
		tag, _, _ := unstructured.NestedString(obj.Object, "spec", "ref", "tag")
		if url != p.URL || tag != p.Tag {
			return false, fmt.Sprintf("still points at %s:%s", url, tag), nil
		}
		return true, "", nil
	})
	if err != nil {
		return "", err
	}
	obj, err := c.reconcileAndWait(ctx, repo, failOnAuthError(readyCondition))
	if err != nil {
		return "", err
	}
	revision, _, _ := unstructured.NestedString(obj.Object, "status", "artifact", "revision")
	if revision == "" {
		return "", fmt.Errorf("%s has no artifact revision", repo)
	}

	ks := ref{gvk: kustomizationGVK, namespace: FluxNamespace, name: PlatformSyncName}
	err = c.waitFor(ctx, ks, appliedRevision(revision))
	if err != nil {
		return "", err
	}
	return "applied " + revision, nil
}

// appliedRevision is ready when a Kustomization is ready and has applied exactly revision, so a
// Kustomization that is still ready from an earlier artifact does not pass.
func appliedRevision(revision string) readyFunc {
	return func(obj *unstructured.Unstructured) (bool, string, error) {
		applied, _, _ := unstructured.NestedString(obj.Object, "status", "lastAppliedRevision")
		ok, msg, err := readyCondition(obj)
		if ok && applied != revision {
			return false, "applied " + applied + ", waiting for " + revision, nil
		}
		return ok, msg, err
	}
}
