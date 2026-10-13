package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/BOS-Development/esi-proxy/internal/store"
)

// TestListCharactersFillsAccountIDForGrantedSubset exercises the branch where
// an app has no list_characters permission (so it only sees characters it was
// granted) but does hold list_accounts — which triggers fillAccountIDs to
// attach account_id to each granted character.
func TestListCharactersFillsAccountIDForGrantedSubset(t *testing.T) {
	ts, st, _ := newTestServer(t)
	appID, apiKey := registerApp(t, ts, "Pyfa")

	// A per-character grant (not the list_characters app permission).
	reqBody, _ := json.Marshal(map[string]any{"character_id": testCharacterID, "scopes": []string{"esi-assets.read_assets.v1"}})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/request-access", bytes.NewReader(reqBody))
	req.Header.Set("X-Proxy-Api-Key", apiKey)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	pending, _ := st.ListPendingGrants()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending grant, got %d", len(pending))
	}
	if err := st.ApprovePendingGrant(pending[0].ID); err != nil {
		t.Fatalf("ApprovePendingGrant: %v", err)
	}

	// Grant list_accounts (but deliberately not list_characters).
	if err := st.RequestAppPermission(appID, store.PermissionListAccounts); err != nil {
		t.Fatalf("RequestAppPermission: %v", err)
	}
	pp, _ := st.ListPendingAppPermissions()
	if len(pp) != 1 {
		t.Fatalf("expected 1 pending app permission, got %d", len(pp))
	}
	if err := st.ApprovePendingAppPermission(pp[0].ID); err != nil {
		t.Fatalf("ApprovePendingAppPermission: %v", err)
	}

	listReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/characters", nil)
	listReq.Header.Set("X-Proxy-Api-Key", apiKey)
	listResp, err := http.DefaultClient.Do(listReq)
	if err != nil {
		t.Fatalf("GET /api/characters: %v", err)
	}
	defer listResp.Body.Close()

	var out []map[string]any
	json.NewDecoder(listResp.Body).Decode(&out)
	if len(out) != 1 {
		t.Fatalf("characters = %v, want exactly the granted one", out)
	}
	if _, ok := out[0]["account_id"]; !ok {
		t.Errorf("expected account_id present (list_accounts granted), got %v", out[0])
	}
}
