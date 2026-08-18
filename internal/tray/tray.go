// Package tray implements the system tray UI: the proxy's primary surface
// for adding characters and approving/revoking external application access.
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

type Tray struct {
	st   *store.Store
	srv  *server.Server
	log  *slog.Logger
	port int

	// onCocoaReady, if set, is called once at the start of onReady — after
	// the native event loop (and on macOS, NSApplication's own startup) has
	// finished, but before the tray finishes building its menu. Platform
	// hooks that need to run after Cocoa's own bootstrap (e.g. registering
	// an Apple Event handler, which would otherwise be silently overwritten
	// by NSApplication's default one) belong here instead of before Run.
	onCocoaReady func()

	charsHeader     *systray.MenuItem
	pendingHeader   *systray.MenuItem
	grantsHeader    *systray.MenuItem
	permPendHeader  *systray.MenuItem
	permGrantHeader *systray.MenuItem

	// Placeholder leaf items shown when a section is empty. Without one, an
	// empty header has no submenu attached at all and clicking it does
	// nothing (no dropdown, no indication anything is there to check).
	charsPlaceholder     *systray.MenuItem
	pendingPlaceholder   *systray.MenuItem
	grantsPlaceholder    *systray.MenuItem
	permPendPlaceholder  *systray.MenuItem
	permGrantPlaceholder *systray.MenuItem

	charItems     map[int64]*systray.MenuItem
	pendingItems  map[int64]trackedItem
	grantItems    map[int64]trackedItem
	permPendItems map[int64]trackedItem

	// App Permissions is grouped one entry per application with a sub-entry
	// per permission, so it needs two tracking levels instead of one flat
	// map: permGrantAppItems is the per-application parent entry, keyed by
	// application_id; permGrantItems is each permission's own entry (child
	// of the app's parent), keyed by the app_permissions row ID.
	permGrantAppItems map[int64]*systray.MenuItem
	permGrantItems    map[int64]permGrantChild
}

type trackedItem struct {
	item      *systray.MenuItem
	signature string
}

type permGrantChild struct {
	item          *systray.MenuItem
	applicationID int64
}

func New(st *store.Store, srv *server.Server, log *slog.Logger, port int, onCocoaReady func()) *Tray {
	return &Tray{
		st:                st,
		srv:               srv,
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
	systray.SetTooltip(fmt.Sprintf("ESI Proxy — running on localhost:%d", t.port))

	status := systray.AddMenuItem(fmt.Sprintf("Running on localhost:%d", t.port), "")
	status.Disable()
	systray.AddSeparator()

	setClientID := systray.AddMenuItem("Set EVE Client ID", "Configure the EVE SSO application this proxy uses")
	go func() {
		for range setClientID.ClickedCh {
			t.onSetClientID()
		}
	}()

	addChar := systray.AddMenuItem("Add Character", "Log in a new character via EVE SSO")
	go func() {
		for range addChar.ClickedCh {
			t.onAddCharacter()
		}
	}()

	// These three are section headers with their own submenus, so they must
	// stay enabled: on macOS a disabled NSMenuItem cannot open its submenu
	// at all, which would lock the section shut rather than just graying
	// out the header label. They have no ClickedCh listener, so clicking
	// the header itself is simply a no-op.
	t.charsHeader = systray.AddMenuItem("Characters", "")
	t.charsPlaceholder = t.charsHeader.AddSubMenuItem("No characters added yet", "")
	t.charsPlaceholder.Disable()
	systray.AddSeparator()

	t.pendingHeader = systray.AddMenuItem("Pending Approvals", "")
	t.pendingPlaceholder = t.pendingHeader.AddSubMenuItem("No pending requests", "")
	t.pendingPlaceholder.Disable()
	systray.AddSeparator()

	t.grantsHeader = systray.AddMenuItem("Active Grants", "")
	t.grantsPlaceholder = t.grantsHeader.AddSubMenuItem("No active grants", "")
	t.grantsPlaceholder.Disable()
	systray.AddSeparator()

	t.permPendHeader = systray.AddMenuItem("Permission Requests", "")
	t.permPendPlaceholder = t.permPendHeader.AddSubMenuItem("No pending requests", "")
	t.permPendPlaceholder.Disable()
	systray.AddSeparator()

	t.permGrantHeader = systray.AddMenuItem("App Permissions", "")
	t.permGrantPlaceholder = t.permGrantHeader.AddSubMenuItem("No app permissions granted", "")
	t.permGrantPlaceholder.Disable()
	systray.AddSeparator()

	quit := systray.AddMenuItem("Quit", "Stop esi-proxy")
	go func() {
		<-quit.ClickedCh
		systray.Quit()
	}()

	t.refresh()

	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			systray.Quit()
			return
		case <-ticker.C:
			t.refresh()
		}
	}
}

func (t *Tray) onSetClientID() {
	url := fmt.Sprintf("http://localhost:%d/setup/client-id", t.port)
	if err := openBrowser(url); err != nil {
		t.log.Error("open browser for client id setup", "error", err, "url", url)
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

func setPlaceholderVisible(item *systray.MenuItem, visible bool) {
	if visible {
		item.Show()
	} else {
		item.Hide()
	}
}

func describeScopes(scopes []string) string {
	if len(scopes) == 0 {
		return "no scopes specified"
	}
	return strings.Join(scopes, ", ")
}
