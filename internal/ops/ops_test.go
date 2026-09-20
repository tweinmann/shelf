package ops_test

import (
	"strings"
	"testing"

	"github.com/tweinmann/shelf/internal/cluster"
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
	settings := cluster.Settings{Domain: "example.com", HostSuffix: "-dev"}
	if got := ops.AppHost(settings, "greeter"); got != "greeter-dev.example.com" {
		t.Errorf("host %q", got)
	}
}
