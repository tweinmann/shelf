package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/yaml"

	"github.com/tweinmann/shelf/internal/render"
)

// Rollout is an app's chain together with which artifact and which values each step is about.
// A Ready condition only says that a controller finished its last attempt; whether that attempt
// was about the newest artifact takes these fields.
type Rollout struct {
	Diagnosis Diagnosis
	// Revision and Commit are those of the deploy artifact the OCIRepository holds.
	Revision string
	Commit   string
	// AppliedRevision is the artifact revision the Kustomization last applied, AttemptedRevision
	// the one it last tried; a failure is about the artifact only when it tried that one.
	AppliedRevision   string
	AttemptedRevision string
	// ValuesDigest is the digest of the values the release has to run now, computed the way
	// helm-controller computes configDigest; empty while the values cannot be read.
	ValuesDigest string
	// ReleasedDigest is the configDigest of the release's latest Helm revision, AttemptedDigest
	// that of the values helm-controller last started an attempt with.
	ReleasedDigest  string
	AttemptedDigest string
	// Reconciling means helm-controller is in the middle of an install, upgrade, rollback or
	// retry, so its Ready condition is about to change.
	Reconciling bool
	// TestedDigest is the configDigest the tests stage reports on, empty while it has no result
	// for any values.
	TestedDigest string
}

// AppRollout reads one app's chain and its progress, without changing anything.
func AppRollout(ctx context.Context, cfg *rest.Config, app string) (Rollout, error) {
	c, err := newClient(cfg)
	if err != nil {
		return Rollout{}, err
	}
	apps, err := c.appObjects(ctx)
	if err != nil {
		return Rollout{}, err
	}
	objs, ok := apps[app]
	if !ok {
		return Rollout{}, &NotFoundError{Name: app}
	}
	values, err := c.get(ctx, ref{gvk: configMapGVK, namespace: app, name: render.ConfigMapName})
	if err != nil {
		return Rollout{}, err
	}
	r := rollout(app, objs, values)
	return r, c.addTestLogs(ctx, app, &r.Diagnosis)
}

func rollout(app string, o *appObjects, values *unstructured.Unstructured) Rollout {
	r := Rollout{Diagnosis: diagnose(app, o)}
	r.TestedDigest = r.Diagnosis.testedDigest
	if o.source != nil {
		r.Revision, _, _ = unstructured.NestedString(o.source.Object, "status", "artifact", "revision")
		r.Commit = artifactCommit(o.source)
	}
	if o.kustomization != nil {
		r.AppliedRevision, _, _ = unstructured.NestedString(o.kustomization.Object, "status", "lastAppliedRevision")
		r.AttemptedRevision, _, _ = unstructured.NestedString(o.kustomization.Object, "status", "lastAttemptedRevision")
	}
	if o.release == nil {
		return r
	}
	r.AttemptedDigest, _, _ = unstructured.NestedString(o.release.Object, "status", "lastAttemptedConfigDigest")
	history, _, _ := unstructured.NestedSlice(o.release.Object, "status", "history")
	if len(history) > 0 {
		if entry, ok := history[0].(map[string]any); ok {
			r.ReleasedDigest, _ = entry["configDigest"].(string)
		}
	}
	if reconciling := condition(o.release, "Reconciling"); reconciling != nil {
		r.Reconciling = reconciling["status"] == "True"
	}
	if values != nil {
		data, _, _ := unstructured.NestedString(values.Object, "data", render.ConfigMapKey)
		inline, _, _ := unstructured.NestedMap(o.release.Object, "spec", "values")
		r.ValuesDigest, _ = ConfigDigest(data, inline)
	}
	return r
}

// ConfigDigest is helm-controller's configDigest of a release: the values from the ConfigMap,
// with the HelmRelease's inline values merged on top, as YAML, hashed with SHA-256. It is what
// tells a release that already runs the newest values from one that still runs older ones,
// which neither its conditions nor its timestamps can after a failed upgrade and its rollback.
func ConfigDigest(valuesYAML string, inline map[string]any) (string, error) {
	values := map[string]any{}
	if err := yaml.Unmarshal([]byte(valuesYAML), &values); err != nil {
		return "", err
	}
	data, err := yaml.Marshal(mergeValues(values, inline))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// mergeValues merges b into a as Helm merges values: maps merge key by key, anything else in b
// replaces what a has.
func mergeValues(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		if bm, ok := v.(map[string]any); ok {
			if am, ok := out[k].(map[string]any); ok {
				out[k] = mergeValues(am, bm)
				continue
			}
		}
		out[k] = v
	}
	return out
}
