package server

import (
	"encoding/json"
	"net/http"

	"github.com/BOS-Development/esi-proxy/internal/store"
)

type requestPermissionsRequest struct {
	Permissions []string `json:"permissions"`
}

// handleRequestPermissions lets an application ask for app-wide (not
// per-character) permissions such as listing every character on the proxy.
// Each requested permission is reported back as already "granted" or newly
// "pending" (awaiting approval from the tray), the same shape as
// /api/request-access uses for character grants.
func (s *Server) handleRequestPermissions(w http.ResponseWriter, r *http.Request) {
	app, err := s.authenticateApp(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	var req requestPermissionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Permissions) == 0 {
		http.Error(w, "body must be JSON with a non-empty \"permissions\" array", http.StatusBadRequest)
		return
	}

	statuses := make(map[string]string, len(req.Permissions))
	anyPending := false
	for _, p := range req.Permissions {
		if !store.KnownAppPermissions[p] {
			http.Error(w, "unknown permission: "+p, http.StatusBadRequest)
			return
		}

		granted, err := s.store.HasAppPermission(app.ID, p)
		if err != nil {
			s.log.Error("check app permission", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if granted {
			statuses[p] = "granted"
			continue
		}

		if err := s.store.RequestAppPermission(app.ID, p); err != nil {
			s.log.Error("request app permission", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		statuses[p] = "pending"
		anyPending = true
	}

	s.log.Info("app permissions requested", "application", app.Name, "requested", req.Permissions)
	status := http.StatusOK
	if anyPending {
		status = http.StatusAccepted
	}
	writeJSON(w, status, statuses)
}
