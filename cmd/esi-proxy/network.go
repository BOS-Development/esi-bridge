package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BOS-Development/esi-proxy/internal/discovery"
)

// lanIPCheckInterval is how often the LAN address is re-checked while the
// proxy is bound to all interfaces, so a DHCP renewal or a switch between
// networks updates the mDNS advert without a restart.
const lanIPCheckInterval = 30 * time.Second

// drainTimeout bounds how long connections accepted on a replaced listener
// may finish in-flight requests before they're cut off. Disabling LAN access
// must actually disconnect LAN peers, not leave keep-alive connections open.
const drainTimeout = 5 * time.Second

type advertiseFunc func(log *slog.Logger, port int, baseURL string, ip net.IP) (func(), error)

// network owns the HTTP listener and the mDNS advert, and can rebind both at
// runtime, so toggling LAN access applies immediately instead of on restart.
type network struct {
	log       *slog.Logger
	port      int
	handler   http.Handler
	persist   func(bindHost string) error // saves the bind host to config.json
	advertise advertiseFunc
	lanIP     func() net.IP

	mu         sync.Mutex
	bindHost   string
	ln         net.Listener
	httpSrv    *http.Server
	stopAdvert func()
	advertURL  string // base_url currently advertised
	advertIP   string // A-record IP currently advertised ("" = responder default)
	connectTo  string // host:port other apps should use, for the tray
}

func newNetwork(log *slog.Logger, port int, handler http.Handler, persist func(string) error) *network {
	return &network{
		log:       log,
		port:      port,
		handler:   handler,
		persist:   persist,
		advertise: discovery.Advertise,
		lanIP:     discovery.LANIP,
	}
}

// Start binds bindHost, starts serving, begins advertising, and keeps the
// advert in step with the LAN address until ctx is canceled. A bind failure
// is returned but leaves the network usable: a later SetBindHost retries.
func (n *network) Start(ctx context.Context, bindHost string) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.bindHost = bindHost
	ln, err := net.Listen("tcp", listenAddr(bindHost, n.port))
	if err == nil {
		n.serve(ln)
	}
	n.refreshAdvertLocked()

	go n.watchLANIP(ctx)
	return err
}

// SetBindHost moves the listener to bindHost and re-advertises, then saves
// the new bind host. If the new address can't be bound, the previous listener
// is restored and the error returned, so the proxy is never left unreachable.
func (n *network) SetBindHost(bindHost string) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if bindHost != n.bindHost || n.ln == nil {
		// The old listener has to be closed before binding the new one: on
		// Linux a wildcard bind conflicts with an existing localhost bind on
		// the same port.
		oldHost, oldSrv := n.bindHost, n.httpSrv
		if n.ln != nil {
			n.ln.Close()
			n.ln, n.httpSrv = nil, nil
		}

		ln, err := net.Listen("tcp", listenAddr(bindHost, n.port))
		if err != nil {
			if oldSrv != nil {
				if back, rerr := net.Listen("tcp", listenAddr(oldHost, n.port)); rerr == nil {
					n.serve(back)
				} else {
					n.log.Error("restore previous listener", "addr", listenAddr(oldHost, n.port), "error", rerr)
				}
			}
			n.refreshAdvertLocked()
			return fmt.Errorf("listen on %s: %w", listenAddr(bindHost, n.port), err)
		}
		if oldSrv != nil {
			go drain(oldSrv)
		}
		n.bindHost = bindHost
		n.serve(ln)
	}
	n.refreshAdvertLocked()

	if err := n.persist(bindHost); err != nil {
		// The change is live; it just won't survive a restart.
		n.log.Warn("save bind host; change will not persist across restart", "error", err)
	}
	return nil
}

// BindHost returns the host the listener is bound to right now.
func (n *network) BindHost() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.bindHost
}

// Address returns the host:port other applications should connect to.
func (n *network) Address() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.connectTo
}

// Shutdown withdraws the advert and stops the HTTP server.
func (n *network) Shutdown(ctx context.Context) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.stopAdvert != nil {
		n.stopAdvert()
		n.stopAdvert = nil
	}
	if n.httpSrv != nil {
		_ = n.httpSrv.Shutdown(ctx)
		n.ln, n.httpSrv = nil, nil
	}
}

// serve starts a fresh http.Server on ln. Each bind gets its own server so
// the previous one can drain independently. Callers hold n.mu.
func (n *network) serve(ln net.Listener) {
	srv := &http.Server{Handler: n.handler}
	n.ln, n.httpSrv = ln, srv
	n.log.Info("http server listening", "addr", ln.Addr().String())
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			n.log.Error("http server", "error", err)
		}
	}()
}

// refreshAdvertLocked re-registers the mDNS service if the URL or IP it
// should advertise has changed. Callers hold n.mu.
func (n *network) refreshAdvertLocked() {
	var lanIP net.IP
	if isWildcard(n.bindHost) {
		lanIP = n.lanIP()
	}
	baseURL, ip := connectTarget(n.bindHost, n.port, lanIP)
	n.connectTo = strings.TrimPrefix(baseURL, "http://")

	ipStr := ""
	if ip != nil {
		ipStr = ip.String()
	}
	if n.stopAdvert != nil && baseURL == n.advertURL && ipStr == n.advertIP {
		return
	}

	if n.stopAdvert != nil {
		n.stopAdvert()
		n.stopAdvert = nil
	}
	stop, err := n.advertise(n.log, n.port, baseURL, ip)
	if err != nil {
		// Best-effort: a host without working multicast just isn't
		// auto-discoverable — see internal/discovery.
		n.log.Warn("mdns advertising unavailable", "error", err)
		n.advertURL, n.advertIP = "", ""
		return
	}
	n.stopAdvert, n.advertURL, n.advertIP = stop, baseURL, ipStr
}

func (n *network) watchLANIP(ctx context.Context) {
	ticker := time.NewTicker(lanIPCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.mu.Lock()
			n.refreshAdvertLocked()
			n.mu.Unlock()
		}
	}
}

// drain lets a replaced server finish in-flight requests, then force-closes
// whatever is left.
func drain(srv *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		srv.Close()
	}
}

func listenAddr(bindHost string, port int) string {
	return net.JoinHostPort(strings.Trim(bindHost, "[]"), strconv.Itoa(port))
}

func isWildcard(bindHost string) bool {
	switch bindHost {
	case "", "0.0.0.0", "::", "[::]":
		return true
	}
	return false
}

// connectTarget builds the connect URL published in the mDNS TXT record, and
// the IP to publish in the A record (nil leaves it to the system responder).
// When bound to localhost the proxy is reachable only from this machine, so
// clients must use localhost. When bound to all interfaces we advertise the
// machine's LAN IP, falling back to its mDNS name (hostname.local) if no LAN
// route is found. A specific bind address is advertised as-is.
func connectTarget(bindHost string, port int, lanIP net.IP) (string, net.IP) {
	host := bindHost
	var ip net.IP
	if isWildcard(bindHost) {
		if lanIP != nil {
			host, ip = lanIP.String(), lanIP
		} else {
			host = "localhost"
			if h, err := os.Hostname(); err == nil && h != "" {
				host = strings.TrimSuffix(h, ".local") + ".local"
			}
		}
	} else if p := net.ParseIP(strings.Trim(bindHost, "[]")); p != nil && !p.IsLoopback() {
		host, ip = p.String(), p
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port)), ip
}
