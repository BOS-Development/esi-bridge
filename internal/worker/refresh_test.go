package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BOS-Development/esi-proxy/internal/cryptox"
	"github.com/BOS-Development/esi-proxy/internal/db"
	"github.com/BOS-Development/esi-proxy/internal/esi"
	"github.com/BOS-Development/esi-proxy/internal/store"
	"github.com/BOS-Development/esi-proxy/internal/tokens"
)

type fixture struct {
	store *store.Store
	mgr   *tokens.Manager
	key   *[32]byte
	calls *atomic.Int32
	acct  store.Account
	logs  *bytes.Buffer
	log   *slog.Logger
}

// newFixture builds a store and token manager backed by a fake SSO token
// endpoint. Refreshing the refresh token "bad" fails; anything else succeeds.
func newFixture(t *testing.T) *fixture {
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

	calls := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		if r.PostForm.Get("refresh_token") == "bad" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fresh", "token_type": "Bearer", "expires_in": 1200,
		})
	}))
	t.Cleanup(srv.Close)

	s := store.New(conn)
	acct, err := s.CreateAccount("Main")
	if err != nil {
		t.Fatal(err)
	}
	holder := esi.NewSSOHolder(esi.NewSSO("client", srv.URL, "http://localhost/cb", nil))
	logs := &bytes.Buffer{}
	return &fixture{
		store: s, mgr: tokens.NewManager(s, holder, key), key: key, calls: calls, acct: acct,
		logs: logs, log: slog.New(slog.NewTextHandler(logs, nil)),
	}
}

func (fx *fixture) add(t *testing.T, id int64, name, refresh string, expires time.Time) {
	t.Helper()
	r, _ := cryptox.Seal(fx.key, []byte(refresh))
	a, _ := cryptox.Seal(fx.key, []byte("stale"))
	if err := fx.store.UpsertCharacter(store.Character{
		ID: id, AccountID: fx.acct.ID, Name: name, RefreshTokenEnc: r, AccessTokenEnc: a, AccessTokenExpiresAt: expires,
	}); err != nil {
		t.Fatal(err)
	}
}

func (fx *fixture) access(t *testing.T, id int64) string {
	t.Helper()
	c, err := fx.store.GetCharacter(id)
	if err != nil {
		t.Fatal(err)
	}
	p, err := cryptox.Open(fx.key, c.AccessTokenEnc)
	if err != nil {
		t.Fatal(err)
	}
	return string(p)
}

func TestRefreshDueRefreshesOnlyExpiringCharacters(t *testing.T) {
	fx := newFixture(t)
	now := time.Now()
	fx.add(t, 1, "Soon", "ok", now.Add(2*time.Minute))
	fx.add(t, 2, "Expired", "ok", now.Add(-time.Hour))
	fx.add(t, 3, "Later", "ok", now.Add(time.Hour))

	refreshDue(context.Background(), fx.store, fx.mgr, fx.log)

	if n := fx.calls.Load(); n != 2 {
		t.Fatalf("SSO calls = %d, want 2", n)
	}
	if fx.access(t, 1) != "fresh" || fx.access(t, 2) != "fresh" {
		t.Error("expiring characters were not refreshed")
	}
	if fx.access(t, 3) != "stale" {
		t.Error("character outside the cutoff was refreshed")
	}
}

func TestRefreshDueContinuesAfterFailure(t *testing.T) {
	fx := newFixture(t)
	past := time.Now().Add(-time.Hour)
	fx.add(t, 1, "Broken", "bad", past)
	fx.add(t, 2, "Fine", "ok", past)

	refreshDue(context.Background(), fx.store, fx.mgr, fx.log)

	if fx.access(t, 2) != "fresh" {
		t.Error("a failed refresh stopped later characters from refreshing")
	}
	if fx.access(t, 1) != "stale" {
		t.Error("failed refresh changed the stored token")
	}
	if !strings.Contains(fx.logs.String(), "character must log in again") {
		t.Errorf("invalid_grant not logged as needing re-login: %s", fx.logs.String())
	}

	// The rejected character is now flagged, so the next tick leaves it alone
	// instead of calling SSO with the dead token again.
	before := fx.calls.Load()
	refreshDue(context.Background(), fx.store, fx.mgr, fx.log)
	if got := fx.calls.Load(); got != before {
		t.Errorf("SSO called %d more times for a character flagged for re-login", got-before)
	}
	if c, _ := fx.store.GetCharacter(1); c.ReauthReason == "" {
		t.Error("character rejected with invalid_grant was not flagged")
	}
}

func TestRunTokenRefreshRunsImmediatelyAndStops(t *testing.T) {
	fx := newFixture(t)
	fx.add(t, 1, "Expired", "ok", time.Now().Add(-time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunTokenRefresh(ctx, fx.store, fx.mgr, fx.log)
		close(done)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for fx.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if fx.calls.Load() == 0 {
		t.Fatal("RunTokenRefresh did not refresh on startup")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunTokenRefresh did not return after cancel")
	}
}
