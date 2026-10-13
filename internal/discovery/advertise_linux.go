//go:build linux

package discovery

import (
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/godbus/dbus/v5"
)

// Avahi's AVAHI_IF_UNSPEC / AVAHI_PROTO_UNSPEC sentinels: publish on every
// interface and address family.
const (
	avahiIfaceUnspec = int32(-1)
	avahiProtoUnspec = int32(-1)
)

// Advertise registers the service with the Avahi daemon over the system
// D-Bus, so it's published by the host's own mDNS responder and visible to
// avahi-browse and to other devices. The shutdown func frees the entry group
// and closes the bus connection, withdrawing the service. ip is unused: Avahi
// publishes this host's own address on each interface it answers on.
func Advertise(log *slog.Logger, port int, baseURL string, ip net.IP) (func(), error) {
	conn, err := dbus.SystemBus()
	if err != nil {
		return nil, fmt.Errorf("connect to system d-bus: %w", err)
	}

	server := conn.Object("org.freedesktop.Avahi", dbus.ObjectPath("/"))

	var groupPath dbus.ObjectPath
	if err := server.Call("org.freedesktop.Avahi.Server.EntryGroupNew", 0).Store(&groupPath); err != nil {
		conn.Close()
		return nil, fmt.Errorf("avahi EntryGroupNew (is avahi-daemon running?): %w", err)
	}
	group := conn.Object("org.freedesktop.Avahi", groupPath)

	// Avahi takes TXT as a list of raw "key=value" byte slices.
	var txt [][]byte
	for _, e := range txtRecords(baseURL) {
		txt = append(txt, []byte(e))
	}

	instance := instanceName()
	if call := group.Call("org.freedesktop.Avahi.EntryGroup.AddService", 0,
		avahiIfaceUnspec, avahiProtoUnspec, uint32(0),
		instance, ServiceType, "", "", uint16(port), txt); call.Err != nil {
		conn.Close()
		return nil, fmt.Errorf("avahi AddService: %w", call.Err)
	}
	if call := group.Call("org.freedesktop.Avahi.EntryGroup.Commit", 0); call.Err != nil {
		conn.Close()
		return nil, fmt.Errorf("avahi Commit: %w", call.Err)
	}

	log.Info("mdns advertising (system responder)",
		"service", ServiceType, "instance", instance, "base_url", baseURL)

	var once sync.Once
	return func() {
		once.Do(func() {
			group.Call("org.freedesktop.Avahi.EntryGroup.Free", 0)
			conn.Close()
		})
	}, nil
}
