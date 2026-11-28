//go:build windows

package urlscheme

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows/registry"
)

// register writes HKCU\Software\Classes\eveauth-proxy so Windows routes
// eveauth-proxy:// URLs to this executable, spawning a new process with the
// URL as argv[1] (handled by the forwarder-mode branch in cmd/esi-bridge).
// Idempotent — safe to call on every startup.
func register() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	protoKey, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\`+Scheme, registry.ALL_ACCESS)
	if err != nil {
		return fmt.Errorf("create protocol key: %w", err)
	}
	defer protoKey.Close()
	if err := protoKey.SetStringValue("", "URL:ESI Bridge Auth Callback"); err != nil {
		return fmt.Errorf("set protocol description: %w", err)
	}
	if err := protoKey.SetStringValue("URL Protocol", ""); err != nil {
		return fmt.Errorf("set URL Protocol marker: %w", err)
	}

	cmdKey, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\`+Scheme+`\shell\open\command`, registry.ALL_ACCESS)
	if err != nil {
		return fmt.Errorf("create command key: %w", err)
	}
	defer cmdKey.Close()
	if err := cmdKey.SetStringValue("", fmt.Sprintf(`"%s" "%%1"`, exe)); err != nil {
		return fmt.Errorf("set open command: %w", err)
	}
	return nil
}

func installHandler(callback func(rawURL string)) {
	// Windows always spawns a new process for a custom-scheme URL; there is
	// no "already running" delivery mechanism to hook here.
}
