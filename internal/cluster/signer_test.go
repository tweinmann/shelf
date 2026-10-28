package cluster

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// testSignerKey is a public key made for these tests; its private half was never kept.
func testSignerKey(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "signer.pub"))
	if err != nil {
		t.Fatal(err)
	}
	key, err := ParseSignerKey(data)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestParseSignerKey(t *testing.T) {
	key := testSignerKey(t)
	tests := []struct {
		name, data, wantErr string
	}{
		{"public key", key, ""},
		{"with surrounding blank lines", "\n" + key + "\n\n", ""},
		{"empty", "", "not a PEM public key"},
		{"private key", strings.ReplaceAll(key, "PUBLIC KEY", "PRIVATE KEY"), "not a PEM public key"},
		{"two keys", key + key, "more than one PEM block"},
		{"garbage in the block", "-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----\n", "not a public key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSignerKey([]byte(tt.data))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != key {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

func TestSignerCheck(t *testing.T) {
	tests := []struct {
		name    string
		signer  Signer
		wantErr bool
	}{
		{"default", DefaultSigner(), false},
		{"key", Signer{Key: testSignerKey(t)}, false},
		{"nothing", Signer{}, true},
		{"no subject", Signer{Issuer: DefaultSignerIssuer}, true},
		{"bad subject", Signer{Issuer: DefaultSignerIssuer, Subject: "("}, true},
		{"bad key", Signer{Key: "not a key"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.signer.Check(); (err != nil) != tt.wantErr {
				t.Fatalf("error %v, want error %v", err, tt.wantErr)
			}
		})
	}
}

// TestDefaultSubject pins which certificates the default accepts: shelf's build and release
// workflows at a tag or branch of tweinmann/shelf, and nothing of a fork or another workflow.
func TestDefaultSubject(t *testing.T) {
	re := regexp.MustCompile(DefaultSignerSubject)
	for subject, want := range map[string]bool{
		"https://github.com/tweinmann/shelf/.github/workflows/build.yml@refs/tags/v0":           true,
		"https://github.com/tweinmann/shelf/.github/workflows/build.yml@refs/heads/main":        true,
		"https://github.com/tweinmann/shelf/.github/workflows/release.yml@refs/tags/v0.8.0":     true,
		"https://github.com/tweinmann/shelf/.github/workflows/ci.yml@refs/heads/main":           false,
		"https://github.com/someone/shelf/.github/workflows/build.yml@refs/tags/v0":             false,
		"https://github.com/tweinmann/shelf-hello/.github/workflows/deploy.yml@refs/heads/main": false,
		"https://github.com/tweinmann/shelfXgithub/workflows/build.yml@refs/tags/v0":            false,
	} {
		if got := re.MatchString(subject); got != want {
			t.Errorf("%s: match %v, want %v", subject, got, want)
		}
	}
}

func TestSignerForms(t *testing.T) {
	keyless := DefaultSigner()
	if v := keyless.verify("s"); v["secretRef"] != nil || v["matchOIDCIdentity"] == nil {
		t.Errorf("keyless verify %v", v)
	}
	issuer := keyless.verify("s")["matchOIDCIdentity"].([]any)[0].(map[string]any)["issuer"].(string)
	if !regexp.MustCompile(issuer).MatchString(DefaultSignerIssuer) || regexp.MustCompile(issuer).MatchString(DefaultSignerIssuer+".evil") {
		t.Errorf("issuer regex %q", issuer)
	}
	if a := keyless.authority(); a["keyless"] == nil || a["ctlog"] == nil {
		t.Errorf("keyless authority %v", a)
	}
	key := Signer{Key: testSignerKey(t)}
	if v := key.verify("s"); v["secretRef"] == nil || v["matchOIDCIdentity"] != nil {
		t.Errorf("key verify %v", v)
	}
	// A dev signature has no transparency log entry, so a key authority must not ask for one.
	if a := key.authority(); a["key"] == nil || a["ctlog"] != nil {
		t.Errorf("key authority %v", a)
	}
	if !strings.HasPrefix(key.String(), "cosign key sha256:") || !strings.HasPrefix(keyless.String(), "keyless, issuer ") {
		t.Errorf("String() = %q, %q", key.String(), keyless.String())
	}
}
