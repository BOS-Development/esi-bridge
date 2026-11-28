package buildinfo

import "testing"

func TestDisplayVersion(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })

	Version = ""
	if got := DisplayVersion(); got != "dev" {
		t.Errorf("DisplayVersion() with no version = %q, want dev", got)
	}
	Version = "1.0.42"
	if got := DisplayVersion(); got != "1.0.42" {
		t.Errorf("DisplayVersion() = %q, want 1.0.42", got)
	}
}
