package cluster

import (
	"cmp"
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
	if err := c.warnAboutOldHosts(ctx, rep, current, settings); err != nil {
		return err
	}
	for _, obj := range ConfigObjects(settings) {
		if err := c.applyReport(ctx, rep, obj); err != nil {
			return err
		}
	}
	// Before the platform that expects a login per app is applied.
	if err := c.migrateSharedCredentials(ctx, rep); err != nil {
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

// The objects through which, until Phase 8b, one registry login and one tunnel served every app.
const (
	sharedRegistrySecret = "registry"
	sharedTunnelSecret   = "tunnel"
	sharedExposeProvider = "expose"
)

// SharedRegistryConnection is the name the registry login all apps used to share gets as a
// registry connection.
const SharedRegistryConnection = "ghcr"

// migrateSharedCredentials takes a cluster from one set of credentials for all apps to
// connections the apps choose. The shared registry login becomes the registry connection "ghcr",
// and every app that was registered before gets it, so the apps keep pulling. The shared tunnel
// cannot move: it belongs to a Cloudflare account whose token shelf never stored, so it is
// switched off, and the apps have to be exposed again one by one. It also writes the Secret that
// apps without a registry connection pull with.
func (c *client) migrateSharedCredentials(ctx context.Context, rep progress.Reporter) error {
	anonymous, err := AnonymousRegistrySecret()
	if err != nil {
		return err
	}
	if err := c.applyReport(ctx, rep, anonymous); err != nil {
		return err
	}

	shared, err := c.get(ctx, ref{gvk: secretGVK, namespace: SystemNamespace, name: sharedRegistrySecret})
	if err != nil {
		return err
	}
	inherited := ""
	if shared != nil {
		login, err := registryLogin(shared)
		if err != nil {
			return err
		}
		if login != nil {
			secret, err := RegistryConnectionSecret(SharedRegistryConnection, *login)
			if err != nil {
				return err
			}
			action, err := c.apply(ctx, secret)
			if err != nil {
				return err
			}
			rep.Report(progress.Applied(describe(secret), string(action), "from the login all apps used to share"))
			inherited = SharedRegistryConnection
		}
	}

	providers, err := c.list(ctx, providerGVK, SystemNamespace, AppLabel)
	if err != nil {
		return err
	}
	for _, p := range providers {
		// The platform refers to every input, so an app registered before the inputs existed
		// gets them: the shared login as its connection, the cluster's domain and no tunnel.
		values, _, _ := unstructured.NestedMap(p.Object, "spec", "defaultValues")
		missing := false
		for _, key := range []string{"domain", "tunnel", "registry", "cloudflare"} {
			if _, ok := values[key]; !ok {
				missing = true
			}
		}
		if !missing {
			continue
		}
		state := providerState(p)
		registry := state.Registry
		if _, ok := values["registry"]; !ok {
			registry = inherited
		}
		if err := c.applyReport(ctx, rep, AppProvider(AppOptions{
			Name: state.Name, Artifact: state.Artifact, Insecure: state.Insecure,
			Domain: state.Domain, TunnelID: state.Tunnel, Registry: registry, Cloudflare: state.Cloudflare,
		})); err != nil {
			return err
		}
	}
	if shared != nil {
		if err := c.deleteReport(ctx, rep, refOf(shared)); err != nil {
			return err
		}
	}

	provider := ref{gvk: providerGVK, namespace: SystemNamespace, name: sharedExposeProvider}
	exposed, err := c.get(ctx, provider)
	if err != nil {
		return err
	}
	if exposed != nil {
		if err := c.deleteReport(ctx, rep, provider); err != nil {
			return err
		}
		rep.Report(progress.Warning("The tunnel all apps used to share is switched off; every app now has a tunnel of its own.\n" +
			"Expose each app again with a Cloudflare connection (`shelf connection add cloudflare <name>`,\n" +
			"then `shelf app credentials <app> --cloudflare <name>`). The old tunnel and the DNS records\n" +
			"that point at it stay in Cloudflare; delete them there.\n"))
	}
	return c.deleteReport(ctx, rep, ref{gvk: secretGVK, namespace: SystemNamespace, name: sharedTunnelSecret})
}

// warnAboutOldHosts points out the DNS records apps keep under their previous name when the
// domain or the host suffix changes.
func (c *client) warnAboutOldHosts(ctx context.Context, rep progress.Reporter, before, after Settings) error {
	if !hostsChange(before, after) {
		return nil
	}
	providers, err := c.list(ctx, providerGVK, SystemNamespace, AppLabel)
	if err != nil {
		return err
	}
	apps := make([]AppState, 0, len(providers))
	for _, p := range providers {
		apps = append(apps, providerState(p))
	}
	if warning := OldHostWarning(MovingApps(before, after, apps)); warning != "" {
		rep.Report(progress.Warning(warning))
	}
	return nil
}

// HostsChange reports whether apps answer under a different name after the change. It is only
// true when the cluster had a domain before: the first installation gives the apps their names,
// it does not move them.
func HostsChange(before, after Settings) bool { return hostsChange(before, after) }

// hostsChange reports whether apps answer under a different name after the change.
func hostsChange(before, after Settings) bool {
	return before.Domain != "" && (before.Domain != after.Domain || before.HostSuffix != after.HostSuffix)
}

// HostMove is an app that answers under a different name after the cluster settings change.
type HostMove struct {
	App  string
	From string
	To   string
	// Exposed means the old name has a DNS record, which stays behind.
	Exposed bool
}

// MovingApps returns the apps a change of the cluster settings gives a different host name: all
// of them when the host suffix changes, and those without a domain of their own when the
// cluster's domain does.
func MovingApps(before, after Settings, apps []AppState) []HostMove {
	if !hostsChange(before, after) {
		return nil
	}
	var moves []HostMove
	for _, app := range apps {
		from := app.Name + before.HostSuffix + "." + cmp.Or(app.Domain, before.Domain)
		to := app.Name + after.HostSuffix + "." + cmp.Or(app.Domain, after.Domain)
		if from != to {
			moves = append(moves, HostMove{App: app.Name, From: from, To: to, Exposed: app.Tunnel != ""})
		}
	}
	return moves
}

// OldHostWarning names the DNS records the moved apps keep under their previous host names.
// shelf only knows the current names, so it cannot remove those records by itself.
func OldHostWarning(moves []HostMove) string {
	var b strings.Builder
	for _, m := range moves {
		if !m.Exposed {
			continue
		}
		if b.Len() == 0 {
			fmt.Fprintln(&b, "These apps move to a different name. Their records under the old name stay behind;")
			fmt.Fprintln(&b, "delete them in Cloudflare:")
		}
		fmt.Fprintf(&b, "  %s (now %s)\n", m.From, m.To)
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
	obj, err := c.reconcileAndWait(ctx, repo, failOnAuthError(readyCondition,
		"the platform artifact is read without a login, so it has to be public"))
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
