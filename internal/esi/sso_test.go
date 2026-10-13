package esi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestNewSSOClientID(t *testing.T) {
	s := NewSSO("my-client", "https://login.example", "http://localhost/sso/callback", nil)
	if s.ClientID() != "my-client" {
		t.Errorf("ClientID = %q, want my-client", s.ClientID())
	}
}

func TestLoginURLHasPKCEAndState(t *testing.T) {
	s := NewSSO("my-client", "https://login.example", "http://localhost/sso/callback",
		[]string{"esi-assets.read_assets.v1"})

	rawURL, verifier := s.LoginURL("state-xyz")
	if verifier == "" {
		t.Fatal("LoginURL returned an empty PKCE verifier")
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse login url: %v", err)
	}
	q := u.Query()
	if q.Get("state") != "state-xyz" {
		t.Errorf("state = %q", q.Get("state"))
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", q.Get("code_challenge_method"))
	}
	if q.Get("code_challenge") == "" {
		t.Error("missing code_challenge")
	}
	if q.Get("client_id") != "my-client" {
		t.Errorf("client_id = %q", q.Get("client_id"))
	}
}

func tokenServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"at","refresh_token":"rt","token_type":"Bearer","expires_in":1200}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestExchange(t *testing.T) {
	srv := tokenServer(t)
	s := NewSSO("my-client", srv.URL, "http://localhost/sso/callback", nil)

	tok, err := s.Exchange(context.Background(), "auth-code", "verifier")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if tok.AccessToken != "at" || tok.RefreshToken != "rt" {
		t.Errorf("token = %+v", tok)
	}
}

func TestExchangeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadRequest)
	}))
	defer srv.Close()
	s := NewSSO("my-client", srv.URL, "http://localhost/sso/callback", nil)
	if _, err := s.Exchange(context.Background(), "bad", "verifier"); err == nil {
		t.Fatal("expected Exchange to fail on a 400 from the token endpoint")
	}
}

func TestRefresh(t *testing.T) {
	srv := tokenServer(t)
	s := NewSSO("my-client", srv.URL, "http://localhost/sso/callback", nil)

	tok, err := s.Refresh(context.Background(), "old-refresh")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tok.AccessToken != "at" {
		t.Errorf("refreshed access token = %q, want at", tok.AccessToken)
	}
}

func TestSSOHolderSwap(t *testing.T) {
	a := NewSSO("a", "https://x", "http://localhost/cb", nil)
	b := NewSSO("b", "https://x", "http://localhost/cb", nil)

	h := NewSSOHolder(a)
	if h.Get().ClientID() != "a" {
		t.Fatalf("Get after construction = %q, want a", h.Get().ClientID())
	}
	h.Set(b)
	if h.Get().ClientID() != "b" {
		t.Fatalf("Get after Set = %q, want b", h.Get().ClientID())
	}
}
