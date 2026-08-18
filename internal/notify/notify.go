// Package notify sends OS-level desktop notifications so a pending
// approval request doesn't go unnoticed until the user happens to open the
// tray menu.
package notify

import (
	"log/slog"

	"github.com/gen2brain/beeep"
)

func init() {
	beeep.AppName = "ESI Proxy"
}

// Send shows a desktop notification. Best-effort: a failure (e.g. no
// notification daemon running on a minimal Linux setup) is logged, not
// fatal — the request is still visible in the tray regardless.
func Send(log *slog.Logger, title, message string) {
	// icon must be a string (path to a .png) or []byte (png data) — beeep
	// rejects nil outright, so an empty string (no icon) is the no-icon case.
	if err := beeep.Notify(title, message, ""); err != nil {
		log.Warn("send desktop notification", "error", err)
	}
}
