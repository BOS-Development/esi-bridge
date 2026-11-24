//go:build !windows && !darwin

package update

import "errors"

func canSelfInstall() (bool, string) {
	return false, "Automatic installs aren't supported on this platform. Download the new version from the release page."
}

func platformInstall(string) error {
	return errors.New("automatic installs aren't supported on this platform")
}
