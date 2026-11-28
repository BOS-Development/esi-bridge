package update

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// bundlePath returns the .app bundle the running binary lives in
// (…/ESI Bridge.app/Contents/MacOS/esi-bridge → …/ESI Bridge.app).
func bundlePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	macOS := filepath.Dir(exe)
	contents := filepath.Dir(macOS)
	bundle := filepath.Dir(contents)
	if filepath.Base(macOS) != "MacOS" || filepath.Base(contents) != "Contents" || filepath.Ext(bundle) != ".app" {
		return "", errors.New("not running from an .app bundle")
	}
	return bundle, nil
}

func canSelfInstall() (bool, string) {
	bundle, err := bundlePath()
	if err != nil {
		return false, "ESI Bridge isn't running from an app bundle, so it can't update itself. Download the new version from the release page."
	}
	if strings.Contains(bundle, "/AppTranslocation/") || strings.HasPrefix(bundle, "/Volumes/") {
		return false, "ESI Bridge is running from the disk image or a temporary location. Drag it into Applications first, then it can update itself."
	}
	// Swapping the bundle means renaming it, which needs write access to
	// the folder it's in.
	probe, err := os.CreateTemp(filepath.Dir(bundle), ".esi-proxy-update-probe-*")
	if err != nil {
		return false, "ESI Bridge can't write to " + filepath.Dir(bundle) + ", so it can't update itself. Download the new version from the release page."
	}
	probe.Close()
	os.Remove(probe.Name())
	return true, ""
}

// platformInstall replaces the running .app bundle with the one in the
// downloaded disk image, then starts a helper that reopens the app once this
// process has exited. The old bundle is only removed after the new one is in
// place, and is put back if the swap fails.
func platformInstall(dmg string) error {
	bundle, err := bundlePath()
	if err != nil {
		return err
	}
	parent := filepath.Dir(bundle)
	pid := strconv.Itoa(os.Getpid())

	mount, err := os.MkdirTemp("", "esi-proxy-dmg-")
	if err != nil {
		return err
	}
	defer os.Remove(mount)
	if out, err := exec.Command("hdiutil", "attach", "-nobrowse", "-readonly", "-noautoopen",
		"-mountpoint", mount, dmg).CombinedOutput(); err != nil {
		return fmt.Errorf("mount update disk image: %v: %s", err, strings.TrimSpace(string(out)))
	}
	detach := func() { _ = exec.Command("hdiutil", "detach", "-force", mount).Run() }

	apps, _ := filepath.Glob(filepath.Join(mount, "*.app"))
	if len(apps) != 1 {
		detach()
		return fmt.Errorf("update disk image should hold one .app, found %d", len(apps))
	}

	// Copy onto the same volume as the installed bundle first, so the swap
	// below is two atomic renames. ditto keeps the code signature intact.
	staged := filepath.Join(parent, ".esi-proxy-update-"+pid+".app")
	os.RemoveAll(staged)
	out, err := exec.Command("ditto", apps[0], staged).CombinedOutput()
	detach()
	if err != nil {
		os.RemoveAll(staged)
		return fmt.Errorf("copy new version: %v: %s", err, strings.TrimSpace(string(out)))
	}

	backup := filepath.Join(parent, ".esi-proxy-old-"+pid+".app")
	if err := os.Rename(bundle, backup); err != nil {
		os.RemoveAll(staged)
		return fmt.Errorf("move old version aside: %w", err)
	}
	if err := os.Rename(staged, bundle); err != nil {
		if rerr := os.Rename(backup, bundle); rerr != nil {
			return fmt.Errorf("install new version: %v (and restoring the old one failed: %v; it's at %s)", err, rerr, backup)
		}
		os.RemoveAll(staged)
		return fmt.Errorf("install new version: %w", err)
	}
	os.RemoveAll(backup)

	// Re-register with Launch Services so eveauth-proxy:// still routes here.
	_ = exec.Command("/System/Library/Frameworks/CoreServices.framework/Versions/A/Frameworks/LaunchServices.framework/Versions/A/Support/lsregister",
		"-f", bundle).Run()

	// Wait for this process to exit, then open the new version. `open` on a
	// still-running app would only bring the old one to the front.
	relaunch := exec.Command("/bin/sh", "-c",
		`while kill -0 "$1" 2>/dev/null; do sleep 0.2; done; open "$2"`, "sh", pid, bundle)
	relaunch.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := relaunch.Start(); err != nil {
		return fmt.Errorf("schedule relaunch: %w", err)
	}
	return relaunch.Process.Release()
}
