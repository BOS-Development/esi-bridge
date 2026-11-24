// Package assets holds the ESI Bridge brand images, embedded so every part
// of the app (tray, notifications, management UI) uses the same files.
//
// bridge.ico and bridge.png here are also the source for the Windows exe
// resource (cmd/esi-proxy/rsrc_windows_*.syso, see scripts/gen-winres.ps1),
// the installer icon and the macOS app icon.
package assets

import _ "embed"

// ICO is the app icon as a Windows .ico file.
//
//go:embed bridge.ico
var ICO []byte

// PNG is the app icon as a PNG image.
//
//go:embed bridge.png
var PNG []byte
