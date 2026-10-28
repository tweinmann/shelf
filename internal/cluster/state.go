package cluster

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
)

var (
	podGVK   = schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
	eventGVK = schema.GroupVersionKind{Version: "v1", Kind: "Event"}
)

// Phase is how far an app, or one step towards it, has got.
type Phase string

const (
	// PhaseReady means this is done and healthy.
	PhaseReady Phase = "Ready"
	// PhaseWorking means a controller is still on it; waiting is the right response.
	PhaseWorking Phase = "Working"
	// PhaseFailed means it will not fix itself.
	PhaseFailed Phase = "Failed"
	// PhaseMissing means the object does not exist yet.
	PhaseMissing Phase = "Missing"
)

// Stage is one step of the chain from the deploy artifact to running pods. The steps are the
// same ones `shelf app add` waits for, which is why a failing app can be explained by naming
// the first stage that is not ready.
type Stage struct {
	// Name is what the step is called in the output of `shelf app add`.
	Name string `json:"name"`
	// Object is the Kubernetes object that carries this step, for anyone who wants to look.
	Object string `json:"object"`
	Phase  Phase  `json:"phase"`
	// Reason is the short reason a controller gives, such as ArtifactFailed.
	Reason string `json:"reason,omitempty"`
	// Message explains it in the controller's own words.
	Message string `json:"message,omitempty"`
	// Hint is shelf's explanation of what this means, if it has one.
	Hint string `json:"hint,omitempty"`
}

// Diagnosis is the whole chain of an app, in the order the steps happen.
type Diagnosis struct {
	Name   string  `json:"name"`
	Stages []Stage `json:"stages"`
}

// Trouble returns the first stage that is not ready, or nil when the app is healthy.
func (d Diagnosis) Trouble() *Stage {
	for i, s := range d.Stages {
		if s.Phase != PhaseReady {
			return &d.Stages[i]
		}
	}
	return nil
}

// Phase is the state of the app: that of the first stage that is not ready.
func (d Diagnosis) Phase() Phase {
	if t := d.Trouble(); t != nil {
		return t.Phase
	}
	return PhaseReady
}

// AppState is what an app looks like in the cluster, for a list of all of them.
type AppState struct {
	Name     string   `json:"name"`
	Artifact Artifact `json:"artifact"`
	Insecure bool     `json:"insecure,omitempty"`
	// Domain is the app's own domain, empty when it answers at <app>.shelf.internal.
	Domain string `json:"domain,omitempty"`
	// Tunnel is the app's Cloudflare tunnel, empty when the app has none of its own.
	Tunnel string `json:"tunnel,omitempty"`
	// Quick means the app is exposed through a quick tunnel.
	Quick bool `json:"quick,omitempty"`
	// QuickURL is the current address of that quick tunnel, empty while cloudflared has none.
	QuickURL string `json:"quickURL,omitempty"`
	// Registry is the app's registry connection, empty when it pulls without a login.
	Registry string `json:"registry,omitempty"`
	// Cloudflare is the app's Cloudflare connection, empty when it is not exposed.
	Cloudflare string `json:"cloudflare,omitempty"`
	// Revision is the deploy artifact that is applied, e.g. main@sha256:1f4e….
	Revision string `json:"revision,omitempty"`
	// Commit is the git commit the deploy artifact was built from, empty when the artifact does
	// not name one.
	Commit string `json:"commit,omitempty"`
	// Deployed is when the release last changed.
	Deployed time.Time `json:"deployed,omitzero"`
	Phase    Phase     `json:"phase"`
	// Reason is why the phase is not Ready, in one line.
	Reason string `json:"reason,omitempty"`
	// Stage is the step that is not ready, empty when the app is healthy.
	Stage string `json:"stage,omitempty"`
	// PreviewLimit is how many previews the app may have, 0 when it has none switched on.
	PreviewLimit int `json:"previewLimit,omitempty"`
	// Previews are the app's previews that exist, ordered by name.
	Previews []Preview `json:"previews,omitempty"`
}

// appObjects are the objects that carry one app.
type appObjects struct {
	// previewOf and slug are set for the namespace of a preview, which has no provider of its
	// own: the provider of the app's previews stands for all of them.
	previewOf     string
	slug          string
	provider      *unstructured.Unstructured
	source        *unstructured.Unstructured
	kustomization *unstructured.Unstructured
	chart         *unstructured.Unstructured
	release       *unstructured.Unstructured
	pods          []*unstructured.Unstructured
	// refusals are the FailedCreate events in the namespace: a pod the API server did not
	// create, because the admission webhook or a quota refused it, never shows up as a pod.
	refusals []*unstructured.Unstructured
}

// NotFoundError means there is no app, or no preview, of that name in the cluster.
type NotFoundError struct{ Name string }

func (e *NotFoundError) Error() string { return "app " + e.Name + " does not exist" }

// isApp tells an app from a preview: only an app has a provider of its own.
func (o *appObjects) isApp() bool { return o.provider != nil }

// AppStates returns every registered app with its current state and its previews. It reads the
// whole cluster in nine list calls, however many apps and previews there are, plus two for each
// app with a quick tunnel.
func AppStates(ctx context.Context, cfg *rest.Config) ([]AppState, error) {
	c, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	apps, err := c.appObjects(ctx)
	if err != nil {
		return nil, err
	}
	limits, err := c.previewLimits(ctx)
	if err != nil {
		return nil, err
	}
	previews := map[string][]Preview{}
	for _, name := range slices.Sorted(maps.Keys(apps)) {
		if o := apps[name]; !o.isApp() {
			previews[o.previewOf] = append(previews[o.previewOf],
				Preview{AppState: previewState(name, o), App: o.previewOf, Slug: o.slug})
		}
	}
	states := make([]AppState, 0, len(apps))
	for _, name := range slices.Sorted(maps.Keys(apps)) {
		if !apps[name].isApp() {
			continue
		}
		state := appState(name, apps[name])
		state.PreviewLimit, state.Previews = limits[name], previews[name]
		if state.Quick {
			// A quick tunnel's name exists only in its cloudflared. One that cannot be asked
			// yet has no address, which the page says; it is not a reason to fail the list.
			state.QuickURL, _ = c.quickURL(ctx, name)
		}
		states = append(states, state)
	}
	return states, nil
}

// AppDiagnosis returns the whole chain of one app, including its pods. app may also be the
// namespace of a preview.
func AppDiagnosis(ctx context.Context, cfg *rest.Config, app string) (Diagnosis, error) {
	c, err := newClient(cfg)
	if err != nil {
		return Diagnosis{}, err
	}
	apps, err := c.appObjects(ctx)
	if err != nil {
		return Diagnosis{}, err
	}
	objs, ok := apps[app]
	if !ok {
		return Diagnosis{}, &NotFoundError{Name: app}
	}
	return diagnose(app, objs), nil
}

// appObjects collects everything that belongs to an app, in one pass over the cluster. The
// objects of an app all live in the namespace named after it and have fixed names, so they are
// found by where they are rather than by a label.
func (c *client) appObjects(ctx context.Context) (map[string]*appObjects, error) {
	providers, err := c.list(ctx, providerGVK, SystemNamespace, AppLabel)
	if err != nil {
		return nil, err
	}
	apps := map[string]*appObjects{}
	for _, p := range providers {
		apps[p.GetName()] = &appObjects{provider: p}
	}
	if err := c.previewNamespaces(ctx, apps); err != nil {
		return nil, err
	}
	sources, err := c.list(ctx, ociRepositoryGVK, "", "")
	if err != nil {
		return nil, err
	}
	for _, s := range sources {
		app, ok := apps[s.GetNamespace()]
		if !ok {
			continue
		}
		switch s.GetName() {
		case deployName:
			app.source = s
		case chartName:
			app.chart = s
		}
	}
	kustomizations, err := c.list(ctx, kustomizationGVK, "", "")
	if err != nil {
		return nil, err
	}
	for _, k := range kustomizations {
		if app, ok := apps[k.GetNamespace()]; ok && k.GetName() == deployName {
			app.kustomization = k
		}
	}
	releases, err := c.list(ctx, helmReleaseGVK, "", "")
	if err != nil {
		return nil, err
	}
	for _, r := range releases {
		if app, ok := apps[r.GetNamespace()]; ok && r.GetName() == r.GetNamespace() {
			app.release = r
		}
	}
	pods, err := c.list(ctx, podGVK, "", AppLabel)
	if err != nil {
		return nil, err
	}
	for _, p := range pods {
		if app, ok := apps[p.GetNamespace()]; ok {
			app.pods = append(app.pods, p)
		}
	}
	refusals, err := c.listFields(ctx, eventGVK, "reason=FailedCreate")
	if err != nil {
		return nil, err
	}
	for _, e := range refusals {
		if app, ok := apps[e.GetNamespace()]; ok {
			app.refusals = append(app.refusals, e)
		}
	}
	return apps, nil
}

// list returns the objects of a kind. An empty namespace means every namespace, an empty
// selector every object.
func (c *client) list(ctx context.Context, gvk schema.GroupVersionKind, namespace, selector string) (
	[]*unstructured.Unstructured, error) {
	return c.listWith(ctx, gvk, namespace, metav1.ListOptions{LabelSelector: selector})
}

// listFields returns the objects of a kind in every namespace that match a field selector.
func (c *client) listFields(ctx context.Context, gvk schema.GroupVersionKind, selector string) (
	[]*unstructured.Unstructured, error) {
	return c.listWith(ctx, gvk, "", metav1.ListOptions{FieldSelector: selector})
}

func (c *client) listWith(ctx context.Context, gvk schema.GroupVersionKind, namespace string, opts metav1.ListOptions) (
	[]*unstructured.Unstructured, error) {
	ri, err := c.resource(gvk, namespace)
	if err != nil {
		return nil, err
	}
	list, err := ri.List(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", gvk.Kind, err)
	}
	out := make([]*unstructured.Unstructured, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, &list.Items[i])
	}
	return out, nil
}

// providerState is what the provider of an app says about it: how it is registered, but not
// how it is doing.
func providerState(provider *unstructured.Unstructured) AppState {
	state := AppState{Name: provider.GetName()}
	values, _, _ := unstructured.NestedMap(provider.Object, "spec", "defaultValues")
	url, _ := values["url"].(string)
	tag, _ := values["tag"].(string)
	state.Artifact = Artifact{URL: url, Tag: tag}
	state.Insecure, _ = values["insecure"].(bool)
	state.Domain, _ = values["domain"].(string)
	state.Tunnel, _ = values["tunnel"].(string)
	state.Quick, _ = values["quick"].(bool)
	state.Registry, _ = values["registry"].(string)
	state.Cloudflare, _ = values["cloudflare"].(string)
	return state
}

func appState(name string, o *appObjects) AppState {
	state := providerState(o.provider)
	state.Name = name
	if o.source != nil {
		state.Revision, _, _ = unstructured.NestedString(o.source.Object, "status", "artifact", "revision")
		state.Commit = artifactCommit(o.source)
	}
	if o.release != nil {
		state.Deployed = lastDeployed(o.release)
	}
	applyDiagnosis(&state, diagnose(name, o))
	return state
}

// applyDiagnosis puts the outcome of a diagnosis into a state: the phase, and for anything but
// Ready the stage that is the cause and its reason in one line.
func applyDiagnosis(state *AppState, d Diagnosis) {
	state.Phase = d.Phase()
	if t := d.Trouble(); t != nil {
		state.Stage = t.Name
		state.Reason = strings.TrimSpace(t.Message)
		if state.Reason == "" {
			state.Reason = t.Reason
		}
	}
}

// revisionAnnotation is where `flux push artifact --revision` records the commit an artifact was
// built from. Flux copies the artifact's annotations into the status of the OCIRepository, so
// the cluster knows the commit without asking the forge.
const revisionAnnotation = "org.opencontainers.image.revision"

// gitSHA is a full git commit hash.
var gitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// artifactCommit returns the commit the source's artifact was built from: the revision
// annotation in its `<ref>@sha1:<sha>` form, or a bare commit hash. Anything else names no
// commit, as an artifact pushed with `--source local` and no revision does not.
func artifactCommit(source *unstructured.Unstructured) string {
	value, _, _ := unstructured.NestedString(source.Object, "status", "artifact", "metadata", revisionAnnotation)
	if _, sha, found := strings.Cut(value, "@sha1:"); found {
		value = sha
	}
	if !gitSHA.MatchString(value) {
		return ""
	}
	return value
}

// diagnose walks the chain in the order things have to happen and reports every step. Walking
// in order is what makes the result useful: the first step that is not ready is the cause, and
// everything after it is a consequence.
func diagnose(name string, o *appObjects) Diagnosis {
	d := Diagnosis{Name: name}
	d.Stages = append(d.Stages,
		fluxStage("the deploy artifact", "OCIRepository "+name+"/"+deployName, o.source,
			"the platform has not created the app yet",
			"shelf cannot read the deploy artifact from the registry"),
		fluxStage("the app values", "Kustomization "+name+"/"+deployName, o.kustomization,
			"the platform has not created the app yet",
			"the deploy artifact was rejected; it may hold objects an app may not create"),
		fluxStage("the chart", "OCIRepository "+name+"/"+chartName, o.chart,
			"the platform has not created the app yet",
			"shelf cannot read the shelf-app chart from the registry"),
		fluxStage("the app", "HelmRelease "+name+"/"+name, o.release,
			"the platform has not created the app yet",
			"the app.yaml was rendered but the release did not succeed"),
	)
	d.Stages = append(d.Stages, podStage(o.pods, o.refusals))
	return d
}

// fluxStage reads the Ready condition of a Flux object, which every one of them reports the
// same way.
func fluxStage(name, object string, obj *unstructured.Unstructured, missing, failedHint string) Stage {
	s := Stage{Name: name, Object: object}
	if obj == nil {
		s.Phase, s.Message = PhaseMissing, missing
		return s
	}
	if stalled := condition(obj, "Stalled"); stalled != nil && stalled["status"] == "True" {
		s.Phase = PhaseFailed
		s.Reason, _ = stalled["reason"].(string)
		s.Message, _ = stalled["message"].(string)
		s.Hint = failedHint
		return s
	}
	ready := condition(obj, "Ready")
	if ready == nil {
		s.Phase, s.Message = PhaseWorking, "waiting for the controller"
		return s
	}
	s.Reason, _ = ready["reason"].(string)
	s.Message, _ = ready["message"].(string)
	switch {
	case ready["status"] == "True":
		s.Phase = PhaseReady
	case working(s.Reason) || ready["status"] == "Unknown":
		s.Phase = PhaseWorking
	default:
		s.Phase = PhaseFailed
		s.Hint = failedHint
		// Only a registry can refuse a login, and only the deploy artifact is read with the
		// app's. The same words from the step that applies the artifact mean the opposite: the
		// cluster refused what the artifact asked for.
		if app, found := strings.CutPrefix(s.Object, "OCIRepository "); found && authProblem.MatchString(s.Message) {
			if app, found = strings.CutSuffix(app, "/"+deployName); found {
				s.Hint = "the registry refused to hand out the deploy artifact; " + registryHint(app)
			}
		}
		// Flux fetched the artifact but its signature is not the cluster's signer's.
		if s.Reason == "VerificationError" {
			if strings.HasSuffix(s.Object, "/"+chartName) {
				s.Hint = "the chart is not signed by the cluster's signer; install a released platform and chart"
			} else {
				s.Hint = "the deploy artifact is not signed by the cluster's signer; push again with the " +
					"current workflow, which signs it (its caller needs `id-token: write`)"
			}
		}
	}
	return s
}

// working reports whether a Flux reason means the controller is still trying.
func working(reason string) bool {
	return strings.HasPrefix(reason, "Progressing") || reason == "DependencyNotReady" ||
		reason == "ArtifactOutdated" || reason == "Unknown"
}

// podStage looks at the pods the chart created, because a release can succeed while the
// container it starts does not run at all, or never gets a pod: a refusal newer than every pod
// means a workload is still being refused. An older one was about a pod that exists by now.
func podStage(pods, refusals []*unstructured.Unstructured) Stage {
	s := Stage{Name: "the pods", Object: "Pods"}
	if refused := currentRefusal(pods, refusals); refused != nil {
		involved, _, _ := unstructured.NestedMap(refused.Object, "involvedObject")
		kind, _ := involved["kind"].(string)
		name, _ := involved["name"].(string)
		message, _, _ := unstructured.NestedString(refused.Object, "message")
		return Stage{
			Name: "the pods", Object: kind + " " + refused.GetNamespace() + "/" + name,
			Phase: PhaseFailed, Reason: "FailedCreate", Message: message, Hint: podHint("FailedCreate", message),
		}
	}
	if len(pods) == 0 {
		s.Phase, s.Message = PhaseMissing, "no pods yet"
		return s
	}
	var working []string
	for _, pod := range pods {
		phase, reason, message := podTrouble(pod)
		switch phase {
		case PhaseFailed:
			return Stage{
				Name: "the pods", Object: "Pod " + pod.GetNamespace() + "/" + pod.GetName(),
				Phase: PhaseFailed, Reason: reason, Message: message, Hint: podHint(reason, message),
			}
		case PhaseWorking:
			working = append(working, pod.GetName())
		}
	}
	if len(working) > 0 {
		s.Phase = PhaseWorking
		s.Message = "starting: " + strings.Join(working, ", ")
		return s
	}
	s.Phase = PhaseReady
	s.Message = fmt.Sprintf("%d running", len(pods))
	return s
}

// failedWaiting are the container states that do not fix themselves.
var failedWaiting = []string{
	"ImagePullBackOff", "ErrImagePull", "InvalidImageName",
	"CrashLoopBackOff", "CreateContainerConfigError", "CreateContainerError",
}

// podTrouble reports whether a pod is fine, still starting, or stuck.
func podTrouble(pod *unstructured.Unstructured) (Phase, string, string) {
	phase, _, _ := unstructured.NestedString(pod.Object, "status", "phase")
	if phase == "Succeeded" {
		return PhaseReady, "", ""
	}
	statuses, _, _ := unstructured.NestedSlice(pod.Object, "status", "containerStatuses")
	allReady := len(statuses) > 0
	for _, item := range statuses {
		cs, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if ready, _ := cs["ready"].(bool); !ready {
			allReady = false
		}
		if waiting, ok := nested(cs, "state", "waiting"); ok {
			reason, _ := waiting["reason"].(string)
			if slices.Contains(failedWaiting, reason) {
				message, _ := waiting["message"].(string)
				return PhaseFailed, reason, message
			}
		}
		if terminated, ok := nested(cs, "lastState", "terminated"); ok {
			if reason, _ := terminated["reason"].(string); reason == "OOMKilled" {
				name, _ := cs["name"].(string)
				return PhaseFailed, reason, "container " + name + " ran out of memory"
			}
		}
	}
	if allReady && phase == "Running" {
		return PhaseReady, "", ""
	}
	return PhaseWorking, "", ""
}

// missingSecret is what kubelet says when the chart asks for a secret value that is not in the
// app's Secret. shelf generates those values, and only when an app is deployed: an app.yaml that
// declares a new secret and is then rolled out by Flux alone leaves the key missing.
var missingSecret = regexp.MustCompile(`couldn't find key (\S+) in Secret \S+/` + AppSecretsName)

func podHint(reason, message string) string {
	switch reason {
	case "FailedCreate":
		switch {
		case strings.Contains(message, "policy.sigstore.dev"):
			return "a built image is not signed by the cluster's signer; push again, so that the " +
				"reusable workflow builds and signs it"
		case strings.Contains(message, "exceeded quota"):
			return "the preview's quota is used up; give the components fewer resources, or fewer instances"
		}
		return "the workload cannot create its pods; the message says why"
	case "ImagePullBackOff", "ErrImagePull":
		return "the image cannot be pulled; check the reference and whether the cluster may read that registry"
	case "CrashLoopBackOff":
		return "the container starts and exits again; its log says why"
	case "OOMKilled":
		return "the container needs more memory than its limit allows"
	case "CreateContainerConfigError":
		if m := missingSecret.FindStringSubmatch(message); m != nil {
			return "the app declares the secret " + m[1] + " and shelf has not generated it yet; " +
				"deploy the app again (`shelf app add`, or Deploy on its page) so that it does"
		}
		return "the container cannot be configured, usually a missing secret or config map"
	}
	return ""
}

// currentRefusal returns the newest refusal that no pod came after, or nil.
func currentRefusal(pods, refusals []*unstructured.Unstructured) *unstructured.Unstructured {
	var newestPod string
	for _, p := range pods {
		if created := p.GetCreationTimestamp().UTC().Format(time.RFC3339); created > newestPod {
			newestPod = created
		}
	}
	var found *unstructured.Unstructured
	var foundAt string
	for _, e := range refusals {
		at, _, _ := unstructured.NestedString(e.Object, "lastTimestamp")
		if at == "" {
			at, _, _ = unstructured.NestedString(e.Object, "eventTime")
		}
		if at > newestPod && at > foundAt {
			found, foundAt = e, at
		}
	}
	return found
}

// nested returns a nested map, e.g. state.waiting.
func nested(m map[string]any, keys ...string) (map[string]any, bool) {
	out, found, err := unstructured.NestedMap(m, keys...)
	return out, found && err == nil && len(out) > 0
}

// condition returns the condition of a type, or nil.
func condition(obj *unstructured.Unstructured, name string) map[string]any {
	for _, c := range conditions(obj) {
		if c["type"] == name {
			return c
		}
	}
	return nil
}

// lastDeployed is when the HelmRelease last changed the release.
func lastDeployed(release *unstructured.Unstructured) time.Time {
	history, _, _ := unstructured.NestedSlice(release.Object, "status", "history")
	if len(history) > 0 {
		if entry, ok := history[0].(map[string]any); ok {
			if ts, ok := entry["lastDeployed"].(string); ok {
				if t, err := time.Parse(time.RFC3339, ts); err == nil {
					return t
				}
			}
		}
	}
	if ready := condition(release, "Ready"); ready != nil {
		if ts, ok := ready["lastTransitionTime"].(string); ok {
			if t, err := time.Parse(time.RFC3339, ts); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}
