package server

import (
	"net/http"

	"github.com/BOS-Development/esi-proxy/internal/store"
)

type characterResponse struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	AccountID *int64 `json:"account_id,omitempty"`
}

// handleListCharacters lists characters visible to the calling application:
// every character on the proxy if it holds the list_characters app
// permission, otherwise only the characters it has an active per-character
// grant for. account_id is only included if the app also holds the
// list_accounts permission — account grouping stays gated independently of
// character visibility.
func (s *Server) handleListCharacters(w http.ResponseWriter, r *http.Request) {
	app, err := s.authenticateApp(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	canListAccounts, err := s.store.HasAppPermission(app.ID, store.PermissionListAccounts)
	if err != nil {
		s.log.Error("check list_accounts permission", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	canListAll, err := s.store.HasAppPermission(app.ID, store.PermissionListCharacters)
	if err != nil {
		s.log.Error("check list_characters permission", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var out []characterResponse
	if canListAll {
		chars, err := s.store.ListCharacters()
		if err != nil {
			s.log.Error("list characters", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		out = make([]characterResponse, len(chars))
		for i, c := range chars {
			out[i] = characterResponse{ID: c.ID, Name: c.Name}
			if canListAccounts {
				out[i].AccountID = &c.AccountID
			}
		}
	} else {
		grants, err := s.store.ListActiveGrantsForApplication(app.ID)
		if err != nil {
			s.log.Error("list characters", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		out = make([]characterResponse, len(grants))
		for i, g := range grants {
			out[i] = characterResponse{ID: g.CharacterID, Name: g.CharacterName}
		}
		if canListAccounts {
			if err := fillAccountIDs(s, out); err != nil {
				s.log.Error("fill account ids", "error", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
		}
	}

	writeJSON(w, http.StatusOK, out)
}

// fillAccountIDs looks up each response's real account_id. Only used on the
// granted-subset path, where ListActiveGrantsForApplication doesn't already
// carry it (the list_characters path gets it directly from ListCharacters).
func fillAccountIDs(s *Server, out []characterResponse) error {
	for i := range out {
		c, err := s.store.GetCharacter(out[i].ID)
		if err != nil {
			return err
		}
		out[i].AccountID = &c.AccountID
	}
	return nil
}

type accountResponse struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// handleListAccounts lists accounts, gated behind the list_accounts app
// permission — account grouping is user-managed metadata ESI itself never
// exposes, so it's never available by default.
func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	app, err := s.authenticateApp(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	granted, err := s.store.HasAppPermission(app.ID, store.PermissionListAccounts)
	if err != nil {
		s.log.Error("check list_accounts permission", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !granted {
		http.Error(w, "list_accounts permission not granted; call /api/request-permissions", http.StatusForbidden)
		return
	}

	accounts, err := s.store.ListAccounts()
	if err != nil {
		s.log.Error("list accounts", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	out := make([]accountResponse, len(accounts))
	for i, a := range accounts {
		out[i] = accountResponse{ID: a.ID, Name: a.Name}
	}
	writeJSON(w, http.StatusOK, out)
}

type grantResponse struct {
	CharacterID   int64    `json:"character_id"`
	CharacterName string   `json:"character_name"`
	Scopes        []string `json:"scopes"`
}

// handleListGrants lists the calling application's own active grants
// (character plus the scopes it was specifically approved for), so it can
// check what it currently has access to without probing every proxy path.
func (s *Server) handleListGrants(w http.ResponseWriter, r *http.Request) {
	app, err := s.authenticateApp(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	grants, err := s.store.ListActiveGrantsForApplication(app.ID)
	if err != nil {
		s.log.Error("list grants", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	out := make([]grantResponse, len(grants))
	for i, g := range grants {
		out[i] = grantResponse{CharacterID: g.CharacterID, CharacterName: g.CharacterName, Scopes: g.Scopes}
	}
	writeJSON(w, http.StatusOK, out)
}
