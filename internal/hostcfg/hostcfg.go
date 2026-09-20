// Package hostcfg holds the state shelf keeps on the machine it runs on, under ~/.shelf: who
// may use the admin UI, and which sessions are open. The secret backups of the apps live in the
// same directory but belong to internal/secrets.
//
// Everything here is written with mode 0600 in a directory with mode 0700, and replaced
// atomically, so a crash never leaves half a file behind. Nothing in this package is encrypted:
// anyone with a shell as this user can read it, which is the trade the plan records.
package hostcfg

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"go.yaml.in/yaml/v3"
)

// AdminFile is the file under the shelf directory that this package writes.
const AdminFile = "admin.yaml"

const adminHeader = "# shelf admin state. Keep this file private; it holds the password hash and the open sessions.\n"

// Store is a shelf directory.
type Store struct{ dir string }

// Open makes sure the directory exists and is private.
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("no shelf directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// MkdirAll leaves an existing directory alone; this one has to be private.
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// Dir is the directory the store keeps its files in.
func (s *Store) Dir() string { return s.dir }

// Session is one open login. The identifier is the value of the cookie: sessions are kept on
// this side rather than signed into the cookie, so that a logout, or a password change, ends
// them for good.
type Session struct {
	ID      string    `yaml:"id"`
	Created time.Time `yaml:"created"`
	Expires time.Time `yaml:"expires"`
}

// Admin is who may use the admin UI.
type Admin struct {
	// SetupToken claims the instance: the installer prints it, and until it has been used the
	// server serves nothing but the claim page. It is empty once the instance is claimed.
	SetupToken string `yaml:"setupToken,omitempty"`
	// Password is the hash of the admin password, in the format HashPassword writes.
	Password string `yaml:"password,omitempty"`
	// Sessions are the logins that are still valid. They are stored so that a restart, or an
	// update of shelf itself, does not log everyone out.
	Sessions []Session `yaml:"sessions,omitempty"`
}

// Claimed reports whether the instance has an admin password.
func (a Admin) Claimed() bool { return a.Password != "" }

func (s *Store) adminPath() string { return filepath.Join(s.dir, AdminFile) }

// Admin reads the admin state, or an empty one if the instance has never been started.
func (s *Store) Admin() (Admin, error) {
	data, err := os.ReadFile(s.adminPath())
	if errors.Is(err, fs.ErrNotExist) {
		return Admin{}, nil
	}
	if err != nil {
		return Admin{}, err
	}
	var a Admin
	if err := yaml.Unmarshal(data, &a); err != nil {
		return Admin{}, fmt.Errorf("%s: %w", s.adminPath(), err)
	}
	return a, nil
}

// SaveAdmin replaces the admin state.
func (s *Store) SaveAdmin(a Admin) error {
	data, err := yaml.Marshal(a)
	if err != nil {
		return err
	}
	return writeFile(s.adminPath(), append([]byte(adminHeader), data...))
}

// writeFile replaces path with content, privately and atomically.
func writeFile(path string, content []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
