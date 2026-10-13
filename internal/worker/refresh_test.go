package worker

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BOS-Development/esi-proxy/internal/cryptox"
	"github.com/BOS-Development/esi-proxy/internal/db"
	"github.com/BOS-Development/esi-proxy/internal/esi"
	"github.com/BOS-Development/esi-proxy/internal/store"
	"github.com/BOS-Development/esi-proxy/internal/tokens"
)

func newWorkerFixture(t *testing.T, ssoBaseURL string) (*store.Store, *tokens.Manager, *[32]byte) {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.Open(dir)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	key, err := cryptox.MasterKey(dir)
	if err != nil {
		t.Fatalf("MasterKey: %v", err)
	}
	st := store.New(conn)
	sso := esi.NewSSOHolder(esi.NewSSO("client", ssoBaseURL, "http://localhost/cb", nil))
	return st, tokens.NewManager(st, sso, key), key
}

func TestRefreshDueRefreshesExpiringCharacters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"fresh-access","refresh_token":"fresh-refresh","token_type":"Bearer","expires_in":1200}`)
	}))
	defer srv.Close()

	st, mgr, key := newWorkerFixture(t, srv.URL)
	acc, _ := st.GetOrCreateDefaultAccount()

	sealed := func(s string) []byte {
		b, _ := cryptox.Seal(key, []byte(s))
		return b
	}
	// One character already expired (due), one far in the future (not due).
	if err := st.UpsertCharacter(store.Character{
		ID: 1, AccountID: acc.ID, Name: "Due",
		RefreshTokenEnc: sealed("old"), AccessTokenEnc: sealed("old"),
		AccessTokenExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertCharacter(store.Character{
		ID: 2, AccountID: acc.ID, Name: "Fresh",
		RefreshTokenEnc: sealed("keep"), AccessTokenEnc: sealed("keep"),
		AccessTokenExpiresAt: time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	refreshDue(context.Background(), st, mgr, log)

	due, _ := st.GetCharacter(1)
	if plain, _ := cryptox.Open(key, due.AccessTokenEnc); string(plain) != "fresh-access" {
		t.Errorf("due character access token = %q, want fresh-access", plain)
	}
	if !due.AccessTokenExpiresAt.After(time.Now()) {
		t.Errorf("due character expiry = %v, want refreshed into the future", due.AccessTokenExpiresAt)
	}

	// The not-due character must be left untouched.
	fresh, _ := st.GetCharacter(2)
	if plain, _ := cryptox.Open(key, fresh.AccessTokenEnc); string(plain) != "keep" {
		t.Errorf("fresh character token changed to %q, want untouched", plain)
	}
}

func TestRunTokenRefreshStopsOnContextCancel(t *testing.T) {
	st, mgr, _ := newWorkerFixture(t, "https://unused.invalid")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunTokenRefresh(ctx, st, mgr, log) // no characters due, so no network
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunTokenRefresh did not return after context cancel")
	}
}
