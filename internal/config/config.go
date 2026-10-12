// Package config loads and persists the proxy's local configuration file.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Config holds the settings needed to run the proxy. ClientID must be filled
// in by the user on first run with a client_id from an EVE SSO application
// registered at developers.eveonline.com (redirect URI must point at
// http://localhost:<Port>/sso/callback).
type Config struct {
	Port int `json:"port"`
	// BindHost is the host the HTTP server binds to. The default,
	// "localhost", keeps the token-holding proxy reachable only from this
	// machine; set it to "0.0.0.0" (or a specific LAN address) to let other
	// machines on the network connect to the proxy they discover over mDNS.
	BindHost      string `json:"bind_host"`
	DataDir       string `json:"data_dir"`
	EVEClientID   string `json:"eve_client_id"`
	EVESSOBaseURL string `json:"eve_sso_base_url"`
	ESIBaseURL    string `json:"esi_base_url"`
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
