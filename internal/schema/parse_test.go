package schema

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string // substring; empty means success
		check   func(t *testing.T, app *App)
	}{
		{
			name: "minimal",
			yaml: "apiVersion: shelf.dev/v1alpha1\nname: a\ncomponents:\n  web:\n    image: nginx\n",
			check: func(t *testing.T, app *App) {
				if app.Components["web"].Image != "nginx" {
					t.Errorf("image = %q", app.Components["web"].Image)
				}
			},
		},
		{
			name: "route short form",
			yaml: "components:\n  web:\n    route: /api\n",
			check: func(t *testing.T, app *App) {
				if got := *app.Components["web"].Route; got != (Route{Path: "/api"}) {
					t.Errorf("route = %+v", got)
				}
			},
		},
		{
			name: "route long form",
			yaml: "components:\n  web:\n    route: { path: /api, port: http, stripPrefix: true }\n",
			check: func(t *testing.T, app *App) {
				want := Route{Path: "/api", Port: "http", StripPrefix: true}
				if got := *app.Components["web"].Route; got != want {
					t.Errorf("route = %+v, want %+v", got, want)
				}
			},
		},
		{
			name: "env scalars keep their text",
			yaml: "components:\n  web:\n    env: { PORT: 8080, DEBUG: true, RATIO: 0.5 }\n",
			check: func(t *testing.T, app *App) {
				env := app.Components["web"].Env
				if env["PORT"] != "8080" || env["DEBUG"] != "true" || env["RATIO"] != "0.5" {
					t.Errorf("env = %v", env)
				}
			},
		},
		{
			name: "explicit zero is kept apart from unset",
			yaml: "components:\n  web:\n    instances: 0\n  api: {}\n",
			check: func(t *testing.T, app *App) {
				if app.Components["web"].Instances == nil || app.Components["api"].Instances != nil {
					t.Errorf("instances: web=%v api=%v", app.Components["web"].Instances, app.Components["api"].Instances)
				}
			},
		},
		{name: "empty file", yaml: "", wantErr: "file is empty"},
		{name: "comment only", yaml: "# nothing\n", wantErr: "file is empty"},
		{name: "unknown top-level field", yaml: "nmae: a\n", wantErr: "field nmae not found"},
		{name: "unknown component field", yaml: "components:\n  web:\n    replicas: 2\n", wantErr: "field replicas not found"},
		{name: "unknown route field", yaml: "components:\n  web:\n    route: { path: /, strip: true }\n", wantErr: "field strip not found in route"},
		{name: "route of wrong type", yaml: "components:\n  web:\n    route: [/]\n", wantErr: "route must be a path or a mapping"},
		{name: "route as number", yaml: "components:\n  web:\n    route: 80\n", wantErr: "route must be a path or a mapping"},
		{name: "duplicate component", yaml: "components:\n  web: {}\n  web: {}\n", wantErr: `mapping key "web" already defined`},
		{name: "duplicate volume", yaml: "components:\n  db:\n    volumes:\n      data: { path: /a, size: 1Gi }\n      data: { path: /b, size: 1Gi }\n", wantErr: `mapping key "data" already defined`},
		{name: "port as string", yaml: "components:\n  web:\n    port: http\n", wantErr: "cannot unmarshal"},
		{name: "two documents", yaml: "name: a\n---\nname: b\n", wantErr: "single YAML document"},
		{name: "not a mapping", yaml: "- a\n", wantErr: "cannot unmarshal"},
		{name: "syntax error", yaml: "name: [a\n", wantErr: "app.yaml: yaml:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := Parse("app.yaml", []byte(tt.yaml))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, doc.App)
		})
	}
}

func TestDocumentLine(t *testing.T) {
	src := `apiVersion: shelf.dev/v1alpha1
name: a
components:
  web:
    image: nginx
    args:
      - one
      - two
    env:
      image: not-a-key-match
`
	doc, err := Parse("app.yaml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path []string
		want int
	}{
		{nil, 1},
		{[]string{"name"}, 2},
		{[]string{"components", "web"}, 4},
		{[]string{"components", "web", "image"}, 5},
		{[]string{"components", "web", "args", "1"}, 8},
		{[]string{"components", "web", "env", "image"}, 10},
		{[]string{"components", "web", "route"}, 4},      // missing: nearest ancestor
		{[]string{"components", "web", "args", "5"}, 6},  // index out of range
		{[]string{"components", "web", "image", "x"}, 5}, // below a scalar
		{[]string{"components", "api", "image"}, 3},      // missing component
		{[]string{"secrets", "db-password"}, 1},          // missing top-level key
	}
	for _, tt := range tests {
		if got := doc.Line(tt.path...); got != tt.want {
			t.Errorf("Line(%v) = %d, want %d", tt.path, got, tt.want)
		}
	}
}

func TestSecretEnvName(t *testing.T) {
	if got := SecretEnvName("db-password"); got != "SHELF_SECRET_DB_PASSWORD" {
		t.Errorf("got %q", got)
	}
}
