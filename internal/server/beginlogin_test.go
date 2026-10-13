package server

import (
	"io"
	"log/slog"
	"testing"

	"github.com/BOS-Development/esi-proxy/internal/esi"
)

func newLoginServer(t *testing.T, clientID string) *Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sso := esi.NewSSOHolder(esi.NewSSO(clientID, "https://login.example", "http://localhost/cb", nil))
	return New(nil, sso, nil, nil, nil, log, t.TempDir(), "https://login.example", "http://localhost/cb")
}

func TestBeginLoginReturnsURLAndStoresState(t *testing.T) {
	srv := newLoginServer(t, "client-id")

	url, err := srv.BeginLogin(7)
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	if url == "" {
		t.Fatal("BeginLogin returned an empty URL")
	}

	srv.loginMu.Lock()
	n := len(srv.loginTxn)
	srv.loginMu.Unlock()
	if n != 1 {
		t.Fatalf("pending login transactions = %d, want 1", n)
	}
}

func TestBeginLoginRequiresClientID(t *testing.T) {
	srv := newLoginServer(t, "")
	if _, err := srv.BeginLogin(1); err == nil {
		t.Fatal("expected BeginLogin to fail when no client_id is configured")
	}
}
