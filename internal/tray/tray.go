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
	"strings"
	"time"

	"fyne.io/systray"

	"github.com/BOS-Development/esi-proxy/internal/notify"
	"github.com/BOS-Development/esi-proxy/internal/server"
	"github.com/BOS-Development/esi-proxy/internal/store"
)

const refreshInterval = 3 * time.Second

// Network is the live HTTP listener and mDNS advert, which the LAN access
// toggle rebinds without a restart.
type Network interface {
	// BindHost is the host the listener is bound to right now.
	BindHost() string
	// SetBindHost rebinds the listener and re-advertises; on error the
	// previous bind is still in effect.
	SetBindHost(host string) error
	// Address is the host:port other applications should connect to. It
	// can change on its own, e.g. when the machine's LAN IP does.
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

type trackedItem struct {
	item      *systray.MenuItem
	signature string
}

type permGrantChild struct {
	item          *systray.MenuItem
	applicationID int64
}

func New(st *store.Store, srv *server.Server, network Network, log *slog.Logger, port int, onCocoaReady func()) *Tray {
	return &Tray{
		st:                st,
		srv:               srv,
		network:           network,
		log:               log,
		port:              port,
		onCocoaReady:      onCocoaReady,
		charItems:         make(map[int64]*systray.MenuItem),
		pendingItems:      make(map[int64]trackedItem),
		grantItems:        make(map[int64]trackedItem),
		permPendItems:     make(map[int64]trackedItem),
		permGrantAppItems: make(map[int64]*systray.MenuItem),
		permGrantItems:    make(map[int64]permGrantChild),
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

	setClientID := systray.AddMenuItem("Set EVE Client ID", "Configure the EVE SSO application this proxy uses")
	go func() {
		for range setClientID.ClickedCh {
			t.onSetClientID()
		}
	}()

	lanAccess := systray.AddMenuItemCheckbox("Allow LAN Access",
		"Let other machines on your network connect to this proxy", isLANBind(t.network.BindHost()))
	go func() {
		for range lanAccess.ClickedCh {
			t.onToggleLANAccess(lanAccess)
		}
	}()

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

// onToggleLANAccess flips the bind host between localhost-only and
// all-interfaces. The listener is rebound and the mDNS advert updated
// immediately, and the choice is saved for future runs.
func (t *Tray) onToggleLANAccess(item *systray.MenuItem) {
	enable := !item.Checked()

	host := "localhost"
	if enable {
		host = "0.0.0.0"
	}
	if err := t.network.SetBindHost(host); err != nil {
		t.log.Error("set bind host", "error", err)
		notify.Send(t.log, "esi-proxy: couldn't change LAN access", err.Error())
		return
	}

	t.updateAddressDisplay()
	if enable {
		item.Check()
		notify.Send(t.log, "esi-proxy: LAN access enabled",
			"Other machines on your network can now connect at "+t.shownAddr+".")
	} else {
		item.Uncheck()
		notify.Send(t.log, "esi-proxy: LAN access disabled",
			"The proxy is now reachable from this machine only.")
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

// isLANBind reports whether host exposes the proxy beyond this machine.
// Loopback names bind same-machine only; anything else (0.0.0.0, a LAN IP)
// is reachable from the network.
func isLANBind(host string) bool {
	switch host {
	case "", "localhost", "127.0.0.1", "::1", "[::1]":
		return false
	default:
		return true
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

func (t *Tray) refresh() {
	t.updateAddressDisplay()
	t.refreshCharacters()
	t.refreshPending()
	t.refreshGrants()
	t.refreshPendingPermissions()
	t.refreshAppPermissions()
}

func (t *Tray) refreshCharacters() {
	chars, err := t.st.ListCharacters()
	if err != nil {
		t.log.Error("list characters", "error", err)
		return
	}

	seen := make(map[int64]bool, len(chars))
	for _, c := range chars {
		seen[c.ID] = true
		if _, ok := t.charItems[c.ID]; ok {
			continue
		}
		item := t.charsHeader.AddSubMenuItem(c.Name, "")
		item.Disable()
		t.charItems[c.ID] = item
	}
	for id, item := range t.charItems {
		if !seen[id] {
			item.Remove()
			delete(t.charItems, id)
		}
	}
	setPlaceholderVisible(t.charsPlaceholder, len(t.charItems) == 0)
}

func (t *Tray) refreshPending() {
	pending, err := t.st.ListPendingGrants()
	if err != nil {
		t.log.Error("list pending grants", "error", err)
		return
	}

	seen := make(map[int64]bool, len(pending))
	for _, p := range pending {
		seen[p.ID] = true
		sig := fmt.Sprintf("%s|%s", p.ApplicationName, strings.Join(p.RequestedScopes, ","))

		existing, ok := t.pendingItems[p.ID]
		if ok && existing.signature == sig {
			continue
		}
		if ok {
			existing.item.Remove()
		}

		title := fmt.Sprintf("%s wants access to %s", p.ApplicationName, p.CharacterName)
		notify.Send(t.log, "esi-proxy: access request", title)
		item := t.pendingHeader.AddSubMenuItem(title, describeScopes(p.RequestedScopes))
		approve := item.AddSubMenuItem("Approve", "")
		deny := item.AddSubMenuItem("Deny", "")

		id := p.ID
		go func() {
			for range approve.ClickedCh {
				if err := t.st.ApprovePendingGrant(id); err != nil {
					t.log.Error("approve pending grant", "id", id, "error", err)
					continue
				}
				t.log.Info("access approved", "pending_grant_id", id)
			}
		}()
		go func() {
			for range deny.ClickedCh {
				if err := t.st.DenyPendingGrant(id); err != nil {
					t.log.Error("deny pending grant", "id", id, "error", err)
					continue
				}
				t.log.Info("access denied", "pending_grant_id", id)
			}
		}()

		t.pendingItems[p.ID] = trackedItem{item: item, signature: sig}
	}
	for id, tracked := range t.pendingItems {
		if !seen[id] {
			tracked.item.Remove()
			delete(t.pendingItems, id)
		}
	}
	setPlaceholderVisible(t.pendingPlaceholder, len(t.pendingItems) == 0)
}

func (t *Tray) refreshGrants() {
	grants, err := t.st.ListActiveGrantsView()
	if err != nil {
		t.log.Error("list active grants", "error", err)
		return
	}

	seen := make(map[int64]bool, len(grants))
	for _, g := range grants {
		seen[g.ID] = true
		sig := strings.Join(g.Scopes, ",")

		existing, ok := t.grantItems[g.ID]
		if ok && existing.signature == sig {
			continue
		}
		if ok {
			existing.item.Remove()
		}

		title := fmt.Sprintf("%s → %s", g.ApplicationName, g.CharacterName)
		item := t.grantsHeader.AddSubMenuItem(title, describeScopes(g.Scopes))
		revoke := item.AddSubMenuItem("Revoke", "")

		id := g.ID
		go func() {
			for range revoke.ClickedCh {
				if err := t.st.RevokeGrant(id); err != nil {
					t.log.Error("revoke grant", "id", id, "error", err)
					continue
				}
				t.log.Info("grant revoked", "grant_id", id)
			}
		}()

		t.grantItems[g.ID] = trackedItem{item: item, signature: sig}
	}
	for id, tracked := range t.grantItems {
		if !seen[id] {
			tracked.item.Remove()
			delete(t.grantItems, id)
		}
	}
	setPlaceholderVisible(t.grantsPlaceholder, len(t.grantItems) == 0)
}

func (t *Tray) refreshPendingPermissions() {
	pending, err := t.st.ListPendingAppPermissions()
	if err != nil {
		t.log.Error("list pending app permissions", "error", err)
		return
	}

	seen := make(map[int64]bool, len(pending))
	for _, p := range pending {
		seen[p.ID] = true
		if _, ok := t.permPendItems[p.ID]; ok {
			continue // permission string never changes for a given pending row
		}

		title := fmt.Sprintf("%s wants to %s", p.ApplicationName, describePermission(p.Permission))
		notify.Send(t.log, "esi-proxy: permission request", title)
		item := t.permPendHeader.AddSubMenuItem(title, "")
		approve := item.AddSubMenuItem("Approve", "")
		deny := item.AddSubMenuItem("Deny", "")

		id := p.ID
		go func() {
			for range approve.ClickedCh {
				if err := t.st.ApprovePendingAppPermission(id); err != nil {
					t.log.Error("approve pending app permission", "id", id, "error", err)
					continue
				}
				t.log.Info("app permission approved", "pending_permission_id", id)
			}
		}()
		go func() {
			for range deny.ClickedCh {
				if err := t.st.DenyPendingAppPermission(id); err != nil {
					t.log.Error("deny pending app permission", "id", id, "error", err)
					continue
				}
				t.log.Info("app permission denied", "pending_permission_id", id)
			}
		}()

		t.permPendItems[p.ID] = trackedItem{item: item}
	}
	for id, tracked := range t.permPendItems {
		if !seen[id] {
			tracked.item.Remove()
			delete(t.permPendItems, id)
		}
	}
	setPlaceholderVisible(t.permPendPlaceholder, len(t.permPendItems) == 0)
}

// refreshAppPermissions renders one entry per application under "App
// Permissions", each with a sub-entry (and its own Revoke) per permission
// that application currently holds.
func (t *Tray) refreshAppPermissions() {
	granted, err := t.st.ListActiveAppPermissionsView()
	if err != nil {
		t.log.Error("list app permissions", "error", err)
		return
	}

	appNames := make(map[int64]string)
	seenPerms := make(map[int64]bool, len(granted))
	for _, g := range granted {
		seenPerms[g.ID] = true
		appNames[g.ApplicationID] = g.ApplicationName

		appItem, ok := t.permGrantAppItems[g.ApplicationID]
		if !ok {
			appItem = t.permGrantHeader.AddSubMenuItem(g.ApplicationName, "")
			t.permGrantAppItems[g.ApplicationID] = appItem
		}

		if _, ok := t.permGrantItems[g.ID]; ok {
			continue
		}

		permItem := appItem.AddSubMenuItem(describePermission(g.Permission), "")
		revoke := permItem.AddSubMenuItem("Revoke", "")

		id := g.ID
		go func() {
			for range revoke.ClickedCh {
				if err := t.st.RevokeAppPermission(id); err != nil {
					t.log.Error("revoke app permission", "id", id, "error", err)
					continue
				}
				t.log.Info("app permission revoked", "app_permission_id", id)
			}
		}()

		t.permGrantItems[g.ID] = permGrantChild{item: permItem, applicationID: g.ApplicationID}
	}

	// Drop permission entries that were revoked. Their parent app entry, if
	// now childless, is removed in the pass below (removing it here would
	// also be safe on macOS, which cascades a submenu's children, but the
	// two-pass approach keeps the Go-side bookkeeping in sync everywhere).
	remainingByApp := make(map[int64]int, len(t.permGrantAppItems))
	for id, child := range t.permGrantItems {
		if !seenPerms[id] {
			child.item.Remove()
			delete(t.permGrantItems, id)
			continue
		}
		remainingByApp[child.applicationID]++
	}

	for appID, appItem := range t.permGrantAppItems {
		if remainingByApp[appID] == 0 {
			appItem.Remove()
			delete(t.permGrantAppItems, appID)
		}
	}

	setPlaceholderVisible(t.permGrantPlaceholder, len(t.permGrantAppItems) == 0)
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
