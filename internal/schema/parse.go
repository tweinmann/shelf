package schema

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Document is a parsed app.yaml together with the YAML tree, which maps field paths back to
// line numbers for error messages.
type Document struct {
	File string
	App  *App
	root *yaml.Node
}

// Parse decodes an app.yaml file strictly: unknown fields, duplicate keys, wrong types and
// multiple documents are errors. Semantic checks live in package validate.
func Parse(file string, data []byte) (*Document, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if root.Kind == 0 {
		return nil, fmt.Errorf("%s: file is empty", file)
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var app App
	if err := dec.Decode(&app); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: expected a single YAML document", file)
	}
	return &Document{File: file, App: &app, root: &root}, nil
}

// Line returns the line of the node at path, e.g. ("components", "web", "port"). If the path
// does not exist, it returns the line of the deepest ancestor that does, and 0 without a tree.
func (d *Document) Line(path ...string) int {
	if d.root == nil || len(d.root.Content) == 0 {
		return 0
	}
	node := d.root.Content[0]
	line := node.Line
	for _, key := range path {
		switch node.Kind {
		case yaml.MappingNode:
			value := mappingValue(node, key)
			if value == nil {
				return line
			}
			line = value.key.Line
			node = value.value
		case yaml.SequenceNode:
			var idx int
			if _, err := fmt.Sscanf(key, "%d", &idx); err != nil || idx < 0 || idx >= len(node.Content) {
				return line
			}
			node = node.Content[idx]
			line = node.Line
		default:
			return line
		}
	}
	return line
}

type keyValue struct{ key, value *yaml.Node }

// mappingValue returns the entry for key in a mapping node, or nil. Content alternates keys
// and values.
func mappingValue(node *yaml.Node, key string) *keyValue {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return &keyValue{node.Content[i], node.Content[i+1]}
		}
	}
	return nil
}

var routeKeys = []string{"path", "port", "stripPrefix"}

// UnmarshalYAML accepts the short form `route: /path` and the long form
// `route: { path, port, stripPrefix }`.
func (r *Route) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Tag != "!!str" {
			return fmt.Errorf("line %d: route must be a path or a mapping", node.Line)
		}
		*r = Route{Path: node.Value}
		return nil
	case yaml.MappingNode:
		// node.Decode does not honor KnownFields, so check the keys here.
		for i := 0; i < len(node.Content); i += 2 {
			k := node.Content[i]
			if !slices.Contains(routeKeys, k.Value) {
				return fmt.Errorf("line %d: field %s not found in route (allowed: %s)",
					k.Line, k.Value, strings.Join(routeKeys, ", "))
			}
		}
		type plain Route
		var p plain
		if err := node.Decode(&p); err != nil {
			return err
		}
		*r = Route(p)
		return nil
	default:
		return fmt.Errorf("line %d: route must be a path or a mapping", node.Line)
	}
}
