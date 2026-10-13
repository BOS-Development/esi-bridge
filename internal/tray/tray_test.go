package tray

import (
	"net"
	"testing"
)

func TestIsLANBind(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"localhost", false},
		{"127.0.0.1", false},
		{"::1", false},
		{"[::1]", false},
		{"", false},
		{"0.0.0.0", true},
		{"192.168.1.50", true},
		{"10.0.0.1", true},
	}
	for _, tt := range tests {
		if got := isLANBind(tt.host); got != tt.want {
			t.Errorf("isLANBind(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestLocalIP(t *testing.T) {
	got := localIP()
	if got == "" {
		t.Fatal("localIP returned empty string")
	}
	// Either a parseable IP (there's a route) or the localhost fallback.
	if got != "localhost" && net.ParseIP(got) == nil {
		t.Errorf("localIP = %q, want an IP address or \"localhost\"", got)
	}
}

func TestDisplayHost(t *testing.T) {
	tr := &Tray{}

	if got := tr.displayHost("localhost"); got != "localhost" {
		t.Errorf("displayHost(localhost) = %q, want localhost", got)
	}
	if got := tr.displayHost("192.168.1.50"); got != "192.168.1.50" {
		t.Errorf("displayHost(specific IP) = %q, want it unchanged", got)
	}
	// A wildcard bind resolves to the machine's LAN IP.
	if got := tr.displayHost("0.0.0.0"); got != localIP() {
		t.Errorf("displayHost(0.0.0.0) = %q, want localIP() %q", got, localIP())
	}
}
