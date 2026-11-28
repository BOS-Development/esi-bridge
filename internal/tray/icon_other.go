//go:build !windows

package tray

import "github.com/BOS-Development/esi-bridge/internal/assets"

// icon returns the tray icon as PNG bytes, which fyne.io/systray accepts
// directly on macOS and Linux.
func icon() []byte { return assets.PNG }
