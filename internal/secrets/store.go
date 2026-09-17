package secrets

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

// Source tells where a secret value came from.
type Source string

const (
	Generated Source = "generated"
	Kept      Source = "kept"
	Restored  Source = "restored from the backup"
	// Replaced means the cluster value was kept and the differing backup value replaced.
	Replaced Source = "kept; the backup had a different value and was updated"
)

// Merge returns the values for the declared secret names. A value in the cluster is kept, since
// the app already uses it; a value only in the backup is restored, e.g. after a cluster rebuild;
// anything else is generated. Values for names that are no longer declared stay, so removing a
// secret from app.yaml and adding it back does not change it.
func Merge(declared []string, cluster, backup map[string]string) (map[string]string, map[string]Source) {
	values := map[string]string{}
	maps.Copy(values, backup)
	maps.Copy(values, cluster)
	sources := map[string]Source{}
	for _, name := range declared {
		c, inCluster := cluster[name]
		b, inBackup := backup[name]
		switch {
		case inCluster && inBackup && c != b:
			sources[name] = Replaced
		case inCluster:
			sources[name] = Kept
		case inBackup:
			sources[name] = Restored
		default:
			values[name] = Generate()
			sources[name] = Generated
		}
	}
	return values, sources
}

// Backup keeps an app's secret values on the host, so they survive the cluster.
type Backup struct {
	// Dir holds one directory per app, e.g. ~/.shelf/apps.
	Dir string
}

type backupFile struct {
	App     string            `yaml:"app"`
	Secrets map[string]string `yaml:"secrets"`
}

const backupHeader = "# shelf secret backup. Keep this file private; it restores the values after a cluster rebuild.\n"

// Path returns the backup file of app.
func (b Backup) Path(app string) string {
	return filepath.Join(b.Dir, app, "secrets.yaml")
}

// Load returns the stored values of app, or none if there is no backup.
func (b Backup) Load(app string) (map[string]string, error) {
	data, err := os.ReadFile(b.Path(app))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var f backupFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", b.Path(app), err)
	}
	if f.App != app {
		return nil, fmt.Errorf("%s belongs to app %q, not %q", b.Path(app), f.App, app)
	}
	if f.Secrets == nil {
		f.Secrets = map[string]string{}
	}
	return f.Secrets, nil
}

// Save writes the values of app with mode 0600 in a directory with mode 0700. It replaces the
// file atomically, so a crash never leaves a half-written backup.
func (b Backup) Save(app string, values map[string]string) error {
	dir := filepath.Dir(b.Path(app))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// MkdirAll leaves existing directories alone; the backup directory must be private.
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(backupFile{App: app, Secrets: values})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".secrets-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append([]byte(backupHeader), data...)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), b.Path(app))
}
