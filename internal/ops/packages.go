package ops

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/github"
)

// GitHubAPI is the part of the GitHub API shelf uses: listing what a registry connection could
// pull, so that a form can offer it. Nothing that deploys an app calls it.
type GitHubAPI interface {
	Packages(ctx context.Context) ([]github.Package, error)
	Tags(ctx context.Context, pkg string) ([]github.Tag, error)
}

func liveGitHub(token string) GitHubAPI { return github.New(token) }

// Package is a deploy artifact a registry connection can pull, without a tag.
type Package struct {
	Name string `json:"name"`
	// Artifact is oci://ghcr.io/<owner>/<name>; a tag makes it what `shelf app add` takes.
	Artifact string    `json:"artifact"`
	Updated  time.Time `json:"updated"`
}

// Tag is a tag of a deploy artifact.
type Tag struct {
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
}

// registryLogin reads the login of a registry connection.
func (o *Ops) registryLogin(ctx context.Context, registry string) (*cluster.RegistryAuth, error) {
	if registry == "" {
		return nil, fmt.Errorf("without a registry connection, shelf cannot list packages; enter the artifact instead")
	}
	login, err := o.Cluster.RegistryConnection(ctx, o.config(), registry)
	if err != nil {
		return nil, err
	}
	if login == nil {
		return nil, missingRegistry(registry)
	}
	return login, nil
}

// Packages returns the deploy artifacts of the user of a registry connection, by name. The
// images the workflow builds, <name>/<component>, are left out: an app is added from its deploy
// artifact.
func (o *Ops) Packages(ctx context.Context, registry string) ([]Package, error) {
	login, err := o.registryLogin(ctx, registry)
	if err != nil {
		return nil, err
	}
	pkgs, err := o.NewGitHub(login.Token).Packages(ctx)
	if err != nil {
		return nil, err
	}
	out := []Package{}
	for _, p := range pkgs {
		if strings.Contains(p.Name, "/") {
			continue
		}
		owner := strings.ToLower(cmp.Or(p.Owner, login.Username))
		out = append(out, Package{
			Name:     p.Name,
			Artifact: "oci://" + cluster.RegistryHost + "/" + owner + "/" + p.Name,
			Updated:  p.Updated,
		})
	}
	slices.SortFunc(out, func(a, b Package) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// Tags returns the tags of a deploy artifact, newest first. artifact is oci://ghcr.io/<owner>/<name>,
// with or without a tag; it has to belong to the user of the registry connection, since that is
// whose packages the connection can list.
func (o *Ops) Tags(ctx context.Context, registry, artifact string) ([]Tag, error) {
	login, err := o.registryLogin(ctx, registry)
	if err != nil {
		return nil, err
	}
	pkg, err := ownPackage(artifact, login.Username)
	if err != nil {
		return nil, err
	}
	tags, err := o.NewGitHub(login.Token).Tags(ctx, pkg)
	if err != nil {
		return nil, err
	}
	out := []Tag{}
	seen := map[string]bool{}
	for _, t := range tags {
		// A signature or attestation is stored as a tag too, but nobody deploys one.
		if seen[t.Name] || strings.HasPrefix(t.Name, "sha256-") {
			continue
		}
		seen[t.Name] = true
		out = append(out, Tag{Name: t.Name, Created: t.Created})
	}
	return out, nil
}

// AppTags returns the tags of the deploy artifact an app runs, newest first, read with the app's
// registry connection.
func (o *Ops) AppTags(ctx context.Context, name string) ([]Tag, error) {
	apps, err := o.Apps(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range apps {
		if a.Name != name {
			continue
		}
		if a.Registry == "" {
			return nil, fmt.Errorf("%s pulls without a registry connection, so shelf cannot list its tags", name)
		}
		return o.Tags(ctx, a.Registry, a.Artifact.URL)
	}
	return nil, fmt.Errorf("app %s does not exist", name)
}

// ownPackage returns the name of a package oci://ghcr.io/<owner>/<name>[:tag] and checks that
// owner is user.
func ownPackage(artifact, user string) (string, error) {
	rest, ok := strings.CutPrefix(artifact, "oci://")
	if !ok {
		return "", fmt.Errorf("%q must start with oci://", artifact)
	}
	host, path, _ := strings.Cut(rest, "/")
	if i := strings.LastIndex(path, ":"); i >= 0 {
		path = path[:i]
	}
	owner, pkg, _ := strings.Cut(path, "/")
	if host != cluster.RegistryHost || owner == "" || pkg == "" {
		return "", fmt.Errorf("%s is not a package on %s; shelf lists tags there only", artifact, cluster.RegistryHost)
	}
	if !strings.EqualFold(owner, user) {
		return "", fmt.Errorf("%s belongs to %s, and the connection lists the packages of %s only", artifact, owner, user)
	}
	return pkg, nil
}
