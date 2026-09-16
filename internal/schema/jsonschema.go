package schema

import (
	"encoding/json"
	"reflect"

	"github.com/invopop/jsonschema"
)

// SchemaID is where the published schema lives. Editors resolve it from
// `# yaml-language-server: $schema=<SchemaID>`.
const SchemaID = "https://raw.githubusercontent.com/tweinmann/shelf/main/schema/app.schema.json"

// JSONSchema returns the JSON Schema for app.yaml. It covers structure and names; cross-field
// rules (references, collisions) are only checked by `shelf validate`.
func JSONSchema() ([]byte, error) {
	r := &jsonschema.Reflector{
		FieldNameTag:   "yaml",
		Anonymous:      true,
		ExpandedStruct: true,
		Namer:          func(t reflect.Type) string { return t.Name() },
	}
	s := r.Reflect(&App{})
	s.ID = SchemaID
	s.Title = "shelf app (" + APIVersion + ")"
	return json.MarshalIndent(s, "", "  ")
}

func ptr[T any](v T) *T { return &v }

func prop(s *jsonschema.Schema, name string) *jsonschema.Schema {
	p, _ := s.Properties.Get(name)
	return p
}

func namePattern(pattern string, maxLength int) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Pattern: pattern, MaxLength: ptr(uint64(maxLength))}
}

func portNumber() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "integer", Minimum: "1", Maximum: "65535"}
}

// portName matches Kubernetes IANA_SVC_NAME: at most 15 characters, at least one letter.
func portName() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:      "string",
		Pattern:   `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`,
		MaxLength: ptr(uint64(15)),
	}
}

func (App) JSONSchemaExtend(s *jsonschema.Schema) {
	prop(s, "apiVersion").Const = APIVersion
	name := prop(s, "name")
	name.Pattern = AppNamePattern
	name.MaxLength = ptr(uint64(MaxAppNameLength))
	components := prop(s, "components")
	components.PropertyNames = namePattern(ComponentNamePattern, MaxComponentNameLength)
	components.MinProperties = ptr(uint64(1))
	prop(s, "secrets").PropertyNames = namePattern(SecretNamePattern, MaxSecretNameLength)
}

func (Component) JSONSchemaExtend(s *jsonschema.Schema) {
	// YAML users write `PORT: 8080`; the parser accepts any scalar and keeps its text.
	env := prop(s, "env")
	env.PropertyNames = namePattern(EnvNamePattern, 253)
	env.AdditionalProperties = &jsonschema.Schema{AnyOf: []*jsonschema.Schema{
		{Type: "string"}, {Type: "number"}, {Type: "boolean"},
	}}
	port := prop(s, "port")
	port.Minimum, port.Maximum = "1", "65535"
	ports := prop(s, "ports")
	ports.PropertyNames = portName()
	ports.AdditionalProperties = portNumber()
	ports.MinProperties = ptr(uint64(1))
	prop(s, "instances").Minimum = "1"
	prop(s, "volumes").PropertyNames = namePattern(VolumeNamePattern, MaxVolumeNameLength)
	s.Not = &jsonschema.Schema{Required: []string{"port", "ports"}}
}

func (Route) JSONSchema() *jsonschema.Schema {
	path := &jsonschema.Schema{Type: "string", Pattern: "^/"}
	long := &jsonschema.Schema{
		Type:                 "object",
		Properties:           jsonschema.NewProperties(),
		Required:             []string{"path"},
		AdditionalProperties: jsonschema.FalseSchema,
	}
	long.Properties.Set("path", path)
	long.Properties.Set("port", portName())
	long.Properties.Set("stripPrefix", &jsonschema.Schema{Type: "boolean"})
	return &jsonschema.Schema{
		Description: "A path like /api, or { path, port, stripPrefix }.",
		OneOf:       []*jsonschema.Schema{path, long},
	}
}

func (Volume) JSONSchemaExtend(s *jsonschema.Schema) {
	prop(s, "path").Pattern = "^/"
}

func (Health) JSONSchemaExtend(s *jsonschema.Schema) {
	prop(s, "path").Pattern = "^/"
	prop(s, "port").Pattern = portName().Pattern
	prop(s, "initialDelay").Minimum = "0"
}

func (Secret) JSONSchemaExtend(s *jsonschema.Schema) {
	prop(s, "generate").Const = true
}
