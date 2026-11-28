package server

import (
	"encoding/json"
	"net/http"

	"github.com/BOS-Development/esi-bridge/internal/buildinfo"
	"github.com/BOS-Development/esi-bridge/internal/config"
	"github.com/BOS-Development/esi-bridge/internal/update"
)

// SetUpdater wires in the auto-updater behind the /manage/update endpoints.
// It must be called before the server starts handling requests. Without one
// (e.g. serve-ui), the UI reports updates as unavailable.
func (s *Server) SetUpdater(u *update.Updater) {
	s.updater = u
}

// updateStatus is the "update" part of /manage/state.
func (s *Server) updateStatus() update.Status {
	if s.updater == nil {
		return update.Status{CurrentVersion: buildinfo.DisplayVersion(), State: update.StateIdle}
	}
	return s.updater.Status()
}

func (s *Server) handleManageUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		http.Error(w, "updates aren't available in this mode", http.StatusConflict)
		return
	}
	if err := s.updater.CheckNow(); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusAccepted, s.updater.Status())
}

func (s *Server) handleManageUpdateInstall(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		http.Error(w, "updates aren't available in this mode", http.StatusConflict)
		return
	}
	if err := s.updater.Install(); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	s.log.Info("update install started")
	writeJSON(w, http.StatusAccepted, s.updater.Status())
}

type setAutoUpdateRequest struct {
	Enabled bool `json:"enabled"`
}

// handleManageSetAutoUpdate turns the periodic update check on or off and
// saves the choice.
func (s *Server) handleManageSetAutoUpdate(w http.ResponseWriter, r *http.Request) {
	var req setAutoUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "body must be JSON with a boolean \"enabled\"", http.StatusBadRequest)
		return
	}
	s.cfgMu.Lock()
	cfg, err := config.Load(s.dataDir)
	if err == nil {
		cfg.CheckForUpdates = &req.Enabled
		err = config.Save(s.dataDir, cfg)
	}
	s.cfgMu.Unlock()
	if err != nil {
		s.manageError(w, "save update setting", err)
		return
	}
	if s.updater != nil {
		s.updater.SetAutoCheck(req.Enabled)
	}
	w.WriteHeader(http.StatusNoContent)
}
