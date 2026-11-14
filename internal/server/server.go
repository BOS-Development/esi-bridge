// Package server implements the proxy's HTTP surface: the EVE SSO callback,
// external application registration/approval, and the ESI passthrough
// proxy handler.
package server

import (
	"log/slog"
	"net/http"
	"sync"

	"github.com/BOS-Development/esi-proxy/internal/cache"
	"github.com/BOS-Development/esi-proxy/internal/config"
	"github.com/BOS-Development/esi-proxy/internal/esi"
	"github.com/BOS-Development/esi-proxy/internal/store"
	"github.com/BOS-Development/esi-proxy/internal/tokens"
)

type Server struct {
	store     *store.Store
	sso       *esi.SSOHolder
	esiClient *esi.Client
	cache     *cache.Cache
	tokenMgr  *tokens.Manager
	log       *slog.Logger

	// ssoBaseURL/redirectURL are fixed for the process's lifetime;
	// SetClientID rebuilds the SSO client with these plus a new client_id.
	ssoBaseURL  string
	redirectURL string
	dataDir     string

	// sessionToken is an unguessable per-process secret the management UI
	// (/app + /manage) must present on every /manage call. It's injected into
	// the /app page the server serves, so same-origin UI code can read it but
	// a cross-origin website cannot — this is what stops a malicious page in
	// the user's browser from driving approvals via CSRF against localhost.
	// Combined with the loopback-only guard (see manage.go), the privileged
	// surface is reachable only by local UI code holding this token.
	sessionToken string

	cfgMu sync.Mutex // guards persisting config.json from SetClientID

	// rebind, if set, applies a new bind host to the live listener (and mDNS
	// advert) and persists it via SetBindHost once the rebind succeeds. It's
	// wired in by the process that owns the listener (see SetRebinder);
	// without it, /manage/bind-host only saves the setting for next start.
	rebind func(host string) error

	loginMu  sync.Mutex
	loginTxn map[string]pendingLogin // state -> pending login
}

type pendingLogin struct {
	verifier  string
	accountID int64
}

func New(s *store.Store, sso *esi.SSOHolder, esiClient *esi.Client, c *cache.Cache, tokenMgr *tokens.Manager, log *slog.Logger, dataDir, ssoBaseURL, redirectURL string) *Server {
	token, err := generateAPIKey() // 32 bytes of crypto/rand, hex-encoded
	if err != nil {
		// crypto/rand failure is catastrophic and essentially never happens;
		// there is no safe fallback for a session secret, so fail loudly.
		panic("esi-proxy: generate session token: " + err.Error())
	}
	return &Server{
		store:        s,
		sso:          sso,
		esiClient:    esiClient,
		cache:        c,
		tokenMgr:     tokenMgr,
		log:          log,
		dataDir:      dataDir,
		ssoBaseURL:   ssoBaseURL,
		redirectURL:  redirectURL,
		sessionToken: token,
		loginTxn:     make(map[string]pendingLogin),
	}
}

// SetClientID rebuilds the SSO client with a new EVE client_id and persists
// it to config.json, taking effect immediately — no restart required.
func (s *Server) SetClientID(clientID string) error {
	s.sso.Set(esi.NewSSO(clientID, s.ssoBaseURL, s.redirectURL, LoginScopes))

	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()

	cfg, err := config.Load(s.dataDir)
	if err != nil {
		return err
	}
	cfg.EVEClientID = clientID
	return config.Save(s.dataDir, cfg)
}

// ClientID returns the EVE client_id currently in use, for pre-filling the
// setup form.
func (s *Server) ClientID() string {
	return s.sso.Get().ClientID()
}

// SessionToken returns the per-process secret the management UI must present
// on /manage calls. The window subprocess that loads /app is handed this so
// it can authenticate; it's also injected into the served /app page.
func (s *Server) SessionToken() string {
	return s.sessionToken
}

// BindHost returns the host the HTTP server is configured to bind to, read
// from config.json.
func (s *Server) BindHost() (string, error) {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()

	cfg, err := config.Load(s.dataDir)
	if err != nil {
		return "", err
	}
	return cfg.BindHost, nil
}

// SetRebinder installs the function /manage/bind-host uses to apply a bind
// host change live. It must be called before the server starts handling
// requests.
func (s *Server) SetRebinder(rebind func(host string) error) {
	s.rebind = rebind
}

// SetBindHost persists a new bind host to config.json. It only saves the
// setting: the listener itself is rebound live by the caller (see the
// network type in cmd/esi-proxy), which calls this once the rebind succeeds.
func (s *Server) SetBindHost(host string) error {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()

	cfg, err := config.Load(s.dataDir)
	if err != nil {
		return err
	}
	cfg.BindHost = host
	return config.Save(s.dataDir, cfg)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /sso/callback", s.handleSSOCallback)
	mux.HandleFunc("GET /setup/client-id", s.handleClientIDForm)
	mux.HandleFunc("POST /setup/client-id", s.handleSetClientID)
	mux.HandleFunc("POST /api/register", s.handleRegisterApp)
	mux.HandleFunc("GET /api/characters", s.handleListCharacters)
	mux.HandleFunc("GET /api/accounts", s.handleListAccounts)
	mux.HandleFunc("GET /api/grants", s.handleListGrants)
	mux.HandleFunc("POST /api/request-access", s.handleRequestAccess)
	mux.HandleFunc("POST /api/request-permissions", s.handleRequestPermissions)
	mux.HandleFunc("/proxy/{character_id}/{esi_path...}", s.handleProxy)

	// Management UI (Phase 1/2 of the tray→window migration). The full UI
	// page and its static assets are loopback-only; every state-reading or
	// state-changing /manage call additionally requires the session token.
	// Both guards live in manage.go.
	mux.HandleFunc("GET /app", s.requireLocalUI(s.handleAppPage))
	mux.HandleFunc("GET /app/static/", s.requireLocalUI(s.handleAppStatic))
	mux.HandleFunc("GET /manage/state", s.requireSession(s.handleManageState))
	mux.HandleFunc("GET /manage/characters/{id}", s.requireSession(s.handleManageCharacterDetail))
	mux.HandleFunc("POST /manage/characters/add", s.requireSession(s.handleManageAddCharacter))
	mux.HandleFunc("POST /manage/characters/{id}/refresh", s.requireSession(s.handleManageRefreshCharacterTokens))
	mux.HandleFunc("DELETE /manage/characters/{id}", s.requireSession(s.handleManageDeleteCharacter))
	mux.HandleFunc("POST /manage/grants/{id}/scopes", s.requireSession(s.handleManageSetGrantScopes))
	mux.HandleFunc("POST /manage/pending-grants/{id}/approve", s.requireSession(s.handleManageApproveGrant))
	mux.HandleFunc("POST /manage/pending-grants/{id}/deny", s.requireSession(s.handleManageDenyGrant))
	mux.HandleFunc("POST /manage/grants/{id}/revoke", s.requireSession(s.handleManageRevokeGrant))
	mux.HandleFunc("POST /manage/pending-permissions/{id}/approve", s.requireSession(s.handleManageApprovePermission))
	mux.HandleFunc("POST /manage/pending-permissions/{id}/deny", s.requireSession(s.handleManageDenyPermission))
	mux.HandleFunc("POST /manage/app-permissions/{id}/revoke", s.requireSession(s.handleManageRevokePermission))
	mux.HandleFunc("POST /manage/client-id", s.requireSession(s.handleManageSetClientID))
	mux.HandleFunc("POST /manage/bind-host", s.requireSession(s.handleManageSetBindHost))
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}
