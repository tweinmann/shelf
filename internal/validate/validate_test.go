package validate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tweinmann/shelf/internal/schema"
)

func mustParse(t *testing.T, src string) *schema.Document {
	t.Helper()
	doc, err := schema.Parse("app.yaml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

const head = "apiVersion: shelf.dev/v1alpha1\nname: shop\n"

func TestExamplesAreValid(t *testing.T) {
	files, err := filepath.Glob("../../examples/*/app.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no examples found: %v", err)
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if findings := Validate(mustParse(t, string(src))); len(findings) > 0 {
			t.Errorf("%s: unexpected findings:\n%s", f, formatAll(findings))
		}
	}
}

func formatAll(fs Findings) string {
	var lines []string
	for _, f := range fs {
		lines = append(lines, "  "+f.Format("app.yaml"))
	}
	return strings.Join(lines, "\n")
}

func TestValid(t *testing.T) {
	tests := map[string]string{
		"minimal": head + `
components:
  web: { image: nginx }
`,
		"digest-pinned image": head + `
components:
  web: { image: "ghcr.io/o/web:1.0@sha256:0000000000000000000000000000000000000000000000000000000000000000" }
`,
		"route with single port": head + `
components:
  web: { image: nginx, port: 80, route: / }
`,
		"route picks one of several ports": head + `
components:
  web:
    image: nginx
    ports: { http: 80, admin: 81 }
    route: { path: /api, port: http, stripPrefix: true }
    health: { path: /healthz, port: admin, initialDelay: 0 }
`,
		"route and health default to the only named port": head + `
components:
  web:
    image: nginx
    ports: { http: 80 }
    route: /
    health: { path: /healthz }
`,
		"different route paths, including nested ones": head + `
components:
  web: { image: nginx, port: 80, route: / }
  api: { image: nginx, port: 80, route: /api }
  admin: { image: nginx, port: 80, route: /api/admin }
`,
		"references of every kind": head + `
components:
  web:
    image: nginx
    command: ["${api.host}"]
    args: ["${api.ports.http}", "${db.port}"]
    env:
      URL: postgres://u:${secrets.pw}@${db.host}:${db.port}/x
      ADMIN: ${api.ports.admin}
      LITERAL: $$HOME $$${secrets.pw}
      BCRYPT: $2a$10$abcdef
  api: { image: nginx, ports: { http: 80, admin: 81 } }
  db: { image: postgres, port: 5432 }
secrets:
  pw: { generate: true }
`,
		".port on a component with a single named port": head + `
components:
  web: { image: nginx, env: { P: "${api.port}" } }
  api: { image: nginx, ports: { http: 80 } }
`,
		"volumes side by side": head + `
components:
  db:
    image: postgres
    volumes:
      data: { path: /var/lib/data, size: 1Gi }
      data2: { path: /var/lib/data2, size: 500Mi }
`,
		"resources": head + `
components:
  web: { image: nginx, resources: { cpu: "0.5", memory: 1G } }
`,
		"instances": head + `
components:
  web: { image: nginx, instances: 3 }
`,
		"longest names": "apiVersion: shelf.dev/v1alpha1\nname: " + strings.Repeat("a", 40) + `
components:
  ` + strings.Repeat("c", 40) + `:
    image: nginx
    volumes:
      ` + strings.Repeat("v", 40) + `: { path: /d, size: 1Gi }
`,
	}
	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			if findings := Validate(mustParse(t, src)); len(findings) > 0 {
				t.Errorf("unexpected findings:\n%s", formatAll(findings))
			}
		})
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		path string // expected finding path
		msg  string // expected substring of the message
	}{
		// apiVersion and name
		{"missing apiVersion", "name: a\ncomponents:\n  web: { image: nginx }\n", "apiVersion", "must be shelf.dev/v1alpha1"},
		{"wrong apiVersion", "apiVersion: shelf.dev/v1\nname: a\ncomponents:\n  web: { image: nginx }\n", "apiVersion", `got "shelf.dev/v1"`},
		{"missing name", "apiVersion: shelf.dev/v1alpha1\ncomponents:\n  web: { image: nginx }\n", "name", "is required"},
		{"uppercase name", "apiVersion: shelf.dev/v1alpha1\nname: Shop\ncomponents:\n  web: { image: nginx }\n", "name", "lowercase"},
		{"name ends with dash", "apiVersion: shelf.dev/v1alpha1\nname: shop-\ncomponents:\n  web: { image: nginx }\n", "name", "lowercase"},
		{"name too long", "apiVersion: shelf.dev/v1alpha1\nname: " + strings.Repeat("a", 41) + "\ncomponents:\n  web: { image: nginx }\n", "name", "at most 40"},
		{"reserved name", "apiVersion: shelf.dev/v1alpha1\nname: flux-system\ncomponents:\n  web: { image: nginx }\n", "name", "reserved"},
		{"reserved name prefix kube", "apiVersion: shelf.dev/v1alpha1\nname: kube-public\ncomponents:\n  web: { image: nginx }\n", "name", "reserved"},
		{"reserved name prefix shelf", "apiVersion: shelf.dev/v1alpha1\nname: shelf-system\ncomponents:\n  web: { image: nginx }\n", "name", "reserved"},

		// components
		{"no components", head, "components", "at least one component"},
		{"empty components", head + "components: {}\n", "components", "at least one component"},
		{"empty component", head + "components:\n  web:\n", "components.web", "component is empty"},
		{"component name with underscore", head + "components:\n  my_web: { image: nginx }\n", "components.my_web", "component name"},
		{"component name starts with digit", head + "components:\n  1web: { image: nginx }\n", "components.1web", "start with a letter"},
		{"component name too long", head + "components:\n  " + strings.Repeat("c", 41) + ": { image: nginx }\n", "components." + strings.Repeat("c", 41), "at most 40"},
		{"reserved component secrets", head + "components:\n  secrets: { image: nginx }\n", "components.secrets", "reserved"},
		{"reserved component app", head + "components:\n  app: { image: nginx }\n", "components.app", "reserved"},
		{"reserved component shelf", head + "components:\n  shelf: { image: nginx }\n", "components.shelf", "reserved"},
		{"reserved component suffix headless", head + "components:\n  db-headless: { image: nginx }\n", "components.db-headless", "reserved"},
		{"missing image", head + "components:\n  web: { port: 80 }\n", "components.web.image", "is required"},
		{"invalid image", head + "components:\n  web: { image: 'Not An Image' }\n", "components.web.image", "invalid image reference"},
		{"instances zero", head + "components:\n  web: { image: nginx, instances: 0 }\n", "components.web.instances", "at least 1"},

		// ports
		{"port and ports", head + "components:\n  web: { image: nginx, port: 80, ports: { http: 81 } }\n", "components.web.ports", "either port or ports"},
		{"port zero", head + "components:\n  web: { image: nginx, port: 0 }\n", "components.web.port", "between 1 and 65535"},
		{"port too large", head + "components:\n  web: { image: nginx, port: 65536 }\n", "components.web.port", "between 1 and 65535"},
		{"empty ports", head + "components:\n  web: { image: nginx, ports: {} }\n", "components.web.ports", "must not be empty"},
		{"named port out of range", head + "components:\n  web: { image: nginx, ports: { http: -1 } }\n", "components.web.ports.http", "between 1 and 65535"},
		{"port name too long", head + "components:\n  web: { image: nginx, ports: { averyveryverylongname: 80 } }\n", "components.web.ports.averyveryverylongname", "port name"},
		{"port name digits only", head + "components:\n  web: { image: nginx, ports: { '8080': 80 } }\n", "components.web.ports.8080", "port name"},
		{"duplicate port number", head + "components:\n  web: { image: nginx, ports: { a: 80, b: 80 } }\n", "components.web.ports.b", `already used by port "a"`},

		// route
		{"route without port", head + "components:\n  web: { image: nginx, route: / }\n", "components.web.route", "route needs a port"},
		{"route without port name, several ports", head + "components:\n  web: { image: nginx, ports: { http: 80, admin: 81 }, route: / }\n", "components.web.route", "set route.port to one of admin, http"},
		{"route with unknown port", head + "components:\n  web: { image: nginx, ports: { http: 80 }, route: { path: /, port: https } }\n", "components.web.route.port", `unknown port "https"`},
		{"route port on single port", head + "components:\n  web: { image: nginx, port: 80, route: { path: /, port: main } }\n", "components.web.route.port", "remove route.port"},
		{"route path empty", head + "components:\n  web: { image: nginx, port: 80, route: { path: '' } }\n", "components.web.route", "route path is required"},
		{"route path relative", head + "components:\n  web: { image: nginx, port: 80, route: api }\n", "components.web.route", "must start with /"},
		{"route path with query", head + "components:\n  web: { image: nginx, port: 80, route: '/a?b' }\n", "components.web.route", "must not contain"},
		{"colliding routes", head + "components:\n  a: { image: nginx, port: 80, route: /api }\n  b: { image: nginx, port: 80, route: /api }\n", "components.b.route", `already used by component "a"`},
		{"colliding routes with trailing slash", head + "components:\n  a: { image: nginx, port: 80, route: /api/ }\n  b: { image: nginx, port: 80, route: /api }\n", "components.b.route", `already used by component "a"`},
		{"colliding root routes", head + "components:\n  a: { image: nginx, port: 80, route: / }\n  b: { image: nginx, port: 80, route: { path: / } }\n", "components.b.route", `already used by component "a"`},

		// volumes
		{"empty volume", head + "components:\n  db:\n    image: postgres\n    volumes:\n      data:\n", "components.db.volumes.data", "volume is empty"},
		{"volume name invalid", head + "components:\n  db:\n    image: postgres\n    volumes:\n      Data: { path: /d, size: 1Gi }\n", "components.db.volumes.Data", "volume name"},
		{"volume name too long", head + "components:\n  db:\n    image: postgres\n    volumes:\n      " + strings.Repeat("v", 41) + ": { path: /d, size: 1Gi }\n", "components.db.volumes." + strings.Repeat("v", 41), "at most 40"},
		{"volume without path", head + "components:\n  db:\n    image: postgres\n    volumes:\n      data: { size: 1Gi }\n", "components.db.volumes.data.path", "is required"},
		{"volume relative path", head + "components:\n  db:\n    image: postgres\n    volumes:\n      data: { path: data, size: 1Gi }\n", "components.db.volumes.data.path", "must be absolute"},
		{"volume at root", head + "components:\n  db:\n    image: postgres\n    volumes:\n      data: { path: /, size: 1Gi }\n", "components.db.volumes.data.path", "must not be /"},
		{"volume path not clean", head + "components:\n  db:\n    image: postgres\n    volumes:\n      data: { path: /var/lib/, size: 1Gi }\n", "components.db.volumes.data.path", `use "/var/lib"`},
		{"volume without size", head + "components:\n  db:\n    image: postgres\n    volumes:\n      data: { path: /d }\n", "components.db.volumes.data.size", "is required"},
		{"volume invalid size", head + "components:\n  db:\n    image: postgres\n    volumes:\n      data: { path: /d, size: 1GB }\n", "components.db.volumes.data.size", "invalid quantity"},
		{"volume zero size", head + "components:\n  db:\n    image: postgres\n    volumes:\n      data: { path: /d, size: '0' }\n", "components.db.volumes.data.size", "greater than zero"},
		{"volumes with the same path", head + "components:\n  db:\n    image: postgres\n    volumes:\n      a: { path: /data, size: 1Gi }\n      b: { path: /data, size: 1Gi }\n", "components.db.volumes.b.path", `overlaps with /data of volume "a"`},
		{"nested volume paths", head + "components:\n  db:\n    image: postgres\n    volumes:\n      a: { path: /data/sub, size: 1Gi }\n      b: { path: /data, size: 1Gi }\n", "components.db.volumes.b.path", `overlaps with /data/sub of volume "a"`},

		// health
		{"health without port", head + "components:\n  web: { image: nginx, health: { path: /healthz } }\n", "components.web.health", "health needs a port"},
		{"health path missing", head + "components:\n  web: { image: nginx, port: 80, health: { initialDelay: 5 } }\n", "components.web.health.path", "is required"},
		{"health path relative", head + "components:\n  web: { image: nginx, port: 80, health: { path: healthz } }\n", "components.web.health.path", "must start with /"},
		{"health without port name, several ports", head + "components:\n  web: { image: nginx, ports: { a: 80, b: 81 }, health: { path: / } }\n", "components.web.health", "set health.port"},
		{"health with unknown port", head + "components:\n  web: { image: nginx, ports: { a: 80 }, health: { path: /, port: b } }\n", "components.web.health.port", `unknown port "b"`},
		{"health port on single port", head + "components:\n  web: { image: nginx, port: 80, health: { path: /, port: main } }\n", "components.web.health.port", "remove health.port"},
		{"health negative delay", head + "components:\n  web: { image: nginx, port: 80, health: { path: /, initialDelay: -1 } }\n", "components.web.health.initialDelay", "must not be negative"},

		// resources
		{"invalid cpu", head + "components:\n  web: { image: nginx, resources: { cpu: half } }\n", "components.web.resources.cpu", "invalid quantity"},
		{"invalid memory", head + "components:\n  web: { image: nginx, resources: { memory: 512MB } }\n", "components.web.resources.memory", "invalid quantity"},
		{"negative memory", head + "components:\n  web: { image: nginx, resources: { memory: -1Gi } }\n", "components.web.resources.memory", "greater than zero"},

		// env
		{"env name with dash", head + "components:\n  web: { image: nginx, env: { MY-VAR: x } }\n", "components.web.env.MY-VAR", "C identifier"},
		{"env name starts with digit", head + "components:\n  web: { image: nginx, env: { 1VAR: x } }\n", "components.web.env.1VAR", "C identifier"},
		{"env name uses secret prefix", head + "components:\n  web: { image: nginx, env: { SHELF_SECRET_PW: x } }\n", "components.web.env.SHELF_SECRET_PW", "reserved for secrets"},

		// references
		{"unterminated reference", head + "components:\n  web: { image: nginx, env: { A: '${db.host' } }\n", "components.web.env.A", "unterminated reference"},
		{"unsupported reference", head + "components:\n  web: { image: nginx, env: { A: '${HOME}' } }\n", "components.web.env.A", "unknown reference ${HOME}"},
		{"unknown component", head + "components:\n  web: { image: nginx, env: { A: '${db.host}' } }\n", "components.web.env.A", `unknown component "db"`},
		{"unknown component in args", head + "components:\n  web: { image: nginx, args: [ok, '${db.port}'] }\n", "components.web.args.1", `unknown component "db"`},
		{"unknown component in command", head + "components:\n  web: { image: nginx, command: ['${db.host}'] }\n", "components.web.command.0", `unknown component "db"`},
		{"host of component without port", head + "components:\n  web: { image: nginx, env: { A: '${db.host}' } }\n  db: { image: postgres }\n", "components.web.env.A", "has no port"},
		{"port on component with several ports", head + "components:\n  web: { image: nginx, env: { A: '${api.port}' } }\n  api: { image: nginx, ports: { a: 80, b: 81 } }\n", "components.web.env.A", "use ${api.ports.<name>} with one of a, b"},
		{"named port on single-port component", head + "components:\n  web: { image: nginx, env: { A: '${db.ports.main}' } }\n  db: { image: postgres, port: 5432 }\n", "components.web.env.A", "use ${db.port}"},
		{"unknown named port", head + "components:\n  web: { image: nginx, env: { A: '${api.ports.c}' } }\n  api: { image: nginx, ports: { a: 80 } }\n", "components.web.env.A", `unknown port "c"`},
		{"unknown secret", head + "components:\n  web: { image: nginx, env: { A: '${secrets.pw}' } }\n", "components.web.env.A", `unknown secret "pw"`},

		// secrets
		{"secret without generate", head + "components:\n  web: { image: nginx, env: { A: '${secrets.pw}' } }\nsecrets:\n  pw: {}\n", "secrets.pw", "generate: true"},
		{"secret with generate false", head + "components:\n  web: { image: nginx, env: { A: '${secrets.pw}' } }\nsecrets:\n  pw: { generate: false }\n", "secrets.pw", "generate: true"},
		{"empty secret", head + "components:\n  web: { image: nginx, env: { A: '${secrets.pw}' } }\nsecrets:\n  pw:\n", "secrets.pw", "generate: true"},
		{"secret name with underscore", head + "components:\n  web: { image: nginx, env: { A: '${secrets.db_pw}' } }\nsecrets:\n  db_pw: { generate: true }\n", "secrets.db_pw", "secret name"},
		{"secret name too long", head + "components:\n  web: { image: nginx }\nsecrets:\n  " + strings.Repeat("s", 64) + ": { generate: true }\n", "secrets." + strings.Repeat("s", 64), "at most 63"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := Validate(mustParse(t, tt.yaml))
			for _, f := range findings {
				if f.Severity == Error && f.Path == tt.path && strings.Contains(f.Message, tt.msg) {
					if f.Line == 0 {
						t.Errorf("finding has no line: %+v", f)
					}
					return
				}
			}
			t.Errorf("want error at %s containing %q, got:\n%s", tt.path, tt.msg, formatAll(findings))
		})
	}
}

func TestWarnings(t *testing.T) {
	doc := mustParse(t, head+`
components:
  web: { image: nginx }
secrets:
  unused: { generate: true }
`)
	findings := Validate(doc)
	if findings.HasErrors() {
		t.Fatalf("unexpected errors:\n%s", formatAll(findings))
	}
	if len(findings) != 1 || findings[0].Path != "secrets.unused" || !strings.Contains(findings[0].Message, "not referenced") {
		t.Errorf("want one warning about the unused secret, got:\n%s", formatAll(findings))
	}
}

func TestFindingFormatAndOrder(t *testing.T) {
	doc := mustParse(t, `apiVersion: shelf.dev/v1alpha1
name: shop
components:
  web:
    image: nginx
    route: /
  db:
    port: 0
`)
	got := formatAll(Validate(doc))
	want := strings.Join([]string{
		"  app.yaml:6: error: components.web.route: route needs a port; add port or ports to the component",
		"  app.yaml:7: error: components.db.image: is required",
		"  app.yaml:8: error: components.db.port: must be between 1 and 65535, got 0",
	}, "\n")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
