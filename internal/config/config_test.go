package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/BOS-Development/esi-bridge/internal/buildinfo"
)

func withBundledClientID(t *testing.T, id string) {
	t.Helper()
	prev := buildinfo.BundledClientID
	buildinfo.BundledClientID = id
	t.Cleanup(func() { buildinfo.BundledClientID = prev })
}

func TestClientIDSelection(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name    string
		bundled string
		cfg     Config
		custom  bool
		want    string
	}{
		{"fresh install uses bundled", "bundled", Config{}, false, "bundled"},
		{"legacy config with an ID keeps it", "bundled", Config{EVEClientID: "mine"}, true, "mine"},
		{"explicit custom", "bundled", Config{EVEClientID: "mine", UseCustomClientID: &yes}, true, "mine"},
		{"explicit bundled ignores saved ID", "bundled", Config{EVEClientID: "mine", UseCustomClientID: &no}, false, "bundled"},
		{"no bundled ID forces custom", "", Config{EVEClientID: "mine", UseCustomClientID: &no}, true, "mine"},
		{"no bundled ID and none saved", "", Config{}, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withBundledClientID(t, tc.bundled)
			if got := tc.cfg.CustomClientID(); got != tc.custom {
				t.Errorf("CustomClientID() = %v, want %v", got, tc.custom)
			}
			if got := tc.cfg.ClientID(); got != tc.want {
				t.Errorf("ClientID() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDirInMovesLegacyDir(t *testing.T) {
	base := t.TempDir()
	legacy := filepath.Join(base, legacyDirName)
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "marker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir, err := dirIn(base)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(base, dirName); dir != want {
		t.Fatalf("dirIn = %q, want %q", dir, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "marker")); err != nil {
		t.Errorf("legacy contents not moved: %v", err)
	}
	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("legacy dir still exists (err = %v)", err)
	}
}

func TestDirInKeepsExistingDir(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{dirName, legacyDirName} {
		if err := os.MkdirAll(filepath.Join(base, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := dirIn(base)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(base, dirName); dir != want {
		t.Fatalf("dirIn = %q, want %q", dir, want)
	}
	if _, err := os.Stat(filepath.Join(base, legacyDirName)); err != nil {
		t.Errorf("legacy dir should be left alone when the new one exists: %v", err)
	}
}

func TestLoadRewritesLegacyDataDir(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, dirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := defaults(filepath.Join(base, legacyDirName))
	if err := Save(dir, old); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != dir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, dir)
	}
}
