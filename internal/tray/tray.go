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

	"github.com/BOS-Development/esi-proxy/internal/browser"
	"github.com/BOS-Development/esi-proxy/internal/notify"
	"github.com/BOS-Development/esi-proxy/internal/server"
	"github.com/BOS-Development/esi-proxy/internal/store"
	"github.com/BOS-Development/esi-proxy/internal/update"
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

	// updater, if set, backs the update menu item, which stays hidden until a
	// newer release is found. shownUpdate records what the item currently
	// shows, so the refresh only touches it on a change.
	updater     *update.Updater
	updateItem  *systray.MenuItem
	shownUpdate string

	// onCocoaReady, if set, is called once at the start of onReady — after
	// the native event loop (and on macOS, NSApplication's own startup) has
	// finished, but before the tray finishes building its menu. Platform
	// hooks that need to run after Cocoa's own bootstrap (e.g. registering
	// an Apple Event handler, which would otherwise be silently overwritten
	// by NSApplication's default one) belong here instead of before Run.
	onCocoaReady func()

	// onOpenWindow, if set, backs the "Open ESI Bridge" menu item: it opens
	// (or re-opens) the native management window. It may block briefly, so
	// clicks invoke it on their own goroutine.
	onOpenWindow func()

	// grantBatch / permBatch track which pending request IDs have already
	// been surfaced as a desktop notification, and debounce new arrivals so a
	// burst (e.g. an app requesting many characters at once) yields a single
	// notification. The tray no longer lists these inline — the window's
	// Requests view owns approve/deny — so this is the only signal a request
	// arrived while the window was closed.
	grantBatch *notifyBatcher
	permBatch  *notifyBatcher
}

func New(st *store.Store, srv *server.Server, network Network, updater *update.Updater, log *slog.Logger, port int, onCocoaReady, onOpenWindow func()) *Tray {
	return &Tray{
		updater:      updater,
		st:           st,
		srv:          srv,
		network:      network,
		log:          log,
		port:         port,
		onCocoaReady: onCocoaReady,
		onOpenWindow: onOpenWindow,
		grantBatch:   newNotifyBatcher(notifyQuiet, notifyMaxWait),
		permBatch:    newNotifyBatcher(notifyQuiet, notifyMaxWait),
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
	systray.SetTitle("ESI Bridge")

	t.status = systray.AddMenuItem("", "")
	t.status.Disable()
	t.updateAddressDisplay()
	systray.AddSeparator()

	// "Open ESI Bridge" re-opens the native management window (the app's main
	// surface) after it's been closed; the tray otherwise just lingers in the
	// background. Only shown when a launcher was wired in.
	if t.onOpenWindow != nil {
		openWindow := systray.AddMenuItem("Open ESI Bridge", "Open the ESI Bridge window")
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

	if t.updater != nil {
		t.updateItem = systray.AddMenuItem("", "")
		t.updateItem.Hide()
		go func() {
			for range t.updateItem.ClickedCh {
				t.onUpdateClicked()
			}
		}()
		t.updateUpdateItem()
	}

	quit := systray.AddMenuItem("Quit", "Stop ESI Bridge")
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
			t.updateUpdateItem()
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

	byID := make(map[int64]store.PendingGrant, len(pending))
	ids := make([]int64, len(pending))
	for i, p := range pending {
		ids[i] = p.ID
		byID[p.ID] = p
	}
	batch := t.grantBatch.observe(ids, time.Now())
	if len(batch) == 0 {
		return
	}
	reqs := make([]store.PendingGrant, len(batch))
	for i, id := range batch {
		reqs[i] = byID[id]
	}
	notify.Send(t.log, "ESI Bridge: access request", describeGrants(reqs))
}

// describeGrants summarizes one batch of new access requests for a single
// notification.
func describeGrants(reqs []store.PendingGrant) string {
	if len(reqs) == 1 {
		return fmt.Sprintf("%s wants access to %s", reqs[0].ApplicationName, reqs[0].CharacterName)
	}
	if app, ok := sameApp(reqs, func(r store.PendingGrant) string { return r.ApplicationName }); ok {
		return fmt.Sprintf("%s wants access to %d characters", app, len(reqs))
	}
	return fmt.Sprintf("%d new access requests are waiting", len(reqs))
}

func (t *Tray) notifyNewPermissions() {
	pending, err := t.st.ListPendingAppPermissions()
	if err != nil {
		t.log.Error("list pending app permissions", "error", err)
		return
	}

	byID := make(map[int64]store.PendingAppPermission, len(pending))
	ids := make([]int64, len(pending))
	for i, p := range pending {
		ids[i] = p.ID
		byID[p.ID] = p
	}
	batch := t.permBatch.observe(ids, time.Now())
	if len(batch) == 0 {
		return
	}
	reqs := make([]store.PendingAppPermission, len(batch))
	for i, id := range batch {
		reqs[i] = byID[id]
	}
	notify.Send(t.log, "ESI Bridge: permission request", describePermissions(reqs))
}

// describePermissions summarizes one batch of new permission requests for a
// single notification.
func describePermissions(reqs []store.PendingAppPermission) string {
	if len(reqs) == 1 {
		return fmt.Sprintf("%s wants to %s", reqs[0].ApplicationName, describePermission(reqs[0].Permission))
	}
	if app, ok := sameApp(reqs, func(r store.PendingAppPermission) string { return r.ApplicationName }); ok {
		return fmt.Sprintf("%s is requesting %d permissions", app, len(reqs))
	}
	return fmt.Sprintf("%d new permission requests are waiting", len(reqs))
}

// sameApp reports the application name shared by every request, if there is
// one.
func sameApp[T any](reqs []T, name func(T) string) (string, bool) {
	app := name(reqs[0])
	for _, r := range reqs[1:] {
		if name(r) != app {
			return "", false
		}
	}
	return app, true
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
	if err := browser.Open(url); err != nil {
		t.log.Error("open browser for login", "error", err, "url", url)
	}
}

// updateUpdateItem shows the update menu item while a newer release is
// available (or being installed), and hides it otherwise.
func (t *Tray) updateUpdateItem() {
	if t.updateItem == nil {
		return
	}
	st := t.updater.Status()
	var title, tip string
	enabled := true
	switch {
	case st.LatestVersion == "":
	case st.State == update.StateDownloading || st.State == update.StateInstalling:
		title = "Installing update (" + st.LatestVersion + ")…"
		enabled = false
	case st.CanInstall:
		title = "Install update (" + st.LatestVersion + ")"
		tip = "Download and install ESI Bridge " + st.LatestVersion + ", then restart"
	default:
		title = "Update available (" + st.LatestVersion + ")"
		tip = "Open the release page to download ESI Bridge " + st.LatestVersion
	}
	key := fmt.Sprintf("%s|%t", title, enabled)
	if key == t.shownUpdate {
		return
	}
	t.shownUpdate = key
	if title == "" {
		t.updateItem.Hide()
		return
	}
	t.updateItem.SetTitle(title)
	t.updateItem.SetTooltip(tip)
	if enabled {
		t.updateItem.Enable()
	} else {
		t.updateItem.Disable()
	}
	t.updateItem.Show()
}

// onUpdateClicked installs the available update, or opens its release page
// when this copy can't update itself.
func (t *Tray) onUpdateClicked() {
	rel := t.updater.Available()
	if rel == nil {
		return
	}
	if !t.updater.Status().CanInstall {
		if err := browser.Open(rel.URL); err != nil {
			t.log.Error("open release page", "error", err)
		}
		return
	}
	if err := t.updater.Install(); err != nil {
		t.log.Error("install update", "error", err)
		notify.Send(t.log, "ESI Bridge update failed", err.Error())
	}
	// The menu item catches up on the next refresh tick.
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
	systray.SetTooltip("ESI Bridge — running on " + addr)
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
