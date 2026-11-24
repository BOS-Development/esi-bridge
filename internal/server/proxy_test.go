package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BOS-Development/esi-proxy/internal/cache"
	"github.com/BOS-Development/esi-proxy/internal/cryptox"
	"github.com/BOS-Development/esi-proxy/internal/db"
	"github.com/BOS-Development/esi-proxy/internal/esi"
	"github.com/BOS-Development/esi-proxy/internal/server"
	"github.com/BOS-Development/esi-proxy/internal/store"
	"github.com/BOS-Development/esi-proxy/internal/tokens"
)

const testCharacterID = 999

func newTestServer(t *testing.T) (*httptest.Server, *store.Store, *int32) {
	t.Helper()

	tempDir := t.TempDir()
	conn, err := db.Open(tempDir)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	masterKey, err := cryptox.MasterKey(tempDir)
	if err != nil {
		t.Fatalf("cryptox.MasterKey: %v", err)
	}

	st := store.New(conn)
	account, err := st.CreateAccount("Test")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	var esiCalls int32
	fakeESI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&esiCalls, 1)
		if got := r.Header.Get("Authorization"); got != "Bearer fake-access-token" {
			t.Errorf("fake ESI got Authorization %q", got)
		}
		w.Header().Set("Expires", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"character_id":999}`)
	}))
	t.Cleanup(fakeESI.Close)

	redirectURL := "http://localhost/sso/callback"
	ssoBaseURL := "https://login.example.invalid"
	sso := esi.NewSSOHolder(esi.NewSSO("dummy-client-id", ssoBaseURL, redirectURL, nil))
	esiClient := esi.NewClient(fakeESI.URL)
	tokenMgr := tokens.NewManager(st, sso, masterKey)

	accessEnc, err := cryptox.Seal(masterKey, []byte("fake-access-token"))
	if err != nil {
		t.Fatalf("Seal access token: %v", err)
	}
	refreshEnc, err := cryptox.Seal(masterKey, []byte("fake-refresh-token"))
	if err != nil {
		t.Fatalf("Seal refresh token: %v", err)
	}
	char := store.Character{
		ID:                   testCharacterID,
		AccountID:            account.ID,
		Name:                 "Test Character",
		RefreshTokenEnc:      refreshEnc,
		AccessTokenEnc:       accessEnc,
		AccessTokenExpiresAt: time.Now().Add(time.Hour), // far from expiry: no refresh should be triggered
	}
	if err := st.UpsertCharacter(char); err != nil {
		t.Fatalf("UpsertCharacter: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(st, sso, esiClient, cache.New(), tokenMgr, log, tempDir, ssoBaseURL, redirectURL)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return ts, st, &esiCalls
}

func registerApp(t *testing.T, ts *httptest.Server, name string) (appID int64, apiKey string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"name": name})
	resp, err := http.Post(ts.URL+"/api/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("register app: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("register app: status %d: %s", resp.StatusCode, b)
	}
	var out struct {
		ApplicationID int64  `json:"application_id"`
		APIKey        string `json:"api_key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	return out.ApplicationID, out.APIKey
}

// TestApprovalAndProxyFlow exercises the full README flow end to end:
// an app registers, requests access, the user approves it (simulated here
// directly via store.ApprovePendingGrant, as the window's approve action does), and the app
// can then use the proxy, with a second identical request served from
// cache instead of hitting ESI again.
func TestApprovalAndProxyFlow(t *testing.T) {
	ts, st, esiCalls := newTestServer(t)

	_, apiKey := registerApp(t, ts, "Pyfa")

	reqBody, _ := json.Marshal(map[string]any{
		"character_id": testCharacterID,
		"scopes":       []string{"esi-assets.read_assets.v1"},
	})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/request-access", bytes.NewReader(reqBody))
	req.Header.Set("X-Proxy-Api-Key", apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request-access: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("request-access status = %d, want 202 (pending)", resp.StatusCode)
	}

	proxyReq := func() *http.Response {
		r, _ := http.NewRequest(http.MethodGet, ts.URL+"/proxy/999/characters/999/assets/", nil)
		r.Header.Set("X-Proxy-Api-Key", apiKey)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatalf("proxy request: %v", err)
		}
		return resp
	}

	if resp := proxyReq(); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("proxy before approval: status = %d, want 403", resp.StatusCode)
	}

	pending, err := st.ListPendingGrants()
	if err != nil || len(pending) != 1 {
		t.Fatalf("ListPendingGrants: got %d, err %v", len(pending), err)
	}
	// The 403'd proxy call must not have wiped the scopes request-access asked for.
	if got := pending[0].RequestedScopes; len(got) != 1 || got[0] != "esi-assets.read_assets.v1" {
		t.Fatalf("pending grant scopes after proxy miss = %v, want [esi-assets.read_assets.v1]", got)
	}
	if err := st.ApprovePendingGrant(pending[0].ID); err != nil {
		t.Fatalf("ApprovePendingGrant: %v", err)
	}

	resp1 := proxyReq()
	body1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("proxy after approval: status = %d, body = %s", resp1.StatusCode, body1)
	}
	if string(body1) != `{"character_id":999}` {
		t.Fatalf("proxy body = %s", body1)
	}
	if got := atomic.LoadInt32(esiCalls); got != 1 {
		t.Fatalf("esi calls after first proxy request = %d, want 1", got)
	}

	resp2 := proxyReq()
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if string(body2) != string(body1) {
		t.Fatalf("second response body = %s, want same as first", body2)
	}
	if got := atomic.LoadInt32(esiCalls); got != 1 {
		t.Fatalf("esi calls after second (should be cached) proxy request = %d, want still 1", got)
	}
}

func TestProxyRejectsMissingAPIKey(t *testing.T) {
	ts, _, _ := newTestServer(t)

	resp, err := http.Get(ts.URL + "/proxy/999/characters/999/assets/")
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}
