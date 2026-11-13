package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// state is this example's tiny persisted store: the api_key issued per proxy
// base URL, so re-runs reuse the same registration (and thus the same
// approvals) instead of making the user approve a fresh app every time. It
// mirrors how the proxy itself keeps a small JSON file under the OS config
// directory.
type state struct {
	APIKeys map[string]string `json:"api_keys"` // base URL -> api_key
}

func statePath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "esi-proxy-example")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "state.json"), nil
}

// loadState reads the saved state, returning an empty (usable) state if none
// exists or it can't be read — persistence here is a convenience, never a
// hard dependency.
func loadState() *state {
	s := &state{APIKeys: map[string]string{}}
	path, err := statePath()
	if err != nil {
		return s
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(data, s)
	if s.APIKeys == nil {
		s.APIKeys = map[string]string{}
	}
	return s
}

func (s *state) keyFor(baseURL string) string { return s.APIKeys[baseURL] }

func (s *state) setKey(baseURL, key string) { s.APIKeys[baseURL] = key }

func (s *state) save() error {
	path, err := statePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
