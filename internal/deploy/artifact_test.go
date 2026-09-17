package deploy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// helloConfigMap is what `shelf render` prints for examples/hello.
func helloConfigMap(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../render/testdata/hello.configmap.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// tarball builds the content layer of a Flux artifact from file names and contents.
func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "sub/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDecode(t *testing.T) {
	hello := helloConfigMap(t)
	other := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: other\ndata:\n  x: y\n"
	tests := []struct {
		name    string
		files   map[string]string
		wantErr string
	}{
		{name: "hello", files: map[string]string{"configmap.yaml": hello}},
		{name: "hello with other files", files: map[string]string{
			"sub/configmap.yml": hello, "other.yaml": other, "README.md": "# not yaml",
		}},
		{name: "multi-document file", files: map[string]string{"all.yaml": other + "---\n" + hello}},
		{name: "empty", files: map[string]string{}, wantErr: "found 0"},
		{name: "two apps", files: map[string]string{"a.yaml": hello, "b.yaml": hello}, wantErr: "found 2"},
		{name: "wrong apiVersion", files: map[string]string{
			"configmap.yaml": strings.Replace(hello, "apiVersion: shelf.dev/v1alpha1", "apiVersion: shelf.dev/v9", 1),
		}, wantErr: `unsupported apiVersion "shelf.dev/v9"`},
		{name: "invalid app", files: map[string]string{
			"configmap.yaml": strings.Replace(hello, "    name: hello\n", "    name: Hello\n", 1),
		}, wantErr: "invalid"},
		{name: "broken yaml", files: map[string]string{"x.yaml": "kind: [\n"}, wantErr: "x.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, err := Decode(bytes.NewReader(tarball(t, tt.files)))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if app.Name != "hello" || len(app.Components) != 3 {
				t.Errorf("unexpected app %+v", app)
			}
			if got := SecretNames(app); len(got) != 1 || got[0] != "db-password" {
				t.Errorf("secrets %v", got)
			}
		})
	}
}

func TestDecodeRejectsNonGzip(t *testing.T) {
	if _, err := Decode(strings.NewReader("plain")); err == nil {
		t.Error("want an error")
	}
}

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	push := func(repoTag string, layerType types.MediaType, content []byte) string {
		t.Helper()
		img := mutate.MediaType(empty.Image, types.OCIManifestSchema1)
		img = mutate.ConfigMediaType(img, "application/vnd.cncf.flux.config.v1+json")
		img, err := mutate.Append(img, mutate.Addendum{Layer: static.NewLayer(content, layerType)})
		if err != nil {
			t.Fatal(err)
		}
		ref := u.Host + "/" + repoTag
		r, err := name.ParseReference(ref, name.Insecure)
		if err != nil {
			t.Fatal(err)
		}
		if err := remote.Write(r, img); err != nil {
			t.Fatal(err)
		}
		return ref
	}

	good := push("hello-deploy:main", ContentMediaType, tarball(t, map[string]string{"configmap.yaml": helloConfigMap(t)}))
	app, err := Fetch(context.Background(), good, true)
	if err != nil {
		t.Fatal(err)
	}
	if app.Name != "hello" {
		t.Errorf("app %q", app.Name)
	}

	other := push("image:latest", types.DockerLayer, []byte("x"))
	if _, err := Fetch(context.Background(), other, true); err == nil || !strings.Contains(err.Error(), "not a deploy artifact") {
		t.Errorf("error %v", err)
	}
	if _, err := Fetch(context.Background(), u.Host+"/missing:main", true); err == nil {
		t.Error("a missing artifact must be an error")
	}
}
