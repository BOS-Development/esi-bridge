package discovery

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestShortHost(t *testing.T) {
	for in, want := range map[string]string{
		"studio":                 "studio",
		"machine.tailnet.ts.net": "machine",
		"host.local":             "host",
		".leading-dot":           ".leading-dot",
		"":                       "",
	} {
		if got := shortHost(in); got != want {
			t.Errorf("shortHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInstanceName(t *testing.T) {
	name := instanceName()
	host, err := os.Hostname()
	if err != nil || host == "" {
		if name != "ESI Bridge" {
			t.Fatalf("instanceName() = %q, want ESI Bridge", name)
		}
		return
	}
	want := "ESI Bridge (" + shortHost(host) + ")"
	if name != want {
		t.Fatalf("instanceName() = %q, want %q", name, want)
	}
	if strings.Contains(name, ".") {
		t.Errorf("instanceName() kept a domain suffix: %q", name)
	}
}

func TestTXTRecords(t *testing.T) {
	got := txtRecords("http://192.168.1.5:8080")
	want := []string{"base_url=http://192.168.1.5:8080", "path=/"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("txtRecords = %v, want %v", got, want)
	}
}

func TestLANIP(t *testing.T) {
	ip := LANIP()
	if ip == nil {
		t.Skip("no outbound IPv4 route on this machine")
	}
	if ip.To4() == nil || ip.IsLoopback() {
		t.Fatalf("LANIP() = %v, want a non-loopback IPv4 address", ip)
	}
}
