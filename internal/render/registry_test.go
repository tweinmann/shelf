package render

import (
	"context"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// testRegistry is an in-memory registry. Pushed images are addressed as <host>/<repo>:<tag>.
type testRegistry struct {
	t    *testing.T
	host string
}

func newTestRegistry(t *testing.T) *testRegistry {
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &testRegistry{t: t, host: u.Host}
}

func (r *testRegistry) resolver() *RegistryResolver {
	return &RegistryResolver{NameOptions: []name.Option{name.Insecure}}
}

func (r *testRegistry) ref(repoTag string) name.Reference {
	ref, err := name.ParseReference(r.host+"/"+repoTag, name.Insecure)
	if err != nil {
		r.t.Fatal(err)
	}
	return ref
}

func image(t *testing.T, platform v1.Platform, exposed ...string) v1.Image {
	t.Helper()
	img, err := random.Image(64, 1)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := img.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	cfg = cfg.DeepCopy()
	cfg.OS, cfg.Architecture = platform.OS, platform.Architecture
	cfg.Config.ExposedPorts = map[string]struct{}{}
	for _, p := range exposed {
		cfg.Config.ExposedPorts[p] = struct{}{}
	}
	img, err = mutate.ConfigFile(img, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func (r *testRegistry) pushImage(repoTag string, img v1.Image) v1.Hash {
	if err := remote.Write(r.ref(repoTag), img); err != nil {
		r.t.Fatal(err)
	}
	d, err := img.Digest()
	if err != nil {
		r.t.Fatal(err)
	}
	return d
}

type variant struct {
	platform v1.Platform
	image    v1.Image
}

func (r *testRegistry) pushIndex(repoTag string, variants ...variant) v1.Hash {
	var adds []mutate.IndexAddendum
	for _, v := range variants {
		adds = append(adds, mutate.IndexAddendum{Add: v.image, Descriptor: v1.Descriptor{Platform: &v.platform}})
	}
	idx := mutate.AppendManifests(empty.Index, adds...)
	if err := remote.WriteIndex(r.ref(repoTag), idx); err != nil {
		r.t.Fatal(err)
	}
	d, err := idx.Digest()
	if err != nil {
		r.t.Fatal(err)
	}
	return d
}

var amd64 = v1.Platform{OS: "linux", Architecture: "amd64"}

func TestRegistryResolver(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	armDigest := reg.pushImage("web:arm", image(t, TargetPlatform, "8080/tcp", "80", "53/udp"))
	reg.pushImage("web:amd", image(t, amd64, "80/tcp"))
	indexDigest := reg.pushIndex("multi:1",
		variant{amd64, image(t, amd64, "1/tcp")},
		variant{TargetPlatform, image(t, TargetPlatform, "5432/tcp")},
	)
	reg.pushIndex("amdonly:1", variant{amd64, image(t, amd64)})

	tests := []struct {
		name    string
		image   string
		want    ImageInfo
		wantErr string
	}{
		{
			name:  "single-platform image",
			image: reg.host + "/web:arm",
			want:  ImageInfo{Digest: armDigest.String(), ExposedPorts: []int{80, 8080}},
		},
		{
			name:  "by digest",
			image: reg.host + "/web:arm@" + armDigest.String(),
			want:  ImageInfo{Digest: armDigest.String(), ExposedPorts: []int{80, 8080}},
		},
		{
			name:  "index pins the index digest and reads the arm64 config",
			image: reg.host + "/multi:1",
			want:  ImageInfo{Digest: indexDigest.String(), ExposedPorts: []int{5432}},
		},
		{name: "wrong architecture", image: reg.host + "/web:amd", wantErr: "is linux/amd64, need linux/arm64"},
		{name: "index without arm64", image: reg.host + "/amdonly:1", wantErr: "has no linux/arm64 variant"},
		{name: "unknown tag", image: reg.host + "/web:missing", wantErr: "look up " + reg.host + "/web:missing"},
		{name: "invalid reference", image: "Not Valid", wantErr: "could not parse reference"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := reg.resolver().Resolve(ctx, tt.image)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRegistryResolverHonorsContext(t *testing.T) {
	reg := newTestRegistry(t)
	reg.pushImage("web:arm", image(t, TargetPlatform))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reg.resolver().Resolve(ctx, reg.host+"/web:arm"); err == nil {
		t.Error("want an error for a canceled context")
	}
}
