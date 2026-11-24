// Package config loads and persists the proxy's local configuration file.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/BOS-Development/esi-proxy/internal/buildinfo"
)

// Config holds the settings needed to run the proxy. By default the proxy
// logs characters in with the EVE SSO application bundled with the build
// (buildinfo.BundledClientID). A user can instead register their own
// application at developers.eveonline.com (callback URL
// eveauth-proxy://callback) and set UseCustomClientID + EVEClientID.
type Config struct {
	Port int `json:"port"`
	// BindHost is the host the HTTP server binds to. The default,
	// "localhost", keeps the token-holding proxy reachable only from this
	// machine; set it to "0.0.0.0" (or a specific LAN address) to let other
	// machines on the network connect to the proxy they discover over mDNS.
	BindHost string `json:"bind_host"`
	DataDir  string `json:"data_dir"`
	// EVEClientID is the user's own client_id, used only when
	// CustomClientID() is true.
	EVEClientID string `json:"eve_client_id"`
	// UseCustomClientID selects EVEClientID over the bundled client_id.
	// It's a pointer so configs written before the bundled ID existed (no
	// field at all) keep using the client_id the user already entered.
	UseCustomClientID *bool  `json:"use_custom_client_id,omitempty"`
	EVESSOBaseURL     string `json:"eve_sso_base_url"`
	ESIBaseURL        string `json:"esi_base_url"`
	// CheckForUpdates turns the periodic check for new releases on or off.
	// It's a pointer so configs written before the updater existed get the
	// default (on); see AutoCheckUpdates.
	CheckForUpdates *bool `json:"check_for_updates,omitempty"`
}

// AutoCheckUpdates reports whether the app should check for new releases on
// its own. On unless the user turned it off.
func (c Config) AutoCheckUpdates() bool {
	return c.CheckForUpdates == nil || *c.CheckForUpdates
}

// CustomClientID reports whether the user's own client_id is in use rather
// than the bundled one. It's forced on when the build has no bundled ID.
func (c Config) CustomClientID() bool {
	if buildinfo.BundledClientID == "" {
		return true
	}
	if c.UseCustomClientID != nil {
		return *c.UseCustomClientID
	}
	// Legacy config with no explicit choice: honour an ID the user entered.
	return c.EVEClientID != ""
}

// ClientID returns the EVE SSO client_id the proxy should log in with.
func (c Config) ClientID() string {
	if c.CustomClientID() {
		return c.EVEClientID
	}
	return buildinfo.BundledClientID
}

func defaults(dataDir string) Config {
	return Config{
		Port:          8085,
		BindHost:      "localhost",
		DataDir:       dataDir,
		EVEClientID:   "",
		EVESSOBaseURL: "https://login.eveonline.com",
		ESIBaseURL:    "https://esi.evetech.net/latest",
	}
}

// Dir returns the OS-appropriate directory the proxy stores its config and
// database in, creating it if necessary.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "esi-proxy")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// Load reads the config file from dir, creating one populated with defaults
// if it doesn't exist yet.
func Load(dir string) (Config, error) {
	path := filepath.Join(dir, "config.json")

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg := defaults(dir)
		if werr := Save(dir, cfg); werr != nil {
			return Config{}, werr
		}
		return cfg, nil
	}
	if err != nil {
		return Config{}, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	if cfg.DataDir == "" {
		cfg.DataDir = dir
	}
	if cfg.BindHost == "" {
		cfg.BindHost = "localhost"
	}
	return cfg, nil
}

// Save writes cfg to dir/config.json.
func Save(dir string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600)
}
