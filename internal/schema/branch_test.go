package schema

import (
	"regexp"
	"strings"
	"testing"
)

func TestBranchSlug(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"feature/Login":           "feature-login",
		"main":                    "main",
		"fix--typo":               "fix-typo",
		"--lead/and/trail--":      "lead-and-trail",
		"feature-login":           "feature-login",
		"Ümlaut_ünd spaces":       "mlaut-nd-spaces",
		"agent/claude-1/refactor": "agent-claude-ad9bc7",
	}
	for branch, want := range tests {
		if got := BranchSlug(branch); got != want {
			t.Errorf("BranchSlug(%q) = %q, want %q", branch, got, want)
		}
	}
	if got := BranchTag("feature/Login"); got != "branch-feature-login" {
		t.Errorf("tag %q", got)
	}
}

func TestLongBranchSlugs(t *testing.T) {
	t.Parallel()
	label := regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	a := BranchSlug("agent/claude-1/refactor-the-billing-module")
	b := BranchSlug("agent/claude-1/refactor-the-billing-api")
	if a == b {
		t.Errorf("two long branches share the slug %q", a)
	}
	for _, s := range []string{a, b, BranchSlug(strings.Repeat("x", 200)), BranchSlug("a-------------b-c-d-e-f-g")} {
		if len(s) > MaxBranchSlugLength || !label.MatchString(s) {
			t.Errorf("slug %q is longer than %d or not a DNS label", s, MaxBranchSlugLength)
		}
	}
	if BranchSlug(a) != a {
		t.Errorf("a slug is not its own slug: %q -> %q", a, BranchSlug(a))
	}
}
