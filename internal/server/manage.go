package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BOS-Development/esi-proxy/internal/buildinfo"
	"github.com/BOS-Development/esi-proxy/internal/store"
	"github.com/BOS-Development/esi-proxy/internal/update"
	"github.com/BOS-Development/esi-proxy/internal/webui"
)

// The management UI is the browser/webview-facing replacement for the tray's
// approval menus (the tray→window migration). It carries two guards:
//
//   - requireLocalUI: the request must come from a loopback address. This
//     holds even when the proxy is bound to 0.0.0.0 for LAN access — the
//     token-driving approval surface must never be reachable from the network,
//     or any LAN machine could approve its own access requests.
//   - requireSession: on top of loopback, the caller must present the
//     per-process session token. The token is injected into the /app page and
//     is unreadable cross-origin, so a malicious website in the user's browser
//     can't forge /manage calls (CSRF), and a blind local process can't drive
//     approvals without first scraping the page.

// requireLocalUI rejects any request that doesn't originate from loopback.
func (s *Server) requireLocalUI(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackAddr(r.RemoteAddr) {
			http.Error(w, "forbidden: the management UI is only reachable from this machine", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// requireSession layers the session-token check on top of requireLocalUI.
func (s *Server) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return s.requireLocalUI(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-Proxy-Session")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.sessionToken)) != 1 {
			http.Error(w, "forbidden: missing or invalid session token", http.StatusForbidden)
			return
		}
		next(w, r)
	})
}

// isLoopbackAddr reports whether a RemoteAddr ("host:port") is a loopback IP.
func isLoopbackAddr(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr // no port (unusual); treat the whole thing as the host
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// --- /app page + static assets (loopback-only) ---

// handleAppPage serves the single-page management UI, injecting the session
// token so same-origin UI code can authenticate its /manage calls.
func (s *Server) handleAppPage(w http.ResponseWriter, r *http.Request) {
	html, err := webui.IndexHTML(s.sessionToken)
	if err != nil {
		s.log.Error("render app page", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(html)
}

// handleAppStatic serves the UI's static JS/CSS from the embedded asset FS.
func (s *Server) handleAppStatic(w http.ResponseWriter, r *http.Request) {
	webui.StaticServer().ServeHTTP(w, r)
}

// --- /manage JSON API (loopback + session token) ---

type manageCharacter struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	AccountID   int64  `json:"account_id"`
	AccountName string `json:"account_name"`
	// Token timestamps (RFC3339), for showing how old a character's tokens are
	// and when the access token expires. Empty if unset.
	AccessTokenExpiresAt string `json:"access_token_expires_at"`
	TokensUpdatedAt      string `json:"tokens_updated_at"`
	CreatedAt            string `json:"created_at"`
}

type manageAccount struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type managePendingGrant struct {
	ID              int64    `json:"id"`
	ApplicationName string   `json:"application_name"`
	CharacterName   string   `json:"character_name"`
	Scopes          []string `json:"scopes"`
}

type manageGrant struct {
	ID              int64    `json:"id"`
	ApplicationID   int64    `json:"application_id"`
	ApplicationName string   `json:"application_name"`
	CharacterID     int64    `json:"character_id"`
	CharacterName   string   `json:"character_name"`
	Scopes          []string `json:"scopes"`
}

type managePendingPermission struct {
	ID              int64  `json:"id"`
	ApplicationName string `json:"application_name"`
	Permission      string `json:"permission"`
}

type manageAppPermission struct {
	ID              int64  `json:"id"`
	ApplicationID   int64  `json:"application_id"`
	ApplicationName string `json:"application_name"`
	Permission      string `json:"permission"`
}

// manageState is the single snapshot the UI polls, mirroring everything the
// old tray rendered across its five sections plus the settings it exposed.
type manageState struct {
	ClientIDSet bool `json:"client_id_set"`
	// UseCustomClientID is true when the user's own EVE application is in
	// use instead of the one bundled with the build; BundledClientID says
	// whether this build has one at all (if not, custom is forced on).
	UseCustomClientID bool   `json:"use_custom_client_id"`
	BundledClientID   bool   `json:"bundled_client_id"`
	CustomClientID    string `json:"custom_client_id"`
	// CallbackURL is the redirect URI a custom EVE application must be
	// registered with.
	CallbackURL        string                    `json:"callback_url"`
	BindHost           string                    `json:"bind_host"`
	LANAccess          bool                      `json:"lan_access"`
	Characters         []manageCharacter         `json:"characters"`
	Accounts           []manageAccount           `json:"accounts"`
	PendingGrants      []managePendingGrant      `json:"pending_grants"`
	ActiveGrants       []manageGrant             `json:"active_grants"`
	PendingPermissions []managePendingPermission `json:"pending_permissions"`
	AppPermissions     []manageAppPermission     `json:"app_permissions"`
	// DevBuild is true in development builds (the default; not -tags release); the UI shows its
	// developer-only Settings options only then.
	DevBuild bool `json:"dev_build"`
	// Update is the auto-updater's status, including the running version.
	Update update.Status `json:"update"`
}

func (s *Server) handleManageState(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.store.ListAccounts()
	if err != nil {
		s.manageError(w, "list accounts", err)
		return
	}
	accountName := make(map[int64]string, len(accounts))
	outAccounts := make([]manageAccount, len(accounts))
	for i, a := range accounts {
		accountName[a.ID] = a.Name
		outAccounts[i] = manageAccount{ID: a.ID, Name: a.Name}
	}

	chars, err := s.store.ListCharacters()
	if err != nil {
		s.manageError(w, "list characters", err)
		return
	}
	outChars := make([]manageCharacter, len(chars))
	for i, c := range chars {
		outChars[i] = manageCharacter{
			ID:                   c.ID,
			Name:                 c.Name,
			AccountID:            c.AccountID,
			AccountName:          accountName[c.AccountID],
			AccessTokenExpiresAt: formatTimeOrEmpty(c.AccessTokenExpiresAt),
			TokensUpdatedAt:      formatTimeOrEmpty(c.TokensUpdatedAt),
			CreatedAt:            formatTimeOrEmpty(c.CreatedAt),
		}
	}

	pendingGrants, err := s.store.ListPendingGrants()
	if err != nil {
		s.manageError(w, "list pending grants", err)
		return
	}
	outPending := make([]managePendingGrant, len(pendingGrants))
	for i, p := range pendingGrants {
		outPending[i] = managePendingGrant{ID: p.ID, ApplicationName: p.ApplicationName, CharacterName: p.CharacterName, Scopes: p.RequestedScopes}
	}

	grants, err := s.store.ListActiveGrantsView()
	if err != nil {
		s.manageError(w, "list active grants", err)
		return
	}
	outGrants := make([]manageGrant, len(grants))
	for i, g := range grants {
		outGrants[i] = manageGrant{
			ID:              g.ID,
			ApplicationID:   g.ApplicationID,
			ApplicationName: g.ApplicationName,
			CharacterID:     g.CharacterID,
			CharacterName:   g.CharacterName,
			Scopes:          g.Scopes,
		}
	}

	pendingPerms, err := s.store.ListPendingAppPermissions()
	if err != nil {
		s.manageError(w, "list pending app permissions", err)
		return
	}
	outPendingPerms := make([]managePendingPermission, len(pendingPerms))
	for i, p := range pendingPerms {
		outPendingPerms[i] = managePendingPermission{ID: p.ID, ApplicationName: p.ApplicationName, Permission: p.Permission}
	}

	appPerms, err := s.store.ListActiveAppPermissionsView()
	if err != nil {
		s.manageError(w, "list app permissions", err)
		return
	}
	outAppPerms := make([]manageAppPermission, len(appPerms))
	for i, p := range appPerms {
		outAppPerms[i] = manageAppPermission{ID: p.ID, ApplicationID: p.ApplicationID, ApplicationName: p.ApplicationName, Permission: p.Permission}
	}

	bindHost, err := s.BindHost()
	if err != nil {
		s.manageError(w, "read bind host", err)
		return
	}

	useCustom, customID, err := s.ClientIDSettings()
	if err != nil {
		s.manageError(w, "read client id settings", err)
		return
	}

	writeJSON(w, http.StatusOK, manageState{
		ClientIDSet:        s.ClientID() != "",
		UseCustomClientID:  useCustom,
		BundledClientID:    buildinfo.BundledClientID != "",
		CustomClientID:     customID,
		CallbackURL:        s.redirectURL,
		BindHost:           bindHost,
		LANAccess:          isLANBindHost(bindHost),
		Characters:         outChars,
		Accounts:           outAccounts,
		PendingGrants:      outPending,
		ActiveGrants:       outGrants,
		PendingPermissions: outPendingPerms,
		AppPermissions:     outAppPerms,
		DevBuild:           buildinfo.Dev,
		Update:             s.updateStatus(),
	})
}

type addCharacterResponse struct {
	LoginURL string `json:"login_url"`
}

// handleManageAddCharacter begins an SSO login under the default account and
// returns the URL the UI should open in a real browser (the custom-scheme
// callback can't be completed inside a webview). Mirrors the tray's old
// "Add Character" action.
func (s *Server) handleManageAddCharacter(w http.ResponseWriter, r *http.Request) {
	account, err := s.store.GetOrCreateDefaultAccount()
	if err != nil {
		s.manageError(w, "get default account", err)
		return
	}
	url, err := s.BeginLogin(account.ID)
	if err != nil {
		// Most likely the client ID isn't set yet — a user-actionable state,
		// not an internal fault, so report it as a 400 with the reason.
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, addCharacterResponse{LoginURL: url})
}

type characterDetailResponse struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	AccountName string `json:"account_name"`
	// AvailableScopes is what this character's token can serve (set at login),
	// i.e. the universe the per-grant scope editor may choose from.
	AvailableScopes []string `json:"available_scopes"`
}

// handleManageCharacterDetail returns a single character plus the scopes its
// token holds, powering the per-character detail view and its scope editor.
// The character's grants themselves come from the polled /manage/state, so
// the detail view stays live without re-fetching this.
func (s *Server) handleManageCharacterDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	c, err := s.store.GetCharacter(id)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		s.manageError(w, "get character", err)
		return
	}
	accountName := ""
	if a, aerr := s.store.GetAccount(c.AccountID); aerr == nil {
		accountName = a.Name
	}
	writeJSON(w, http.StatusOK, characterDetailResponse{
		ID:              c.ID,
		Name:            c.Name,
		AccountName:     accountName,
		AvailableScopes: c.Scopes,
	})
}

// handleManageDeleteCharacter removes a character from the proxy entirely,
// cascading to its grants and pending grants. Backs the "Delete character"
// action on the character detail page.
func (s *Server) handleManageDeleteCharacter(w http.ResponseWriter, r *http.Request) {
	s.manageMutate(w, r, s.store.DeleteCharacter)
}

type setGrantScopesRequest struct {
	Scopes []string `json:"scopes"`
}

// handleManageSetGrantScopes replaces the scopes on an active grant — the
// "drill in and update the specific permissions" action. Removing every scope
// is intentionally not allowed here; that's what revoking the grant is for.
func (s *Server) handleManageSetGrantScopes(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	var req setGrantScopesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "body must be JSON with a \"scopes\" array", http.StatusBadRequest)
		return
	}
	scopes := make([]string, 0, len(req.Scopes))
	for _, sc := range req.Scopes {
		if sc = strings.TrimSpace(sc); sc != "" {
			scopes = append(scopes, sc)
		}
	}
	if len(scopes) == 0 {
		http.Error(w, "at least one scope is required — revoke the grant to remove all access", http.StatusBadRequest)
		return
	}
	if err := s.store.UpdateGrantScopes(id, scopes); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		s.manageError(w, "update grant scopes", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleManageApproveGrant(w http.ResponseWriter, r *http.Request) {
	s.manageMutate(w, r, s.store.ApprovePendingGrant)
}

func (s *Server) handleManageDenyGrant(w http.ResponseWriter, r *http.Request) {
	s.manageMutate(w, r, s.store.DenyPendingGrant)
}

func (s *Server) handleManageRevokeGrant(w http.ResponseWriter, r *http.Request) {
	s.manageMutate(w, r, s.store.RevokeGrant)
}

func (s *Server) handleManageApprovePermission(w http.ResponseWriter, r *http.Request) {
	s.manageMutate(w, r, s.store.ApprovePendingAppPermission)
}

func (s *Server) handleManageDenyPermission(w http.ResponseWriter, r *http.Request) {
	s.manageMutate(w, r, s.store.DenyPendingAppPermission)
}

func (s *Server) handleManageRevokePermission(w http.ResponseWriter, r *http.Request) {
	s.manageMutate(w, r, s.store.RevokeAppPermission)
}

// manageMutate runs an id-keyed store mutation named by the {id} path value,
// translating store.ErrNotFound to 404 and anything else to 500.
func (s *Server) manageMutate(w http.ResponseWriter, r *http.Request, fn func(int64) error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := fn(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		s.manageError(w, "apply change", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type setClientIDRequest struct {
	ClientID string `json:"client_id"`
}

func (s *Server) handleManageSetClientID(w http.ResponseWriter, r *http.Request) {
	var req setClientIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "body must be JSON with a \"client_id\"", http.StatusBadRequest)
		return
	}
	clientID := strings.TrimSpace(req.ClientID)
	if clientID == "" {
		http.Error(w, "client_id must not be empty", http.StatusBadRequest)
		return
	}
	if err := s.SetClientID(clientID); err != nil {
		s.manageError(w, "save client id", err)
		return
	}
	s.log.Info("eve client id updated")
	w.WriteHeader(http.StatusNoContent)
}

type setUseCustomClientIDRequest struct {
	Enabled bool `json:"enabled"`
}

// handleManageSetUseCustomClientID switches between the bundled EVE
// application and the user's own. Builds without a bundled client_id can't
// turn custom off.
func (s *Server) handleManageSetUseCustomClientID(w http.ResponseWriter, r *http.Request) {
	var req setUseCustomClientIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "body must be JSON with an \"enabled\" boolean", http.StatusBadRequest)
		return
	}
	if !req.Enabled && buildinfo.BundledClientID == "" {
		http.Error(w, "this build has no bundled client ID; a custom client ID is required", http.StatusBadRequest)
		return
	}
	if err := s.SetUseCustomClientID(req.Enabled); err != nil {
		s.manageError(w, "save client id setting", err)
		return
	}
	s.log.Info("eve client id source updated", "custom", req.Enabled)
	w.WriteHeader(http.StatusNoContent)
}

type setBindHostRequest struct {
	// LAN toggles between localhost-only (false) and all-interfaces (true).
	// When a rebinder is installed (the normal app), the listener moves
	// immediately; otherwise the change is only saved for the next start.
	LAN bool `json:"lan"`
}

type setBindHostResponse struct {
	// Live reports whether the change took effect immediately (true) or
	// only on the next start (false).
	Live bool `json:"live"`
}

func (s *Server) handleManageSetBindHost(w http.ResponseWriter, r *http.Request) {
	var req setBindHostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "body must be JSON with a boolean \"lan\"", http.StatusBadRequest)
		return
	}
	host := "localhost"
	if req.LAN {
		host = "0.0.0.0"
	}
	if s.rebind != nil {
		if err := s.rebind(host); err != nil {
			// The previous listener is still in place; report why the new
			// one couldn't be bound (e.g. port in use) so the UI can show it.
			s.log.Error("rebind listener", "bind_host", host, "error", err)
			http.Error(w, "couldn't change LAN access: "+err.Error(), http.StatusConflict)
			return
		}
		s.log.Info("bind host updated", "bind_host", host)
		writeJSON(w, http.StatusOK, setBindHostResponse{Live: true})
		return
	}
	if err := s.SetBindHost(host); err != nil {
		s.manageError(w, "set bind host", err)
		return
	}
	s.log.Info("bind host updated (effective on restart)", "bind_host", host)
	writeJSON(w, http.StatusOK, setBindHostResponse{Live: false})
}

// isLANBindHost reports whether host exposes the proxy beyond this machine: loopback names are same-machine
// only; anything else exposes the proxy to the network.
func isLANBindHost(host string) bool {
	switch host {
	case "", "localhost", "127.0.0.1", "::1", "[::1]":
		return false
	default:
		return true
	}
}

// handleManageRefreshCharacterTokens forces an immediate token refresh for one
// character, exchanging its refresh token for a fresh access/refresh pair even
// though the current access token may not be near expiry. Backs the "Refresh
// tokens" button on the character detail page.
func (s *Server) handleManageRefreshCharacterTokens(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	char, err := s.store.GetCharacter(id)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		s.manageError(w, "get character", err)
		return
	}
	if _, err := s.tokenMgr.Refresh(r.Context(), char); err != nil {
		// A refresh failure is usually an upstream/credential problem (e.g. the
		// refresh token was revoked at EVE SSO), which the user can act on, so
		// surface the reason rather than a generic 500.
		s.log.Error("force refresh character tokens", "character_id", id, "error", err)
		http.Error(w, "token refresh failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	s.log.Info("character tokens force-refreshed", "character_id", id)
	w.WriteHeader(http.StatusNoContent)
}

// formatTimeOrEmpty renders t as RFC3339 for the UI, or "" if t is the zero
// time (so the client can show "unknown" rather than a year-1 date).
func formatTimeOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return store.FormatTime(t)
}

func (s *Server) manageError(w http.ResponseWriter, context string, err error) {
	s.log.Error(context, "error", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}
