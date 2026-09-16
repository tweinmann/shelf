// Package validate checks an app.yaml beyond its structure: names, references, ports, routes,
// volumes and quantities.
package validate

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// Severity tells whether a finding blocks rendering.
type Severity int

const (
	Error Severity = iota
	Warning
)

func (s Severity) String() string {
	if s == Warning {
		return "warning"
	}
	return "error"
}

// Finding is one problem in an app.yaml, located by field path and line.
type Finding struct {
	Severity Severity
	Path     string // dotted field path, e.g. components.web.route
	Line     int    // 0 if unknown
	Message  string
}

// Format renders the finding as "<file>:<line>: <severity>: <path>: <message>".
func (f Finding) Format(file string) string {
	loc := file
	if f.Line > 0 {
		loc = fmt.Sprintf("%s:%d", file, f.Line)
	}
	return fmt.Sprintf("%s: %s: %s: %s", loc, f.Severity, f.Path, f.Message)
}

// Findings is a list of findings.
type Findings []Finding

// HasErrors reports whether any finding is an error.
func (fs Findings) HasErrors() bool {
	return slices.ContainsFunc(fs, func(f Finding) bool { return f.Severity == Error })
}

// Count returns the number of findings with the given severity.
func (fs Findings) Count(s Severity) int {
	n := 0
	for _, f := range fs {
		if f.Severity == s {
			n++
		}
	}
	return n
}

// Sort orders findings by line, then path, then message, so output is stable.
func (fs Findings) Sort() {
	slices.SortStableFunc(fs, func(a, b Finding) int {
		return cmp.Or(cmp.Compare(a.Line, b.Line), strings.Compare(a.Path, b.Path),
			strings.Compare(a.Message, b.Message))
	})
}
