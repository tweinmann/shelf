// Package testutil holds helpers shared by tests.
package testutil

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files instead of comparing against them")

// Golden compares got with the file at path, or rewrites the file when -update is set.
func Golden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run the test with -update to create it)", err)
	}
	if string(got) != string(want) {
		t.Errorf("output differs from %s (run the test with -update to accept it)\n--- got ---\n%s\n--- want ---\n%s",
			path, got, want)
	}
}
