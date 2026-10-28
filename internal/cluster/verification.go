package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/yaml"

	"github.com/tweinmann/shelf/internal/render"
)

// Annotations of the deploy artifact that verification reads, as Flux copies them into the
// OCIRepository's status.artifact.metadata. The scan summary is written by the reusable workflow
// before it signs the artifact, so the signature covers it.
const (
	ScanAnnotation   = "dev.shelf.scan"
	sourceAnnotation = "org.opencontainers.image.source"
)

// sourceVerified is the condition Flux sets on an OCIRepository with verify.
const sourceVerified = "SourceVerified"

// Verification says whether what an app or preview runs is what the cluster's signer signed and
// CI scanned. Only a verified app may be exposed.
type Verification struct {
	Verified bool
	// Reason is the first thing that does not hold, when not verified.
	Reason string
	// Source is the repository the deploy artifact says it was built from, and Commit the
	// commit; both come from annotations the signature covers.
	Source string
	Commit string
	// Scan is the scan summary of the running deploy artifact, nil when it has none.
	Scan *Scan
}

// Scan is what CI found in the images of one deploy artifact.
type Scan struct {
	Scanner   string
	ScannedAt string
	// Images are the app's components with their image digest and findings, by component name.
	Images []ScannedImage
}

// ScannedImage is one component's image and what the scan found in it. Scanned is false when
// the summary does not name the image's digest.
type ScannedImage struct {
	Component string
	Image     string
	Digest    string
	Scanned   bool
	Critical  int
	High      int
}

// scanSummary is the JSON of the annotation dev.shelf.scan.
type scanSummary struct {
	Scanner   string `json:"scanner"`
	ScannedAt string `json:"scannedAt"`
	Images    map[string]struct {
		Critical int `json:"critical"`
		High     int `json:"high"`
	} `json:"images"`
}

// AppVerification reads whether an app or a preview (by its namespace) runs a verified deploy
// artifact, without changing anything.
func AppVerification(ctx context.Context, cfg *rest.Config, name string) (Verification, error) {
	c, err := newClient(cfg)
	if err != nil {
		return Verification{}, err
	}
	apps, err := c.appObjects(ctx)
	if err != nil {
		return Verification{}, err
	}
	objs, ok := apps[name]
	if !ok {
		return Verification{}, &NotFoundError{Name: name}
	}
	values, err := c.get(ctx, ref{gvk: configMapGVK, namespace: name, name: render.ConfigMapName})
	if err != nil {
		return Verification{}, err
	}
	return verification(name, objs, values), nil
}

// verification checks, in the order things happen: the deploy artifact's signature, that the
// release runs that artifact's values, the chart's signature, and that the scan summary covers
// every image the values name.
func verification(name string, o *appObjects, values *unstructured.Unstructured) Verification {
	v := Verification{}
	if o.source != nil {
		v.Source, _, _ = unstructured.NestedString(o.source.Object, "status", "artifact", "metadata", sourceAnnotation)
		v.Commit = artifactCommit(o.source)
	}
	if reason := signatureReason("deploy artifact", o.source); reason != "" {
		v.Reason = reason
		return v
	}
	if !rollout(name, o, values).Running() {
		v.Reason = "the release does not run the verified deploy artifact yet"
		return v
	}
	if reason := signatureReason("chart", o.chart); reason != "" {
		v.Reason = reason
		return v
	}
	images, err := valueImages(values)
	if err != nil {
		v.Reason = err.Error()
		return v
	}
	raw, _, _ := unstructured.NestedString(o.source.Object, "status", "artifact", "metadata", ScanAnnotation)
	if raw == "" {
		v.Reason = "the deploy artifact has no scan (" + ScanAnnotation + ")"
		return v
	}
	var summary scanSummary
	if err := json.Unmarshal([]byte(raw), &summary); err != nil {
		v.Reason = "the deploy artifact's scan (" + ScanAnnotation + ") cannot be read: " + err.Error()
		return v
	}
	v.Scan = &Scan{Scanner: summary.Scanner, ScannedAt: summary.ScannedAt}
	var unscanned []string
	for _, img := range images {
		found, ok := summary.Images[img.Digest]
		img.Scanned = ok
		img.Critical, img.High = found.Critical, found.High
		if !ok {
			unscanned = append(unscanned, img.Component)
		}
		v.Scan.Images = append(v.Scan.Images, img)
	}
	if len(unscanned) > 0 {
		v.Reason = "the scan does not cover the image of " + strings.Join(unscanned, ", ")
		return v
	}
	v.Verified = true
	return v
}

// signatureReason is why a source's artifact does not count as signed, or "" if it does. A
// source without the condition is one the cluster does not verify, as on a platform from before
// verification.
func signatureReason(what string, source *unstructured.Unstructured) string {
	if source == nil {
		return "the " + what + " is not there yet"
	}
	cond := condition(source, sourceVerified)
	if cond == nil {
		return "the cluster does not verify the " + what + "'s signature"
	}
	if cond["status"] != "True" {
		msg, _ := cond["message"].(string)
		return "the " + what + " is not signed by the cluster's signer: " + msg
	}
	return ""
}

// valueImages returns the image of every component and test in the app values, sorted by name;
// test names never clash with component names. shelf render pins each one as
// <reference>@sha256:<digest>.
func valueImages(values *unstructured.Unstructured) ([]ScannedImage, error) {
	if values == nil {
		return nil, fmt.Errorf("the app values (%s) are not there yet", render.ConfigMapName)
	}
	data, _, _ := unstructured.NestedString(values.Object, "data", render.ConfigMapKey)
	var app struct {
		Components map[string]struct {
			Image string `json:"image"`
		} `json:"components"`
		Tests map[string]struct {
			Image string `json:"image"`
		} `json:"tests"`
	}
	if err := yaml.Unmarshal([]byte(data), &app); err != nil {
		return nil, fmt.Errorf("the app values cannot be read: %w", err)
	}
	var out []ScannedImage
	add := func(name, image string) {
		_, digest, _ := strings.Cut(image, "@")
		out = append(out, ScannedImage{Component: name, Image: image, Digest: digest})
	}
	for name, comp := range app.Components {
		add(name, comp.Image)
	}
	for name, test := range app.Tests {
		add(name, test.Image)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Component < out[j].Component })
	return out, nil
}

// Running reports whether the release runs the values of the deploy artifact the source holds:
// the artifact is applied and the release's latest revision has its values.
func (r Rollout) Running() bool {
	return r.Revision != "" && r.AppliedRevision == r.Revision &&
		r.ValuesDigest != "" && r.ReleasedDigest == r.ValuesDigest
}
