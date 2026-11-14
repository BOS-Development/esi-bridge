//go:build darwin && cgo

package window

/*
#cgo LDFLAGS: -framework Cocoa
void esiproxy_makeForegroundApp(void);
*/
import "C"

// makeForeground promotes this process to a regular foreground macOS app. It
// must run on the main thread after the shared NSApplication exists (i.e.
// after the webview has been created). The implementation is in
// activate_darwin.m — the Objective-C call can't live in the cgo preamble,
// which is compiled as C.
func makeForeground() { C.esiproxy_makeForegroundApp() }
