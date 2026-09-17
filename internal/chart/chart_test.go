// Package chart tests the shelf-app Helm chart: it renders app.yaml files with the real
// renderer and compares `helm template` output against golden files.
package chart

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tweinmann/shelf/internal/render"
	"github.com/tweinmann/shelf/internal/schema"
	"github.com/tweinmann/shelf/internal/testutil"
)

const chartDir = "../../charts/shelf-app"

// middlewareAPI is what a cluster with the Traefik CRDs reports.
const middlewareAPI = "traefik.io/v1alpha1/Middleware"

// readChart reads every chart file. helm reads them in a subprocess, which go test does not
// see; files the test itself opens become part of the test cache key, so a chart change
// invalidates cached results.
func readChart(t *testing.T) {
	t.Helper()
	err := filepath.WalkDir(chartDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		_, err = os.ReadFile(path)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

type fakeResolver struct{}

func (fakeResolver) Resolve(_ context.Context, image string) (render.ImageInfo, error) {
	return render.ImageInfo{Digest: "sha256:" + strings.Repeat("0", 64)}, nil
}

// renderValues turns an app.yaml into the chart values file, as `shelf render -o app` does.
func renderValues(t *testing.T, file string) string {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := schema.Parse(file, src)
	if err != nil {
		t.Fatal(err)
	}
	app, findings, err := render.Render(context.Background(), doc, fakeResolver{}, nil)
	if err != nil {
		t.Fatalf("%v: %+v", err, findings)
	}
	if findings.HasErrors() {
		t.Fatalf("unexpected errors: %+v", findings)
	}
	out, err := render.MarshalApp(app)
	if err != nil {
		t.Fatal(err)
	}
	return writeFile(t, "values.yaml", string(out))
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// helmTemplate runs `helm template` and returns stdout, or stderr as the error.
func helmTemplate(t *testing.T, namespace string, args ...string) (string, error) {
	t.Helper()
	readChart(t)
	cmdArgs := append([]string{"template", "release", chartDir, "--namespace", namespace}, args...)
	cmd := exec.Command("helm", cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("running helm: %v", err)
		}
		return "", errors.New(stderr.String())
	}
	return stdout.String(), nil
}

func TestTemplate(t *testing.T) {
	tests := []struct {
		name     string
		app      string
		platform string
		apis     []string
	}{
		{
			name:     "hello",
			app:      "../../examples/hello/app.yaml",
			platform: "platform:\n  domain: dev.local\n",
		},
		{
			name:     "features",
			app:      "testdata/features.app.yaml",
			platform: "platform:\n  domain: example.com\n  imagePullSecret: ghcr-pull\n",
			apis:     []string{middlewareAPI},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := []string{"-f", renderValues(t, tt.app), "-f", writeFile(t, "platform.yaml", tt.platform)}
			for _, api := range tt.apis {
				args = append(args, "--api-versions", api)
			}
			out, err := helmTemplate(t, tt.name, args...)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out, "LoadBalancer") {
				t.Error("the chart must never render a LoadBalancer")
			}
			testutil.Golden(t, filepath.Join("testdata", tt.name+".manifests.yaml"), []byte(out))
		})
	}
}

// TestSecretReferences checks which secrets a container receives: only those referenced
// outside an escaped $$, each once, before all other variables.
func TestSecretReferences(t *testing.T) {
	values := renderValues(t, "testdata/features.app.yaml")
	platform := writeFile(t, "platform.yaml", "platform:\n  domain: example.com\n")
	out, err := helmTemplate(t, "features", "-f", values, "-f", platform, "--api-versions", middlewareAPI,
		"--show-only", "templates/workloads.yaml")
	if err != nil {
		t.Fatal(err)
	}
	api := out[strings.Index(out, "name: api\n"):strings.Index(out, "kind: StatefulSet")]

	if strings.Contains(api, "SHELF_SECRET_UNUSED") {
		t.Error("a secret referenced only as $${secrets.unused} must not be injected")
	}
	if n := strings.Count(api, "name: SHELF_SECRET_API_TOKEN"); n != 1 {
		t.Errorf("SHELF_SECRET_API_TOKEN defined %d times, want 1", n)
	}
	envStart := strings.Index(api, "env:")
	for _, name := range []string{"SHELF_SECRET_API_TOKEN", "SHELF_SECRET_SESSION_KEY"} {
		if i := strings.Index(api, "name: "+name); i < envStart || i > strings.Index(api, "name: HOME_DIR") {
			t.Errorf("%s must come first in env", name)
		}
	}
	for _, want := range []string{
		`"--token=$(SHELF_SECRET_API_TOKEN)"`,
		`"--literal=$${secrets.unused}"`,
		`"--price=$$5"`,
		`value: "$(SHELF_SECRET_SESSION_KEY)-$(SHELF_SECRET_API_TOKEN)"`,
		`value: "$$HOME"`,
	} {
		if !strings.Contains(api, want) {
			t.Errorf("api container lacks %s", want)
		}
	}
}

// TestChartVersionLabel checks the chart label with a version as Flux sets it, with the chart
// digest appended after "+", which label values do not allow.
func TestChartVersionLabel(t *testing.T) {
	values := renderValues(t, "../../examples/hello/app.yaml")
	platform := writeFile(t, "platform.yaml", "platform:\n  domain: dev.local\n")
	chart := t.TempDir()
	cmd := exec.Command("helm", "package", chartDir, "--version", "0.1.0+"+strings.Repeat("a", 64), "--destination", chart)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helm package: %v\n%s", err, out)
	}
	readChart(t)
	out, err := exec.Command("helm", "template", "hello", filepath.Join(chart, "shelf-app-0.1.0+"+strings.Repeat("a", 64)+".tgz"),
		"--namespace", "hello", "-f", values, "-f", platform).Output()
	if err != nil {
		t.Fatal(err)
	}
	want := "helm.sh/chart: shelf-app-0.1.0_" + strings.Repeat("a", 63-len("shelf-app-0.1.0_"))
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "helm.sh/chart:") && strings.TrimSpace(line) != want {
			t.Fatalf("got %q, want %q", strings.TrimSpace(line), want)
		}
	}
}

func TestTemplateErrors(t *testing.T) {
	hello := "../../examples/hello/app.yaml"
	features := "testdata/features.app.yaml"
	domain := "platform:\n  domain: dev.local\n"
	tests := []struct {
		name      string
		app       string // empty: chart defaults only
		namespace string
		platform  string
		want      string
	}{
		{"no values", "", "hello", domain, "name is required"},
		{"wrong namespace", hello, "default", domain, `app "hello" must be installed into namespace "hello"`},
		{"route without domain", hello, "hello", "platform:\n  domain: \"\"\n", "platform.domain is not set"},
		{"stripPrefix without Traefik CRD", features, "features", domain, "needs the Traefik Middleware CRD"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := []string{"-f", writeFile(t, "platform.yaml", tt.platform)}
			if tt.app != "" {
				args = append(args, "-f", renderValues(t, tt.app))
			}
			_, err := helmTemplate(t, tt.namespace, args...)
			if err == nil {
				t.Fatal("helm template succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not contain %q", err, tt.want)
			}
		})
	}
}
