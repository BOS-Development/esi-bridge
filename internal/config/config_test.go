package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCreatesDefaultsWhenMissing(t *testing.T) {
	dir := t.TempDir()

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Port != 8085 {
		t.Errorf("Port = %d, want 8085", cfg.Port)
	}
	if cfg.BindHost != "localhost" {
		t.Errorf("BindHost = %q, want localhost", cfg.BindHost)
	}
	if cfg.DataDir != dir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
		t.Errorf("expected Load to persist config.json: %v", err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := Config{
		Port:          9000,
		BindHost:      "0.0.0.0",
		DataDir:       dir,
		EVEClientID:   "client-abc",
		EVESSOBaseURL: "https://login.example",
		ESIBaseURL:    "https://esi.example",
	}

	if err := Save(dir, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

// TestLoadBackfillsBindHost guards the migration path: a config.json written
// before bind_host existed must come back bound to localhost, not "".
func TestLoadBackfillsBindHost(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"port":8085,"eve_client_id":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BindHost != "localhost" {
		t.Errorf("BindHost = %q, want localhost fallback", cfg.BindHost)
	}
	if cfg.DataDir != dir {
		t.Errorf("DataDir = %q, want %q fallback", cfg.DataDir, dir)
	}
}

func TestLoadRejectsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("expected an error loading malformed config.json")
	}
}

func TestDirCreatesConfigDir(t *testing.T) {
	// Point the OS config-dir lookup at a temp HOME so the test doesn't touch
	// the real user config directory. XDG_CONFIG_HOME is cleared so the Linux
	// lookup falls back to $HOME/.config; macOS uses $HOME/Library/... .
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")

	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("expected Dir to create a directory, got %q (err %v)", dir, err)
	}
	if filepath.Base(dir) != "esi-proxy" {
		t.Errorf("Dir base = %q, want esi-proxy", filepath.Base(dir))
	}
}
