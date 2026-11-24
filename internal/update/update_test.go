package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.42", "1.0.42", 0},
		{"v1.0.42", "1.0.42", 0},
		{"1.0", "1.0.0", 0},
		{"1.0.9", "1.0.10", -1},
		{"1.1.0", "1.0.99", 1},
		{"2", "1.9.9", 1},
	}
	for _, c := range cases {
		a, err := parseVersion(c.a)
		if err != nil {
			t.Fatal(err)
		}
		b, err := parseVersion(c.b)
		if err != nil {
			t.Fatal(err)
		}
		if got := a.compare(b); got != c.want {
			t.Errorf("compare(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	for _, bad := range []string{"", "dev", "1.x", "1.-2", "1..2"} {
		if _, err := parseVersion(bad); err == nil {
			t.Errorf("parseVersion(%q) succeeded, want error", bad)
		}
	}
}

func TestNewerReleasePicksPlatformAsset(t *testing.T) {
	rel := &ghRelease{
		TagName: "v1.0.50",
		HTMLURL: "https://github.com/x/y/releases/tag/v1.0.50",
		Assets: []ghAsset{
			{Name: "esi-proxy-1.0.50-macos-universal.dmg", BrowserDownloadURL: "https://dl/mac", Size: 10, Digest: "sha256:AA"},
			{Name: "esi-proxy-1.0.50-windows-x64-setup.exe", BrowserDownloadURL: "https://dl/win", Size: 20, Digest: "sha256:bb"},
		},
	}
	cur, _ := parseVersion("1.0.49")

	got, err := newerRelease(rel, cur, "windows")
	if err != nil || got == nil {
		t.Fatalf("newerRelease = %v, %v", got, err)
	}
	if got.Version != "1.0.50" || got.Asset.URL != "https://dl/win" || got.Asset.SHA256 != "bb" {
		t.Fatalf("windows release = %+v", got)
	}
	if got, _ := newerRelease(rel, cur, "darwin"); got.Asset.URL != "https://dl/mac" || got.Asset.SHA256 != "aa" {
		t.Fatalf("darwin release = %+v", got)
	}
	if got, _ := newerRelease(rel, cur, "linux"); got == nil || got.Asset.URL != "" {
		t.Fatalf("linux release = %+v, want release with no asset", got)
	}

	same, _ := parseVersion("1.0.50")
	if got, _ := newerRelease(rel, same, "windows"); got != nil {
		t.Fatalf("same version: got %+v, want nil", got)
	}
	rel.Prerelease = true
	if got, _ := newerRelease(rel, cur, "windows"); got != nil {
		t.Fatalf("prerelease: got %+v, want nil", got)
	}
}

// fakeGitHub serves a latest release whose Windows asset is payload, with the
// given digest.
func fakeGitHub(t *testing.T, tag string, payload []byte, digest string) *httptest.Server {
	t.Helper()
	var ts *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("request without User-Agent")
		}
		json.NewEncoder(w).Encode(ghRelease{
			TagName: tag,
			HTMLURL: ts.URL + "/release",
			Assets: []ghAsset{{
				Name:               "esi-proxy-" + strings.TrimPrefix(tag, "v") + "-windows-x64-setup.exe",
				BrowserDownloadURL: ts.URL + "/asset",
				Size:               int64(len(payload)),
				Digest:             digest,
			}},
		})
	})
	mux.HandleFunc("GET /asset", func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	})
	ts = httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

func newTestUpdater(ts *httptest.Server, current string, installer func(string) error, quit func()) *Updater {
	return New(Options{
		CurrentVersion: current,
		APIBaseURL:     ts.URL,
		Repo:           "o/r",
		GOOS:           "windows",
		Installer:      installer,
		CanSelfInstall: func() (bool, string) { return true, "" },
		Quit:           quit,
	})
}

func waitFor(t *testing.T, u *Updater, want State) Status {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st := u.Status()
		if st.State == want {
			return st
		}
		time.Sleep(10 * time.Millisecond)
	}
	st := u.Status()
	t.Fatalf("state = %s (error %q), want %s", st.State, st.Error, want)
	return st
}

func TestCheckAndInstall(t *testing.T) {
	payload := []byte("pretend installer")
	ts := fakeGitHub(t, "v1.0.50", payload, sha(payload))

	var installed atomic.Value
	quit := make(chan struct{})
	u := newTestUpdater(ts, "1.0.49", func(path string) error {
		got, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		installed.Store(string(got))
		return nil
	}, func() { close(quit) })

	var notified []string
	u.onAvailable = func(r Release) { notified = append(notified, r.Version) }

	u.check(context.Background())
	st := u.Status()
	if st.State != StateAvailable || st.LatestVersion != "1.0.50" || !st.CanInstall {
		t.Fatalf("after check: %+v", st)
	}
	u.check(context.Background())
	if len(notified) != 1 {
		t.Fatalf("notified %v, want exactly once", notified)
	}

	if err := u.Install(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-quit:
	case <-time.After(5 * time.Second):
		t.Fatalf("quit not called; status %+v", u.Status())
	}
	if got, _ := installed.Load().(string); got != string(payload) {
		t.Fatalf("installer got %q, want %q", got, payload)
	}
}

func TestInstallRejectsDigestMismatch(t *testing.T) {
	ts := fakeGitHub(t, "v1.0.50", []byte("tampered"), sha([]byte("original")))
	called := false
	u := newTestUpdater(ts, "1.0.49", func(string) error { called = true; return nil }, nil)
	u.check(context.Background())
	if err := u.Install(); err != nil {
		t.Fatal(err)
	}
	st := waitFor(t, u, StateError)
	if !strings.Contains(st.Error, "SHA-256 mismatch") {
		t.Fatalf("error = %q, want SHA-256 mismatch", st.Error)
	}
	if called {
		t.Fatal("installer ran on a mismatched download")
	}
}

func TestInstallRequiresDigest(t *testing.T) {
	ts := fakeGitHub(t, "v1.0.50", []byte("x"), "")
	u := newTestUpdater(ts, "1.0.49", func(string) error { return nil }, nil)
	u.check(context.Background())
	if err := u.Install(); err != nil {
		t.Fatal(err)
	}
	if st := waitFor(t, u, StateError); !strings.Contains(st.Error, "no SHA-256 digest") {
		t.Fatalf("error = %q", st.Error)
	}
}

func TestUpToDate(t *testing.T) {
	ts := fakeGitHub(t, "v1.0.49", []byte("x"), sha([]byte("x")))
	u := newTestUpdater(ts, "1.0.49", nil, nil)
	u.check(context.Background())
	if st := u.Status(); st.State != StateUpToDate || st.LatestVersion != "" {
		t.Fatalf("status = %+v", st)
	}
	if err := u.Install(); err == nil {
		t.Fatal("Install with nothing available succeeded")
	}
}

func TestDevBuildDisabled(t *testing.T) {
	u := New(Options{CurrentVersion: ""})
	if u.Enabled() {
		t.Fatal("unversioned build is enabled")
	}
	if err := u.CheckNow(); err == nil {
		t.Fatal("CheckNow on dev build succeeded")
	}
	if st := u.Status(); st.CurrentVersion != "dev" || st.Enabled {
		t.Fatalf("status = %+v", st)
	}
}

func TestCannotSelfInstall(t *testing.T) {
	payload := []byte("x")
	ts := fakeGitHub(t, "v1.0.50", payload, sha(payload))
	u := New(Options{
		CurrentVersion: "1.0.49",
		APIBaseURL:     ts.URL,
		Repo:           "o/r",
		GOOS:           "windows",
		CanSelfInstall: func() (bool, string) { return false, "not installed" },
	})
	u.check(context.Background())
	st := u.Status()
	if st.State != StateAvailable || st.CanInstall || st.InstallHint != "not installed" {
		t.Fatalf("status = %+v", st)
	}
	if err := u.Install(); err == nil || err.Error() != "not installed" {
		t.Fatalf("Install err = %v", err)
	}
}
