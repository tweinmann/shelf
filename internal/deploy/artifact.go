// Package deploy reads an app's deploy artifact: the OCI artifact that `flux push artifact`
// creates from the output of `shelf render`, holding the ConfigMap with the resolved app.yaml.
package deploy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"go.yaml.in/yaml/v3"

	"github.com/tweinmann/shelf/internal/render"
	"github.com/tweinmann/shelf/internal/schema"
	"github.com/tweinmann/shelf/internal/validate"
)

// ContentMediaType is the layer media type of artifacts pushed with `flux push artifact`.
const ContentMediaType types.MediaType = "application/vnd.cncf.flux.content.v1.tar+gzip"

// maxFileSize bounds each file read from an artifact; a deploy artifact is a few kilobytes.
const maxFileSize = 1 << 20

// Fetch downloads the artifact at ref (<registry>/<repository>:<tag>) with credentials from the
// Docker config and returns the app it deploys.
func Fetch(ctx context.Context, ref string, insecure bool) (*schema.App, error) {
	var nameOpts []name.Option
	if insecure {
		nameOpts = append(nameOpts, name.Insecure)
	}
	r, err := name.ParseReference(ref, nameOpts...)
	if err != nil {
		return nil, err
	}
	img, err := remote.Image(r, remote.WithContext(ctx), remote.WithAuthFromKeychain(authn.DefaultKeychain))
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", ref, err)
	}
	layers, err := img.Layers()
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", ref, err)
	}
	for _, layer := range layers {
		mt, err := layer.MediaType()
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", ref, err)
		}
		if mt != ContentMediaType {
			continue
		}
		rc, err := layer.Compressed()
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", ref, err)
		}
		defer rc.Close()
		app, err := Decode(rc)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ref, err)
		}
		return app, nil
	}
	return nil, fmt.Errorf("%s is not a deploy artifact: it has no layer of type %s", ref, ContentMediaType)
}

// Decode reads the gzipped tarball of a deploy artifact. It must contain exactly one
// ConfigMap with the key app.yaml, whose content must be a valid app.yaml of the supported
// apiVersion.
func Decode(r io.Reader) (*schema.App, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("not a gzipped tarball: %w", err)
	}
	tr := tar.NewReader(gz)
	var found []string
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tarball: %w", err)
		}
		ext := path.Ext(hdr.Name)
		if hdr.Typeflag != tar.TypeReg || (ext != ".yaml" && ext != ".yml") {
			continue
		}
		if hdr.Size > maxFileSize {
			return nil, fmt.Errorf("%s is larger than %d bytes", hdr.Name, maxFileSize)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", hdr.Name, err)
		}
		values, err := appValues(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", hdr.Name, err)
		}
		found = append(found, values...)
	}
	if len(found) != 1 {
		return nil, fmt.Errorf("expected one ConfigMap with the key %s, found %d", render.ConfigMapKey, len(found))
	}
	return parseApp(found[0])
}

type manifest struct {
	Kind string            `yaml:"kind"`
	Data map[string]string `yaml:"data"`
}

// appValues returns the app.yaml values of all ConfigMaps in a multi-document YAML file.
func appValues(data []byte) ([]string, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var out []string
	for {
		var m manifest
		err := dec.Decode(&m)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if v, ok := m.Data[render.ConfigMapKey]; ok && m.Kind == "ConfigMap" {
			out = append(out, v)
		}
	}
}

func parseApp(values string) (*schema.App, error) {
	doc, err := schema.Parse(render.ConfigMapKey, []byte(values))
	if err != nil {
		return nil, err
	}
	if doc.App.APIVersion != schema.APIVersion {
		return nil, fmt.Errorf("unsupported apiVersion %q; this shelf understands %s", doc.App.APIVersion, schema.APIVersion)
	}
	findings := validate.Validate(doc)
	if findings.HasErrors() {
		var msgs []string
		for _, f := range findings {
			if f.Severity == validate.Error {
				msgs = append(msgs, f.Format(render.ConfigMapKey))
			}
		}
		return nil, fmt.Errorf("the app.yaml in the artifact is invalid:\n%s", strings.Join(msgs, "\n"))
	}
	return doc.App, nil
}

// SecretNames returns the secrets an app declares, sorted.
func SecretNames(app *schema.App) []string {
	return slices.Sorted(maps.Keys(app.Secrets))
}
