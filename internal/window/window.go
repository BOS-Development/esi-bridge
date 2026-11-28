// Package window hosts the proxy's management UI inside a native desktop
// window, so the user interacts with an ordinary app rather than a browser
// tab. The window is a thin native shell (a system webview — WebKit on macOS,
// WebView2 on Windows, WebKitGTK on Linux) pointed at the proxy's loopback
// management page (/app); it carries no address bar or browser chrome, so the
// fact that the UI is served over local HTTP is an implementation detail the
// user never sees.
//
// The webview owns the process's main thread run loop (on macOS, Cocoa's), the
// same thread the system tray needs. Those two can't share one process, so the
// window runs as a separate child process (the parent's `--window` invocation
// of the same binary); see cmd/esi-bridge. The loopback server it points at
// stays fully reachable from a browser — the window is an additional surface,
// not a replacement.
package window

// Supported reports whether this build can host a native window. It's false
// for a CGO-disabled build (the webview is a C library), in which case callers
// fall back to opening the management page in the user's browser.
const Supported = supported
