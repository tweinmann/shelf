package ops_test

import (
	"strings"
	"testing"

	"github.com/tweinmann/shelf/internal/ops"
)

// The operations themselves are exercised through the commands in internal/cli, which run them
// against fakes. What is checked here is the input the admin UI will validate with the same
// functions, and the host names both callers derive from the cluster settings.

func TestCheckAppName(t *testing.T) {
	t.Parallel()
	tests := map[string]bool{
		"hello":                        true,
		"a":                            true,
		"my-app-2":                     true,
		"":                             false,
		"Hello":                        false,
		"a_b":                          false,
		"-lead":                        false,
		"trail-":                       false,
		strings.Repeat("a", 40):        true,
		strings.Repeat("a", 41):        false,
		"a." + strings.Repeat("b", 10): false,
	}
	for name, valid := range tests {
		err := ops.CheckAppName(name)
		if valid && err != nil {
			t.Errorf("%q: %v", name, err)
		}
		if !valid {
			if err == nil {
				t.Errorf("%q was accepted", name)
			} else if !strings.Contains(err.Error(), "DNS label") {
				t.Errorf("%q: %v", name, err)
			}
		}
	}
}

// TestCheckDomainAndSuffix also pins that the messages carry no field name: the command line
// puts "--domain" in front of them, the admin UI will put its own label there.
func TestCheckDomainAndSuffix(t *testing.T) {
	t.Parallel()
	if err := ops.CheckDomain("example.com"); err != nil {
		t.Errorf("example.com: %v", err)
	}
	for _, domain := range []string{"", "Not A Domain", "-example.com"} {
		err := ops.CheckDomain(domain)
		if err == nil {
			t.Fatalf("%q was accepted", domain)
		}
		if strings.HasPrefix(err.Error(), "-") {
			t.Errorf("the message names a flag: %v", err)
		}
	}
	for _, suffix := range []string{"", "-dev", "-2"} {
		if err := ops.CheckHostSuffix(suffix); err != nil {
			t.Errorf("%q: %v", suffix, err)
		}
	}
	for _, suffix := range []string{"-a_b", ".dev", "-" + strings.Repeat("x", 70)} {
		if err := ops.CheckHostSuffix(suffix); err == nil {
			t.Errorf("%q was accepted", suffix)
		}
	}
}

func TestHosts(t *testing.T) {
	t.Parallel()
	if got := ops.Hosts("example.com", "-dev"); got != "<app>-dev.example.com" {
		t.Errorf("hosts %q", got)
	}
	if got := ops.Hosts("example.com", ""); got != "<app>.example.com" {
		t.Errorf("hosts %q", got)
	}
	if got := ops.AppHost("greeter", "-dev", "example.com"); got != "greeter-dev.example.com" {
		t.Errorf("host %q", got)
	}
	if got := ops.TunnelName("greeter", "-dev"); got != "shelf-dev-greeter" {
		t.Errorf("tunnel %q", got)
	}
}

// TestPublicDomain pins which names shelf may offer a link to. A reserved name cannot be
// delegated to a DNS provider, so no record will ever point at the tunnel — the development
// cluster uses dev.local and would otherwise show links that can never work.
func TestPublicDomain(t *testing.T) {
	t.Parallel()
	public := []string{"example.com", "shelf.dev", "a.b.example.co.uk", "EXAMPLE.COM", "example.com."}
	for _, domain := range public {
		if !ops.PublicDomain(domain) {
			t.Errorf("%q is a public domain", domain)
		}
	}
	reserved := []string{
		"", "localhost", "dev.local", "mini.local", "shelf.internal", "app.test",
		"x.invalid", "my.example", "shelf.home.arpa", "nodot",
	}
	for _, domain := range reserved {
		if ops.PublicDomain(domain) {
			t.Errorf("%q cannot exist on the internet", domain)
		}
	}
}

// TestHostsMoveError checks that the refusal only promises stranded records where records can
// exist: a development cluster under dev.local has none, whatever else changes.
func TestHostsMoveError(t *testing.T) {
	t.Parallel()
	public := &ops.HostsMoveError{
		From: "<app>-dev.tobile.ch", To: "<app>.dev.local",
		Apps: []string{"greeter"}, StrandsRecords: true,
	}
	if !strings.Contains(public.Error(), "records under the old names stay behind") {
		t.Errorf("a public name leaves a record behind: %v", public)
	}
	local := &ops.HostsMoveError{
		From: "<app>.dev.local", To: "<app>-dev.tobile.ch", Apps: []string{"greeter", "shop"},
	}
	if strings.Contains(local.Error(), "records") {
		t.Errorf("dev.local can have no records: %v", local)
	}
	if !strings.Contains(local.Error(), "apps greeter and shop") {
		t.Errorf("the message does not name the apps: %v", local)
	}
	if !strings.Contains(local.Error(), "--move-hosts") {
		t.Errorf("the message does not say how to do it on purpose: %v", local)
	}
}
