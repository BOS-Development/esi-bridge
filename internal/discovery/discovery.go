// Package discovery advertises the proxy on the local network using the host
// OS's own mDNS/DNS-SD responder — Bonjour (mDNSResponder) on macOS, Avahi on
// Linux, and the DNS-SD API on Windows. Registering with the system responder
// is what makes the service visible to the OS's own tooling (e.g. dns-sd) and
// to other devices on the network; an in-process, second responder is not.
// Per-OS registration lives in advertise_<goos>.go; Advertise has the same
// signature on every platform: ip, if non-nil, is the address the service
// should resolve to on responders that need one supplied (Windows).
package discovery

import (
	"net"
	"os"
	"strings"
)

// ServiceType is the DNS-SD service type external applications browse for.
const ServiceType = "_esi-proxy._tcp"

// instanceName is the human-readable DNS-SD instance name, e.g.
// "ESI Proxy (studio)".
func instanceName() string {
	if host, err := os.Hostname(); err == nil && host != "" {
		return "ESI Proxy (" + shortHost(host) + ")"
	}
	return "ESI Proxy"
}

// shortHost trims a FQDN down to its first label, so a long name (such as a
// Tailscale "machine.tailnet.ts.net") doesn't bloat the advertised instance
// name.
func shortHost(h string) string {
	if i := strings.IndexByte(h, '.'); i > 0 {
		return h[:i]
	}
	return h
}

// txtRecords is the fixed set of DNS-SD TXT entries the proxy advertises.
// base_url is the authoritative URL clients should connect to (the SRV/A
// records may point at an address that isn't reachable under a localhost
// bind), and path is the base request path.
func txtRecords(baseURL string) []string {
	return []string{"base_url=" + baseURL, "path=/"}
}

// LANIP returns this machine's primary outbound IPv4 address by inspecting
// the route to a public address. No packets are sent — a UDP "connection"
// only resolves the local interface. Returns nil if there's no route (e.g.
// the machine is offline).
func LANIP() net.IP {
	conn, err := net.Dial("udp4", "8.8.8.8:80")
	if err != nil {
		return nil
	}
	defer conn.Close()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && !addr.IP.IsLoopback() {
		return addr.IP.To4()
	}
	return nil
}
