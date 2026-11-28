package server_test

import (
	"net/http"
	"testing"

	"github.com/BOS-Development/esi-bridge/internal/buildinfo"
)

// The developer-only endpoints must exist only in development builds, and the
// UI's dev_build flag must match. Run with and without -tags release to cover both.
// The dev endpoint isn't called with a valid token here, since that would pop a
// real desktop notification on the machine running the tests.
func TestDevEndpointsFollowBuildTag(t *testing.T) {
	ts, _, _ := newTestServer(t)
	token := uiToken(t, ts)

	state := manageState(t, ts, token)
	if got, _ := state["dev_build"].(bool); got != buildinfo.Dev {
		t.Fatalf("dev_build = %v, want %v", state["dev_build"], buildinfo.Dev)
	}

	if buildinfo.Dev {
		resp := manageReq(t, ts, http.MethodPost, "/manage/dev/test-notification", "", nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("dev endpoint without session token: status = %d, want 403", resp.StatusCode)
		}
		return
	}

	resp := manageReq(t, ts, http.MethodPost, "/manage/dev/test-notification", token, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("dev endpoint in release build: status = %d, want 404", resp.StatusCode)
	}
}
