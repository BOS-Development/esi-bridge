package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// innoUninstallKey is where Inno Setup records the install. The GUID is the
// AppId in packaging/windows/esi-proxy.iss; keep them in step.
const innoUninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\{2642D8DA-C1C0-42A3-92C2-037FF57CE3DE}_is1`

// canSelfInstall reports whether the running exe is the one the installer
// put in place. A copy run from anywhere else (a build folder, a download)
// would be left untouched by the installer, so it's pointed at the release
// page instead.
func canSelfInstall() (bool, string) {
	exe, err := os.Executable()
	if err != nil {
		return false, "Couldn't locate the running program."
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	exeDir := filepath.Clean(filepath.Dir(exe))
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		k, err := registry.OpenKey(root, innoUninstallKey, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		loc, _, err := k.GetStringValue("InstallLocation")
		k.Close()
		if err == nil && strings.EqualFold(filepath.Clean(loc), exeDir) {
			return true, ""
		}
	}
	return false, "This copy of ESI Bridge wasn't installed with the installer, so it can't update itself. Download the new version from the release page."
}

// platformInstall starts the downloaded Inno Setup installer and returns
// once it's running; the caller then quits so the installer can replace the
// files. /SILENT shows only a progress window, and reuses the existing install
// folder and options. /RELAUNCH=1 makes the installer start the new version
// when it's done (see ShouldRelaunch in esi-proxy.iss).
func platformInstall(path string) error {
	logPath := filepath.Join(filepath.Dir(path), "install.log")
	cmd := exec.Command(path,
		"/SILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/CLOSEAPPLICATIONS",
		"/RELAUNCH=1", "/LOG="+logPath)
	// Detach so the installer outlives this process.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start installer: %w", err)
	}
	return cmd.Process.Release()
}
