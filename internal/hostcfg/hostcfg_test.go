package hostcfg_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tweinmann/shelf/internal/hostcfg"
)

func TestPassword(t *testing.T) {
	t.Parallel()
	hash, err := hostcfg.HashPassword("a-good-password")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(hash, "a-good-password") {
		t.Fatal("the password is in the hash")
	}
	if !strings.HasPrefix(hash, "pbkdf2-sha256$600000$") {
		t.Errorf("the hash does not name its algorithm: %s", hash)
	}
	if !hostcfg.CheckPassword(hash, "a-good-password") {
		t.Error("the right password was refused")
	}
	for _, wrong := range []string{"", "a-good-passwore", "A-GOOD-PASSWORD", hash} {
		if hostcfg.CheckPassword(hash, wrong) {
			t.Errorf("%q was accepted", wrong)
		}
	}
	// Two hashes of the same password differ, because each one has its own salt.
	other, err := hostcfg.HashPassword("a-good-password")
	if err != nil {
		t.Fatal(err)
	}
	if other == hash {
		t.Error("two hashes of the same password are the same; the salt is not random")
	}
}

func TestPasswordTooShort(t *testing.T) {
	t.Parallel()
	if _, err := hostcfg.HashPassword("short"); !errors.Is(err, hostcfg.ErrPasswordTooShort) {
		t.Errorf("error %v", err)
	}
}

// TestCheckPasswordRejectsNonsense makes sure a damaged admin file does not let anyone in.
func TestCheckPasswordRejectsNonsense(t *testing.T) {
	t.Parallel()
	for _, stored := range []string{
		"", "plaintext", "pbkdf2-sha256$600000$saltonly", "pbkdf2-sha256$0$c2FsdA$aGFzaA",
		"pbkdf2-sha256$600000$!!!$aGFzaA", "argon2id$600000$c2FsdA$aGFzaA",
		"pbkdf2-sha256$600000$c2FsdA$",
	} {
		if hostcfg.CheckPassword(stored, "a-good-password") {
			t.Errorf("%q was accepted as a hash", stored)
		}
	}
}

func TestAdminRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := hostcfg.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	empty, err := store.Admin()
	if err != nil {
		t.Fatal(err)
	}
	if empty.Claimed() {
		t.Error("a directory without a file claims to have an owner")
	}

	want := hostcfg.Admin{
		Password: "pbkdf2-sha256$600000$c2FsdA$aGFzaA",
		Sessions: []hostcfg.Session{{
			ID:      "a-session",
			Created: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC),
			Expires: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
		}},
	}
	if err := store.SaveAdmin(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Admin()
	if err != nil {
		t.Fatal(err)
	}
	if !got.Claimed() || got.Password != want.Password || len(got.Sessions) != 1 ||
		got.Sessions[0].ID != "a-session" || !got.Sessions[0].Expires.Equal(want.Sessions[0].Expires) {
		t.Errorf("read back %+v", got)
	}

	// The file holds a password hash and open sessions; nobody else on the machine may read it.
	info, err := os.Stat(filepath.Join(dir, hostcfg.AdminFile))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode %o, want 600", mode)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Errorf("directory mode %o, want 700", mode)
	}
}

// TestOpenMakesAnExistingDirectoryPrivate covers an upgrade from a directory that was created
// with looser permissions.
func TestOpenMakesAnExistingDirectoryPrivate(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "shelf")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := hostcfg.Open(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o700 {
		t.Errorf("mode %o, want 700", mode)
	}
}

func TestNewTokenIsRandom(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for range 100 {
		token := hostcfg.NewToken()
		if len(token) < 20 {
			t.Fatalf("token %q is too short", token)
		}
		if seen[token] {
			t.Fatalf("token %q came twice", token)
		}
		seen[token] = true
	}
}

func TestCloudflareAccess(t *testing.T) {
	t.Parallel()
	access := hostcfg.AppAccess{Dir: filepath.Join(t.TempDir(), "apps")}
	got, err := access.Cloudflare("greeter")
	if err != nil || got != nil {
		t.Fatalf("an app without access: %+v, %v", got, err)
	}
	want := hostcfg.Cloudflare{Token: "not-a-real-token", Account: "acc-1"}
	if err := access.SaveCloudflare("greeter", want); err != nil {
		t.Fatal(err)
	}
	got, err = access.Cloudflare("greeter")
	if err != nil || got == nil || *got != want {
		t.Fatalf("read back %+v, %v", got, err)
	}
	info, err := os.Stat(access.Path("greeter"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v", info.Mode().Perm())
	}
	dir, err := os.Stat(filepath.Dir(access.Path("greeter")))
	if err != nil {
		t.Fatal(err)
	}
	if dir.Mode().Perm() != 0o700 {
		t.Errorf("directory mode %v", dir.Mode().Perm())
	}

	// A file copied from another app is refused rather than used for the wrong zone.
	if err := os.MkdirAll(filepath.Dir(access.Path("shop")), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(access.Path("greeter"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(access.Path("shop"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := access.Cloudflare("shop"); err == nil || !strings.Contains(err.Error(), `belongs to app "greeter"`) {
		t.Errorf("a file of another app: %v", err)
	}

	if err := access.DeleteCloudflare("greeter"); err != nil {
		t.Fatal(err)
	}
	if got, err := access.Cloudflare("greeter"); err != nil || got != nil {
		t.Errorf("after delete: %+v, %v", got, err)
	}
	if err := access.DeleteCloudflare("greeter"); err != nil {
		t.Errorf("deleting twice: %v", err)
	}
}
