package server

import (
	"net/http"

	"github.com/BOS-Development/esi-proxy/internal/notify"
)

// Developer-only /manage endpoints. They are registered only in development
// builds (buildinfo.Dev, the default unless built with the "release" tag); see routes in server.go.

// handleManageDevTestNotification sends a sample desktop notification, so a
// developer can check that notifications show up on this OS without making a
// real access request or SSO login.
func (s *Server) handleManageDevTestNotification(w http.ResponseWriter, r *http.Request) {
	notify.Send(s.log, "ESI Bridge: test notification", "This is a test notification from a development build.")
	s.log.Info("dev: test notification sent")
	w.WriteHeader(http.StatusNoContent)
}
