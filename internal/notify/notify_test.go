package notify

import (
	"testing"

	"github.com/gen2brain/beeep"
)

// TestInitSetsAppName verifies the package init wired beeep's app name, which
// is what desktop notifications are attributed to. Send itself is a thin
// wrapper over the OS notification daemon and is intentionally not invoked
// here — doing so would fire a real notification on the host.
func TestInitSetsAppName(t *testing.T) {
	if beeep.AppName != "ESI Proxy" {
		t.Errorf("beeep.AppName = %q, want ESI Proxy", beeep.AppName)
	}
}
