package discovery

import (
	"io"
	"log/slog"
	"net"
	"os"
	"slices"
	"strings"
	"testing"
)

// TestAdvertiseRegistersAndShutsDown exercises the happy path against the
// host's system responder, both leaving the A record to the responder (nil
// ip) and supplying an address. That responder (Bonjour, Avahi, or the
// Windows DNS-SD API) may be missing in sandboxed/CI environments; since
// advertising is best-effort by design, a registration failure skips rather
// than fails the test.
func TestAdvertiseRegistersAndShutsDown(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	for _, tc := range []struct {
		name    string
		baseURL string
		ip      net.IP
	}{
		{"responder address", "http://localhost:8085", nil},
		{"explicit address", "http://192.168.1.20:8085", net.ParseIP("192.168.1.20")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stop, err := Advertise(log, 8085, tc.baseURL, tc.ip)
			if err != nil {
				t.Skipf("mDNS unavailable in this environment (best-effort feature): %v", err)
			}
			if stop == nil {
				t.Fatal("Advertise returned a nil shutdown func")
			}
			stop()
			stop() // shutdown must be idempotent
		})
	}
}

// TestAdvertiseCanReregister checks the withdraw-then-register cycle the
// proxy performs when LAN access is toggled: the second registration of the
// same instance name must succeed once the first has been withdrawn.
func TestAdvertiseCanReregister(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	stop, err := Advertise(log, 8085, "http://localhost:8085", nil)
	if err != nil {
		t.Skipf("mDNS unavailable in this environment (best-effort feature): %v", err)
	}
	stop()

	stop, err = Advertise(log, 8085, "http://192.168.1.20:8085", net.ParseIP("192.168.1.20"))
	if err != nil {
		t.Fatalf("re-register after withdrawal: %v", err)
	}
	stop()
}

func TestShortHost(t *testing.T) {
	tests := []struct{ in, want string }{
		{"studio", "studio"},
		{"studio.local", "studio"},
		{"machine.tailnet.ts.net", "machine"},
		{".leading", ".leading"}, // no label before the dot: left as-is
	}
	for _, tt := range tests {
		if got := shortHost(tt.in); got != tt.want {
			t.Errorf("shortHost(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestInstanceName(t *testing.T) {
	got := instanceName()
	h, err := os.Hostname()
	if err != nil || h == "" {
		if got != "ESI Proxy" {
			t.Errorf("instanceName() = %q, want fallback %q", got, "ESI Proxy")
		}
		return
	}
	if want := "ESI Proxy (" + shortHost(h) + ")"; got != want {
		t.Errorf("instanceName() = %q, want %q", got, want)
	}
}

func TestTXTRecords(t *testing.T) {
	got := txtRecords("http://192.168.1.20:8085")
	want := []string{"base_url=http://192.168.1.20:8085", "path=/"}
	if !slices.Equal(got, want) {
		t.Errorf("txtRecords = %q, want %q", got, want)
	}
}

func TestLANIP(t *testing.T) {
	ip := LANIP()
	if ip == nil {
		t.Skip("no outbound IPv4 route in this environment")
	}
	if ip.To4() == nil {
		t.Errorf("LANIP() = %v, want an IPv4 address", ip)
	}
	if ip.IsLoopback() || ip.IsUnspecified() {
		t.Errorf("LANIP() = %v, want a routable interface address", ip)
	}
	if strings.Contains(ip.String(), ":") {
		t.Errorf("LANIP() = %v, want dotted-quad form", ip)
	}
}
