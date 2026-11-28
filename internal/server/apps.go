package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/BOS-Development/esi-bridge/internal/store"
)

func hashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func generateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// authenticateApp resolves the application identified by the
// X-Proxy-Api-Key header.
func (s *Server) authenticateApp(r *http.Request) (store.Application, error) {
	key := r.Header.Get("X-Proxy-Api-Key")
	if key == "" {
		return store.Application{}, errors.New("missing X-Proxy-Api-Key header")
	}
	app, err := s.store.GetApplicationByKeyHash(hashAPIKey(key))
	if errors.Is(err, store.ErrNotFound) {
		return store.Application{}, errors.New("invalid api key")
	}
	return app, err
}

type registerAppRequest struct {
	Name string `json:"name"`
}

type registerAppResponse struct {
	ApplicationID int64  `json:"application_id"`
	APIKey        string `json:"api_key"`
}

// handleRegisterApp lets an external application self-register with a
// name it specifies itself. The returned api_key has no grants until the
// user approves an access request for a specific character in the window.
//
// Names are not required to be unique: identity is the api_key, not the
// name, so two registrations (e.g. two installs of the same app, or a
// re-register after a lost key) can share a name without conflict — the
// window shows each as a separate entry to approve/deny independently.
func (s *Server) handleRegisterApp(w http.ResponseWriter, r *http.Request) {
	var req registerAppRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		http.Error(w, "body must be JSON with a non-empty \"name\"", http.StatusBadRequest)
		return
	}

	apiKey, err := generateAPIKey()
	if err != nil {
		s.log.Error("generate api key", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	app, err := s.store.CreateApplication(req.Name, hashAPIKey(apiKey))
	if err != nil {
		s.log.Error("create application", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	s.log.Info("application registered", "application_id", app.ID, "name", app.Name)
	writeJSON(w, http.StatusCreated, registerAppResponse{ApplicationID: app.ID, APIKey: apiKey})
}

type requestAccessRequest struct {
	CharacterID int64    `json:"character_id"`
	Scopes      []string `json:"scopes"`
}

type requestAccessResponse struct {
	Status string `json:"status"` // "granted" or "pending"
}

// handleRequestAccess is called by an application to ask for access to a
// character. If a covering grant already exists it's reported immediately;
// otherwise a pending_grants row is created for the user to approve/deny
// in the window.
func (s *Server) handleRequestAccess(w http.ResponseWriter, r *http.Request) {
	app, err := s.authenticateApp(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	var req requestAccessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CharacterID == 0 {
		http.Error(w, "body must be JSON with a non-zero \"character_id\"", http.StatusBadRequest)
		return
	}

	if _, err := s.store.GetCharacter(req.CharacterID); errors.Is(err, store.ErrNotFound) {
		http.Error(w, "unknown character_id", http.StatusNotFound)
		return
	}

	grant, err := s.store.GetActiveGrant(app.ID, req.CharacterID)
	if err == nil && coversScopes(grant.Scopes, req.Scopes) {
		writeJSON(w, http.StatusOK, requestAccessResponse{Status: "granted"})
		return
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Error("get active grant", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := s.store.UpsertPendingGrant(app.ID, req.CharacterID, req.Scopes); err != nil {
		s.log.Error("upsert pending grant", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	s.log.Info("access requested, awaiting approval", "application", app.Name, "character_id", req.CharacterID, "scopes", req.Scopes)
	writeJSON(w, http.StatusAccepted, requestAccessResponse{Status: "pending"})
}

func coversScopes(granted, requested []string) bool {
	have := make(map[string]bool, len(granted))
	for _, s := range granted {
		have[s] = true
	}
	for _, s := range requested {
		if !have[s] {
			return false
		}
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status line is already sent; usually this is the client having
		// gone away, but a non-encodable value is a bug worth seeing.
		slog.Warn("write json response", "error", err)
	}
}
