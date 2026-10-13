//go:build darwin

package urlscheme

import (
	"io"
	"log/slog"
	"testing"
)

func TestScheme(t *testing.T) {
	if Scheme != "eveauth-proxy" {
		t.Errorf("Scheme = %q, want eveauth-proxy", Scheme)
	}
}

func TestBundlePathFor(t *testing.T) {
	tests := []struct {
		name   string
		exe    string
		want   string
		wantOK bool
	}{
		{"inside an app bundle", "/Applications/ESI Proxy.app/Contents/MacOS/esi-proxy", "/Applications/ESI Proxy.app", true},
		{"nested bundle path", "/Users/x/build/Foo.app/Contents/MacOS/foo", "/Users/x/build/Foo.app", true},
		{"bare binary", "/usr/local/bin/esi-proxy", "", false},
		{"wrong leaf dir", "/opt/ESI Proxy.app/Contents/bin/esi-proxy", "", false},
		{"missing .app suffix", "/opt/Proxy/Contents/MacOS/esi-proxy", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := bundlePathFor(tt.exe)
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("bundlePathFor(%q) = (%q, %v), want (%q, %v)", tt.exe, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// TestRegisterOutsideBundle confirms register fails descriptively (rather than
// mutating Launch Services) when the running binary — here the go test
// binary — is not inside an .app bundle. Register must not panic.
func TestRegisterOutsideBundle(t *testing.T) {
	if err := register(); err == nil {
		t.Error("expected register() to fail when not run from an .app bundle")
	}
	Register(slog.New(slog.NewTextHandler(io.Discard, nil))) // must not panic
}
