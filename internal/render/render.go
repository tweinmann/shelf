// Package render turns a validated app.yaml into its resolved form and wraps that into the
// ConfigMap manifest shipped in the deploy artifact.
//
// The resolved app.yaml is the values file of the shelf-app chart. Compared with the input it
// has images pinned by digest, host and port references substituted, every literal $ escaped
// as $$ for kubelet, and a normalized shape: ports is always a map (a single port is named
// "main"), route and health always name their port, and instances is always set.
// ${secrets.<name>} is left in place; the chart turns it into $(SHELF_SECRET_<NAME>). Because
// every literal $ is doubled, the chart finds references by splitting on "$$".
package render

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/tweinmann/shelf/internal/schema"
	"github.com/tweinmann/shelf/internal/validate"
)

// ErrInvalid is returned when the app.yaml has validation errors.
var ErrInvalid = errors.New("app.yaml has errors")

// ImageInfo is what the renderer needs to know about an image.
type ImageInfo struct {
	Digest       string // e.g. sha256:abc...
	ExposedPorts []int  // TCP ports from the image's EXPOSE, if any
}

// Resolver looks up images in their registry.
type Resolver interface {
	Resolve(ctx context.Context, image string) (ImageInfo, error)
}

// Render validates the document, resolves its images and returns the resolved app. built holds
// the image reference for every component with a `build` directory, by component name; the CI
// pushes those images and passes what it got. Findings include validation findings and warnings
// from the image lookup. On validation errors it returns ErrInvalid together with the findings.
func Render(ctx context.Context, doc *schema.Document, resolver Resolver, built map[string]string) (*schema.App, validate.Findings, error) {
	findings := validate.Validate(doc)
	if findings.HasErrors() {
		return nil, findings, ErrInvalid
	}
	src := doc.App

	refs, err := imageRefs(src, built)
	if err != nil {
		return nil, findings, err
	}
	images := map[string]ImageInfo{}
	for _, compName := range slices.Sorted(maps.Keys(src.Components)) {
		ref := refs[compName]
		info, ok := images[ref]
		if !ok {
			var err error
			info, err = resolver.Resolve(ctx, ref)
			if err != nil {
				return nil, findings, fmt.Errorf("component %s: %w", compName, err)
			}
			images[ref] = info
		}
		findings = append(findings, checkExposedPorts(doc, compName, ref, info)...)
	}
	findings.Sort()

	out := &schema.App{
		APIVersion: src.APIVersion,
		Name:       src.Name,
		Components: map[string]*schema.Component{},
		Secrets:    src.Secrets,
	}
	for compName, comp := range src.Components {
		out.Components[compName] = resolveComponent(src, comp, refs[compName], images[refs[compName]])
	}
	return out, findings, nil
}

// imageRefs returns the image reference of every component: the one from app.yaml, or the one
// the CI passed for a component built from a directory.
func imageRefs(app *schema.App, built map[string]string) (map[string]string, error) {
	for _, compName := range slices.Sorted(maps.Keys(built)) {
		comp, ok := app.Components[compName]
		switch {
		case !ok:
			return nil, fmt.Errorf("no component %s in this app.yaml, but an image was passed for it", compName)
		case !comp.IsBuilt():
			return nil, fmt.Errorf("component %s has an image in app.yaml, so no image can be passed for it", compName)
		}
	}
	refs := map[string]string{}
	for _, compName := range slices.Sorted(maps.Keys(app.Components)) {
		comp := app.Components[compName]
		if !comp.IsBuilt() {
			refs[compName] = comp.Image
			continue
		}
		ref, ok := built[compName]
		if !ok {
			return nil, fmt.Errorf("component %s is built from %s; pass the image it was pushed as (--image %s=<reference>)",
				compName, comp.Build, compName)
		}
		refs[compName] = ref
	}
	return refs, nil
}

// checkExposedPorts warns about declared ports the image does not EXPOSE. Images without any
// EXPOSE are not checked.
func checkExposedPorts(doc *schema.Document, compName, image string, info ImageInfo) validate.Findings {
	comp := doc.App.Components[compName]
	if len(info.ExposedPorts) == 0 || !comp.HasPorts() {
		return nil
	}
	var findings validate.Findings
	exposed := joinInts(info.ExposedPorts)
	add := func(p []string, port int) {
		if !slices.Contains(info.ExposedPorts, port) {
			findings = append(findings, validate.Finding{
				Severity: validate.Warning,
				Path:     strings.Join(p, "."),
				Line:     doc.Line(p...),
				Message:  fmt.Sprintf("port %d is not exposed by image %s (EXPOSE %s)", port, image, exposed),
			})
		}
	}
	if comp.Port != nil {
		add([]string{"components", compName, "port"}, *comp.Port)
	}
	for _, portName := range slices.Sorted(maps.Keys(comp.Ports)) {
		add([]string{"components", compName, "ports", portName}, comp.Ports[portName])
	}
	return findings
}

func joinInts(ns []int) string {
	s := make([]string, len(ns))
	for i, n := range ns {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, " ")
}

func resolveComponent(app *schema.App, comp *schema.Component, image string, info ImageInfo) *schema.Component {
	out := &schema.Component{
		Image:     pinImage(image, info.Digest),
		Command:   resolveList(app, comp.Command),
		Args:      resolveList(app, comp.Args),
		Volumes:   comp.Volumes,
		Resources: comp.Resources,
	}
	if comp.HasPorts() {
		out.Ports = comp.PortNames()
	}
	instances := 1
	if comp.Instances != nil {
		instances = *comp.Instances
	}
	out.Instances = &instances
	if comp.Env != nil {
		out.Env = map[string]string{}
		for k, v := range comp.Env {
			out.Env[k] = resolveString(app, v)
		}
	}
	if comp.Route != nil {
		r := *comp.Route
		r.Port = soleOr(out.Ports, r.Port)
		out.Route = &r
	}
	if comp.Health != nil {
		h := *comp.Health
		h.Port = soleOr(out.Ports, h.Port)
		out.Health = &h
	}
	return out
}

// soleOr returns portName, or the only port's name if portName is empty.
func soleOr(ports map[string]int, portName string) string {
	if portName != "" || len(ports) != 1 {
		return portName
	}
	for n := range ports {
		return n
	}
	return ""
}

// pinImage replaces any digest in image with the resolved one and keeps the tag for
// readability.
func pinImage(image, digest string) string {
	if i := strings.Index(image, "@"); i >= 0 {
		image = image[:i]
	}
	return image + "@" + digest
}

func resolveList(app *schema.App, in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = resolveString(app, s)
	}
	return out
}

// resolveString substitutes host and port references and escapes literal $ for kubelet.
// The input was validated, so parsing cannot fail and every reference exists.
func resolveString(app *schema.App, s string) string {
	tokens, err := schema.ParseTemplate(s)
	if err != nil {
		panic(fmt.Sprintf("render: unvalidated template %q: %v", s, err))
	}
	var b strings.Builder
	for _, t := range tokens {
		if t.Ref == nil {
			b.WriteString(strings.ReplaceAll(t.Literal, "$", "$$"))
			continue
		}
		switch t.Ref.Kind {
		case schema.RefHost:
			b.WriteString(t.Ref.Component)
		case schema.RefPort:
			for _, port := range app.Components[t.Ref.Component].PortNames() {
				b.WriteString(strconv.Itoa(port))
			}
		case schema.RefNamedPort:
			b.WriteString(strconv.Itoa(app.Components[t.Ref.Component].Ports[t.Ref.Port]))
		case schema.RefSecret:
			b.WriteString("${secrets." + t.Ref.Secret + "}")
		}
	}
	return b.String()
}

// MarshalApp encodes a resolved app as YAML.
func MarshalApp(app *schema.App) ([]byte, error) {
	return encodeYAML(app)
}

func encodeYAML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ConfigMapName is the name of the ConfigMap that carries an app's resolved app.yaml.
func ConfigMapName(app string) string { return app + "-values" }

// ConfigMapKey is the data key holding the resolved app.yaml; the HelmRelease reads it through
// valuesFrom.
const ConfigMapKey = "app.yaml"

// AppLabel marks every object that belongs to an app.
const AppLabel = "shelf.dev/app"

type configMap struct {
	APIVersion string            `yaml:"apiVersion"`
	Kind       string            `yaml:"kind"`
	Metadata   configMapMeta     `yaml:"metadata"`
	Data       map[string]string `yaml:"data"`
}

type configMapMeta struct {
	Name   string            `yaml:"name"`
	Labels map[string]string `yaml:"labels"`
}

// ConfigMap returns the ConfigMap manifest for a resolved app. It has no namespace; the Flux
// Kustomization sets targetNamespace.
func ConfigMap(app *schema.App) ([]byte, error) {
	values, err := MarshalApp(app)
	if err != nil {
		return nil, err
	}
	cm := configMap{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Metadata: configMapMeta{
			Name:   ConfigMapName(app.Name),
			Labels: map[string]string{AppLabel: app.Name},
		},
		Data: map[string]string{ConfigMapKey: string(values)},
	}
	return encodeYAML(cm)
}
