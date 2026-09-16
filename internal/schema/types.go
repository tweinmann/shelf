// Package schema defines the app.yaml format (shelf.dev/v1alpha1), parses it, and generates its
// JSON Schema.
package schema

import "strings"

// APIVersion is the only app.yaml version this build understands.
const APIVersion = "shelf.dev/v1alpha1"

// DefaultPortName is the port name a single `port` gets when a component is normalized.
const DefaultPortName = "main"

// SecretEnvPrefix prefixes the environment variable that carries a secret into a container.
const SecretEnvPrefix = "SHELF_SECRET_"

// Name limits. Component and app names end up in object names with suffixes (StatefulSet pod
// names, controller-revision-hash labels, "<app>-values"), so they stay well below 63.
const (
	MaxAppNameLength       = 40
	MaxComponentNameLength = 40
	MaxVolumeNameLength    = 40
	MaxSecretNameLength    = 63
)

// Name patterns, shared by validation and the JSON Schema.
const (
	// AppNamePattern is a DNS-1123 label: the app name becomes the namespace name.
	AppNamePattern = `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// ComponentNamePattern is a DNS-1035 label: the component name becomes a Service name.
	ComponentNamePattern = `^[a-z]([-a-z0-9]*[a-z0-9])?$`
	// VolumeNamePattern is a DNS-1123 label: the volume name becomes part of the PVC name.
	VolumeNamePattern = `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// SecretNamePattern allows no underscores, so SecretEnvName never maps two names to one.
	SecretNamePattern = `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// EnvNamePattern is a C identifier, which every shell can reference.
	EnvNamePattern = `^[A-Za-z_][A-Za-z0-9_]*$`
)

// App is the root of an app.yaml file.
type App struct {
	APIVersion string                `yaml:"apiVersion" jsonschema_description:"Always shelf.dev/v1alpha1."`
	Name       string                `yaml:"name" jsonschema_description:"App name. Becomes the namespace and the host name <name>.<domain>."`
	Components map[string]*Component `yaml:"components" jsonschema_description:"The containers that make up the app, by name."`
	Secrets    map[string]*Secret    `yaml:"secrets,omitempty" jsonschema_description:"Secrets referenced as ${secrets.<name>}, by name."`
}

// Component is one container image with its runtime settings.
type Component struct {
	Image     string             `yaml:"image" jsonschema_description:"Image reference. Tags are pinned to a digest by shelf render."`
	Command   []string           `yaml:"command,omitempty" jsonschema_description:"Overrides the image entrypoint. Supports ${...} references; write $$ for a literal $."`
	Args      []string           `yaml:"args,omitempty" jsonschema_description:"Overrides the image command. Supports ${...} references; write $$ for a literal $."`
	Env       map[string]string  `yaml:"env,omitempty" jsonschema_description:"Environment variables. Values support ${...} references; write $$ for a literal $."`
	Port      *int               `yaml:"port,omitempty" jsonschema_description:"The single port the container listens on. Mutually exclusive with ports."`
	Ports     map[string]int     `yaml:"ports,omitempty" jsonschema_description:"Named ports the container listens on. Mutually exclusive with port."`
	Route     *Route             `yaml:"route,omitempty" jsonschema_description:"Makes the component reachable from outside under this path."`
	Instances *int               `yaml:"instances,omitempty" jsonschema_description:"Number of replicas. Default 1. Instances do not form a cluster."`
	Volumes   map[string]*Volume `yaml:"volumes,omitempty" jsonschema_description:"Persistent volumes by name. Turns the component into a StatefulSet."`
	Health    *Health            `yaml:"health,omitempty" jsonschema_description:"HTTP readiness and liveness probe."`
	Resources *Resources         `yaml:"resources,omitempty" jsonschema_description:"Resource requests. Memory is also the limit."`
}

// Route exposes a component over HTTP. In app.yaml it is either a path or a mapping.
type Route struct {
	Path        string `yaml:"path" jsonschema_description:"Path prefix, starting with /. Passed through unchanged unless stripPrefix is set."`
	Port        string `yaml:"port,omitempty" jsonschema_description:"Port name from ports. Required when the component has more than one port."`
	StripPrefix bool   `yaml:"stripPrefix,omitempty" jsonschema_description:"Remove the path prefix before forwarding the request."`
}

// Volume is a persistent volume mounted into a component.
type Volume struct {
	Path string `yaml:"path" jsonschema_description:"Absolute mount path in the container."`
	Size string `yaml:"size" jsonschema_description:"Requested size, e.g. 10Gi. Not enforced by the local-path provisioner."`
}

// Health configures the HTTP readiness and liveness probe.
type Health struct {
	Path         string `yaml:"path" jsonschema_description:"HTTP path that answers 2xx or 3xx when the component is healthy."`
	Port         string `yaml:"port,omitempty" jsonschema_description:"Port name from ports. Required when the component has more than one port."`
	InitialDelay *int   `yaml:"initialDelay,omitempty" jsonschema_description:"Seconds to wait before the first probe."`
}

// Resources are the resource requests of a component; memory is also used as the limit.
type Resources struct {
	CPU    string `yaml:"cpu,omitempty" jsonschema_description:"CPU request, e.g. 500m."`
	Memory string `yaml:"memory,omitempty" jsonschema_description:"Memory request and limit, e.g. 512Mi."`
}

// Secret declares a secret value that the platform provides.
type Secret struct {
	Generate bool `yaml:"generate" jsonschema_description:"Generate a random URL-safe value once when the app is added. Must be true."`
}

// SecretEnvName returns the environment variable that carries the named secret.
func SecretEnvName(secret string) string {
	return SecretEnvPrefix + strings.ToUpper(strings.ReplaceAll(secret, "-", "_"))
}

// PortNames returns the component's ports by name, with a single `port` under DefaultPortName.
func (c *Component) PortNames() map[string]int {
	if c.Port != nil {
		return map[string]int{DefaultPortName: *c.Port}
	}
	return c.Ports
}

// HasPorts reports whether the component declares any port.
func (c *Component) HasPorts() bool {
	return c.Port != nil || len(c.Ports) > 0
}
