//go:build darwin

package urlscheme

/*
#cgo LDFLAGS: -framework Cocoa
void esiproxy_installURLEventHandler(void);
*/
import "C"

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// lsregister is the standard Launch Services tool for (re-)registering an
// app bundle; there's no public API for this, only this binary.
const lsregisterPath = "/System/Library/Frameworks/CoreServices.framework/Versions/A/Frameworks/LaunchServices.framework/Versions/A/Support/lsregister"

// register associates the running .app bundle with Scheme via Launch
// Services. Launch Services can only route a custom URL scheme to a proper
// .app bundle (Info.plist CFBundleURLTypes) — there is no way to register a
// bare Mach-O binary, so this fails descriptively when not running from one
// (e.g. a plain `go build && ./esi-proxy`, rather than
// scripts/build-macos-app.sh).
func register() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	bundlePath, ok := bundlePathFor(exe)
	if !ok {
		return fmt.Errorf("not running from an .app bundle — build and run via scripts/build-macos-app.sh for eveauth-proxy:// login to work on macOS")
	}

	if err := exec.Command(lsregisterPath, "-f", bundlePath).Run(); err != nil {
		return fmt.Errorf("lsregister -f %s: %w", bundlePath, err)
	}
	return nil
}

// bundlePathFor walks up from an executable path looking for the
// Contents/MacOS layout of an .app bundle, returning the bundle root.
func bundlePathFor(exe string) (string, bool) {
	macOSDir := filepath.Dir(exe) // .../ESI Proxy.app/Contents/MacOS
	if filepath.Base(macOSDir) != "MacOS" {
		return "", false
	}
	contentsDir := filepath.Dir(macOSDir) // .../ESI Proxy.app/Contents
	if filepath.Base(contentsDir) != "Contents" {
		return "", false
	}
	bundle := filepath.Dir(contentsDir) // .../ESI Proxy.app
	if !strings.HasSuffix(bundle, ".app") {
		return "", false
	}
	return bundle, true
}

var urlCallback func(string)

func installHandler(callback func(rawURL string)) {
	urlCallback = callback
	C.esiproxy_installURLEventHandler()
}

//export goHandleAuthCallbackURL
func goHandleAuthCallbackURL(cURL *C.char) {
	if urlCallback != nil {
		urlCallback(C.GoString(cURL))
	}
}
