package render

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/tweinmann/shelf/internal/schema"
	"github.com/tweinmann/shelf/internal/testutil"
	"github.com/tweinmann/shelf/internal/validate"
)

// fakeResolver returns fixed image data without a registry.
type fakeResolver map[string]ImageInfo

func (f fakeResolver) Resolve(_ context.Context, image string) (ImageInfo, error) {
	info, ok := f[image]
	if !ok {
		return ImageInfo{}, errors.New("image not found: " + image)
	}
	return info, nil
}

func digest(c byte) string { return "sha256:" + strings.Repeat(string(c), 64) }

var helloImages = fakeResolver{
	"traefik/whoami:v1.11.0": {Digest: digest('a'), ExposedPorts: []int{80}},
	"postgres:16":            {Digest: digest('b'), ExposedPorts: []int{5432}},
}

func parse(t *testing.T, file string, src []byte) *schema.Document {
	t.Helper()
	doc, err := schema.Parse(file, src)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestRenderHello(t *testing.T) {
	const file = "../../examples/hello/app.yaml"
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	app, findings, err := Render(context.Background(), parse(t, file, src), helloImages, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) > 0 {
		t.Errorf("unexpected findings: %+v", findings)
	}
	cm, err := ConfigMap(app)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Golden(t, "testdata/hello.configmap.yaml", cm)
}

const head = "apiVersion: shelf.dev/v1alpha1\nname: shop\n"

func renderApp(t *testing.T, src string, images fakeResolver, built ...map[string]string) (*schema.App, validate.Findings) {
	t.Helper()
	var b map[string]string
	if len(built) > 0 {
		b = built[0]
	}
	app, findings, err := Render(context.Background(), parse(t, "app.yaml", []byte(src)), images, b)
	if err != nil {
		t.Fatalf("%v: %+v", err, findings)
	}
	return app, findings
}

func TestResolveStrings(t *testing.T) {
	src := head + `
components:
  web:
    image: nginx
    env:
      A: ${ENV_VALUE}
  api: { image: nginx, ports: { http: 8080, admin: 9090 } }
  db: { image: postgres, port: 5432 }
secrets:
  db-password: { generate: true }
`
	tests := []struct {
		in, want string
	}{
		{"plain", "plain"},
		{"", ""},
		{"$HOME", "$$HOME"},
		{"$$HOME", "$$HOME"},
		{"$(HOME)", "$$(HOME)"},
		{"$$(HOME)", "$$(HOME)"},
		{"$2a$10$xyz", "$$2a$$10$$xyz"},
		{"cost $", "cost $$"},
		{"$$${db.host}", "$$db"},
		{"$${db.host}", "$${db.host}"},
		{"${db.host}:${db.port}", "db:5432"},
		{"${api.host}:${api.ports.admin}", "api:9090"},
		{"postgres://app:${secrets.db-password}@${db.host}/x", "postgres://app:${secrets.db-password}@db/x"},
		{"$$${secrets.db-password}", "$$${secrets.db-password}"},
		{"$${secrets.db-password}", "$${secrets.db-password}"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			doc := strings.Replace(src, "${ENV_VALUE}", yamlQuote(tt.in), 1)
			doc = strings.Replace(doc, "image: nginx\n    env:", "image: nginx\n    args: ["+yamlQuote(tt.in)+"]\n    command: ["+yamlQuote(tt.in)+"]\n    env:", 1)
			app, _ := renderApp(t, doc, fakeResolver{"nginx": {Digest: digest('1')}, "postgres": {Digest: digest('2')}})
			web := app.Components["web"]
			for field, got := range map[string]string{"env": web.Env["A"], "args": web.Args[0], "command": web.Command[0]} {
				if got != tt.want {
					t.Errorf("%s: got %q, want %q", field, got, tt.want)
				}
			}
		})
	}
}

func yamlQuote(s string) string {
	b, err := yaml.Marshal(s)
	if err != nil {
		panic(err)
	}
	out := strings.TrimSuffix(string(b), "\n")
	if !strings.HasPrefix(out, "'") && !strings.HasPrefix(out, `"`) {
		out = "'" + strings.ReplaceAll(out, "'", "''") + "'"
	}
	return out
}

// Kubelet expands $(VAR) and turns $$ into $. For any literal text, escaping then expanding
// must give the text back, and no $(...) may survive as a reference.
func TestEscapingRoundTrip(t *testing.T) {
	for _, s := range []string{"$", "$$", "$(A)", "$$(A)", "a$b$(c)$$(d)$", "$$$", "$(", "$(A"} {
		escaped := strings.ReplaceAll(s, "$", "$$")
		if got := kubeletExpand(escaped); got != s {
			t.Errorf("expand(escape(%q)) = %q", s, got)
		}
	}
}

// kubeletExpand follows kubelet's expansion syntax (third_party/forked/golang/expansion):
// $$ becomes $, and $(NAME) is a reference. References are replaced by a marker, which
// escaped text must never produce.
func kubeletExpand(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '$' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		switch s[i+1] {
		case '$':
			b.WriteByte('$')
			i++
		case '(':
			end := strings.IndexByte(s[i+2:], ')')
			if end < 0 {
				b.WriteString(s[i:])
				return b.String()
			}
			b.WriteString("<EXPANDED>")
			i += 2 + end
		default:
			b.WriteByte('$')
		}
	}
	return b.String()
}

func TestNormalize(t *testing.T) {
	app, _ := renderApp(t, head+`
components:
  web:
    image: nginx:1.27
    port: 80
    route: /
    health: { path: /healthz }
  api:
    image: ghcr.io/o/api@sha256:`+strings.Repeat("0", 64)+`
    ports: { http: 8080, admin: 9090 }
    route: { path: /api, port: http, stripPrefix: true }
    health: { path: /healthz, port: admin, initialDelay: 5 }
    instances: 3
  single:
    image: nginx:1.27
    ports: { http: 8080 }
    route: /single
    health: { path: /healthz }
  worker:
    image: nginx:1.27
`, fakeResolver{
		"nginx:1.27": {Digest: digest('1')},
		"ghcr.io/o/api@sha256:" + strings.Repeat("0", 64): {Digest: digest('2')},
	})

	web := app.Components["web"]
	if web.Image != "nginx:1.27@"+digest('1') {
		t.Errorf("web image = %s", web.Image)
	}
	if web.Port != nil || web.Ports["main"] != 80 || len(web.Ports) != 1 {
		t.Errorf("web ports = %v, port = %v", web.Ports, web.Port)
	}
	if *web.Route != (schema.Route{Path: "/", Port: "main"}) {
		t.Errorf("web route = %+v", *web.Route)
	}
	if web.Health.Port != "main" || *web.Instances != 1 {
		t.Errorf("web health = %+v, instances = %d", *web.Health, *web.Instances)
	}

	api := app.Components["api"]
	if api.Image != "ghcr.io/o/api@"+digest('2') {
		t.Errorf("api image = %s (the old digest must be replaced)", api.Image)
	}
	if *api.Route != (schema.Route{Path: "/api", Port: "http", StripPrefix: true}) {
		t.Errorf("api route = %+v", *api.Route)
	}
	if api.Health.Port != "admin" || *api.Health.InitialDelay != 5 || *api.Instances != 3 {
		t.Errorf("api health = %+v, instances = %d", *api.Health, *api.Instances)
	}

	single := app.Components["single"]
	if single.Route.Port != "http" || single.Health.Port != "http" {
		t.Errorf("single route = %+v, health = %+v", *single.Route, *single.Health)
	}

	worker := app.Components["worker"]
	if worker.Ports != nil || worker.Route != nil || worker.Health != nil {
		t.Errorf("worker = %+v", worker)
	}

	// A single port is only ever written as ports.main.
	out, err := MarshalApp(app)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "\n    port:") {
		t.Errorf("normalized output still has a single port field:\n%s", out)
	}
}

func TestRenderDoesNotModifyInput(t *testing.T) {
	doc := parse(t, "app.yaml", []byte(head+`
components:
  web: { image: nginx, port: 80, route: /, env: { A: $x } }
`))
	if _, _, err := Render(context.Background(), doc, fakeResolver{"nginx": {Digest: digest('1')}}, nil); err != nil {
		t.Fatal(err)
	}
	web := doc.App.Components["web"]
	if web.Image != "nginx" || web.Env["A"] != "$x" || web.Route.Port != "" || web.Instances != nil || web.Port == nil {
		t.Errorf("input was modified: %+v", web)
	}
}

func TestExposedPortWarnings(t *testing.T) {
	src := head + `
components:
  web: { image: web, port: 8080 }
  api: { image: api, ports: { http: 80, metrics: 9100 } }
  bare: { image: bare, port: 1234 }
  worker: { image: web }
`
	_, findings := renderApp(t, src, fakeResolver{
		"web":  {Digest: digest('1'), ExposedPorts: []int{80, 443}},
		"api":  {Digest: digest('2'), ExposedPorts: []int{80}},
		"bare": {Digest: digest('3')},
	})
	var got []string
	for _, f := range findings {
		got = append(got, f.Format("app.yaml"))
	}
	want := []string{
		"app.yaml:5: warning: components.web.port: port 8080 is not exposed by image web (EXPOSE 80 443)",
		"app.yaml:6: warning: components.api.ports.metrics: port 9100 is not exposed by image api (EXPOSE 80)",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestRenderErrors(t *testing.T) {
	t.Run("invalid app", func(t *testing.T) {
		doc := parse(t, "app.yaml", []byte(head+"components:\n  web: { image: nginx, route: / }\n"))
		app, findings, err := Render(context.Background(), doc, fakeResolver{}, nil)
		if !errors.Is(err, ErrInvalid) || app != nil || !findings.HasErrors() {
			t.Errorf("got app=%v findings=%v err=%v", app, findings, err)
		}
	})
	t.Run("resolver fails", func(t *testing.T) {
		doc := parse(t, "app.yaml", []byte(head+"components:\n  web: { image: nginx }\n"))
		_, _, err := Render(context.Background(), doc, fakeResolver{}, nil)
		if err == nil || !strings.Contains(err.Error(), "component web: image not found: nginx") {
			t.Errorf("err = %v", err)
		}
	})
}

type countingResolver struct {
	fakeResolver
	calls map[string]int
}

func (c *countingResolver) Resolve(ctx context.Context, image string) (ImageInfo, error) {
	c.calls[image]++
	return c.fakeResolver.Resolve(ctx, image)
}

func TestRenderResolvesEachImageOnce(t *testing.T) {
	r := &countingResolver{fakeResolver{"postgres:16": {Digest: digest('1')}}, map[string]int{}}
	doc := parse(t, "app.yaml", []byte(head+"components:\n  a: { image: 'postgres:16' }\n  b: { image: 'postgres:16' }\n"))
	if _, _, err := Render(context.Background(), doc, r, nil); err != nil {
		t.Fatal(err)
	}
	if r.calls["postgres:16"] != 1 {
		t.Errorf("resolved %d times", r.calls["postgres:16"])
	}
}

func TestConfigMapKeepsSecretsOut(t *testing.T) {
	app, _ := renderApp(t, head+`
components:
  db:
    image: postgres
    env: { POSTGRES_PASSWORD: "${secrets.pw}" }
secrets:
  pw: { generate: true }
`, fakeResolver{"postgres": {Digest: digest('1')}})
	cm, err := ConfigMap(app)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Metadata struct {
			Name      string
			Namespace string
			Labels    map[string]string
		}
		Data map[string]string
	}
	if err := yaml.Unmarshal(cm, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Metadata.Name != "shop-values" || parsed.Metadata.Namespace != "" ||
		parsed.Metadata.Labels["shelf.dev/app"] != "shop" {
		t.Errorf("metadata = %+v", parsed.Metadata)
	}
	// The embedded app.yaml must parse back as an app and keep the reference.
	back, err := schema.Parse("app.yaml", []byte(parsed.Data["app.yaml"]))
	if err != nil {
		t.Fatal(err)
	}
	if got := back.App.Components["db"].Env["POSTGRES_PASSWORD"]; got != "${secrets.pw}" {
		t.Errorf("POSTGRES_PASSWORD = %q", got)
	}
}

func TestRenderBuiltComponents(t *testing.T) {
	const src = head + `
components:
  web:
    build: ./web
    port: 8080
  db:
    image: postgres:16
    port: 5432
`
	images := fakeResolver{
		"ghcr.io/o/shop-web:main": {Digest: digest('c'), ExposedPorts: []int{8080}},
		"postgres:16":             {Digest: digest('b'), ExposedPorts: []int{5432}},
	}
	built := map[string]string{"web": "ghcr.io/o/shop-web:main"}

	app, findings := renderApp(t, src, images, built)
	if len(findings) > 0 {
		t.Errorf("unexpected findings: %+v", findings)
	}
	if got := app.Components["web"].Image; got != "ghcr.io/o/shop-web:main@"+digest('c') {
		t.Errorf("web image %q", got)
	}
	if app.Components["web"].Build != "" {
		t.Error("the resolved app.yaml must not keep the build directory")
	}
	if got := app.Components["db"].Image; got != "postgres:16@"+digest('b') {
		t.Errorf("db image %q", got)
	}
}

func TestRenderBuiltComponentErrors(t *testing.T) {
	const src = head + `
components:
  web: { build: ./web }
  db: { image: postgres:16 }
`
	images := fakeResolver{"ghcr.io/o/shop-web:main": {Digest: digest('c')}, "postgres:16": {Digest: digest('b')}}
	tests := []struct {
		name    string
		built   map[string]string
		wantErr string
	}{
		{"no image for a built component", nil, "pass the image it was pushed as"},
		{"image for an unknown component", map[string]string{"web": "ghcr.io/o/shop-web:main", "nope": "x"}, "no component nope"},
		{"image for a component with an image", map[string]string{"web": "ghcr.io/o/shop-web:main", "db": "x"}, "component db has an image"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := Render(context.Background(), parse(t, "app.yaml", []byte(src)), images, tt.built)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}
