package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tweinmann/shelf/internal/render"
	"github.com/tweinmann/shelf/internal/testutil"
)

type fakeResolver map[string]render.ImageInfo

func (f fakeResolver) Resolve(_ context.Context, image string) (render.ImageInfo, error) {
	info, ok := f[image]
	if !ok {
		return render.ImageInfo{}, errors.New("image not found: " + image)
	}
	return info, nil
}

var images = fakeResolver{
	"traefik/whoami:v1.11.0": {Digest: "sha256:" + strings.Repeat("a", 64), ExposedPorts: []int{80}},
	"postgres:16":            {Digest: "sha256:" + strings.Repeat("b", 64), ExposedPorts: []int{5432}},
}

const hello = "../../examples/hello/app.yaml"

func run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := New(images)
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	code = Execute(context.Background(), cmd)
	return out.String(), errOut.String(), code
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestValidate(t *testing.T) {
	invalid := writeFile(t, "apiVersion: shelf.dev/v1alpha1\nname: a\ncomponents:\n  web: { image: nginx, route: / }\n")
	warning := writeFile(t, "apiVersion: shelf.dev/v1alpha1\nname: a\ncomponents:\n  web: { image: nginx }\nsecrets:\n  pw: { generate: true }\n")
	broken := writeFile(t, "name: [\n")

	tests := []struct {
		name       string
		args       []string
		code       int
		wantOut    string
		wantErrOut string
	}{
		{"valid", []string{"validate", hello}, 0, hello + ": valid\n", ""},
		{"warning only", []string{"validate", warning}, 0, warning + ": valid (1 warning)\n",
			warning + ":6: warning: secrets.pw: secret \"pw\" is not referenced by any component\n"},
		{"invalid", []string{"validate", invalid}, 1, invalid + ": invalid (1 error, 0 warnings)\n",
			invalid + ":4: error: components.web.route: route needs a port; add port or ports to the component\n"},
		{"several files, one invalid", []string{"validate", hello, invalid}, 1,
			hello + ": valid\n" + invalid + ": invalid (1 error, 0 warnings)\n", "route needs a port"},
		{"parse error", []string{"validate", broken}, 1, "", broken + ": yaml:"},
		{"missing file", []string{"validate", "/nonexistent/app.yaml"}, 1, "", "no such file"},
		{"no arguments", []string{"validate"}, 1, "", "error: requires at least 1 arg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, errOut, code := run(t, tt.args...)
			if code != tt.code {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, tt.code, errOut)
			}
			if out != tt.wantOut {
				t.Errorf("stdout = %q, want %q", out, tt.wantOut)
			}
			if !strings.Contains(errOut, tt.wantErrOut) {
				t.Errorf("stderr = %q, want it to contain %q", errOut, tt.wantErrOut)
			}
		})
	}
}

func TestRender(t *testing.T) {
	golden := map[string]string{
		"configmap": "../render/testdata/hello.configmap.yaml",
		"app":       "testdata/render-hello.app.yaml",
	}
	for output, file := range golden {
		t.Run(output, func(t *testing.T) {
			out, errOut, code := run(t, "render", "-o", output, hello)
			if code != 0 {
				t.Fatalf("exit code %d: %s", code, errOut)
			}
			testutil.Golden(t, file, []byte(out))
			testutil.Golden(t, "testdata/render-hello.summary.txt", []byte(errOut))
		})
	}
}

func TestRenderDefaultsToConfigMap(t *testing.T) {
	out, _, code := run(t, "render", hello)
	if code != 0 || !strings.HasPrefix(out, "apiVersion: v1\nkind: ConfigMap\n") {
		t.Errorf("exit code %d, output:\n%s", code, out)
	}
}

func TestRenderFailures(t *testing.T) {
	invalid := writeFile(t, "apiVersion: shelf.dev/v1alpha1\nname: a\ncomponents:\n  web: { image: nginx, route: / }\n")
	unknownImage := writeFile(t, "apiVersion: shelf.dev/v1alpha1\nname: a\ncomponents:\n  web: { image: nginx }\n")
	tests := []struct {
		name, wantErrOut string
		args             []string
	}{
		{"invalid app", "route needs a port", []string{"render", invalid}},
		{"unknown image", "error: component web: image not found: nginx", []string{"render", unknownImage}},
		{"bad output", `error: --output must be configmap or app, got "yaml"`, []string{"render", "-o", "yaml", hello}},
		{"two files", "error: accepts 1 arg(s), received 2", []string{"render", hello, hello}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, errOut, code := run(t, tt.args...)
			if code != 1 || out != "" || !strings.Contains(errOut, tt.wantErrOut) {
				t.Errorf("code = %d, stdout = %q, stderr = %q, want stderr to contain %q", code, out, errOut, tt.wantErrOut)
			}
		})
	}
}

func TestSchemaCommandMatchesCommittedFile(t *testing.T) {
	out, _, code := run(t, "schema")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	want, err := os.ReadFile("../../schema/app.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if out != string(want) {
		t.Error("shelf schema differs from schema/app.schema.json; run `just schema`")
	}
}

func TestVersion(t *testing.T) {
	out, _, code := run(t, "version")
	if code != 0 || !strings.HasPrefix(out, "shelf ") || !strings.Contains(out, "(app.yaml shelf.dev/v1alpha1)") {
		t.Errorf("code = %d, output = %q", code, out)
	}
}
