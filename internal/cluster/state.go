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

var podGVK = schema.GroupVersionKind{Version: "v1", Kind: "Pod"}

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
	// Domain is the app's own domain, empty when it answers under the cluster's.
	Domain string `json:"domain,omitempty"`
	// Tunnel is the app's Cloudflare tunnel, empty when the app is not exposed.
	Tunnel string `json:"tunnel,omitempty"`
	// RegistryUser is who the app's registry login belongs to, empty without a login. The
	// token is never part of the state.
	RegistryUser string `json:"registryUser,omitempty"`
	// Revision is the deploy artifact that is applied, e.g. main@sha256:1f4e….
	Revision string `json:"revision,omitempty"`
	// Deployed is when the release last changed.
	Deployed time.Time `json:"deployed,omitzero"`
	Phase    Phase     `json:"phase"`
	// Reason is why the phase is not Ready, in one line.
	Reason string `json:"reason,omitempty"`
	// Stage is the step that is not ready, empty when the app is healthy.
	Stage string `json:"stage,omitempty"`
}

// appObjects are the objects that carry one app.
type appObjects struct {
	provider      *unstructured.Unstructured
	registry      *unstructured.Unstructured
	source        *unstructured.Unstructured
	kustomization *unstructured.Unstructured
	chart         *unstructured.Unstructured
	release       *unstructured.Unstructured
	pods          []*unstructured.Unstructured
}

// AppStates returns every registered app with its current state. It reads the whole cluster in
// five list calls, however many apps there are.
func AppStates(ctx context.Context, cfg *rest.Config) ([]AppState, error) {
	c, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	apps, err := c.appObjects(ctx)
	if err != nil {
		return nil, err
	}
	states := make([]AppState, 0, len(apps))
	for _, name := range slices.Sorted(maps.Keys(apps)) {
		states = append(states, appState(name, apps[name]))
	}
	return states, nil
}

// AppDiagnosis returns the whole chain of one app, including its pods.
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
		return Diagnosis{}, fmt.Errorf("app %s does not exist", app)
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
	secrets, err := c.list(ctx, secretGVK, SystemNamespace, AppLabel)
	if err != nil {
		return nil, err
	}
	for _, s := range secrets {
		if app, ok := apps[s.GetLabels()[AppLabel]]; ok && s.GetName() == RegistrySecretName(s.GetLabels()[AppLabel]) {
			app.registry = s
		}
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
	return apps, nil
}

// list returns the objects of a kind. An empty namespace means every namespace, an empty
// selector every object.
func (c *client) list(ctx context.Context, gvk schema.GroupVersionKind, namespace, selector string) (
	[]*unstructured.Unstructured, error) {
	ri, err := c.resource(gvk, namespace)
	if err != nil {
		return nil, err
	}
	list, err := ri.List(ctx, metav1.ListOptions{LabelSelector: selector})
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
	return state
}

func appState(name string, o *appObjects) AppState {
	state := providerState(o.provider)
	state.Name = name
	if o.registry != nil {
		// A Secret that cannot be read shows as no login; the diagnosis says what fails.
		if login, err := registryLogin(o.registry); err == nil && login != nil {
			state.RegistryUser = login.Username
		}
	}
	if o.source != nil {
		state.Revision, _, _ = unstructured.NestedString(o.source.Object, "status", "artifact", "revision")
	}
	if o.release != nil {
		state.Deployed = lastDeployed(o.release)
	}
	d := diagnose(name, o)
	state.Phase = d.Phase()
	if t := d.Trouble(); t != nil {
		state.Stage = t.Name
		state.Reason = strings.TrimSpace(t.Message)
		if state.Reason == "" {
			state.Reason = t.Reason
		}
	}
	return state
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
	d.Stages = append(d.Stages, podStage(o.pods))
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
	}
	return s
}

// working reports whether a Flux reason means the controller is still trying.
func working(reason string) bool {
	return strings.HasPrefix(reason, "Progressing") || reason == "DependencyNotReady" ||
		reason == "ArtifactOutdated" || reason == "Unknown"
}

// podStage looks at the pods the chart created, because a release can succeed while the
// container it starts does not run at all.
func podStage(pods []*unstructured.Unstructured) Stage {
	s := Stage{Name: "the pods", Object: "Pods"}
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
