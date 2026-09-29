package schema

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"

	"github.com/tweinmann/shelf/internal/testutil"
)

const schemaFile = "../../schema/app.schema.json"

// TestJSONSchemaIsCurrent keeps the committed schema in sync with the Go types.
// Regenerate with `just schema`.
func TestJSONSchemaIsCurrent(t *testing.T) {
	s, err := JSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	testutil.Golden(t, schemaFile, append(s, '\n'))
}

func compileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	s, err := JSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(SchemaID, doc); err != nil {
		t.Fatal(err)
	}
	compiled, err := c.Compile(SchemaID)
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

// yamlToJSONValue converts YAML into the generic form the schema validator expects.
func yamlToJSONValue(t *testing.T, src []byte) any {
	t.Helper()
	var v any
	if err := yaml.Unmarshal(src, &v); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	out, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// The schema must accept every valid app, or editors would flag correct files.
func TestJSONSchemaAcceptsExamples(t *testing.T) {
	s := compileSchema(t)
	files, err := filepath.Glob("../../examples/*/app.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no examples found: %v", err)
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Validate(yamlToJSONValue(t, src)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}

func TestJSONSchema(t *testing.T) {
	s := compileSchema(t)
	const head = "apiVersion: shelf.dev/v1alpha1\nname: a\n"
	tests := []struct {
		name  string
		yaml  string
		valid bool
	}{
		{"minimal", head + "components:\n  web: { image: nginx }\n", true},
		{"route short", head + "components:\n  web: { image: nginx, port: 80, route: / }\n", true},
		{"route long", head + "components:\n  web: { image: nginx, ports: { http: 80 }, route: { path: /, port: http } }\n", true},
		{"env scalars", head + "components:\n  web: { image: nginx, env: { A: 1, B: true, C: x } }\n", true},
		{"wrong apiVersion", "apiVersion: shelf.dev/v1\nname: a\ncomponents:\n  web: { image: nginx }\n", false},
		{"missing name", "apiVersion: shelf.dev/v1alpha1\ncomponents:\n  web: { image: nginx }\n", false},
		{"bad app name", "apiVersion: shelf.dev/v1alpha1\nname: A\ncomponents:\n  web: { image: nginx }\n", false},
		{"no components", head + "components: {}\n", false},
		{"missing image", head + "components:\n  web: { port: 80 }\n", false},
		{"unknown field", head + "components:\n  web: { image: nginx, replicas: 2 }\n", false},
		{"port and ports", head + "components:\n  web: { image: nginx, port: 80, ports: { http: 81 } }\n", false},
		{"port out of range", head + "components:\n  web: { image: nginx, port: 70000 }\n", false},
		{"bad component name", head + "components:\n  Web: { image: nginx }\n", false},
		{"route without slash", head + "components:\n  web: { image: nginx, port: 80, route: api }\n", false},
		{"route unknown key", head + "components:\n  web: { image: nginx, port: 80, route: { path: /, x: 1 } }\n", false},
		{"bad env name", head + "components:\n  web: { image: nginx, env: { A-B: x } }\n", false},
		{"env list value", head + "components:\n  web: { image: nginx, env: { A: [x] } }\n", false},
		{"relative volume path", head + "components:\n  web: { image: nginx, volumes: { data: { path: data, size: 1Gi } } }\n", false},
		{"generate false", head + "components:\n  web: { image: nginx }\nsecrets:\n  pw: { generate: false }\n", false},
		{"instances zero", head + "components:\n  web: { image: nginx, instances: 0 }\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.Validate(yamlToJSONValue(t, []byte(tt.yaml)))
			if tt.valid && err != nil {
				t.Errorf("want valid, got %v", err)
			}
			if !tt.valid && err == nil {
				t.Errorf("want invalid, got valid")
			}
		})
	}
}

func TestJSONSchemaID(t *testing.T) {
	s, err := JSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(s), `"$id": "`+SchemaID+`"`) {
		t.Errorf("schema does not carry $id %s", SchemaID)
	}
}
