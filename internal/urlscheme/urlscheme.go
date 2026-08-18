// Package urlscheme registers esi-proxy as the OS handler for the
// eveauth-proxy:// custom URI scheme used as the EVE SSO redirect_uri, and
// delivers incoming callback URLs to the already-running instance.
//
// Registration and delivery both work differently per OS:
//   - Windows: a registry entry under HKCU\Software\Classes; the OS spawns
//     a new process with the URL as argv[1].
//   - Linux: a .desktop file with a MimeType=x-scheme-handler entry; the OS
//     also spawns a new process with the URL as argv[1].
//   - macOS: Launch Services can only route a custom scheme to a proper
//     .app bundle (see scripts/build-macos-app.sh) — there is no way to
//     register a bare binary. When already running, macOS delivers the URL
//     to the existing process via an Apple Event instead of spawning a new
//     one, which is what InstallHandler wires up.
package urlscheme

import "log/slog"

// Scheme is the custom URI scheme esi-proxy registers as its EVE SSO
// redirect_uri, i.e. redirect_uri = Scheme + "://callback".
const Scheme = "eveauth-proxy"

// Register attempts to register Scheme with the OS so eveauth-proxy://
// URLs are routed to this program. Best-effort: a failure is logged, not
// fatal — the proxy still runs fine without it, just without automatic
// SSO-callback routing until it's registered (manually, or by fixing
// whatever the logged error describes).
func Register(log *slog.Logger) {
	if err := register(); err != nil {
		log.Warn("register eveauth-proxy:// url scheme", "error", err)
	}
}

// InstallHandler wires delivery of eveauth-proxy:// URLs that arrive via an
// OS mechanism for an already-running process (only macOS has one — Apple
// Events). It's a no-op on Windows/Linux, where a custom-scheme URL always
// spawns a new process instead, handled via that new process's own argv.
// Must be called from the main goroutine before the tray's event loop
// starts running.
func InstallHandler(callback func(rawURL string)) {
	installHandler(callback)
}
