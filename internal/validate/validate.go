package validate

import (
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	"k8s.io/apimachinery/pkg/api/resource"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"

	"github.com/tweinmann/shelf/internal/schema"
)

var (
	appNameRE       = regexp.MustCompile(schema.AppNamePattern)
	componentNameRE = regexp.MustCompile(schema.ComponentNamePattern)
	volumeNameRE    = regexp.MustCompile(schema.VolumeNamePattern)
	secretNameRE    = regexp.MustCompile(schema.SecretNamePattern)
	envNameRE       = regexp.MustCompile(schema.EnvNamePattern)
)

// reservedComponentNames would be ambiguous in ${...} references or clash with platform names.
var reservedComponentNames = []string{"secrets", "app", "shelf"}

// headlessSuffix names the extra headless Service of a StatefulSet component, so no component
// name may end with it.
const headlessSuffix = "-headless"

// reservedAppNames are namespaces the platform or Kubernetes already uses.
var reservedAppNames = []string{"default", "flux-system", "traefik", "cloudflared", "external-dns"}

var reservedAppPrefixes = []string{"kube-", "shelf-"}

type checker struct {
	doc      *schema.Document
	findings Findings
}

func (c *checker) add(sev Severity, fieldPath []string, format string, args ...any) {
	c.findings = append(c.findings, Finding{
		Severity: sev,
		Path:     strings.Join(fieldPath, "."),
		Line:     c.doc.Line(fieldPath...),
		Message:  fmt.Sprintf(format, args...),
	})
}

func (c *checker) errorf(fieldPath []string, format string, args ...any) {
	c.add(Error, fieldPath, format, args...)
}

// at builds a field path without aliasing the caller's slice.
func at(base []string, more ...string) []string {
	return append(slices.Clip(base), more...)
}

// Validate checks a parsed app.yaml and returns all findings, sorted.
func Validate(doc *schema.Document) Findings {
	c := &checker{doc: doc}
	app := doc.App

	if app.APIVersion != schema.APIVersion {
		c.errorf([]string{"apiVersion"}, "must be %s, got %q", schema.APIVersion, app.APIVersion)
	}
	c.checkAppName(app.Name)

	if len(app.Components) == 0 {
		c.errorf([]string{"components"}, "at least one component is required")
	}
	for _, compName := range slices.Sorted(maps.Keys(app.Components)) {
		c.checkComponent(app, compName)
	}
	c.checkRouteCollisions(app)
	c.checkSecrets(app)

	c.findings.Sort()
	return c.findings
}

func (c *checker) checkAppName(n string) {
	p := []string{"name"}
	switch {
	case n == "":
		c.errorf(p, "is required")
	case len(n) > schema.MaxAppNameLength:
		c.errorf(p, "must be at most %d characters", schema.MaxAppNameLength)
	case !appNameRE.MatchString(n):
		c.errorf(p, "%q must consist of lowercase letters, digits and '-', and start and end "+
			"with a letter or digit", n)
	case slices.Contains(reservedAppNames, n) ||
		slices.ContainsFunc(reservedAppPrefixes, func(pre string) bool { return strings.HasPrefix(n, pre) }):
		c.errorf(p, "%q is reserved for the platform", n)
	}
}

func (c *checker) checkComponent(app *schema.App, compName string) {
	p := []string{"components", compName}
	comp := app.Components[compName]

	switch {
	case len(compName) > schema.MaxComponentNameLength:
		c.errorf(p, "component name must be at most %d characters", schema.MaxComponentNameLength)
	case !componentNameRE.MatchString(compName):
		c.errorf(p, "component name %q must consist of lowercase letters, digits and '-', start "+
			"with a letter and end with a letter or digit", compName)
	case slices.Contains(reservedComponentNames, compName):
		c.errorf(p, "component name %q is reserved", compName)
	case strings.HasSuffix(compName, headlessSuffix):
		c.errorf(p, "component name %q must not end with %q, which is reserved for the platform",
			compName, headlessSuffix)
	}
	if comp == nil {
		c.errorf(p, "component is empty; image is required")
		return
	}

	if comp.Image == "" {
		c.errorf(at(p, "image"), "is required")
	} else if _, err := name.ParseReference(comp.Image); err != nil {
		c.errorf(at(p, "image"), "invalid image reference %q: %v", comp.Image, err)
	}

	c.checkPorts(p, comp)
	c.checkRoute(p, comp)
	if comp.Instances != nil && *comp.Instances < 1 {
		c.errorf(at(p, "instances"), "must be at least 1")
	}
	c.checkVolumes(p, comp)
	c.checkHealth(p, comp)
	c.checkResources(p, comp)

	for i, s := range comp.Command {
		c.checkTemplate(app, at(p, "command", fmt.Sprint(i)), s)
	}
	for i, s := range comp.Args {
		c.checkTemplate(app, at(p, "args", fmt.Sprint(i)), s)
	}
	for _, k := range slices.Sorted(maps.Keys(comp.Env)) {
		ep := at(p, "env", k)
		switch {
		case !envNameRE.MatchString(k):
			c.errorf(ep, "environment variable name %q must be a C identifier ([A-Za-z_][A-Za-z0-9_]*)", k)
		case strings.HasPrefix(k, schema.SecretEnvPrefix):
			c.errorf(ep, "the prefix %s is reserved for secrets; use ${secrets.<name>} instead",
				schema.SecretEnvPrefix)
		}
		c.checkTemplate(app, ep, comp.Env[k])
	}
}

func (c *checker) checkPorts(p []string, comp *schema.Component) {
	if comp.Port != nil && comp.Ports != nil {
		c.errorf(at(p, "ports"), "use either port or ports, not both")
	}
	if comp.Port != nil && !validPort(*comp.Port) {
		c.errorf(at(p, "port"), "must be between 1 and 65535, got %d", *comp.Port)
	}
	if comp.Ports != nil && len(comp.Ports) == 0 {
		c.errorf(at(p, "ports"), "must not be empty; omit it for a component without ports")
	}
	seen := map[int]string{}
	for _, portName := range slices.Sorted(maps.Keys(comp.Ports)) {
		pp := at(p, "ports", portName)
		for _, msg := range k8svalidation.IsValidPortName(portName) {
			c.errorf(pp, "port name %q: %s", portName, msg)
		}
		num := comp.Ports[portName]
		if !validPort(num) {
			c.errorf(pp, "must be between 1 and 65535, got %d", num)
			continue
		}
		if other, dup := seen[num]; dup {
			c.errorf(pp, "port %d is already used by port %q", num, other)
		}
		seen[num] = portName
	}
}

func validPort(n int) bool { return n >= 1 && n <= 65535 }

// checkPortName checks the port field of a route or health check.
func (c *checker) checkPortName(pp []string, comp *schema.Component, portName, what string) {
	switch {
	case !comp.HasPorts():
		c.errorf(pp[:len(pp)-1], "%s needs a port; add port or ports to the component", what)
	case comp.Port != nil && portName != "":
		c.errorf(pp, "the component has a single port; remove %s.port", what)
	case comp.Ports != nil && portName == "" && len(comp.Ports) > 1:
		c.errorf(pp[:len(pp)-1], "the component has several ports; set %s.port to one of %s",
			what, strings.Join(slices.Sorted(maps.Keys(comp.Ports)), ", "))
	case comp.Ports != nil && portName != "":
		if _, ok := comp.Ports[portName]; !ok {
			c.errorf(pp, "unknown port %q; the component has %s",
				portName, strings.Join(slices.Sorted(maps.Keys(comp.Ports)), ", "))
		}
	}
}

func (c *checker) checkRoute(p []string, comp *schema.Component) {
	r := comp.Route
	if r == nil {
		return
	}
	rp := at(p, "route")
	if msg := checkHTTPPath(r.Path); msg != "" {
		c.errorf(rp, "route path %s", msg)
	}
	c.checkPortName(at(rp, "port"), comp, r.Port, "route")
}

// checkHTTPPath returns a problem description, or "" if the path is fine.
func checkHTTPPath(p string) string {
	switch {
	case p == "":
		return "is required"
	case !strings.HasPrefix(p, "/"):
		return fmt.Sprintf("%q must start with /", p)
	case strings.ContainsAny(p, "?# \t\n"):
		return fmt.Sprintf("%q must not contain spaces, ? or #", p)
	}
	return ""
}

// normalizeRoutePath makes /api and /api/ compare equal.
func normalizeRoutePath(p string) string {
	if p == "/" {
		return p
	}
	return strings.TrimSuffix(p, "/")
}

func (c *checker) checkRouteCollisions(app *schema.App) {
	owner := map[string]string{}
	for _, compName := range slices.Sorted(maps.Keys(app.Components)) {
		comp := app.Components[compName]
		if comp == nil || comp.Route == nil || comp.Route.Path == "" {
			continue
		}
		key := normalizeRoutePath(comp.Route.Path)
		if other, dup := owner[key]; dup {
			c.errorf([]string{"components", compName, "route"},
				"route path %s is already used by component %q", comp.Route.Path, other)
			continue
		}
		owner[key] = compName
	}
}

func (c *checker) checkVolumes(p []string, comp *schema.Component) {
	names := slices.Sorted(maps.Keys(comp.Volumes))
	for _, volName := range names {
		vp := at(p, "volumes", volName)
		vol := comp.Volumes[volName]
		switch {
		case len(volName) > schema.MaxVolumeNameLength:
			c.errorf(vp, "volume name must be at most %d characters", schema.MaxVolumeNameLength)
		case !volumeNameRE.MatchString(volName):
			c.errorf(vp, "volume name %q must consist of lowercase letters, digits and '-', and "+
				"start and end with a letter or digit", volName)
		}
		if vol == nil {
			c.errorf(vp, "volume is empty; path and size are required")
			continue
		}
		switch {
		case vol.Path == "":
			c.errorf(at(vp, "path"), "is required")
		case !path.IsAbs(vol.Path):
			c.errorf(at(vp, "path"), "%q must be absolute", vol.Path)
		case vol.Path == "/":
			c.errorf(at(vp, "path"), "must not be /")
		case path.Clean(vol.Path) != vol.Path:
			c.errorf(at(vp, "path"), "%q is not clean; use %q", vol.Path, path.Clean(vol.Path))
		}
		c.checkQuantity(at(vp, "size"), vol.Size, true)
	}

	// Mount paths must not be equal or nested: one mount would hide (part of) the other.
	for i, a := range names {
		for _, b := range names[i+1:] {
			va, vb := comp.Volumes[a], comp.Volumes[b]
			if va == nil || vb == nil || va.Path == "" || vb.Path == "" {
				continue
			}
			if pathsOverlap(va.Path, vb.Path) {
				c.errorf(at(p, "volumes", b, "path"), "%s overlaps with %s of volume %q",
					vb.Path, va.Path, a)
			}
		}
	}
}

func pathsOverlap(a, b string) bool {
	a, b = path.Clean(a), path.Clean(b)
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func (c *checker) checkHealth(p []string, comp *schema.Component) {
	h := comp.Health
	if h == nil {
		return
	}
	hp := at(p, "health")
	if msg := checkHTTPPath(h.Path); msg != "" {
		c.errorf(at(hp, "path"), "%s", msg)
	}
	c.checkPortName(at(hp, "port"), comp, h.Port, "health")
	if h.InitialDelay != nil && *h.InitialDelay < 0 {
		c.errorf(at(hp, "initialDelay"), "must not be negative")
	}
}

func (c *checker) checkResources(p []string, comp *schema.Component) {
	r := comp.Resources
	if r == nil {
		return
	}
	rp := at(p, "resources")
	if r.CPU != "" {
		c.checkQuantity(at(rp, "cpu"), r.CPU, false)
	}
	if r.Memory != "" {
		c.checkQuantity(at(rp, "memory"), r.Memory, false)
	}
}

func (c *checker) checkQuantity(p []string, s string, required bool) {
	if s == "" {
		if required {
			c.errorf(p, "is required")
		}
		return
	}
	q, err := resource.ParseQuantity(s)
	if err != nil {
		c.errorf(p, "invalid quantity %q (examples: 500m, 512Mi, 10Gi)", s)
		return
	}
	if q.Sign() <= 0 {
		c.errorf(p, "must be greater than zero, got %s", s)
	}
}

func (c *checker) checkTemplate(app *schema.App, p []string, s string) {
	tokens, err := schema.ParseTemplate(s)
	if err != nil {
		c.errorf(p, "%v", err)
		return
	}
	for _, t := range tokens {
		if t.Ref != nil {
			c.checkRef(app, p, t.Ref)
		}
	}
}

func (c *checker) checkRef(app *schema.App, p []string, ref *schema.Ref) {
	if ref.Kind == schema.RefSecret {
		if _, ok := app.Secrets[ref.Secret]; !ok {
			c.errorf(p, "${%s} refers to an unknown secret %q; declare it under secrets", ref.Raw, ref.Secret)
		}
		return
	}
	target, ok := app.Components[ref.Component]
	if !ok || target == nil {
		c.errorf(p, "${%s} refers to an unknown component %q", ref.Raw, ref.Component)
		return
	}
	if !target.HasPorts() {
		c.errorf(p, "${%s}: component %q has no port and therefore no host name", ref.Raw, ref.Component)
		return
	}
	switch ref.Kind {
	case schema.RefPort:
		if len(target.PortNames()) > 1 {
			c.errorf(p, "${%s}: component %q has several ports; use ${%s.ports.<name>} with one of %s",
				ref.Raw, ref.Component, ref.Component,
				strings.Join(slices.Sorted(maps.Keys(target.Ports)), ", "))
		}
	case schema.RefNamedPort:
		if target.Ports == nil {
			c.errorf(p, "${%s}: component %q has a single port; use ${%s.port}",
				ref.Raw, ref.Component, ref.Component)
		} else if _, ok := target.Ports[ref.Port]; !ok {
			c.errorf(p, "${%s} refers to an unknown port %q of component %q",
				ref.Raw, ref.Port, ref.Component)
		}
	}
}

func (c *checker) checkSecrets(app *schema.App) {
	used := map[string]bool{}
	for _, comp := range app.Components {
		if comp == nil {
			continue
		}
		strs := slices.Concat(comp.Command, comp.Args, slices.Collect(maps.Values(comp.Env)))
		for _, s := range strs {
			tokens, _ := schema.ParseTemplate(s)
			for _, t := range tokens {
				if t.Ref != nil && t.Ref.Kind == schema.RefSecret {
					used[t.Ref.Secret] = true
				}
			}
		}
	}

	for _, secretName := range slices.Sorted(maps.Keys(app.Secrets)) {
		sp := []string{"secrets", secretName}
		switch {
		case len(secretName) > schema.MaxSecretNameLength:
			c.errorf(sp, "secret name must be at most %d characters", schema.MaxSecretNameLength)
		case !secretNameRE.MatchString(secretName):
			c.errorf(sp, "secret name %q must consist of lowercase letters, digits and '-', and "+
				"start and end with a letter or digit", secretName)
		}
		if s := app.Secrets[secretName]; s == nil || !s.Generate {
			c.errorf(sp, "only generated secrets are supported; set generate: true")
		}
		if !used[secretName] {
			c.add(Warning, sp, "secret %q is not referenced by any component", secretName)
		}
	}
}
