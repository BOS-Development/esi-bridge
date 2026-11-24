package server_test

import (
	"net/http"
	"testing"
)

// Without an updater wired in (as in serve-ui), the state still reports the
// running version, the update endpoints need the session token, and check /
// install answer with a clear refusal rather than failing.
func TestUpdateEndpointsWithoutUpdater(t *testing.T) {
	ts, _, _ := newTestServer(t)
	token := uiToken(t, ts)

	state := manageState(t, ts, token)
	upd, ok := state["update"].(map[string]any)
	if !ok {
		t.Fatalf("state has no update object: %v", state["update"])
	}
	if upd["current_version"] != "dev" || upd["enabled"] != false {
		t.Fatalf("update status = %v, want unversioned and disabled", upd)
	}

	for _, path := range []string{"/manage/update/check", "/manage/update/install", "/manage/update/auto-check"} {
		resp := manageReq(t, ts, http.MethodPost, path, "", map[string]bool{"enabled": true})
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("POST %s without token: status = %d, want 403", path, resp.StatusCode)
		}
	}
	for _, path := range []string{"/manage/update/check", "/manage/update/install"} {
		resp := manageReq(t, ts, http.MethodPost, path, token, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("POST %s: status = %d, want 409", path, resp.StatusCode)
		}
	}

	resp := manageReq(t, ts, http.MethodPost, "/manage/update/auto-check", token, map[string]bool{"enabled": false})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /manage/update/auto-check: status = %d, want 204", resp.StatusCode)
	}
}
