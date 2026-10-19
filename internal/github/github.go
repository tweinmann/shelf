// Package github reads the container packages of a GitHub user and their tags, so that the admin
// UI can offer them as a choice. It is a convenience of the host service: nothing that deploys an
// app depends on it, the registry stays the only interface between build and platform.
package github

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

// DefaultBaseURL is the GitHub REST API.
const DefaultBaseURL = "https://api.github.com"

// maxPages bounds how far a list follows the paging. A package that is pushed on every commit
// collects versions without end; the newest few hundred are what anyone picks from.
const maxPages = 5

// Client calls the GitHub API with a token. A classic personal access token with read:packages,
// the one a registry connection holds for pulling from GHCR, is enough.
type Client struct {
	Token   string
	BaseURL string
	HTTP    *http.Client
}

// New returns a client for the given token.
func New(token string) *Client {
	return &Client{
		Token:   strings.TrimSpace(token),
		BaseURL: DefaultBaseURL,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Package is a container package.
type Package struct {
	Name    string    `json:"name"`
	Owner   string    `json:"owner"`
	Updated time.Time `json:"updated"`
}

// Tag is a tag of a package version.
type Tag struct {
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
}

// Packages returns the container packages of the token's user.
func (c *Client) Packages(ctx context.Context) ([]Package, error) {
	var raw []struct {
		Name  string `json:"name"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	if err := c.list(ctx, "/user/packages?package_type=container&per_page=100", &raw); err != nil {
		return nil, err
	}
	out := make([]Package, 0, len(raw))
	for _, p := range raw {
		out = append(out, Package{Name: p.Name, Owner: p.Owner.Login, Updated: p.UpdatedAt})
	}
	return out, nil
}

// Tags returns the tags of a container package of the token's user, newest first. A version
// without a tag is left out; a version with several tags gives one entry for each.
func (c *Client) Tags(ctx context.Context, pkg string) ([]Tag, error) {
	var raw []struct {
		CreatedAt time.Time `json:"created_at"`
		Metadata  struct {
			Container struct {
				Tags []string `json:"tags"`
			} `json:"container"`
		} `json:"metadata"`
	}
	path := "/user/packages/container/" + url.PathEscape(pkg) + "/versions?per_page=100"
	if err := c.list(ctx, path, &raw); err != nil {
		return nil, err
	}
	var out []Tag
	for _, v := range raw {
		for _, t := range v.Metadata.Container.Tags {
			out = append(out, Tag{Name: t, Created: v.CreatedAt})
		}
	}
	slices.SortStableFunc(out, func(a, b Tag) int { return b.Created.Compare(a.Created) })
	return out, nil
}

// next finds the URL of the next page in a Link header.
var next = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// list gets every page of a list, up to maxPages, and appends the items to result, which points
// at a slice.
func (c *Client) list(ctx context.Context, path string, result any) error {
	base := cmp.Or(c.BaseURL, DefaultBaseURL)
	target := base + path
	var all []json.RawMessage
	for range maxPages {
		var page []json.RawMessage
		link, err := c.get(ctx, target, &page)
		if err != nil {
			return err
		}
		all = append(all, page...)
		m := next.FindStringSubmatch(link)
		// The token goes only to the API it was sent to: a next page elsewhere is not followed.
		if m == nil || !strings.HasPrefix(m[1], base+"/") {
			break
		}
		target = m[1]
	}
	data, err := json.Marshal(all)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, result)
}

// get sends a GET request, unmarshals the reply into result and returns its Link header, or
// returns what GitHub complained about.
func (c *Client) get(ctx context.Context, target string, result any) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	shown := req.URL.Path
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET %s: %w", shown, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		var reply struct {
			Message string `json:"message"`
		}
		msg := fmt.Sprintf("HTTP %d", resp.StatusCode)
		if json.Unmarshal(data, &reply) == nil && reply.Message != "" {
			msg = fmt.Sprintf("%s (HTTP %d)", reply.Message, resp.StatusCode)
		}
		return "", fmt.Errorf("GET %s: %s", shown, msg)
	}
	if err := json.Unmarshal(data, result); err != nil {
		return "", fmt.Errorf("GET %s: unexpected reply", shown)
	}
	return resp.Header.Get("Link"), nil
}
