package buildinfo

// Version is the release version this binary was built as, e.g. "1.0.42".
// The packaging scripts inject it at build time with
//
//	-ldflags "-X github.com/BOS-Development/esi-proxy/internal/buildinfo.Version=1.0.42"
//
// It's empty for local builds that weren't given a version; the auto-updater
// treats those as unversioned and never offers them an update.
var Version = ""

// DisplayVersion is Version, or "dev" for an unversioned build.
func DisplayVersion() string {
	if Version == "" {
		return "dev"
	}
	return Version
}
