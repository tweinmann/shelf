package hostcfg

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

// CloudflareFile is the file in an app's directory that holds the app's Cloudflare access.
const CloudflareFile = "cloudflare.yaml"

const cloudflareHeader = "# shelf Cloudflare access of one app. Keep this file private; the token can change the app's DNS zone.\n"

// Cloudflare is how shelf reaches the Cloudflare account an app is exposed through: the API token
// and the account the app's tunnel belongs to. It lives on this machine only. Nothing in the
// cluster needs it, and a token that can rewrite a DNS zone has no business there.
type Cloudflare struct {
	Token   string `yaml:"token"`
	Account string `yaml:"account"`
}

// AppAccess keeps the Cloudflare access of the apps, one file per app next to its secret backup.
type AppAccess struct {
	// Dir holds one directory per app, e.g. ~/.shelf/apps.
	Dir string
}

type cloudflareFile struct {
	App        string `yaml:"app"`
	Cloudflare `yaml:",inline"`
}

// Path returns the file with the Cloudflare access of app.
func (a AppAccess) Path(app string) string { return filepath.Join(a.Dir, app, CloudflareFile) }

// Cloudflare returns the stored access of app, or nil if this machine holds none.
func (a AppAccess) Cloudflare(app string) (*Cloudflare, error) {
	if a.Dir == "" {
		return nil, nil
	}
	data, err := os.ReadFile(a.Path(app))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f cloudflareFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", a.Path(app), err)
	}
	if f.App != app {
		return nil, fmt.Errorf("%s belongs to app %q, not %q", a.Path(app), f.App, app)
	}
	if f.Token == "" {
		return nil, fmt.Errorf("%s holds no token", a.Path(app))
	}
	return &f.Cloudflare, nil
}

// SaveCloudflare replaces the access of app, privately and atomically.
func (a AppAccess) SaveCloudflare(app string, c Cloudflare) error {
	if a.Dir == "" {
		return errors.New("no directory for the Cloudflare access")
	}
	dir := filepath.Dir(a.Path(app))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// MkdirAll leaves existing directories alone; this one has to be private.
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(cloudflareFile{App: app, Cloudflare: c})
	if err != nil {
		return err
	}
	return writeFile(a.Path(app), append([]byte(cloudflareHeader), data...))
}

// DeleteCloudflare removes the access of app. It is not an error if there is none.
func (a AppAccess) DeleteCloudflare(app string) error {
	if a.Dir == "" {
		return nil
	}
	err := os.Remove(a.Path(app))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
