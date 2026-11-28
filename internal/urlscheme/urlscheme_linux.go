//go:build linux

package urlscheme

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// register writes a .desktop file declaring ESI Bridge as the handler for
// x-scheme-handler/eveauth-proxy and points xdg-mime at it, so Linux routes
// eveauth-proxy:// URLs to this executable, spawning a new process with the
// URL as argv[1] (handled by the forwarder-mode branch in cmd/esi-bridge).
// Idempotent — safe to call on every startup.
func register() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home dir: %w", err)
	}
	appsDir := filepath.Join(home, ".local", "share", "applications")
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		return fmt.Errorf("create applications dir: %w", err)
	}

	const desktopFileName = "esi-proxy.desktop"
	content := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=ESI Bridge
Exec=%s %%u
StartupNotify=false
NoDisplay=true
MimeType=x-scheme-handler/%s;
`, exe, Scheme)
	if err := os.WriteFile(filepath.Join(appsDir, desktopFileName), []byte(content), 0o644); err != nil {
		return fmt.Errorf("write desktop file: %w", err)
	}

	// Best-effort: these tools may be missing on a minimal setup (e.g. a
	// headless dev box), and their absence shouldn't stop the proxy from
	// running — just means OS-level routing won't work until they're
	// available and this runs again.
	if err := exec.Command("update-desktop-database", appsDir).Run(); err != nil {
		return fmt.Errorf("update-desktop-database (desktop file was written to %s, but the OS won't pick it up until this succeeds): %w", appsDir, err)
	}
	if err := exec.Command("xdg-mime", "default", desktopFileName, "x-scheme-handler/"+Scheme).Run(); err != nil {
		return fmt.Errorf("xdg-mime default: %w", err)
	}
	return nil
}

func installHandler(callback func(rawURL string)) {
	// Linux always spawns a new process for a custom-scheme URL; there is
	// no "already running" delivery mechanism to hook here.
}
