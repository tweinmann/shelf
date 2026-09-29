package hostcfg

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ConnectionsDir is the directory under the shelf directory that holds the connections kept on
// this machine, one directory per kind.
const ConnectionsDir = "connections"

const cloudflareHeader = "# shelf Cloudflare connection. Keep this file private; the token can change DNS zones.\n"

// Cloudflare is a Cloudflare connection: an API token and the account the apps' tunnels are made
// in. It lives on this machine only. Nothing in the cluster needs it, and a token that can rewrite
// a DNS zone has no business there.
type Cloudflare struct {
	Name    string `yaml:"name"`
	Token   string `yaml:"token"`
	Account string `yaml:"account"`
}

// Connections keeps the Cloudflare connections the user defined, one file each, so that any
// number of apps can be exposed through the same one.
type Connections struct {
	// Dir is the connections directory, e.g. ~/.shelf/connections.
	Dir string
}

func (c Connections) cloudflareDir() string { return filepath.Join(c.Dir, "cloudflare") }

// CloudflarePath returns the file of a Cloudflare connection.
func (c Connections) CloudflarePath(name string) string {
	return filepath.Join(c.cloudflareDir(), name+".yaml")
}

// Cloudflare returns a Cloudflare connection, or nil if this machine holds none of that name.
func (c Connections) Cloudflare(name string) (*Cloudflare, error) {
	if c.Dir == "" || name == "" {
		return nil, nil
	}
	data, err := os.ReadFile(c.CloudflarePath(name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var conn Cloudflare
	if err := yaml.Unmarshal(data, &conn); err != nil {
		return nil, fmt.Errorf("%s: %w", c.CloudflarePath(name), err)
	}
	if conn.Name != name {
		return nil, fmt.Errorf("%s holds the connection %q, not %q", c.CloudflarePath(name), conn.Name, name)
	}
	if conn.Token == "" {
		return nil, fmt.Errorf("%s holds no token", c.CloudflarePath(name))
	}
	return &conn, nil
}

// CloudflareConnections returns every Cloudflare connection on this machine, by name.
func (c Connections) CloudflareConnections() ([]Cloudflare, error) {
	if c.Dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(c.cloudflareDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Cloudflare
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".yaml")
		if !ok || e.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		conn, err := c.Cloudflare(name)
		if err != nil {
			return nil, err
		}
		if conn != nil {
			out = append(out, *conn)
		}
	}
	slices.SortFunc(out, func(a, b Cloudflare) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// SaveCloudflare creates or replaces a Cloudflare connection, privately and atomically.
func (c Connections) SaveCloudflare(conn Cloudflare) error {
	if c.Dir == "" {
		return errors.New("no directory for the connections")
	}
	for _, dir := range []string{c.Dir, c.cloudflareDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		// MkdirAll leaves existing directories alone; these have to be private.
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	data, err := yaml.Marshal(conn)
	if err != nil {
		return err
	}
	return writeFile(c.CloudflarePath(conn.Name), append([]byte(cloudflareHeader), data...))
}

// DeleteCloudflare removes a Cloudflare connection and reports whether it existed.
func (c Connections) DeleteCloudflare(name string) (bool, error) {
	if c.Dir == "" {
		return false, nil
	}
	err := os.Remove(c.CloudflarePath(name))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
