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

	cfgMu sync.Mutex // guards persisting config.json from SetClientID

	loginMu  sync.Mutex
	loginTxn map[string]pendingLogin // state -> pending login
}

type pendingLogin struct {
	verifier  string
	accountID int64
}

func New(s *store.Store, sso *esi.SSOHolder, esiClient *esi.Client, c *cache.Cache, tokenMgr *tokens.Manager, log *slog.Logger, dataDir, ssoBaseURL, redirectURL string) *Server {
	return &Server{
		store:       s,
		sso:         sso,
		esiClient:   esiClient,
		cache:       c,
		tokenMgr:    tokenMgr,
		log:         log,
		dataDir:     dataDir,
		ssoBaseURL:  ssoBaseURL,
		redirectURL: redirectURL,
		loginTxn:    make(map[string]pendingLogin),
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
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}
