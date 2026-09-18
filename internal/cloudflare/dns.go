package cloudflare

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Record is a DNS record in a zone.
type Record struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	// Proxied sends the traffic through Cloudflare, which a tunnel target requires: a
	// cfargotunnel.com name does not resolve on its own, and the certificate comes from there.
	Proxied bool `json:"proxied"`
	TTL     int  `json:"ttl,omitempty"`
}

// Zone is a DNS zone.
type Zone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Action is what EnsureRecord did.
type Action string

const (
	Created   Action = "created"
	Updated   Action = "updated"
	Unchanged Action = "unchanged"
)

// ZoneFor returns the zone that holds name: the zone of the name itself, or of one of its parent
// domains, so a host in a subdomain is found too.
func (c *Client) ZoneFor(ctx context.Context, name string) (*Zone, error) {
	labels := strings.Split(strings.TrimSuffix(name, "."), ".")
	for i := 0; i+1 < len(labels); i++ {
		candidate := strings.Join(labels[i:], ".")
		var zones []Zone
		if err := c.do(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(candidate), nil, &zones); err != nil {
			return nil, err
		}
		for _, z := range zones {
			if z.Name == candidate {
				return &z, nil
			}
		}
	}
	return nil, fmt.Errorf("no Cloudflare zone holds %s; the API token must cover its zone", name)
}

// FindRecord returns the record of that type and name, or nil if there is none.
func (c *Client) FindRecord(ctx context.Context, zone, recordType, name string) (*Record, error) {
	path := fmt.Sprintf("/zones/%s/dns_records?type=%s&name=%s", zone, recordType, url.QueryEscape(name))
	var records []Record
	if err := c.do(ctx, http.MethodGet, path, nil, &records); err != nil {
		return nil, err
	}
	for _, r := range records {
		if r.Name == name && r.Type == recordType {
			return &r, nil
		}
	}
	return nil, nil
}

// EnsureRecord makes name a proxied CNAME to target and reports what it had to change.
func (c *Client) EnsureRecord(ctx context.Context, zone, name, target string) (Action, error) {
	want := Record{Type: "CNAME", Name: name, Content: target, Proxied: true, TTL: 1}
	existing, err := c.FindRecord(ctx, zone, want.Type, name)
	if err != nil {
		return "", err
	}
	switch {
	case existing == nil:
		if err := c.do(ctx, http.MethodPost, "/zones/"+zone+"/dns_records", want, nil); err != nil {
			return "", fmt.Errorf("creating the record for %s: %w", name, err)
		}
		return Created, nil
	case existing.Content == target && existing.Proxied:
		return Unchanged, nil
	default:
		path := fmt.Sprintf("/zones/%s/dns_records/%s", zone, existing.ID)
		if err := c.do(ctx, http.MethodPut, path, want, nil); err != nil {
			return "", fmt.Errorf("updating the record for %s: %w", name, err)
		}
		return Updated, nil
	}
}

// DeleteRecord removes the CNAME of name and reports whether there was one.
func (c *Client) DeleteRecord(ctx context.Context, zone, name string) (bool, error) {
	existing, err := c.FindRecord(ctx, zone, "CNAME", name)
	if err != nil || existing == nil {
		return false, err
	}
	path := fmt.Sprintf("/zones/%s/dns_records/%s", zone, existing.ID)
	if err := c.do(ctx, http.MethodDelete, path, nil, nil); err != nil {
		return false, fmt.Errorf("deleting the record for %s: %w", name, err)
	}
	return true, nil
}
