// Package notify sends OS-level desktop notifications so a pending
// approval request doesn't go unnoticed until the user happens to open the
// window.
package notify

import (
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/BOS-Development/esi-bridge/internal/assets"
	"github.com/gen2brain/beeep"
)

func init() {
	beeep.AppName = "ESI Bridge"
}

var (
	iconOnce sync.Once
	iconPath string
)

// icon returns the path of the app icon written out as a PNG file, or "" if
// it couldn't be written. beeep also accepts PNG bytes, but then writes a
// fresh temp file on every notification and never removes it, so the icon is
// written once per process here instead.
func icon() string {
	iconOnce.Do(func() {
		p := filepath.Join(os.TempDir(), "esi-bridge-icon.png")
		if err := os.WriteFile(p, assets.PNG, 0o644); err == nil {
			iconPath = p
		}
	})
	return iconPath
}

// Send shows a desktop notification. Best-effort: a failure (e.g. no
// notification daemon running on a minimal Linux setup) is logged, not
// fatal — the request is still visible in the window regardless.
func Send(log *slog.Logger, title, message string) {
	// icon must be a string (path to a .png) or []byte (png data) — beeep
	// rejects nil outright, so an empty string (no icon) is the no-icon case.
	if err := beeep.Notify(title, message, icon()); err != nil {
		log.Warn("send desktop notification", "error", err)
	}
}
