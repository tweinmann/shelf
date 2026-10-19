package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// fakeAPI answers like the GitHub packages API, with the replies given by the raw path.
type fakeAPI struct {
	t       *testing.T
	replies map[string]string
	// links is the Link header a path answers with.
	links    map[string]string
	requests []string
}

func (f *fakeAPI) server() *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.EscapedPath() + "?" + r.URL.RawQuery
		f.requests = append(f.requests, path)
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"message":"Bad credentials"}`)
			return
		}
		reply, ok := f.replies[path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
			return
		}
		if link, ok := f.links[path]; ok {
			w.Header().Set("Link", strings.ReplaceAll(link, "BASE", "http://"+r.Host))
		}
		fmt.Fprint(w, reply)
	}))
	f.t.Cleanup(srv.Close)
	return srv
}

func newTestClient(t *testing.T, api *fakeAPI, token string) *Client {
	t.Helper()
	api.t = t
	srv := api.server()
	return &Client{Token: token, BaseURL: srv.URL, HTTP: srv.Client()}
}

func TestPackagesFollowsPaging(t *testing.T) {
	const first = "/user/packages?package_type=container&per_page=100"
	api := &fakeAPI{
		replies: map[string]string{
			first: `[{"name":"greeter","owner":{"login":"alice"},"updated_at":"2026-09-01T10:00:00Z"}]`,
			"/user/packages?package_type=container&per_page=100&page=2": `[{"name":"greeter/web","owner":{"login":"alice"}}]`,
		},
		links: map[string]string{
			first: `<BASE/user/packages?package_type=container&per_page=100&page=2>; rel="next", <BASE/x>; rel="last"`,
		},
	}
	c := newTestClient(t, api, "test-token")
	got, err := c.Packages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "greeter" || got[0].Owner != "alice" || got[0].Updated.IsZero() ||
		got[1].Name != "greeter/web" {
		t.Errorf("packages %+v", got)
	}
}

func TestPagingStaysOnTheAPI(t *testing.T) {
	const first = "/user/packages?package_type=container&per_page=100"
	api := &fakeAPI{
		replies: map[string]string{first: `[]`},
		links:   map[string]string{first: `<https://elsewhere.example/steal>; rel="next"`},
	}
	c := newTestClient(t, api, "test-token")
	if _, err := c.Packages(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(api.requests) != 1 {
		t.Errorf("requests %v: the token must not follow a link to another host", api.requests)
	}
}

func TestTagsNewestFirst(t *testing.T) {
	api := &fakeAPI{replies: map[string]string{
		"/user/packages/container/greeter%2Fweb/versions?per_page=100": `[
			{"created_at":"2026-09-01T10:00:00Z","metadata":{"container":{"tags":["sha-111"]}}},
			{"created_at":"2026-09-03T10:00:00Z","metadata":{"container":{"tags":["main","sha-333"]}}},
			{"created_at":"2026-09-02T10:00:00Z","metadata":{"container":{"tags":[]}}}
		]`,
	}}
	c := newTestClient(t, api, "test-token")
	got, err := c.Tags(context.Background(), "greeter/web")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tag := range got {
		names = append(names, tag.Name)
	}
	if want := []string{"main", "sha-333", "sha-111"}; !slices.Equal(names, want) {
		t.Errorf("tags %v, want %v", names, want)
	}
}

func TestErrorsNameWhatGitHubSaid(t *testing.T) {
	c := newTestClient(t, &fakeAPI{}, "not-a-real-token")
	_, err := c.Packages(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Bad credentials (HTTP 401)") {
		t.Fatalf("error %v", err)
	}
	if strings.Contains(err.Error(), "not-a-real-token") {
		t.Errorf("the error shows the token: %v", err)
	}
}
