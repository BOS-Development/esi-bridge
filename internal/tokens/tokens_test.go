package tokens_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/BOS-Development/esi-proxy/internal/cryptox"
	"github.com/BOS-Development/esi-proxy/internal/db"
	"github.com/BOS-Development/esi-proxy/internal/esi"
	"github.com/BOS-Development/esi-proxy/internal/store"
	"github.com/BOS-Development/esi-proxy/internal/tokens"
)

type fakeSSO struct {
	calls        atomic.Int32
	lastRefresh  atomic.Value // string
	refreshToken string       // returned refresh_token; "" to omit it
	fail         bool
}

func (f *fakeSSO) handler(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	_ = r.ParseForm()
	f.lastRefresh.Store(r.PostForm.Get("refresh_token"))
	if f.fail {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		return
	}
	body := map[string]any{
		"access_token": "new-access",
		"token_type":   "Bearer",
		"expires_in":   1200,
	}
	if f.refreshToken != "" {
		body["refresh_token"] = f.refreshToken
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

type fixture struct {
	store *store.Store
	mgr   *tokens.Manager
	key   *[32]byte
	sso   *fakeSSO
	acct  store.Account
}

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
	f := &fakeSSO{}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)

	s := store.New(conn)
	acct, err := s.CreateAccount("Main")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	holder := esi.NewSSOHolder(esi.NewSSO("client", srv.URL, "http://localhost/cb", nil))
	return &fixture{store: s, mgr: tokens.NewManager(s, holder, key), key: key, sso: f, acct: acct}
}

func (fx *fixture) addCharacter(t *testing.T, id int64, refresh, access string, expires time.Time) store.Character {
	t.Helper()
	r, err := cryptox.Seal(fx.key, []byte(refresh))
	if err != nil {
		t.Fatal(err)
	}
	a, err := cryptox.Seal(fx.key, []byte(access))
	if err != nil {
		t.Fatal(err)
	}
	c := store.Character{ID: id, AccountID: fx.acct.ID, Name: "Char", RefreshTokenEnc: r, AccessTokenEnc: a, AccessTokenExpiresAt: expires}
	if err := fx.store.UpsertCharacter(c); err != nil {
		t.Fatalf("UpsertCharacter: %v", err)
	}
	return c
}

func (fx *fixture) decrypt(t *testing.T, sealed []byte) string {
	t.Helper()
	p, err := cryptox.Open(fx.key, sealed)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	return string(p)
}

func TestEnsureAccessTokenUsesValidToken(t *testing.T) {
	fx := newFixture(t)
	fx.addCharacter(t, 1, "refresh", "still-valid", time.Now().Add(time.Hour))

	got, err := fx.mgr.EnsureAccessToken(context.Background(), 1)
	if err != nil {
		t.Fatalf("EnsureAccessToken: %v", err)
	}
	if got != "still-valid" {
		t.Fatalf("token = %q, want still-valid", got)
	}
	if n := fx.sso.calls.Load(); n != 0 {
		t.Fatalf("SSO called %d times for a valid token", n)
	}
}

func TestEnsureAccessTokenRefreshesNearExpiry(t *testing.T) {
	fx := newFixture(t)
	// Inside the 2 minute refresh buffer, so it must refresh.
	fx.addCharacter(t, 1, "old-refresh", "old-access", time.Now().Add(time.Minute))
	fx.sso.refreshToken = "rotated-refresh"

	got, err := fx.mgr.EnsureAccessToken(context.Background(), 1)
	if err != nil {
		t.Fatalf("EnsureAccessToken: %v", err)
	}
	if got != "new-access" {
		t.Fatalf("token = %q, want new-access", got)
	}
	if fx.sso.lastRefresh.Load() != "old-refresh" {
		t.Fatalf("SSO got refresh_token %v, want old-refresh", fx.sso.lastRefresh.Load())
	}

	c, _ := fx.store.GetCharacter(1)
	if fx.decrypt(t, c.AccessTokenEnc) != "new-access" {
		t.Error("new access token not persisted")
	}
	if fx.decrypt(t, c.RefreshTokenEnc) != "rotated-refresh" {
		t.Error("rotated refresh token not persisted")
	}
	if !c.AccessTokenExpiresAt.After(time.Now().Add(10 * time.Minute)) {
		t.Errorf("expiry not updated: %v", c.AccessTokenExpiresAt)
	}
}

func TestRefreshKeepsRefreshTokenWhenNotRotated(t *testing.T) {
	fx := newFixture(t)
	char := fx.addCharacter(t, 1, "stable-refresh", "old-access", time.Now().Add(-time.Hour))

	if _, err := fx.mgr.Refresh(context.Background(), char); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	c, _ := fx.store.GetCharacter(1)
	if got := fx.decrypt(t, c.RefreshTokenEnc); got != "stable-refresh" {
		t.Fatalf("refresh token = %q, want stable-refresh", got)
	}
}

func TestRefreshSSOFailure(t *testing.T) {
	fx := newFixture(t)
	char := fx.addCharacter(t, 1, "refresh", "old-access", time.Now().Add(-time.Hour))
	fx.sso.fail = true

	if _, err := fx.mgr.Refresh(context.Background(), char); err == nil {
		t.Fatal("expected error when SSO rejects the refresh")
	}
	c, _ := fx.store.GetCharacter(1)
	if fx.decrypt(t, c.AccessTokenEnc) != "old-access" {
		t.Error("stored token changed despite failed refresh")
	}
}

func TestRefreshUndecryptableToken(t *testing.T) {
	fx := newFixture(t)
	char := fx.addCharacter(t, 1, "refresh", "access", time.Now())
	char.RefreshTokenEnc = []byte("not sealed")

	if _, err := fx.mgr.Refresh(context.Background(), char); err == nil {
		t.Fatal("expected decrypt error")
	}
	if n := fx.sso.calls.Load(); n != 0 {
		t.Fatalf("SSO called %d times with an undecryptable token", n)
	}
}

func TestRefreshDeletedCharacter(t *testing.T) {
	fx := newFixture(t)
	char := fx.addCharacter(t, 1, "refresh", "access", time.Now())
	if err := fx.store.DeleteCharacter(1); err != nil {
		t.Fatal(err)
	}
	_, err := fx.mgr.Refresh(context.Background(), char)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestEnsureAccessTokenUnknownCharacter(t *testing.T) {
	fx := newFixture(t)
	_, err := fx.mgr.EnsureAccessToken(context.Background(), 999)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestEnsureAccessTokenUndecryptable(t *testing.T) {
	fx := newFixture(t)
	c := fx.addCharacter(t, 1, "refresh", "access", time.Now().Add(time.Hour))
	c.AccessTokenEnc = []byte("garbage")
	if err := fx.store.UpsertCharacter(c); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.mgr.EnsureAccessToken(context.Background(), 1); err == nil {
		t.Fatal("expected decrypt error")
	}
}

func fakeJWT(payload string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc([]byte(payload)) + ".sig"
}

func TestSaveLogin(t *testing.T) {
	fx := newFixture(t)
	access := fakeJWT(`{"sub":"CHARACTER:EVE:2112625428","name":"CCP Bartender","scp":["esi-a.v1","esi-b.v1"]}`)
	expiry := time.Now().Add(20 * time.Minute).UTC().Truncate(time.Millisecond)
	tok := &oauth2.Token{AccessToken: access, RefreshToken: "login-refresh", Expiry: expiry}

	char, err := fx.mgr.SaveLogin(fx.acct.ID, tok)
	if err != nil {
		t.Fatalf("SaveLogin: %v", err)
	}
	if char.ID != 2112625428 || char.Name != "CCP Bartender" || char.AccountID != fx.acct.ID {
		t.Fatalf("char = %+v", char)
	}

	got, err := fx.store.GetCharacter(2112625428)
	if err != nil {
		t.Fatalf("GetCharacter: %v", err)
	}
	if strings.Join(got.Scopes, " ") != "esi-a.v1 esi-b.v1" {
		t.Errorf("scopes = %v", got.Scopes)
	}
	if !got.AccessTokenExpiresAt.Equal(expiry) {
		t.Errorf("expiry = %v, want %v", got.AccessTokenExpiresAt, expiry)
	}
	if fx.decrypt(t, got.AccessTokenEnc) != access {
		t.Error("access token not stored encrypted")
	}
	if fx.decrypt(t, got.RefreshTokenEnc) != "login-refresh" {
		t.Error("refresh token not stored encrypted")
	}
}

func TestSaveLoginRejectsBadToken(t *testing.T) {
	fx := newFixture(t)
	if _, err := fx.mgr.SaveLogin(fx.acct.ID, &oauth2.Token{AccessToken: "not-a-jwt"}); err == nil {
		t.Fatal("expected error for an unparseable access token")
	}
	if chars, _ := fx.store.ListCharacters(); len(chars) != 0 {
		t.Fatalf("characters saved despite error: %+v", chars)
	}
}

func TestSaveLoginUnknownAccount(t *testing.T) {
	fx := newFixture(t)
	tok := &oauth2.Token{AccessToken: fakeJWT(`{"sub":"CHARACTER:EVE:1","name":"X","scp":"esi-a.v1"}`), RefreshToken: "r"}
	if _, err := fx.mgr.SaveLogin(9999, tok); err == nil {
		t.Fatal("expected error saving to a nonexistent account")
	}
}
