package cloudflare

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeAPI answers like the Cloudflare API: every reply is wrapped in its envelope.
type fakeAPI struct {
	t        *testing.T
	accounts []Account
	tunnels  []Tunnel
	// requests records method and path of every call.
	requests []string
	// created holds the body of the last create call.
	created map[string]any
	fail    map[string]apiError
}

func (f *fakeAPI) server() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			f.reply(w, false, nil, apiError{Code: 9109, Message: "Invalid access token"})
			return
		}
		if e, ok := f.fail[r.Method+" "+r.URL.Path]; ok {
			f.reply(w, false, nil, e)
			return
		}
		switch {
		case r.URL.Path == "/user/tokens/verify":
			f.reply(w, true, map[string]string{"status": "active"})
		case r.URL.Path == "/accounts":
			f.reply(w, true, f.accounts)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/cfd_tunnel"):
			name := r.URL.Query().Get("name")
			var found []Tunnel
			for _, t := range f.tunnels {
				if t.Name == name {
					found = append(found, t)
				}
			}
			f.reply(w, true, found)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cfd_tunnel"):
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &f.created); err != nil {
				f.t.Fatal(err)
			}
			tunnel := Tunnel{ID: "tunnel-id", Name: f.created["name"].(string)}
			f.tunnels = append(f.tunnels, tunnel)
			f.reply(w, true, tunnel)
		case r.Method == http.MethodDelete:
			f.tunnels = nil
			f.reply(w, true, nil)
		default:
			f.t.Fatalf("unexpected request %s %s", r.Method, r.URL)
		}
	})
	srv := httptest.NewServer(mux)
	f.t.Cleanup(srv.Close)
	return srv
}

func (f *fakeAPI) reply(w http.ResponseWriter, success bool, result any, errs ...apiError) {
	data, err := json.Marshal(result)
	if err != nil {
		f.t.Fatal(err)
	}
	if !success {
		w.WriteHeader(http.StatusForbidden)
	}
	if err := json.NewEncoder(w).Encode(response{Success: success, Errors: errs, Result: data}); err != nil {
		f.t.Fatal(err)
	}
}

func newTestClient(t *testing.T, api *fakeAPI) *Client {
	t.Helper()
	api.t = t
	srv := api.server()
	return &Client{Token: "test-token", BaseURL: srv.URL, HTTP: srv.Client()}
}

func TestVerifyToken(t *testing.T) {
	c := newTestClient(t, &fakeAPI{})
	if err := c.VerifyToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A Global API Key is not a bearer token; Cloudflare rejects the header itself.
	wrong := &Client{Token: "global-api-key", BaseURL: c.BaseURL, HTTP: c.HTTP}
	err := wrong.VerifyToken(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Global API Key") {
		t.Errorf("error %v, want the hint about the token kind", err)
	}
}

func TestTokenWhitespaceIsTrimmed(t *testing.T) {
	if got := New("  token\n").Token; got != "token" {
		t.Errorf("token %q", got)
	}
}

func TestAccountID(t *testing.T) {
	tests := []struct {
		name     string
		accounts []Account
		want     string
		wantErr  string
	}{
		{name: "one account", accounts: []Account{{ID: "acc-1", Name: "Personal"}}, want: "acc-1"},
		{name: "no account", wantErr: "sees no account"},
		{
			name:     "several accounts",
			accounts: []Account{{ID: "acc-1", Name: "Personal"}, {ID: "acc-2", Name: "Work"}},
			wantErr:  "CF_ACCOUNT_ID",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, &fakeAPI{accounts: tt.accounts})
			got, err := c.AccountID(context.Background())
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

func TestFindTunnel(t *testing.T) {
	api := &fakeAPI{tunnels: []Tunnel{{ID: "t-1", Name: "shelf"}, {ID: "t-2", Name: "shelf-dev"}}}
	c := newTestClient(t, api)

	got, err := c.FindTunnel(context.Background(), "acc-1", "shelf-dev")
	if err != nil || got == nil || got.ID != "t-2" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if got.Target() != "t-2.cfargotunnel.com" {
		t.Errorf("target %q", got.Target())
	}
	missing, err := c.FindTunnel(context.Background(), "acc-1", "nope")
	if err != nil || missing != nil {
		t.Fatalf("got %+v, %v", missing, err)
	}
}

func TestCreateTunnel(t *testing.T) {
	api := &fakeAPI{}
	c := newTestClient(t, api)

	tunnel, credentials, err := c.CreateTunnel(context.Background(), "acc-1", "shelf-dev")
	if err != nil {
		t.Fatal(err)
	}
	if tunnel.ID != "tunnel-id" || tunnel.Name != "shelf-dev" {
		t.Errorf("tunnel %+v", tunnel)
	}
	if api.created["config_src"] != "local" {
		t.Errorf("shelf keeps the configuration local, got %v", api.created["config_src"])
	}

	var creds map[string]string
	if err := json.Unmarshal(credentials, &creds); err != nil {
		t.Fatal(err)
	}
	if creds["AccountTag"] != "acc-1" || creds["TunnelID"] != "tunnel-id" {
		t.Errorf("credentials %v", creds)
	}
	secret, err := base64.StdEncoding.DecodeString(creds["TunnelSecret"])
	if err != nil || len(secret) != 32 {
		t.Errorf("secret of %d bytes, %v", len(secret), err)
	}
	if creds["TunnelSecret"] != api.created["tunnel_secret"] {
		t.Error("the credentials must carry the secret Cloudflare was given")
	}

	// A second tunnel gets its own secret.
	_, other, err := c.CreateTunnel(context.Background(), "acc-1", "shelf")
	if err != nil {
		t.Fatal(err)
	}
	if string(other) == string(credentials) {
		t.Error("two tunnels must not share a secret")
	}
}

func TestAPIErrors(t *testing.T) {
	c := newTestClient(t, &fakeAPI{fail: map[string]apiError{
		"GET /accounts/acc-1/cfd_tunnel": {Code: 10000, Message: "Authentication error"},
	}})
	if _, err := c.FindTunnel(context.Background(), "acc-1", "shelf"); err == nil ||
		!strings.Contains(err.Error(), "Authentication error (code 10000)") {
		t.Errorf("error %v", err)
	}

	wrong := &Client{Token: "nope", BaseURL: c.BaseURL, HTTP: c.HTTP}
	if _, err := wrong.Accounts(context.Background()); err == nil || !strings.Contains(err.Error(), "Invalid access token") {
		t.Errorf("error %v", err)
	}
}

func TestDeleteTunnel(t *testing.T) {
	api := &fakeAPI{tunnels: []Tunnel{{ID: "t-1", Name: "shelf"}}}
	c := newTestClient(t, api)
	if err := c.DeleteTunnel(context.Background(), "acc-1", "t-1"); err != nil {
		t.Fatal(err)
	}
	got, err := c.FindTunnel(context.Background(), "acc-1", "shelf")
	if err != nil || got != nil {
		t.Fatalf("got %+v, %v", got, err)
	}
}
