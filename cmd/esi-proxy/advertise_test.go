package main

import (
	"os"
	"strings"
	"testing"
)

func TestAdvertiseURLLocalhostIsVerbatim(t *testing.T) {
	// Bound to localhost: clients must connect to localhost, since the mDNS
	// A record's LAN address would not reach a localhost-only listener.
	if got, want := advertiseURL("localhost", 8085), "http://localhost:8085"; got != want {
		t.Fatalf("advertiseURL = %q, want %q", got, want)
	}
}

func TestAdvertiseURLSpecificHostIsVerbatim(t *testing.T) {
	if got, want := advertiseURL("192.168.1.50", 9000), "http://192.168.1.50:9000"; got != want {
		t.Fatalf("advertiseURL = %q, want %q", got, want)
	}
}

func TestAdvertiseURLWildcardUsesMDNSHostname(t *testing.T) {
	// Bound to all interfaces: advertise the host's mDNS name so LAN peers
	// can resolve and connect.
	for _, bind := range []string{"", "0.0.0.0", "::", "[::]"} {
		got := advertiseURL(bind, 8085)
		if !strings.HasPrefix(got, "http://") || !strings.HasSuffix(got, ":8085") {
			t.Errorf("advertiseURL(%q) = %q, malformed", bind, got)
			continue
		}
		host := strings.TrimSuffix(strings.TrimPrefix(got, "http://"), ":8085")
		if h, err := os.Hostname(); err == nil && h != "" {
			if want := strings.TrimSuffix(h, ".local") + ".local"; host != want {
				t.Errorf("advertiseURL(%q) host = %q, want %q", bind, host, want)
			}
		} else if host != "localhost" {
			t.Errorf("advertiseURL(%q) host = %q, want localhost fallback", bind, host)
		}
	}
}
