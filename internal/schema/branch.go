package schema

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	// BranchTagPrefix starts the tag of every branch's deploy artifact; the platform's preview
	// provider follows only tags with it.
	BranchTagPrefix = "branch-"
	// MaxBranchSlugLength keeps <app>--<slug> a DNS label: 40 + 2 + 20 is 62.
	MaxBranchSlugLength = 20
)

// BranchSlug turns a branch name into the part of a preview's name that stands for it: lower
// case, every run of other characters one "-", no "-" at either end. A longer result keeps its
// first 13 characters and adds 6 hex digits of the branch name's SHA-256, so two long names with
// the same beginning still differ. A slug is its own slug, so a caller may pass either.
//
// The reusable workflow and the CLI both call this through shelf, so a branch maps to the same
// name wherever it is looked at; the cluster only strips the tag's prefix.
func BranchSlug(branch string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(branch) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	slug := strings.TrimRight(b.String(), "-")
	if len(slug) <= MaxBranchSlugLength {
		return slug
	}
	sum := sha256.Sum256([]byte(branch))
	return strings.TrimRight(slug[:13], "-") + "-" + hex.EncodeToString(sum[:])[:6]
}

// BranchTag is the tag a branch's deploy artifact is published under.
func BranchTag(branch string) string { return BranchTagPrefix + BranchSlug(branch) }
