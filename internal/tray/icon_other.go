//go:build !windows

package tray

import (
	"bytes"
	"image/png"
)

// icon returns the tray icon as PNG bytes, which fyne.io/systray accepts
// directly on macOS and Linux.
func icon() []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, iconImage())
	return buf.Bytes()
}
