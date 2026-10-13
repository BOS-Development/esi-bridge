package tokens_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/BOS-Development/esi-proxy/internal/cryptox"
	"github.com/BOS-Development/esi-proxy/internal/db"
	"github.com/BOS-Development/esi-proxy/internal/esi"
	"github.com/BOS-Development/esi-proxy/internal/store"
	"github.com/BOS-Development/esi-proxy/internal/tokens"
)

// tokenEndpoint returns an httptest server that answers the EVE SSO token
// endpoint with the given access/refresh tokens.
func tokenEndpoint(t *testing.T, access, refresh string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":%q,"refresh_token":%q,"token_type":"Bearer","expires_in":1200}`, access, refresh)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fakeJWT(t *testing.T, payloadJSON string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(payloadJSON))
	return header + "." + payload + ".sig"
}

func newFixture(t *testing.T, ssoBaseURL string) (*tokens.Manager, *store.Store, *[32]byte) {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.Open(dir)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	masterKey, err := cryptox.MasterKey(dir)
	if err != nil {
		t.Fatalf("cryptox.MasterKey: %v", err)
	}
	st := store.New(conn)
	sso := esi.NewSSOHolder(esi.NewSSO("client", ssoBaseURL, "http://localhost/sso/callback", nil))
	return tokens.NewManager(st, sso, masterKey), st, masterKey
}

func seal(t *testing.T, key *[32]byte, s string) []byte {
	t.Helper()
	b, err := cryptox.Seal(key, []byte(s))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return b
}

func TestEnsureAccessTokenReturnsCachedWhenValid(t *testing.T) {
	mgr, st, key := newFixture(t, "https://unused.invalid")

	acc, _ := st.GetOrCreateDefaultAccount()
	if err := st.UpsertCharacter(store.Character{
		ID: 1, AccountID: acc.ID, Name: "Cached",
		RefreshTokenEnc:      seal(t, key, "refresh"),
		AccessTokenEnc:       seal(t, key, "cached-access"),
		AccessTokenExpiresAt: time.Now().Add(time.Hour), // not near expiry
	}); err != nil {
		t.Fatal(err)
	}

	got, err := mgr.EnsureAccessToken(context.Background(), 1)
	if err != nil {
		t.Fatalf("EnsureAccessToken: %v", err)
	}
	if got != "cached-access" {
		t.Errorf("token = %q, want the cached plaintext (no refresh expected)", got)
	}
}

func TestEnsureAccessTokenRefreshesWhenExpired(t *testing.T) {
	endpoint := tokenEndpoint(t, "refreshed-access", "rotated-refresh")
	mgr, st, key := newFixture(t, endpoint.URL)

	acc, _ := st.GetOrCreateDefaultAccount()
	if err := st.UpsertCharacter(store.Character{
		ID: 1, AccountID: acc.ID, Name: "Expired",
		RefreshTokenEnc:      seal(t, key, "old-refresh"),
		AccessTokenEnc:       seal(t, key, "old-access"),
		AccessTokenExpiresAt: time.Now().Add(-time.Hour), // expired
	}); err != nil {
		t.Fatal(err)
	}

	got, err := mgr.EnsureAccessToken(context.Background(), 1)
	if err != nil {
		t.Fatalf("EnsureAccessToken: %v", err)
	}
	if got != "refreshed-access" {
		t.Errorf("token = %q, want refreshed-access", got)
	}

	// The rotated tokens must be persisted (re-encrypted) and expiry pushed out.
	char, _ := st.GetCharacter(1)
	if plain, _ := cryptox.Open(key, char.AccessTokenEnc); string(plain) != "refreshed-access" {
		t.Errorf("stored access token = %q, want refreshed-access", plain)
	}
	if plain, _ := cryptox.Open(key, char.RefreshTokenEnc); string(plain) != "rotated-refresh" {
		t.Errorf("stored refresh token = %q, want rotated-refresh", plain)
	}
	if !char.AccessTokenExpiresAt.After(time.Now()) {
		t.Errorf("expiry = %v, want in the future after refresh", char.AccessTokenExpiresAt)
	}
}

func TestEnsureAccessTokenMissingCharacter(t *testing.T) {
	mgr, _, _ := newFixture(t, "https://unused.invalid")
	if _, err := mgr.EnsureAccessToken(context.Background(), 404); err == nil {
		t.Fatal("expected an error for an unknown character")
	}
}

func TestSaveLoginPersistsCharacterFromClaims(t *testing.T) {
	mgr, st, key := newFixture(t, "https://unused.invalid")
	acc, _ := st.GetOrCreateDefaultAccount()

	tok := &oauth2.Token{
		AccessToken:  fakeJWT(t, `{"sub":"CHARACTER:EVE:2112625428","name":"CCP Zoetrope","scp":["esi-assets.read_assets.v1"]}`),
		RefreshToken: "login-refresh",
		Expiry:       time.Now().Add(20 * time.Minute),
	}

	char, err := mgr.SaveLogin(acc.ID, tok)
	if err != nil {
		t.Fatalf("SaveLogin: %v", err)
	}
	if char.ID != 2112625428 || char.Name != "CCP Zoetrope" {
		t.Fatalf("SaveLogin returned %+v", char)
	}

	stored, err := st.GetCharacter(2112625428)
	if err != nil {
		t.Fatalf("GetCharacter: %v", err)
	}
	if plain, _ := cryptox.Open(key, stored.RefreshTokenEnc); string(plain) != "login-refresh" {
		t.Errorf("stored refresh = %q, want login-refresh", plain)
	}
	if len(stored.Scopes) != 1 || stored.Scopes[0] != "esi-assets.read_assets.v1" {
		t.Errorf("stored scopes = %v", stored.Scopes)
	}
}

func TestSaveLoginRejectsNonJWT(t *testing.T) {
	mgr, st, _ := newFixture(t, "https://unused.invalid")
	acc, _ := st.GetOrCreateDefaultAccount()
	if _, err := mgr.SaveLogin(acc.ID, &oauth2.Token{AccessToken: "not-a-jwt"}); err == nil {
		t.Fatal("expected SaveLogin to reject a non-JWT access token")
	}
}
