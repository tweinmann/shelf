package secrets

import (
	"net/url"
	"strings"
	"testing"
)

func TestGenerateIsURLSafe(t *testing.T) {
	seen := map[string]bool{}
	for range 2000 {
		s := Generate()
		if len(s) < 26 {
			t.Fatalf("%q is shorter than 26 characters", s)
		}
		if url.QueryEscape(s) != s || url.PathEscape(s) != s {
			t.Fatalf("%q changes under URL escaping", s)
		}
		// Characters that break connection URIs, shells, YAML or kubelet expansion.
		if i := strings.IndexAny(s, "@:/?#[]$%&'\"`\\ !*+,;=(){}<>|~^-_."); i >= 0 {
			t.Fatalf("%q contains %q", s, s[i])
		}
		if seen[s] {
			t.Fatalf("duplicate value %q", s)
		}
		seen[s] = true
	}
}

// A connection URI built from a generated password must parse back to the same password.
func TestGeneratedPasswordInURI(t *testing.T) {
	for range 200 {
		pw := Generate()
		u, err := url.Parse("postgres://app:" + pw + "@db:5432/app")
		if err != nil {
			t.Fatal(err)
		}
		got, _ := u.User.Password()
		if got != pw || u.Host != "db:5432" {
			t.Fatalf("password %q parsed as %q, host %q", pw, got, u.Host)
		}
	}
}
