package render

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// TargetPlatform is the platform every image must provide: the Mac mini runs linux/arm64.
var TargetPlatform = v1.Platform{OS: "linux", Architecture: "arm64"}

// RegistryResolver resolves images against their registry, with credentials from the Docker
// config (docker login).
type RegistryResolver struct {
	NameOptions   []name.Option
	RemoteOptions []remote.Option
}

// NewRegistryResolver returns a resolver that uses the default Docker keychain.
func NewRegistryResolver() *RegistryResolver {
	return &RegistryResolver{
		RemoteOptions: []remote.Option{remote.WithAuthFromKeychain(authn.DefaultKeychain)},
	}
}

// Resolve returns the digest of image and the ports its linux/arm64 variant exposes. For a
// multi-platform image the digest is the index digest. Images without a linux/arm64 variant
// are an error.
func (r *RegistryResolver) Resolve(ctx context.Context, image string) (ImageInfo, error) {
	ref, err := name.ParseReference(image, r.NameOptions...)
	if err != nil {
		return ImageInfo{}, err
	}
	opts := append(slices.Clip(r.RemoteOptions), remote.WithContext(ctx))
	desc, err := remote.Get(ref, opts...)
	if err != nil {
		return ImageInfo{}, fmt.Errorf("look up %s: %w", image, err)
	}

	var img v1.Image
	switch {
	case desc.MediaType.IsIndex():
		idx, err := desc.ImageIndex()
		if err != nil {
			return ImageInfo{}, fmt.Errorf("read index of %s: %w", image, err)
		}
		manifest, err := idx.IndexManifest()
		if err != nil {
			return ImageInfo{}, fmt.Errorf("read index of %s: %w", image, err)
		}
		i := slices.IndexFunc(manifest.Manifests, func(d v1.Descriptor) bool {
			return d.Platform != nil && d.Platform.OS == TargetPlatform.OS &&
				d.Platform.Architecture == TargetPlatform.Architecture
		})
		if i < 0 {
			return ImageInfo{}, fmt.Errorf("image %s has no %s variant", image, platformString(TargetPlatform))
		}
		if img, err = idx.Image(manifest.Manifests[i].Digest); err != nil {
			return ImageInfo{}, fmt.Errorf("read %s variant of %s: %w", platformString(TargetPlatform), image, err)
		}
	case desc.MediaType.IsImage():
		if img, err = desc.Image(); err != nil {
			return ImageInfo{}, fmt.Errorf("read image %s: %w", image, err)
		}
	default:
		return ImageInfo{}, fmt.Errorf("%s is not an image (media type %s)", image, desc.MediaType)
	}

	cfg, err := img.ConfigFile()
	if err != nil {
		return ImageInfo{}, fmt.Errorf("read config of %s: %w", image, err)
	}
	if cfg.OS != TargetPlatform.OS || cfg.Architecture != TargetPlatform.Architecture {
		return ImageInfo{}, fmt.Errorf("image %s is %s/%s, need %s",
			image, cfg.OS, cfg.Architecture, platformString(TargetPlatform))
	}
	return ImageInfo{Digest: desc.Digest.String(), ExposedPorts: tcpPorts(cfg.Config.ExposedPorts)}, nil
}

func platformString(p v1.Platform) string { return p.OS + "/" + p.Architecture }

// tcpPorts extracts TCP port numbers from EXPOSE entries like "80/tcp" or "80".
func tcpPorts(exposed map[string]struct{}) []int {
	var ports []int
	for spec := range exposed {
		num, proto, _ := strings.Cut(spec, "/")
		if proto != "" && proto != "tcp" {
			continue
		}
		if n, err := strconv.Atoi(num); err == nil {
			ports = append(ports, n)
		}
	}
	slices.Sort(ports)
	return ports
}
