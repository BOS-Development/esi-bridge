package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"

	"github.com/BOS-Development/esi-proxy/internal/notify"
)

// LoginScopes is the fixed set of ESI scopes requested for every character
// added to the proxy. Individual applications are still gated per-character
// by the grant/approval flow; this only controls what the proxy itself is
// able to ask ESI for on a character's behalf.
var LoginScopes = []string{
	"esi-location.read_ship_type.v1",
	"esi-mail.send_mail.v1",
	"esi-wallet.read_character_wallet.v1",
	"esi-universe.read_structures.v1",
	"esi-fleets.read_fleet.v1",
	"esi-ui.write_waypoint.v1",
	"esi-contracts.read_corporation_contracts.v1",
	"esi-corporations.read_starbases.v1",
	"esi-characters.read_fw_stats.v1",
	"esi-corporations.read_projects.v1",
	"esi-corporations.read_freelance_jobs.v1",
	"esi-characters.read_freelance_jobs.v1",
	"esi-structures.read_corporation.v1",
	"esi-structures.read_character.v1",
	"esi-activities.read_character.v1",
	"esi-access.read_lists.v1",
	"esi.activity.char:read",
	"esi.cosmetic.char:read",
	"esi-calendar.respond_calendar_events.v1",
	"esi-calendar.read_calendar_events.v1",
	"esi-location.read_location.v1",
	"esi-mail.organize_mail.v1",
	"esi-mail.read_mail.v1",
	"esi-skills.read_skills.v1",
	"esi-skills.read_skillqueue.v1",
	"esi-wallet.read_corporation_wallet.v1",
	"esi-search.search_structures.v1",
	"esi-clones.read_clones.v1",
	"esi-characters.read_contacts.v1",
	"esi-killmails.read_killmails.v1",
	"esi-corporations.read_corporation_membership.v1",
	"esi-assets.read_assets.v1",
	"esi-planets.manage_planets.v1",
	"esi-fleets.write_fleet.v1",
	"esi-ui.open_window.v1",
	"esi-characters.write_contacts.v1",
	"esi-fittings.read_fittings.v1",
	"esi-fittings.write_fittings.v1",
	"esi-markets.structure_markets.v1",
	"esi-corporations.read_structures.v1",
	"esi-characters.read_loyalty.v1",
	"esi-characters.read_chat_channels.v1",
	"esi-characters.read_medals.v1",
	"esi-characters.read_standings.v1",
	"esi-characters.read_agents_research.v1",
	"esi-industry.read_character_jobs.v1",
	"esi-markets.read_character_orders.v1",
	"esi-characters.read_blueprints.v1",
	"esi-characters.read_corporation_roles.v1",
	"esi-location.read_online.v1",
	"esi-contracts.read_character_contracts.v1",
	"esi-clones.read_implants.v1",
	"esi-characters.read_fatigue.v1",
	"esi-killmails.read_corporation_killmails.v1",
	"esi-corporations.track_members.v1",
	"esi-wallet.read_corporation_wallets.v1",
	"esi-characters.read_notifications.v1",
	"esi-corporations.read_divisions.v1",
	"esi-corporations.read_contacts.v1",
	"esi-assets.read_corporation_assets.v1",
	"esi-corporations.read_titles.v1",
	"esi-corporations.read_blueprints.v1",
	"esi-corporations.read_standings.v1",
	"esi-industry.read_corporation_jobs.v1",
	"esi-markets.read_corporation_orders.v1",
	"esi-corporations.read_container_logs.v1",
	"esi-industry.read_character_mining.v1",
	"esi-industry.read_corporation_mining.v1",
	"esi-planets.read_customs_offices.v1",
	"esi-corporations.read_facilities.v1",
	"esi-corporations.read_medals.v1",
	"esi-characters.read_titles.v1",
	"esi-alliances.read_contacts.v1",
	"esi-corporations.read_fw_stats.v1",
}

// BeginLogin starts an SSO login for a new character under accountID,
// returning the URL to open in the user's browser.
func (s *Server) BeginLogin(accountID int64) (string, error) {
	if s.ClientID() == "" {
		return "", fmt.Errorf("no EVE client_id is configured — set a custom Client ID in Settings first")
	}

	state, err := randomState()
	if err != nil {
		return "", fmt.Errorf("generate state: %w", err)
	}

	url, verifier := s.sso.Get().LoginURL(state)

	s.loginMu.Lock()
	s.loginTxn[state] = pendingLogin{verifier: verifier, accountID: accountID}
	s.loginMu.Unlock()

	return url, nil
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// handleSSOCallback completes a login. It's reached either directly by a
// browser (the old http://localhost/sso/callback redirect_uri) or — now
// that the redirect_uri is the eveauth-proxy:// custom scheme — by a local
// forwarder replaying the callback URL's query string here (see
// runForwardCallback/forwardCallback in cmd/esi-proxy). Since the browser
// generally can't render anything meaningful once a custom-scheme handoff
// has happened, a desktop notification is fired in addition to (not
// instead of) the HTML response, so the user gets feedback either way.
func (s *Server) handleSSOCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if errMsg := q.Get("error"); errMsg != "" {
		s.log.Error("sso login denied", "error", errMsg)
		notify.Send(s.log, "ESI Bridge: login failed", "Login was not completed: "+errMsg)
		writeCallbackPage(w, "Login was not completed: "+errMsg)
		return
	}

	state := q.Get("state")
	code := q.Get("code")
	if state == "" || code == "" {
		http.Error(w, "missing code or state", http.StatusBadRequest)
		return
	}

	s.loginMu.Lock()
	pending, ok := s.loginTxn[state]
	delete(s.loginTxn, state)
	s.loginMu.Unlock()
	if !ok {
		http.Error(w, "unknown or expired login state", http.StatusBadRequest)
		return
	}

	tok, err := s.sso.Get().Exchange(r.Context(), code, pending.verifier)
	if err != nil {
		s.log.Error("sso token exchange failed", "error", err)
		notify.Send(s.log, "ESI Bridge: login failed", "Could not exchange authorization code.")
		writeCallbackPage(w, "Login failed: could not exchange authorization code.")
		return
	}

	char, err := s.tokenMgr.SaveLogin(pending.accountID, tok)
	if err != nil {
		s.log.Error("save login failed", "error", err)
		notify.Send(s.log, "ESI Bridge: login failed", "Could not save the character.")
		writeCallbackPage(w, "Login failed: could not save character.")
		return
	}

	s.log.Info("character added", "character_id", char.ID, "character_name", char.Name)
	notify.Send(s.log, "ESI Bridge: character added", fmt.Sprintf("%s was added to ESI Bridge.", char.Name))
	writeCallbackPage(w, fmt.Sprintf("%s was added to ESI Bridge. You can close this window.", char.Name))
}

func writeCallbackPage(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html><body style="font-family:sans-serif;text-align:center;margin-top:4em"><p>%s</p></body></html>`, message)
}
