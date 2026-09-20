package cluster

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"

	"github.com/tweinmann/shelf/internal/progress"
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
	// Report receives what happens; nil reports nothing.
	Report progress.Reporter
}

var (
	ociRepositoryGVK = schema.GroupVersionKind{Group: "source.toolkit.fluxcd.io", Version: "v1", Kind: "OCIRepository"}
	kustomizationGVK = schema.GroupVersionKind{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Kind: "Kustomization"}
)

// platformSync is the Kustomization the Flux Operator creates for the platform artifact. It
// substitutes the cluster settings, so it has to run again after they change.
var platformSync = ref{gvk: kustomizationGVK, namespace: FluxNamespace, name: PlatformSyncName}

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
	rep := progress.OrDiscard(opts.Report)

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
	rep.Report(progress.Info("Flux Operator %s: %d objects, %s", FluxOperatorVersion, len(objs), summarize(actions)))
	err = c.step(ctx, rep, "the operator", func(ctx context.Context) (string, error) {
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
	current, err := c.settings(ctx)
	if err != nil {
		return err
	}
	if settings.TunnelTarget == "" {
		// `shelf init expose` writes the tunnel target; a later `init cluster` keeps it.
		settings.TunnelTarget = current.TunnelTarget
	}
	if err := c.warnAboutOldHosts(ctx, rep, current, settings); err != nil {
		return err
	}
	for _, obj := range ConfigObjects(settings) {
		if err := c.applyReport(ctx, rep, obj); err != nil {
			return err
		}
	}
	if err := c.ensureRegistrySecret(ctx, rep, opts.Registry); err != nil {
		return err
	}

	instance := FluxInstance(opts.Platform, opts.Settings.InsecureRegistry)
	action, err := c.apply(ctx, instance)
	if err != nil {
		return err
	}
	rep.Report(progress.Info("Flux %s (%s): %s", FluxVersion, describe(instance), action))
	err = c.step(ctx, rep, "Flux", func(ctx context.Context) (string, error) {
		return "", c.waitFor(ctx, refOf(instance), readyCondition)
	})
	if err != nil {
		return err
	}

	rep.Report(progress.Info("Platform %s", opts.Platform))
	return c.step(ctx, rep, "the platform", func(ctx context.Context) (string, error) {
		return c.waitForPlatform(ctx, opts.Platform)
	})
}

// ensureRegistrySecret writes the registry credential if one is given or none exists yet. It
// never prints the credential.
func (c *client) ensureRegistrySecret(ctx context.Context, rep progress.Reporter, auth *RegistryAuth) error {
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
			rep.Report(progress.Applied(describe(secret), "kept (no GHCR_TOKEN given)", ""))
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
	rep.Report(progress.Applied(describe(secret), string(action), login))
	return nil
}

// warnAboutOldHosts points out the DNS records an app keeps under its previous name when the
// domain or the host suffix changes.
func (c *client) warnAboutOldHosts(ctx context.Context, rep progress.Reporter, before, after Settings) error {
	if !hostsChange(before, after) {
		return nil
	}
	apps, err := c.appNames(ctx)
	if err != nil {
		return err
	}
	if warning := OldHostWarning(before, after, apps); warning != "" {
		rep.Report(progress.Warning(warning))
	}
	return nil
}

// hostsChange reports whether apps answer under a different name after the change.
func hostsChange(before, after Settings) bool {
	return before.Domain != "" && (before.Domain != after.Domain || before.HostSuffix != after.HostSuffix)
}

// OldHostWarning names the DNS records the apps keep under their previous host names. shelf
// only knows the current names, so it cannot remove those records by itself.
func OldHostWarning(before, after Settings, apps []string) string {
	if !hostsChange(before, after) || len(apps) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "The apps move from %s to %s.\n",
		"<app>"+before.HostSuffix+"."+before.Domain, "<app>"+after.HostSuffix+"."+after.Domain)
	fmt.Fprintln(&b, "Their records under the old name stay behind; delete them in Cloudflare:")
	for _, app := range apps {
		fmt.Fprintf(&b, "  %s%s.%s\n", app, before.HostSuffix, before.Domain)
	}
	return b.String()
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
func (c *client) step(ctx context.Context, rep progress.Reporter, what string,
	wait func(context.Context) (string, error)) error {
	rep.Report(progress.Step(what))
	start := time.Now()
	detail, err := wait(ctx)
	if err != nil {
		rep.Report(progress.StepFailed(what))
		return err
	}
	rep.Report(progress.StepDone(what, time.Since(start).Round(time.Second), detail))
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

	// The settings are substituted into the platform, so a changed ConfigMap has to be applied
	// even when the artifact itself did not change.
	if _, err := c.reconcileAndWait(ctx, platformSync, appliedRevision(revision)); err != nil {
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
