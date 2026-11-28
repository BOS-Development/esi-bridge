// Package update keeps the app up to date from the project's GitHub
// releases.
//
// The Updater checks GitHub's "latest release" for the repo at startup and
// periodically after that. When a newer version is out it tells the user,
// through the OnAvailable hook (a desktop notification), the tray and the
// Settings view, but nothing is installed until the user asks. Installing
// downloads this platform's installer from the release, checks it against the
// SHA-256 digest GitHub recorded for the upload, hands it to the platform's
// installer (see apply_*.go) and quits so the new version can start.
//
// Unversioned (development) builds never check.
package update

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Repo is the GitHub repository releases are published to.
const Repo = "BOS-Development/esi-bridge"

const (
	defaultAPIBaseURL = "https://api.github.com"
	firstCheckDelay   = 30 * time.Second
	checkInterval     = 6 * time.Hour
)

// State is where the updater is in its check → download → install cycle.
type State string

const (
	StateIdle        State = "idle"
	StateChecking    State = "checking"
	StateUpToDate    State = "up_to_date"
	StateAvailable   State = "available"
	StateDownloading State = "downloading"
	StateInstalling  State = "installing"
	StateError       State = "error"
)

// Status is a snapshot of the updater for the UI.
type Status struct {
	CurrentVersion string `json:"current_version"`
	// Enabled is false for unversioned builds, which never check.
	Enabled   bool   `json:"enabled"`
	AutoCheck bool   `json:"auto_check"`
	State     State  `json:"state"`
	Error     string `json:"error,omitempty"`
	// CheckedAt is when the last check finished (RFC3339), or empty.
	CheckedAt     string `json:"checked_at,omitempty"`
	LatestVersion string `json:"latest_version,omitempty"`
	ReleaseURL    string `json:"release_url,omitempty"`
	// CanInstall says whether the available update can be installed from
	// inside the app; when false, InstallHint says why and the user is
	// pointed at ReleaseURL instead.
	CanInstall  bool   `json:"can_install"`
	InstallHint string `json:"install_hint,omitempty"`
	// Progress is the download's completion, 0–100, while downloading.
	Progress int `json:"progress"`
}

// Options configures an Updater.
type Options struct {
	// CurrentVersion is the running build's version (buildinfo.Version).
	// Empty disables the updater.
	CurrentVersion string
	AutoCheck      bool
	Log            *slog.Logger
	// Quit is called once an update has been handed to the installer, to
	// shut the app down so it can be replaced.
	Quit func()
	// OnAvailable, if set, is called once per newly-found version.
	OnAvailable func(Release)
	// OnInstallFailed, if set, is called when an install started with
	// Install fails partway (download, verification or the installer).
	OnInstallFailed func(error)

	// For tests.
	APIBaseURL string
	Repo       string
	HTTPClient *http.Client
	GOOS       string
	// Installer overrides the platform installer.
	Installer func(path string) error
	// CanSelfInstall overrides the platform check for whether this copy of
	// the app can update itself.
	CanSelfInstall func() (bool, string)
}

// Updater checks for, downloads and installs new releases. Its methods are
// safe for concurrent use.
type Updater struct {
	current     version
	currentRaw  string
	apiBaseURL  string
	repo        string
	client      *http.Client
	goos        string
	log         *slog.Logger
	quit        func()
	onAvailable func(Release)
	onFailed    func(error)
	installer   func(path string) error
	canSelf     func() (bool, string)

	// checkNow wakes the background loop for an immediate check.
	checkNow chan struct{}

	mu           sync.Mutex
	autoCheck    bool
	state        State
	err          string
	checkedAt    time.Time
	latest       *Release
	notified     string
	progress     int
	installing   bool
	canInstall   bool
	installHint  string
	installKnown bool
}

// New returns an Updater. Call Run to start background checks.
func New(opts Options) *Updater {
	u := &Updater{
		currentRaw:  opts.CurrentVersion,
		apiBaseURL:  strings.TrimRight(opts.APIBaseURL, "/"),
		repo:        opts.Repo,
		client:      opts.HTTPClient,
		goos:        opts.GOOS,
		log:         opts.Log,
		quit:        opts.Quit,
		onAvailable: opts.OnAvailable,
		onFailed:    opts.OnInstallFailed,
		installer:   opts.Installer,
		canSelf:     opts.CanSelfInstall,
		autoCheck:   opts.AutoCheck,
		state:       StateIdle,
		checkNow:    make(chan struct{}, 1),
	}
	if v, err := parseVersion(opts.CurrentVersion); err == nil {
		u.current = v
	}
	if u.apiBaseURL == "" {
		u.apiBaseURL = defaultAPIBaseURL
	}
	if u.repo == "" {
		u.repo = Repo
	}
	if u.client == nil {
		u.client = &http.Client{Timeout: 10 * time.Minute}
	}
	if u.goos == "" {
		u.goos = runtime.GOOS
	}
	if u.log == nil {
		u.log = slog.New(slog.DiscardHandler)
	}
	if u.installer == nil {
		u.installer = platformInstall
	}
	if u.canSelf == nil {
		u.canSelf = canSelfInstall
	}
	return u
}

// Enabled reports whether this build checks for updates at all.
func (u *Updater) Enabled() bool { return u.current != nil }

// Run checks for updates in the background (when auto-check is on) until ctx
// is canceled. Manual checks requested with CheckNow are served here too.
func (u *Updater) Run(ctx context.Context) {
	if !u.Enabled() {
		return
	}
	cleanupOldDownloads(u.log)

	timer := time.NewTimer(firstCheckDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-u.checkNow:
			u.check(ctx)
		case <-timer.C:
			if u.AutoCheck() {
				u.check(ctx)
			}
			timer.Reset(checkInterval)
		}
	}
}

// CheckNow asks the background loop to check right away. It doesn't wait for
// the result; poll Status.
func (u *Updater) CheckNow() error {
	if !u.Enabled() {
		return errors.New("this is a development build; updates are disabled")
	}
	u.mu.Lock()
	if u.installing {
		u.mu.Unlock()
		return errors.New("an update is already being installed")
	}
	u.state = StateChecking
	u.err = ""
	u.mu.Unlock()
	select {
	case u.checkNow <- struct{}{}:
	default: // a check is already queued
	}
	return nil
}

// AutoCheck reports whether periodic checks are on.
func (u *Updater) AutoCheck() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.autoCheck
}

// SetAutoCheck turns periodic checks on or off. The caller persists it.
func (u *Updater) SetAutoCheck(on bool) {
	u.mu.Lock()
	u.autoCheck = on
	u.mu.Unlock()
}

// Status returns a snapshot for the UI.
func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	st := Status{
		CurrentVersion: u.currentRaw,
		Enabled:        u.Enabled(),
		AutoCheck:      u.autoCheck,
		State:          u.state,
		Error:          u.err,
		Progress:       u.progress,
	}
	if st.CurrentVersion == "" {
		st.CurrentVersion = "dev"
	}
	if !u.checkedAt.IsZero() {
		st.CheckedAt = u.checkedAt.UTC().Format(time.RFC3339)
	}
	if u.latest != nil {
		st.LatestVersion = u.latest.Version
		st.ReleaseURL = u.latest.URL
		st.CanInstall = u.canInstall
		st.InstallHint = u.installHint
	}
	return st
}

// Available returns the newer release found by the last check, or nil.
func (u *Updater) Available() *Release {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.latest == nil {
		return nil
	}
	r := *u.latest
	return &r
}

func (u *Updater) check(ctx context.Context) {
	u.mu.Lock()
	if u.installing {
		u.mu.Unlock()
		return
	}
	u.state = StateChecking
	u.err = ""
	u.mu.Unlock()

	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	gh, err := u.fetchLatest(checkCtx)
	var rel *Release
	if err == nil {
		rel, err = newerRelease(gh, u.current, u.goos)
	}

	u.mu.Lock()
	if u.installing {
		// Install started while we were checking; leave its state alone.
		u.mu.Unlock()
		return
	}
	u.checkedAt = time.Now()
	if err != nil {
		u.state = StateError
		u.err = err.Error()
		u.mu.Unlock()
		u.log.Warn("update check failed", "error", err)
		return
	}
	if rel == nil {
		u.latest = nil
		u.state = StateUpToDate
		u.mu.Unlock()
		return
	}
	u.latest = rel
	u.state = StateAvailable
	if !u.installKnown {
		u.canInstall, u.installHint = u.canSelf()
		u.installKnown = true
	}
	if u.canInstall && rel.Asset.URL == "" {
		u.canInstall, u.installHint = false, "This release has no installer for your platform."
	}
	notify := u.notified != rel.Version
	u.notified = rel.Version
	u.mu.Unlock()

	u.log.Info("update available", "current", u.currentRaw, "latest", rel.Version)
	if notify && u.onAvailable != nil {
		u.onAvailable(*rel)
	}
}

// Install downloads and installs the available update in the background,
// then quits the app. It returns an error right away if there's nothing to
// install or this copy can't update itself; later failures show up in
// Status.
func (u *Updater) Install() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	switch {
	case u.installing:
		return errors.New("an update is already being installed")
	case u.latest == nil:
		return errors.New("no update is available")
	case !u.canInstall:
		return errors.New(u.installHint)
	}
	u.installing = true
	u.state = StateDownloading
	u.err = ""
	u.progress = 0
	rel := *u.latest
	go u.install(rel)
	return nil
}

func (u *Updater) install(rel Release) {
	err := u.downloadAndApply(rel)
	if err == nil {
		u.log.Info("update handed to installer; quitting", "version", rel.Version)
		if u.quit != nil {
			u.quit()
		}
		return
	}
	u.log.Error("install update", "version", rel.Version, "error", err)
	u.mu.Lock()
	u.installing = false
	u.state = StateError
	u.err = err.Error()
	u.mu.Unlock()
	if u.onFailed != nil {
		u.onFailed(err)
	}
}

func (u *Updater) downloadAndApply(rel Release) error {
	dir, err := os.MkdirTemp("", downloadDirPrefix)
	if err != nil {
		return fmt.Errorf("create download folder: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	path, err := u.download(ctx, rel.Asset, dir, func(done, total int64) {
		pct := int(done * 100 / total)
		u.mu.Lock()
		u.progress = pct
		u.mu.Unlock()
	})
	if err != nil {
		os.RemoveAll(dir)
		return err
	}

	u.mu.Lock()
	u.state = StateInstalling
	u.progress = 100
	u.mu.Unlock()

	if err := u.installer(path); err != nil {
		os.RemoveAll(dir)
		return err
	}
	// The download is left in place: on Windows the installer is still
	// running from it. cleanupOldDownloads removes it on a later start.
	return nil
}

const downloadDirPrefix = "esi-bridge-update-"

// cleanupOldDownloads removes download folders left by earlier updates.
func cleanupOldDownloads(log *slog.Logger) {
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), downloadDirPrefix+"*"))
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil || !info.IsDir() || time.Since(info.ModTime()) < time.Hour {
			continue
		}
		if err := os.RemoveAll(m); err != nil {
			log.Debug("remove old update download", "path", m, "error", err)
		}
	}
}
