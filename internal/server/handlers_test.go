package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/BOS-Development/esi-proxy/internal/server"
)

func TestHealthz(t *testing.T) {
	ts, _, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("healthz = %d %q, want 200 \"ok\"", resp.StatusCode, body)
	}
}

func TestClientIDFormAndSave(t *testing.T) {
	ts, _, _ := newTestServer(t)

	// The form renders with the configured redirect URL for the user to copy.
	resp, err := http.Get(ts.URL + "/setup/client-id")
	if err != nil {
		t.Fatalf("GET form: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "http://localhost/sso/callback") {
		t.Fatalf("form = %d, body missing redirect URL: %s", resp.StatusCode, body)
	}

	// Empty client_id is rejected.
	bad, err := http.PostForm(ts.URL+"/setup/client-id", url.Values{"client_id": {""}})
	if err != nil {
		t.Fatalf("POST empty: %v", err)
	}
	bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST empty client_id = %d, want 400", bad.StatusCode)
	}

	// A valid client_id is saved and echoed back on the next form render.
	ok, err := http.PostForm(ts.URL+"/setup/client-id", url.Values{"client_id": {"new-client-id"}})
	if err != nil {
		t.Fatalf("POST valid: %v", err)
	}
	ok.Body.Close()
	if ok.StatusCode != http.StatusOK {
		t.Fatalf("POST valid client_id = %d, want 200", ok.StatusCode)
	}
	resp2, _ := http.Get(ts.URL + "/setup/client-id")
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if !strings.Contains(string(body2), "new-client-id") {
		t.Errorf("saved client_id not reflected in form: %s", body2)
	}
}

func TestListGrants(t *testing.T) {
	ts, st, _ := newTestServer(t)
	_, apiKey := registerApp(t, ts, "Pyfa")

	// Request access, then approve it the way the tray would.
	reqBody, _ := json.Marshal(map[string]any{"character_id": testCharacterID, "scopes": []string{"esi-assets.read_assets.v1"}})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/request-access", bytes.NewReader(reqBody))
	req.Header.Set("X-Proxy-Api-Key", apiKey)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	pending, err := st.ListPendingGrants()
	if err != nil || len(pending) != 1 {
		t.Fatalf("ListPendingGrants: %d, %v", len(pending), err)
	}
	if err := st.ApprovePendingGrant(pending[0].ID); err != nil {
		t.Fatalf("ApprovePendingGrant: %v", err)
	}

	grReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/grants", nil)
	grReq.Header.Set("X-Proxy-Api-Key", apiKey)
	grResp, err := http.DefaultClient.Do(grReq)
	if err != nil {
		t.Fatalf("GET /api/grants: %v", err)
	}
	defer grResp.Body.Close()
	if grResp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", grResp.StatusCode)
	}
	var out []struct {
		CharacterID int64    `json:"character_id"`
		Scopes      []string `json:"scopes"`
	}
	json.NewDecoder(grResp.Body).Decode(&out)
	if len(out) != 1 || out[0].CharacterID != testCharacterID || len(out[0].Scopes) != 1 {
		t.Fatalf("grants = %+v, want the one approved grant", out)
	}
}

func TestListGrantsRejectsMissingAPIKey(t *testing.T) {
	ts, _, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/api/grants")
	if err != nil {
		t.Fatalf("GET /api/grants: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// The SSO callback's error paths don't touch the network, so they're testable
// without a live EVE login server.
func TestSSOCallbackErrorPaths(t *testing.T) {
	ts, _, _ := newTestServer(t)

	t.Run("provider error renders a page", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/sso/callback?error=access_denied")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "not completed") {
			t.Fatalf("got %d %q", resp.StatusCode, body)
		}
	})

	t.Run("missing code or state is 400", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/sso/callback?state=onlystate")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("unknown state is 400", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/sso/callback?state=bogus&code=abc")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})
}

func TestBindHostPersistence(t *testing.T) {
	// BindHost/SetBindHost only read and write config.json via the server's
	// data dir, so they can be exercised against a server built with a temp
	// dir and otherwise-nil dependencies.
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(nil, nil, nil, nil, nil, log, dir, "", "")

	host, err := srv.BindHost()
	if err != nil {
		t.Fatalf("BindHost: %v", err)
	}
	if host != "localhost" {
		t.Errorf("default BindHost = %q, want localhost", host)
	}

	if err := srv.SetBindHost("0.0.0.0"); err != nil {
		t.Fatalf("SetBindHost: %v", err)
	}
	host, err = srv.BindHost()
	if err != nil {
		t.Fatalf("BindHost after set: %v", err)
	}
	if host != "0.0.0.0" {
		t.Errorf("BindHost = %q, want 0.0.0.0 after SetBindHost", host)
	}
}
