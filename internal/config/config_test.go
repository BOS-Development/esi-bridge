package config

import (
	"testing"

	"github.com/BOS-Development/esi-proxy/internal/buildinfo"
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
