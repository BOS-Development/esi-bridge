package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestAppPermissionsGateCharacterAndAccountVisibility(t *testing.T) {
	ts, st, _ := newTestServer(t)
	_, apiKey := registerApp(t, ts, "MCO")

	getCharacters := func() (int, []map[string]any) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/characters", nil)
		req.Header.Set("X-Proxy-Api-Key", apiKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /api/characters: %v", err)
		}
		defer resp.Body.Close()
		var out []map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	// No grant, no permission: nothing visible.
	if status, chars := getCharacters(); status != http.StatusOK || len(chars) != 0 {
		t.Fatalf("before any permission: status=%d chars=%v, want 200 and empty", status, chars)
	}

	// /api/accounts must refuse until list_accounts is granted.
	acctReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/accounts", nil)
	acctReq.Header.Set("X-Proxy-Api-Key", apiKey)
	acctResp, err := http.DefaultClient.Do(acctReq)
	if err != nil {
		t.Fatalf("GET /api/accounts: %v", err)
	}
	acctResp.Body.Close()
	if acctResp.StatusCode != http.StatusForbidden {
		t.Fatalf("/api/accounts before grant: status = %d, want 403", acctResp.StatusCode)
	}

	// Request both app-wide permissions.
	body, _ := json.Marshal(map[string]any{"permissions": []string{"list_characters", "list_accounts"}})
	permReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/request-permissions", bytes.NewReader(body))
	permReq.Header.Set("X-Proxy-Api-Key", apiKey)
	permResp, err := http.DefaultClient.Do(permReq)
	if err != nil {
		t.Fatalf("POST /api/request-permissions: %v", err)
	}
	permBody, _ := io.ReadAll(permResp.Body)
	permResp.Body.Close()
	if permResp.StatusCode != http.StatusAccepted {
		t.Fatalf("request-permissions status = %d, body = %s, want 202", permResp.StatusCode, permBody)
	}

	// Still gated: nothing approved yet.
	if status, chars := getCharacters(); status != http.StatusOK || len(chars) != 0 {
		t.Fatalf("after request but before approval: status=%d chars=%v, want 200 and empty", status, chars)
	}

	pending, err := st.ListPendingAppPermissions()
	if err != nil || len(pending) != 2 {
		t.Fatalf("ListPendingAppPermissions: got %d, err %v", len(pending), err)
	}
	for _, p := range pending {
		if err := st.ApprovePendingAppPermission(p.ID); err != nil {
			t.Fatalf("ApprovePendingAppPermission(%d): %v", p.ID, err)
		}
	}

	// Now both permissions are active: /api/characters lists everything
	// with account_id, and /api/accounts works.
	status, chars := getCharacters()
	if status != http.StatusOK || len(chars) != 1 {
		t.Fatalf("after approval: status=%d chars=%v, want 200 and 1 character", status, chars)
	}
	if _, ok := chars[0]["account_id"]; !ok {
		t.Fatalf("expected account_id present once list_accounts is granted, got %v", chars[0])
	}

	acctReq2, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/accounts", nil)
	acctReq2.Header.Set("X-Proxy-Api-Key", apiKey)
	acctResp2, err := http.DefaultClient.Do(acctReq2)
	if err != nil {
		t.Fatalf("GET /api/accounts: %v", err)
	}
	defer acctResp2.Body.Close()
	if acctResp2.StatusCode != http.StatusOK {
		t.Fatalf("/api/accounts after grant: status = %d, want 200", acctResp2.StatusCode)
	}
}

func TestRequestPermissionsRejectsUnknownPermission(t *testing.T) {
	ts, _, _ := newTestServer(t)
	_, apiKey := registerApp(t, ts, "Bogus")

	body, _ := json.Marshal(map[string]any{"permissions": []string{"delete_everything"}})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/request-permissions", bytes.NewReader(body))
	req.Header.Set("X-Proxy-Api-Key", apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/request-permissions: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}
