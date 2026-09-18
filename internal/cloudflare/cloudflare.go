// Package cloudflare talks to the Cloudflare API: it finds or creates the tunnel that exposes a
// cluster. DNS records are not managed here; external-dns does that in the cluster.
package cloudflare

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is the Cloudflare API v4.
const DefaultBaseURL = "https://api.cloudflare.com/client/v4"

// TunnelDomain is where a tunnel answers; DNS records point at <tunnel-id>.cfargotunnel.com.
const TunnelDomain = "cfargotunnel.com"

// Client calls the Cloudflare API with an API token. The token needs
// Account:Cloudflare Tunnel:Edit, and Zone:DNS:Edit for the zone external-dns manages.
type Client struct {
	Token   string
	BaseURL string
	HTTP    *http.Client
}

// New returns a client for the given API token. Surrounding whitespace, which a copied token
// easily carries, would make Cloudflare reject the header as malformed.
func New(token string) *Client {
	return &Client{
		Token:   strings.TrimSpace(token),
		BaseURL: DefaultBaseURL,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Tunnel is a Cloudflare tunnel.
type Tunnel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Deleted is set for tunnels that were removed but are still listed.
	Deleted *time.Time `json:"deleted_at"`
}

// Target is what DNS records point at.
func (t Tunnel) Target() string { return t.ID + "." + TunnelDomain }

// Account is a Cloudflare account.
type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// response is the envelope every Cloudflare API reply uses.
type response struct {
	Success bool            `json:"success"`
	Errors  []apiError      `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e apiError) String() string { return fmt.Sprintf("%s (code %d)", e.Message, e.Code) }

// do sends a request and unmarshals result, or returns what Cloudflare complained about.
func (c *Client) do(ctx context.Context, method, path string, body, result any) error {
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(data)
	}
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, payload)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var env response
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("%s %s: unexpected reply (HTTP %d)", method, path, resp.StatusCode)
	}
	if !env.Success {
		var msgs []string
		for _, e := range env.Errors {
			msgs = append(msgs, e.String())
		}
		if len(msgs) == 0 {
			msgs = []string{fmt.Sprintf("HTTP %d", resp.StatusCode)}
		}
		return fmt.Errorf("%s %s: %s", method, path, strings.Join(msgs, "; "))
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(env.Result, result)
}

// VerifyToken checks that the token itself is valid, which tells a wrong kind of credential
// apart from a missing permission.
func (c *Client) VerifyToken(ctx context.Context) error {
	var status struct {
		Status string `json:"status"`
	}
	if err := c.do(ctx, http.MethodGet, "/user/tokens/verify", nil, &status); err != nil {
		return fmt.Errorf("%w\nUse an API token (Cloudflare profile, API Tokens), not the Global API Key", err)
	}
	if status.Status != "" && status.Status != "active" {
		return fmt.Errorf("the API token is %s", status.Status)
	}
	return nil
}

// Accounts returns the accounts the token can see.
func (c *Client) Accounts(ctx context.Context) ([]Account, error) {
	var accounts []Account
	if err := c.do(ctx, http.MethodGet, "/accounts?per_page=50", nil, &accounts); err != nil {
		return nil, err
	}
	return accounts, nil
}

// AccountID returns the only account the token can see. With several accounts the caller has to
// choose one, because a tunnel belongs to exactly one.
func (c *Client) AccountID(ctx context.Context) (string, error) {
	accounts, err := c.Accounts(ctx)
	if err != nil {
		return "", fmt.Errorf("looking up the account: %w\nThe token needs Account Settings:Read for this; "+
			"or set CF_ACCOUNT_ID to the account the tunnel belongs to", err)
	}
	switch len(accounts) {
	case 0:
		return "", fmt.Errorf("the API token sees no account; set CF_ACCOUNT_ID")
	case 1:
		return accounts[0].ID, nil
	default:
		var names []string
		for _, a := range accounts {
			names = append(names, fmt.Sprintf("%s (%s)", a.Name, a.ID))
		}
		return "", fmt.Errorf("the API token sees several accounts, set CF_ACCOUNT_ID to one of: %s",
			strings.Join(names, ", "))
	}
}

// FindTunnel returns the tunnel with that name, or nil if there is none.
func (c *Client) FindTunnel(ctx context.Context, account, name string) (*Tunnel, error) {
	path := fmt.Sprintf("/accounts/%s/cfd_tunnel?is_deleted=false&name=%s", account, url.QueryEscape(name))
	var tunnels []Tunnel
	if err := c.do(ctx, http.MethodGet, path, nil, &tunnels); err != nil {
		return nil, err
	}
	for _, t := range tunnels {
		if t.Name == name && t.Deleted == nil {
			return &t, nil
		}
	}
	return nil, nil
}

// CreateTunnel creates a locally managed tunnel and returns it with the credentials cloudflared
// needs. The secret exists only here and in the cluster; Cloudflare stores its hash.
func (c *Client) CreateTunnel(ctx context.Context, account, name string) (*Tunnel, []byte, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, nil, err
	}
	body := map[string]any{
		"name":          name,
		"tunnel_secret": base64.StdEncoding.EncodeToString(secret),
		"config_src":    "local",
	}
	var tunnel Tunnel
	if err := c.do(ctx, http.MethodPost, "/accounts/"+account+"/cfd_tunnel", body, &tunnel); err != nil {
		return nil, nil, err
	}
	credentials, err := json.Marshal(map[string]string{
		"AccountTag":   account,
		"TunnelID":     tunnel.ID,
		"TunnelSecret": base64.StdEncoding.EncodeToString(secret),
	})
	if err != nil {
		return nil, nil, err
	}
	return &tunnel, credentials, nil
}

// DeleteTunnel removes a tunnel. Cloudflare refuses while connections are still open.
func (c *Client) DeleteTunnel(ctx context.Context, account, id string) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/accounts/%s/cfd_tunnel/%s", account, id), nil, nil)
}
