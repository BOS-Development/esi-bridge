// Package discovery advertises the running proxy on the local network via
// mDNS / DNS-SD (the _esi-proxy._tcp service) so external applications can
// find it without the user hand-configuring a host:port.
package discovery

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/grandcat/zeroconf"
)

// ServiceType is the DNS-SD service type external applications browse for.
const ServiceType = "_esi-proxy._tcp"

// Advertise registers the proxy as an mDNS service and returns a shutdown
// func that deregisters it. baseURL is the URL clients should actually
// connect to; it is published in a TXT record because when the server binds
// localhost only, the address in the mDNS A record is not reachable and the
// TXT value is the authoritative connect URL.
//
// Advertising is best-effort: on a host without working multicast this
// returns an error, which the caller logs rather than treating as fatal —
// the proxy still serves requests, it just isn't auto-discoverable.
func Advertise(log *slog.Logger, port int, baseURL string) (func(), error) {
	instance := "ESI Proxy"
	if host, err := os.Hostname(); err == nil && host != "" {
		instance = fmt.Sprintf("ESI Proxy (%s)", host)
	}

	txt := []string{
		"base_url=" + baseURL,
		"path=/",
	}

	server, err := zeroconf.Register(instance, ServiceType, "local.", port, txt, nil)
	if err != nil {
		return nil, fmt.Errorf("register mdns service: %w", err)
	}

	log.Info("mdns advertising",
		"service", ServiceType, "instance", instance, "base_url", baseURL)

	return server.Shutdown, nil
}
