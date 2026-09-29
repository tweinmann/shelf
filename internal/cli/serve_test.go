package cli

import (
	"net"
	"testing"
)

// fakeAddr is a listening address without a listener.
type fakeAddr string

func (fakeAddr) Network() string  { return "tcp" }
func (a fakeAddr) String() string { return string(a) }

// TestUIURLs pins which address the server offers. It cannot know where the person reading it
// sits, so it names both and says which is which instead of guessing wrong.
func TestUIURLs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		addr       string
		hostname   string
		wantLocal  string
		wantRemote string
	}{
		{
			name: "every address", addr: "[::]:7654", hostname: "shelf",
			wantLocal: "http://localhost:7654/", wantRemote: "http://shelf:7654/",
		},
		{
			name: "every IPv4 address", addr: "0.0.0.0:8080", hostname: "2671e869ca3e",
			wantLocal: "http://localhost:8080/", wantRemote: "http://2671e869ca3e:8080/",
		},
		{
			name: "loopback only", addr: "127.0.0.1:7654", hostname: "shelf",
			wantLocal: "http://127.0.0.1:7654/",
		},
		{
			name: "one address of several", addr: "192.168.1.10:7654", hostname: "shelf",
			wantLocal: "http://192.168.1.10:7654/",
		},
		{
			name: "no host name to offer", addr: "[::]:7654", hostname: "",
			wantLocal: "http://localhost:7654/",
		},
		{
			name: "a host name that adds nothing", addr: "[::]:7654", hostname: "localhost",
			wantLocal: "http://localhost:7654/",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			local, remote := uiURLs(fakeAddr(tt.addr), tt.hostname)
			if local != tt.wantLocal {
				t.Errorf("local %q, want %q", local, tt.wantLocal)
			}
			if remote != tt.wantRemote {
				t.Errorf("remote %q, want %q", remote, tt.wantRemote)
			}
		})
	}
}

// TestUIURLsAcceptsARealListener guards the assumption that a listener's address parses.
func TestUIURLsAcceptsARealListener(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	local, remote := uiURLs(listener.Addr(), "shelf")
	if local == "" || remote != "" {
		t.Errorf("local %q, remote %q", local, remote)
	}
}
