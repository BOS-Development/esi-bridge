package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"testing"
)

var tokenRe = regexp.MustCompile(`name="session-token" content="([0-9a-f]+)"`)

// uiToken fetches /app and extracts the injected session token, exercising the
// token-injection path the browser/webview relies on to authenticate.
func uiToken(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	resp, err := http.Get(ts.URL + "/app")
	if err != nil {
		t.Fatalf("GET /app: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /app status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if bytes.Contains(body, []byte("__SESSION_TOKEN__")) {
		t.Fatalf("GET /app still contains the token placeholder (not injected)")
	}
	m := tokenRe.FindSubmatch(body)
	if m == nil {
		t.Fatalf("GET /app did not contain an injected session token")
	}
	return string(m[1])
}

// manageReq issues a /manage request with the given session token and returns
// the response. An empty token omits the header entirely.
func manageReq(t *testing.T, ts *httptest.Server, method, path, token string, body any) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, ts.URL+path, rdr)
	if token != "" {
		req.Header.Set("X-Proxy-Session", token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func manageState(t *testing.T, ts *httptest.Server, token string) map[string]any {
	t.Helper()
	resp := manageReq(t, ts, http.MethodGet, "/manage/state", token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /manage/state status = %d, want 200", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	return out
}

// stateList returns the named array from a state snapshot (nil treated as empty).
func stateList(t *testing.T, state map[string]any, key string) []any {
	t.Helper()
	v, ok := state[key]
	if !ok || v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		t.Fatalf("state[%q] is not an array: %T", key, v)
	}
	return arr
}

// firstID returns the "id" of the first entry in the named state array.
func firstID(t *testing.T, state map[string]any, key string) string {
	t.Helper()
	arr := stateList(t, state, key)
	if len(arr) == 0 {
		t.Fatalf("state[%q] is empty, wanted at least one entry", key)
	}
	id := arr[0].(map[string]any)["id"].(float64)
	return strconv.FormatInt(int64(id), 10)
}

func TestAppPageInjectsTokenAndServesStatic(t *testing.T) {
	ts, _, _ := newTestServer(t)

	token := uiToken(t, ts)
	if len(token) != 64 { // 32 bytes hex-encoded
		t.Fatalf("session token = %q (len %d), want 64 hex chars", token, len(token))
	}

	resp, err := http.Get(ts.URL + "/app/static/app.js")
	if err != nil {
		t.Fatalf("GET /app/static/app.js: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /app/static/app.js status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("X-Proxy-Session")) {
		t.Fatalf("app.js did not look like the management script")
	}
}

func TestManageRequiresSessionToken(t *testing.T) {
	ts, _, _ := newTestServer(t)
	token := uiToken(t, ts)

	// No token → 403.
	resp := manageReq(t, ts, http.MethodGet, "/manage/state", "", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("state without token: status = %d, want 403", resp.StatusCode)
	}

	// Wrong token → 403.
	resp = manageReq(t, ts, http.MethodGet, "/manage/state", "deadbeef", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("state with wrong token: status = %d, want 403", resp.StatusCode)
	}

	// Correct token → 200.
	resp = manageReq(t, ts, http.MethodGet, "/manage/state", token, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("state with correct token: status = %d, want 200", resp.StatusCode)
	}
}

func TestManageGrantApprovalFlow(t *testing.T) {
	ts, _, _ := newTestServer(t)
	token := uiToken(t, ts)
	_, apiKey := registerApp(t, ts, "MCO")

	// App requests access → a pending grant appears in state.
	reqBody, _ := json.Marshal(map[string]any{
		"character_id": 999, // testCharacterID, seeded by newTestServer
		"scopes":       []string{"esi-assets.read_assets.v1"},
	})
	ra, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/request-access", bytes.NewReader(reqBody))
	ra.Header.Set("X-Proxy-Api-Key", apiKey)
	raResp, err := http.DefaultClient.Do(ra)
	if err != nil {
		t.Fatalf("request-access: %v", err)
	}
	raResp.Body.Close()

	state := manageState(t, ts, token)
	if n := len(stateList(t, state, "pending_grants")); n != 1 {
		t.Fatalf("pending_grants = %d, want 1", n)
	}
	pendingID := firstID(t, state, "pending_grants")

	// Approve → 204, grant becomes active.
	resp := manageReq(t, ts, http.MethodPost, "/manage/pending-grants/"+pendingID+"/approve", token, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("approve: status = %d, want 204", resp.StatusCode)
	}

	state = manageState(t, ts, token)
	if n := len(stateList(t, state, "pending_grants")); n != 0 {
		t.Fatalf("pending_grants after approve = %d, want 0", n)
	}
	if n := len(stateList(t, state, "active_grants")); n != 1 {
		t.Fatalf("active_grants after approve = %d, want 1", n)
	}
	grantID := firstID(t, state, "active_grants")

	// Revoke → 204, no active grants remain.
	resp = manageReq(t, ts, http.MethodPost, "/manage/grants/"+grantID+"/revoke", token, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke: status = %d, want 204", resp.StatusCode)
	}
	state = manageState(t, ts, token)
	if n := len(stateList(t, state, "active_grants")); n != 0 {
		t.Fatalf("active_grants after revoke = %d, want 0", n)
	}
}

func TestManageCharacterDetail(t *testing.T) {
	ts, _, _ := newTestServer(t)
	token := uiToken(t, ts)

	resp := manageReq(t, ts, http.MethodGet, "/manage/characters/999", token, nil) // seeded character
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("character detail: status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		ID              int64    `json:"id"`
		Name            string   `json:"name"`
		AvailableScopes []string `json:"available_scopes"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.ID != 999 || out.Name != "Test Character" {
		t.Fatalf("character detail = %+v, want id 999 / Test Character", out)
	}

	missing := manageReq(t, ts, http.MethodGet, "/manage/characters/424242", token, nil)
	missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown character: status = %d, want 404", missing.StatusCode)
	}
}

func TestManageStateCharacterTokenTimestamps(t *testing.T) {
	ts, _, _ := newTestServer(t)
	token := uiToken(t, ts)

	chars := stateList(t, manageState(t, ts, token), "characters")
	if len(chars) == 0 {
		t.Fatal("expected the seeded character in state")
	}
	c := chars[0].(map[string]any)
	for _, field := range []string{"access_token_expires_at", "tokens_updated_at", "created_at"} {
		v, ok := c[field].(string)
		if !ok || v == "" {
			t.Fatalf("character %s = %v, want a non-empty timestamp", field, c[field])
		}
	}
}

func TestManageRefreshCharacterTokensGuards(t *testing.T) {
	ts, _, _ := newTestServer(t)
	token := uiToken(t, ts)

	// Missing session token: refused before doing anything.
	noAuth := manageReq(t, ts, http.MethodPost, "/manage/characters/999/refresh", "", nil)
	noAuth.Body.Close()
	if noAuth.StatusCode != http.StatusForbidden {
		t.Fatalf("refresh without token: status = %d, want 403", noAuth.StatusCode)
	}

	// Non-numeric id: 400.
	badID := manageReq(t, ts, http.MethodPost, "/manage/characters/abc/refresh", token, nil)
	badID.Body.Close()
	if badID.StatusCode != http.StatusBadRequest {
		t.Fatalf("refresh bad id: status = %d, want 400", badID.StatusCode)
	}

	// Unknown character: 404.
	missing := manageReq(t, ts, http.MethodPost, "/manage/characters/424242/refresh", token, nil)
	missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("refresh unknown character: status = %d, want 404", missing.StatusCode)
	}

	// Known character, but the test SSO base URL is unreachable, so the upstream
	// refresh fails and the handler reports a 502 rather than a generic 500.
	bad := manageReq(t, ts, http.MethodPost, "/manage/characters/999/refresh", token, nil)
	bad.Body.Close()
	if bad.StatusCode != http.StatusBadGateway {
		t.Fatalf("refresh with unreachable SSO: status = %d, want 502", bad.StatusCode)
	}
}

func TestManageDeleteCharacter(t *testing.T) {
	ts, st, _ := newTestServer(t)
	token := uiToken(t, ts)
	_, apiKey := registerApp(t, ts, "Pyfa")

	// Give the character an active grant so we can confirm the delete cascades.
	reqBody, _ := json.Marshal(map[string]any{
		"character_id": 999,
		"scopes":       []string{"esi-assets.read_assets.v1"},
	})
	ra, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/request-access", bytes.NewReader(reqBody))
	ra.Header.Set("X-Proxy-Api-Key", apiKey)
	raResp, err := http.DefaultClient.Do(ra)
	if err != nil {
		t.Fatalf("request-access: %v", err)
	}
	raResp.Body.Close()
	pending, _ := st.ListPendingGrants()
	if len(pending) != 1 {
		t.Fatalf("pending grants = %d, want 1", len(pending))
	}
	if err := st.ApprovePendingGrant(pending[0].ID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// Missing session token: refused.
	noAuth := manageReq(t, ts, http.MethodDelete, "/manage/characters/999", "", nil)
	noAuth.Body.Close()
	if noAuth.StatusCode != http.StatusForbidden {
		t.Fatalf("delete without token: status = %d, want 403", noAuth.StatusCode)
	}

	// Delete succeeds.
	resp := manageReq(t, ts, http.MethodDelete, "/manage/characters/999", token, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete character: status = %d, want 204", resp.StatusCode)
	}

	// The character is gone, and its grant cascaded away.
	state := manageState(t, ts, token)
	if n := len(stateList(t, state, "characters")); n != 0 {
		t.Fatalf("characters after delete = %d, want 0", n)
	}
	if n := len(stateList(t, state, "active_grants")); n != 0 {
		t.Fatalf("active_grants after delete = %d, want 0 (cascade)", n)
	}

	// Deleting again is a 404.
	again := manageReq(t, ts, http.MethodDelete, "/manage/characters/999", token, nil)
	again.Body.Close()
	if again.StatusCode != http.StatusNotFound {
		t.Fatalf("delete missing character: status = %d, want 404", again.StatusCode)
	}
}

func TestManageSetGrantScopes(t *testing.T) {
	ts, st, _ := newTestServer(t)
	token := uiToken(t, ts)
	_, apiKey := registerApp(t, ts, "Pyfa")

	// Create and approve a grant with two scopes.
	reqBody, _ := json.Marshal(map[string]any{
		"character_id": 999,
		"scopes":       []string{"esi-assets.read_assets.v1", "esi-skills.read_skills.v1"},
	})
	ra, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/request-access", bytes.NewReader(reqBody))
	ra.Header.Set("X-Proxy-Api-Key", apiKey)
	raResp, err := http.DefaultClient.Do(ra)
	if err != nil {
		t.Fatalf("request-access: %v", err)
	}
	raResp.Body.Close()
	pending, _ := st.ListPendingGrants()
	if len(pending) != 1 {
		t.Fatalf("pending grants = %d, want 1", len(pending))
	}
	if err := st.ApprovePendingGrant(pending[0].ID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	grantID := firstID(t, manageState(t, ts, token), "active_grants")

	// Narrow to a single scope.
	resp := manageReq(t, ts, http.MethodPost, "/manage/grants/"+grantID+"/scopes", token,
		map[string]any{"scopes": []string{"esi-assets.read_assets.v1"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("set scopes: status = %d, want 204", resp.StatusCode)
	}
	grant := stateList(t, manageState(t, ts, token), "active_grants")[0].(map[string]any)
	scopes := grant["scopes"].([]any)
	if len(scopes) != 1 || scopes[0] != "esi-assets.read_assets.v1" {
		t.Fatalf("scopes after update = %v, want [esi-assets.read_assets.v1]", scopes)
	}

	// Empty set is rejected (revoke is the way to remove all access).
	empty := manageReq(t, ts, http.MethodPost, "/manage/grants/"+grantID+"/scopes", token,
		map[string]any{"scopes": []string{}})
	empty.Body.Close()
	if empty.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty scopes: status = %d, want 400", empty.StatusCode)
	}
}

func TestManagePermissionFlow(t *testing.T) {
	ts, _, _ := newTestServer(t)
	token := uiToken(t, ts)
	_, apiKey := registerApp(t, ts, "MCO")

	body, _ := json.Marshal(map[string]any{"permissions": []string{"list_characters"}})
	pr, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/request-permissions", bytes.NewReader(body))
	pr.Header.Set("X-Proxy-Api-Key", apiKey)
	prResp, err := http.DefaultClient.Do(pr)
	if err != nil {
		t.Fatalf("request-permissions: %v", err)
	}
	prResp.Body.Close()

	state := manageState(t, ts, token)
	if n := len(stateList(t, state, "pending_permissions")); n != 1 {
		t.Fatalf("pending_permissions = %d, want 1", n)
	}
	pendID := firstID(t, state, "pending_permissions")

	resp := manageReq(t, ts, http.MethodPost, "/manage/pending-permissions/"+pendID+"/approve", token, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("approve permission: status = %d, want 204", resp.StatusCode)
	}

	state = manageState(t, ts, token)
	if n := len(stateList(t, state, "app_permissions")); n != 1 {
		t.Fatalf("app_permissions after approve = %d, want 1", n)
	}
	permID := firstID(t, state, "app_permissions")

	resp = manageReq(t, ts, http.MethodPost, "/manage/app-permissions/"+permID+"/revoke", token, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke permission: status = %d, want 204", resp.StatusCode)
	}
	state = manageState(t, ts, token)
	if n := len(stateList(t, state, "app_permissions")); n != 0 {
		t.Fatalf("app_permissions after revoke = %d, want 0", n)
	}
}

func TestManageAddCharacterReturnsLoginURL(t *testing.T) {
	ts, _, _ := newTestServer(t) // seeds a "dummy-client-id", so BeginLogin succeeds
	token := uiToken(t, ts)

	// Without the token the privileged endpoint is refused.
	resp := manageReq(t, ts, http.MethodPost, "/manage/characters/add", "", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("add character without token: status = %d, want 403", resp.StatusCode)
	}

	resp = manageReq(t, ts, http.MethodPost, "/manage/characters/add", token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add character: status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		LoginURL string `json:"login_url"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.LoginURL == "" {
		t.Fatalf("add character returned empty login_url")
	}
}

func TestManageSetBindHostTogglesLANFlag(t *testing.T) {
	ts, _, _ := newTestServer(t)
	token := uiToken(t, ts)

	resp := manageReq(t, ts, http.MethodPost, "/manage/bind-host", token, map[string]any{"lan": true})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set bind-host lan=true: status = %d, want 200", resp.StatusCode)
	}
	state := manageState(t, ts, token)
	if lan, _ := state["lan_access"].(bool); !lan {
		t.Fatalf("lan_access after enabling = %v, want true", state["lan_access"])
	}
	if host, _ := state["bind_host"].(string); host != "0.0.0.0" {
		t.Fatalf("bind_host after enabling = %q, want 0.0.0.0", host)
	}

	resp = manageReq(t, ts, http.MethodPost, "/manage/bind-host", token, map[string]any{"lan": false})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set bind-host lan=false: status = %d, want 200", resp.StatusCode)
	}
	state = manageState(t, ts, token)
	if lan, _ := state["lan_access"].(bool); lan {
		t.Fatalf("lan_access after disabling = %v, want false", state["lan_access"])
	}
}

func TestManageSetClientIDRejectsEmpty(t *testing.T) {
	ts, _, _ := newTestServer(t)
	token := uiToken(t, ts)

	resp := manageReq(t, ts, http.MethodPost, "/manage/client-id", token, map[string]any{"client_id": "  "})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty client_id: status = %d, want 400", resp.StatusCode)
	}

	resp = manageReq(t, ts, http.MethodPost, "/manage/client-id", token, map[string]any{"client_id": "abc123"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("valid client_id: status = %d, want 204", resp.StatusCode)
	}
}
