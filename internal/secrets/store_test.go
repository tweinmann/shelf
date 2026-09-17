package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMerge(t *testing.T) {
	cluster := map[string]string{"kept": "c1", "differs": "c2", "old": "c3"}
	backup := map[string]string{"differs": "b2", "restored": "b4", "gone": "b5"}
	values, sources := Merge([]string{"kept", "differs", "restored", "new"}, cluster, backup)

	want := map[string]Source{"kept": Kept, "differs": Replaced, "restored": Restored, "new": Generated}
	for name, src := range want {
		if sources[name] != src {
			t.Errorf("%s: source %q, want %q", name, sources[name], src)
		}
	}
	if len(sources) != len(want) {
		t.Errorf("sources for undeclared names: %v", sources)
	}
	for name, v := range map[string]string{"kept": "c1", "differs": "c2", "restored": "b4", "old": "c3", "gone": "b5"} {
		if values[name] != v {
			t.Errorf("%s = %q, want %q", name, values[name], v)
		}
	}
	if len(values["new"]) < 26 {
		t.Errorf("generated value %q is too short", values["new"])
	}
	if len(values) != 6 {
		t.Errorf("values: %v", values)
	}
}

func TestMergeEmpty(t *testing.T) {
	values, sources := Merge(nil, nil, nil)
	if values == nil || len(values) != 0 || len(sources) != 0 {
		t.Errorf("got %v %v", values, sources)
	}
}

func TestBackup(t *testing.T) {
	b := Backup{Dir: filepath.Join(t.TempDir(), "apps")}

	got, err := b.Load("hello")
	if err != nil || len(got) != 0 {
		t.Fatalf("missing backup: %v, %v", got, err)
	}

	if err := b.Save("hello", map[string]string{"db-password": "ABC"}); err != nil {
		t.Fatal(err)
	}
	got, err = b.Load("hello")
	if err != nil || got["db-password"] != "ABC" || len(got) != 1 {
		t.Fatalf("round trip: %v, %v", got, err)
	}

	info, err := os.Stat(b.Path("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v, want 0600", info.Mode().Perm())
	}
	dir, err := os.Stat(filepath.Dir(b.Path("hello")))
	if err != nil {
		t.Fatal(err)
	}
	if dir.Mode().Perm() != 0o700 {
		t.Errorf("directory mode %v, want 0700", dir.Mode().Perm())
	}
	data, _ := os.ReadFile(b.Path("hello"))
	if !strings.HasPrefix(string(data), "# shelf secret backup") {
		t.Errorf("backup lacks its header:\n%s", data)
	}

	// Overwriting leaves no temporary files behind.
	if err := b.Save("hello", map[string]string{"db-password": "DEF"}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Dir(b.Path("hello")))
	if len(entries) != 1 {
		t.Errorf("directory has %d entries, want only secrets.yaml", len(entries))
	}
}

func TestBackupOfAnotherApp(t *testing.T) {
	b := Backup{Dir: t.TempDir()}
	if err := b.Save("other", map[string]string{"x": "y"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(b.Path("hello")), 0o700); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(b.Path("other"))
	if err := os.WriteFile(b.Path("hello"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Load("hello"); err == nil || !strings.Contains(err.Error(), `belongs to app "other"`) {
		t.Errorf("error %v", err)
	}
}
