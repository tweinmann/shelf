package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeApp writes an app.yaml into a new directory together with the given build directories.
func writeApp(t *testing.T, content string, dirs ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(root, "app.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const builtApp = `apiVersion: shelf.dev/v1alpha1
name: shop
components:
  web: { build: ./web, port: 8080 }
  api: { build: services/api }
  db: { image: postgres:16, port: 5432 }
`

func TestBuildPlan(t *testing.T) {
	file := writeApp(t, builtApp, "web", "services/api")
	stdout, stderr, code := run(t, "build-plan", file)
	if code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr)
	}
	var plan buildPlan
	if err := json.Unmarshal([]byte(stdout), &plan); err != nil {
		t.Fatalf("%v: %s", err, stdout)
	}
	if plan.App != "shop" {
		t.Errorf("app %q, want shop", plan.App)
	}
	dir := filepath.Dir(file)
	want := []buildItem{
		{Component: "api", Context: filepath.Join(dir, "services/api")},
		{Component: "web", Context: filepath.Join(dir, "web")},
	}
	if len(plan.Builds) != len(want) {
		t.Fatalf("plan %+v, want %+v", plan.Builds, want)
	}
	for i, item := range plan.Builds {
		if item != want[i] {
			t.Errorf("plan[%d] = %+v, want %+v", i, item, want[i])
		}
	}
}

func TestBuildPlanWithoutBuilds(t *testing.T) {
	file := writeApp(t, "apiVersion: shelf.dev/v1alpha1\nname: shop\ncomponents:\n  db: { image: postgres:16 }\n")
	stdout, _, code := run(t, "build-plan", file)
	var plan buildPlan
	if code != 0 || json.Unmarshal([]byte(stdout), &plan) != nil || plan.App != "shop" || len(plan.Builds) != 0 {
		t.Errorf("exit code %d, stdout %q", code, stdout)
	}
}

func TestBuildPlanMissingDirectory(t *testing.T) {
	file := writeApp(t, builtApp, "web")
	_, stderr, code := run(t, "build-plan", file)
	if code == 0 || !strings.Contains(stderr, "build directory") {
		t.Errorf("exit code %d, stderr %q", code, stderr)
	}
}

func TestParseImages(t *testing.T) {
	got, err := parseImages([]string{"web=ghcr.io/o/web@sha256:abc", "api=ghcr.io/o/api:main"})
	if err != nil {
		t.Fatal(err)
	}
	if got["web"] != "ghcr.io/o/web@sha256:abc" || got["api"] != "ghcr.io/o/api:main" || len(got) != 2 {
		t.Errorf("got %v", got)
	}
	for _, bad := range []string{"web", "=ref", "web=", "Web=ref", "my_web=ref"} {
		if _, err := parseImages([]string{bad}); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
	if _, err := parseImages([]string{"web=a", "web=b"}); err == nil {
		t.Error("a repeated component must be rejected")
	}
}
