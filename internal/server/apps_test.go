package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func TestRegisterAppRequiresName(t *testing.T) {
	ts, _, _ := newTestServer(t)

	resp, err := http.Post(ts.URL+"/api/register", "application/json", bytes.NewReader([]byte(`{"name":""}`)))
	if err != nil {
		t.Fatalf("register app: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an empty name", resp.StatusCode)
	}
}

// TestRegisterAppAllowsDuplicateNames guards against accidentally adding a
// uniqueness constraint on application name: identity is the api_key, and
// two registrations (e.g. two installs of the same app) are allowed to
// share a display name.
func TestRegisterAppAllowsDuplicateNames(t *testing.T) {
	ts, _, _ := newTestServer(t)

	id1, key1 := registerApp(t, ts, "Pyfa")
	id2, key2 := registerApp(t, ts, "Pyfa")

	if id1 == id2 {
		t.Fatal("expected two distinct application IDs for two registrations")
	}
	if key1 == key2 {
		t.Fatal("expected two distinct api keys for two registrations")
	}

	body, _ := json.Marshal(map[string]any{"character_id": testCharacterID, "scopes": []string{"esi-assets.read_assets.v1"}})
	for _, key := range []string{key1, key2} {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/request-access", bytes.NewReader(body))
		req.Header.Set("X-Proxy-Api-Key", key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request-access: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("request-access status = %d, want 202 (each same-named app tracked independently)", resp.StatusCode)
		}
	}
}
