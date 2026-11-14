// Package tray implements the proxy's background menu-bar/system-tray
// presence. Since the tray→window migration, the primary UI is the native
// window (see internal/window); the tray is deliberately minimal — it keeps
// the app running when the window is closed, offers a quick way to re-open the
// window or add a character, and raises a desktop notification when an
// external application is waiting for approval. All the actual management
// (approvals, grants, permissions, settings) lives in the window's views.
package tray

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"fyne.io/systray"

	"github.com/BOS-Development/esi-proxy/internal/notify"
	"github.com/BOS-Development/esi-proxy/internal/server"
	"github.com/BOS-Development/esi-proxy/internal/store"
)

const refreshInterval = 3 * time.Second

// Network is the live HTTP listener and mDNS advert. The tray only reads
// the address it's reachable at; LAN access is toggled from the window.
type Network interface {
	// Address is the host:port other applications should connect to. It
	// can change on its own, e.g. when the machine's LAN IP does or LAN
	// access is toggled.
	Address() string
}

type Tray struct {
	st      *store.Store
	srv     *server.Server
	network Network
	log     *slog.Logger
	port    int

	// status is the disabled menu item (and tooltip) showing where the proxy
	// is reachable; shownAddr is what it currently says, so the periodic
	// refresh only touches it when the address actually changes.
	status    *systray.MenuItem
	shownAddr string

	// onCocoaReady, if set, is called once at the start of onReady — after
	// the native event loop (and on macOS, NSApplication's own startup) has
	// finished, but before the tray finishes building its menu. Platform
	// hooks that need to run after Cocoa's own bootstrap (e.g. registering
	// an Apple Event handler, which would otherwise be silently overwritten
	// by NSApplication's default one) belong here instead of before Run.
	onCocoaReady func()

	// onOpenWindow, if set, backs the "Open ESI Proxy" menu item: it opens
	// (or re-opens) the native management window. It may block briefly, so
	// clicks invoke it on their own goroutine.
	onOpenWindow func()

	// notifiedGrants / notifiedPerms track the pending request IDs already
	// surfaced as a desktop notification, so the background poll alerts the
	// user once per new request. The tray no longer lists these inline — the
	// window's Requests view owns approve/deny — so this is the only signal a
	// request arrived while the window was closed.
	notifiedGrants map[int64]bool
	notifiedPerms  map[int64]bool
}

func New(st *store.Store, srv *server.Server, network Network, log *slog.Logger, port int, onCocoaReady, onOpenWindow func()) *Tray {
	return &Tray{
		st:             st,
		srv:            srv,
		network:        network,
		log:            log,
		port:           port,
		onCocoaReady:   onCocoaReady,
		onOpenWindow:   onOpenWindow,
		notifiedGrants: make(map[int64]bool),
		notifiedPerms:  make(map[int64]bool),
	}
}

// Run blocks until ctx is canceled or the user chooses Quit. It must be
// called from the process's main goroutine.
func (t *Tray) Run(ctx context.Context) {
	systray.Run(func() { t.onReady(ctx) }, func() {})
}

func (t *Tray) onReady(ctx context.Context) {
	if t.onCocoaReady != nil {
		t.onCocoaReady()
	}

	systray.SetIcon(icon())
	systray.SetTitle("ESI Proxy")

	t.status = systray.AddMenuItem("", "")
	t.status.Disable()
	t.updateAddressDisplay()
	systray.AddSeparator()

	// "Open ESI Proxy" re-opens the native management window (the app's main
	// surface) after it's been closed; the tray otherwise just lingers in the
	// background. Only shown when a launcher was wired in.
	if t.onOpenWindow != nil {
		openWindow := systray.AddMenuItem("Open ESI Proxy", "Open the ESI Proxy window")
		go func() {
			for range openWindow.ClickedCh {
				// May block briefly waiting for the server; don't stall the
				// menu's click loop.
				go t.onOpenWindow()
			}
		}()
	}

	// Add Character is kept in the tray as a quick action (it's also in the
	// window's top bar) so a character can be added without first opening the
	// window.
	addChar := systray.AddMenuItem("Add Character", "Log in a new character via EVE SSO")
	go func() {
		for range addChar.ClickedCh {
			t.onAddCharacter()
		}
	}()
	systray.AddSeparator()

	quit := systray.AddMenuItem("Quit", "Stop esi-proxy")
	go func() {
		<-quit.ClickedCh
		systray.Quit()
	}()

	t.pollRequests()

	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			systray.Quit()
			return
		case <-ticker.C:
			t.updateAddressDisplay()
			t.pollRequests()
		}
	}
}

// pollRequests raises a desktop notification for each newly-arrived pending
// access/permission request. The tray no longer shows these inline (the
// window's Requests view owns approve/deny); this just lets the user know
// something is waiting while the window may be closed.
func (t *Tray) pollRequests() {
	t.notifyNewGrants()
	t.notifyNewPermissions()
}

func (t *Tray) notifyNewGrants() {
	pending, err := t.st.ListPendingGrants()
	if err != nil {
		t.log.Error("list pending grants", "error", err)
		return
	}

	seen := make(map[int64]bool, len(pending))
	for _, p := range pending {
		seen[p.ID] = true
		if t.notifiedGrants[p.ID] {
			continue
		}
		t.notifiedGrants[p.ID] = true
		notify.Send(t.log, "esi-proxy: access request",
			fmt.Sprintf("%s wants access to %s", p.ApplicationName, p.CharacterName))
	}
	// Forget IDs that are no longer pending (approved/denied), so the map
	// doesn't grow without bound over a long-running session.
	for id := range t.notifiedGrants {
		if !seen[id] {
			delete(t.notifiedGrants, id)
		}
	}
}

func (t *Tray) notifyNewPermissions() {
	pending, err := t.st.ListPendingAppPermissions()
	if err != nil {
		t.log.Error("list pending app permissions", "error", err)
		return
	}

	seen := make(map[int64]bool, len(pending))
	for _, p := range pending {
		seen[p.ID] = true
		if t.notifiedPerms[p.ID] {
			continue
		}
		t.notifiedPerms[p.ID] = true
		notify.Send(t.log, "esi-proxy: permission request",
			fmt.Sprintf("%s wants to %s", p.ApplicationName, describePermission(p.Permission)))
	}
	for id := range t.notifiedPerms {
		if !seen[id] {
			delete(t.notifiedPerms, id)
		}
	}
}

func (t *Tray) onAddCharacter() {
	account, err := t.st.GetOrCreateDefaultAccount()
	if err != nil {
		t.log.Error("get default account", "error", err)
		return
	}
	url, err := t.srv.BeginLogin(account.ID)
	if err != nil {
		t.log.Error("begin login", "error", err)
		return
	}
	if err := openBrowser(url); err != nil {
		t.log.Error("open browser for login", "error", err, "url", url)
	}
}

// updateAddressDisplay updates the status menu item and tooltip to show
// where the proxy is reachable, if that has changed since last shown.
func (t *Tray) updateAddressDisplay() {
	addr := t.network.Address()
	if addr == "" {
		addr = fmt.Sprintf("localhost:%d", t.port)
	}
	if addr == t.shownAddr {
		return
	}
	t.shownAddr = addr
	t.status.SetTitle("Running on " + addr)
	systray.SetTooltip("ESI Proxy — running on " + addr)
}

func describePermission(permission string) string {
	switch permission {
	case store.PermissionListCharacters:
		return "list all characters"
	case store.PermissionListAccounts:
		return "list accounts"
	default:
		return permission
	}
}
