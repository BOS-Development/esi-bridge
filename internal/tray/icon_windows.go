package tray

import "github.com/BOS-Development/esi-proxy/internal/assets"

// icon returns the tray icon as .ico bytes. fyne.io/systray on Windows loads
// the icon via the Win32 LoadImage API with IMAGE_ICON, which requires a
// real .ico file — handing it a bare PNG fails silently (LoadImage returns a
// null handle) and the tray icon never appears.
func icon() []byte { return assets.ICO }
