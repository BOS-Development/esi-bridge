package discovery

import (
	"io"
	"log/slog"
	"testing"
)

// TestAdvertiseRegistersAndShutsDown exercises the happy path. mDNS needs
// working multicast, which some sandboxed/CI environments lack; since
// advertising is best-effort by design, a registration failure skips rather
// than fails the test.
func TestAdvertiseRegistersAndShutsDown(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	stop, err := Advertise(log, 8085, "http://localhost:8085")
	if err != nil {
		t.Skipf("mDNS unavailable in this environment (best-effort feature): %v", err)
	}
	if stop == nil {
		t.Fatal("Advertise returned a nil shutdown func")
	}
	stop()
}
