package main

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestConnectTargetLocalhostIsVerbatim(t *testing.T) {
	// Bound to localhost: clients must connect to localhost, since the mDNS
	// A record's LAN address would not reach a localhost-only listener.
	url, ip := connectTarget("localhost", 8085, net.ParseIP("192.168.1.20"))
	if want := "http://localhost:8085"; url != want {
		t.Fatalf("url = %q, want %q", url, want)
	}
	if ip != nil {
		t.Fatalf("ip = %v, want nil", ip)
	}
}

func TestConnectTargetSpecificHostIsVerbatim(t *testing.T) {
	url, ip := connectTarget("192.168.1.50", 9000, nil)
	if want := "http://192.168.1.50:9000"; url != want {
		t.Fatalf("url = %q, want %q", url, want)
	}
	if !ip.Equal(net.ParseIP("192.168.1.50")) {
		t.Fatalf("ip = %v, want 192.168.1.50", ip)
	}
}

func TestConnectTargetWildcardUsesLANIP(t *testing.T) {
	lan := net.ParseIP("192.168.1.20")
	for _, bind := range []string{"", "0.0.0.0", "::", "[::]"} {
		url, ip := connectTarget(bind, 8085, lan)
		if want := "http://192.168.1.20:8085"; url != want {
			t.Errorf("connectTarget(%q) url = %q, want %q", bind, url, want)
		}
		if !ip.Equal(lan) {
			t.Errorf("connectTarget(%q) ip = %v, want %v", bind, ip, lan)
		}
	}
}

func TestConnectTargetWildcardWithoutLANIPUsesMDNSHostname(t *testing.T) {
	url, ip := connectTarget("0.0.0.0", 8085, nil)
	host := strings.TrimSuffix(strings.TrimPrefix(url, "http://"), ":8085")
	if h, err := os.Hostname(); err == nil && h != "" {
		if want := strings.TrimSuffix(h, ".local") + ".local"; host != want {
			t.Errorf("host = %q, want %q", host, want)
		}
	} else if host != "localhost" {
		t.Errorf("host = %q, want localhost fallback", host)
	}
	if ip != nil {
		t.Errorf("ip = %v, want nil", ip)
	}
}

type fakeAdvert struct {
	url     string
	ip      net.IP
	active  int // registrations not yet withdrawn
	adverts int
}

func newTestNetwork(t *testing.T, lanIP *net.IP, persisted *string) (*network, *fakeAdvert) {
	t.Helper()
	// Reserve a free port, then release it for the network to bind.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	fa := &fakeAdvert{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	n := newNetwork(slog.New(slog.NewTextHandler(io.Discard, nil)), port, handler,
		func(h string) error { *persisted = h; return nil })
	n.lanIP = func() net.IP { return *lanIP }
	n.advertise = func(_ *slog.Logger, _ int, baseURL string, ip net.IP) (func(), error) {
		fa.url, fa.ip = baseURL, ip
		fa.active++
		fa.adverts++
		return func() { fa.active-- }, nil
	}
	t.Cleanup(func() { n.Shutdown(t.Context()) })
	return n, fa
}

func get(t *testing.T, port int) {
	t.Helper()
	resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
}

func TestNetworkTogglesLANAccessLive(t *testing.T) {
	lan := net.ParseIP("10.1.2.3")
	var persisted string
	n, fa := newTestNetwork(t, &lan, &persisted)
	port := strconv.Itoa(n.port)

	if err := n.Start(t.Context(), "localhost"); err != nil {
		t.Fatal(err)
	}
	get(t, n.port)
	if want := "http://localhost:" + port; fa.url != want || fa.ip != nil {
		t.Fatalf("advert = %q %v, want %q <nil>", fa.url, fa.ip, want)
	}

	if err := n.SetBindHost("0.0.0.0"); err != nil {
		t.Fatal(err)
	}
	get(t, n.port)
	if want := "http://10.1.2.3:" + port; fa.url != want || !fa.ip.Equal(lan) {
		t.Fatalf("advert = %q %v, want %q %v", fa.url, fa.ip, want, lan)
	}
	if got, want := n.Address(), "10.1.2.3:"+port; got != want {
		t.Fatalf("Address = %q, want %q", got, want)
	}
	if persisted != "0.0.0.0" || n.BindHost() != "0.0.0.0" {
		t.Fatalf("persisted = %q, BindHost = %q, want 0.0.0.0", persisted, n.BindHost())
	}

	if err := n.SetBindHost("localhost"); err != nil {
		t.Fatal(err)
	}
	get(t, n.port)
	if want := "http://localhost:" + port; fa.url != want {
		t.Fatalf("advert = %q, want %q", fa.url, want)
	}
	if fa.active != 1 {
		t.Fatalf("%d active registrations, want 1 (old ones withdrawn)", fa.active)
	}
}

func TestNetworkReadvertisesWhenLANIPChanges(t *testing.T) {
	lan := net.ParseIP("10.1.2.3")
	var persisted string
	n, fa := newTestNetwork(t, &lan, &persisted)

	if err := n.Start(t.Context(), "0.0.0.0"); err != nil {
		t.Fatal(err)
	}
	refresh := func() {
		n.mu.Lock()
		n.refreshAdvertLocked()
		n.mu.Unlock()
	}

	refresh()
	if fa.adverts != 1 {
		t.Fatalf("re-advertised %d times with an unchanged IP, want 1 advert total", fa.adverts)
	}

	lan = net.ParseIP("10.9.9.9")
	refresh()
	if !fa.ip.Equal(lan) || !strings.Contains(fa.url, "10.9.9.9") {
		t.Fatalf("advert = %q %v, want the new IP", fa.url, fa.ip)
	}
	if fa.active != 1 {
		t.Fatalf("%d active registrations, want 1", fa.active)
	}
}

func TestNetworkFailedRebindKeepsPreviousListener(t *testing.T) {
	lan := net.ParseIP("10.1.2.3")
	persisted := "localhost"
	n, fa := newTestNetwork(t, &lan, &persisted)

	if err := n.Start(t.Context(), "localhost"); err != nil {
		t.Fatal(err)
	}
	// 192.0.2.0/24 is TEST-NET-1: never assigned to a local interface.
	if err := n.SetBindHost("192.0.2.1"); err == nil {
		t.Fatal("SetBindHost to an unassigned address succeeded, want error")
	}
	get(t, n.port)
	if n.BindHost() != "localhost" || persisted != "localhost" {
		t.Fatalf("BindHost = %q, persisted = %q, want localhost", n.BindHost(), persisted)
	}
	if !strings.Contains(fa.url, "localhost") {
		t.Fatalf("advert = %q, want the localhost URL kept", fa.url)
	}
}
