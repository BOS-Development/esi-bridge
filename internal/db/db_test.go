package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestMigrateAddsTokensUpdatedAt simulates upgrading a pre-existing database
// that predates the tokens_updated_at column: Open must add the column and
// backfill existing rows from created_at rather than failing.
func TestMigrateAddsTokensUpdatedAt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "esi-proxy.db")

	// Create an "old" database whose characters table lacks tokens_updated_at.
	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open old db: %v", err)
	}
	_, err = old.Exec(`
		CREATE TABLE characters (
			id                      INTEGER PRIMARY KEY,
			account_id              INTEGER NOT NULL,
			name                    TEXT NOT NULL,
			refresh_token_enc       BLOB NOT NULL,
			access_token_enc        BLOB NOT NULL,
			access_token_expires_at TEXT NOT NULL,
			scopes                  TEXT NOT NULL DEFAULT '',
			created_at              TEXT NOT NULL DEFAULT '2020-01-02T03:04:05.000Z'
		);
		INSERT INTO characters (id, account_id, name, refresh_token_enc, access_token_enc, access_token_expires_at)
		VALUES (1, 1, 'Old Char', x'00', x'00', '2020-01-02T03:04:05.000Z');
	`)
	if err != nil {
		t.Fatalf("seed old schema: %v", err)
	}
	old.Close()

	// Open applies the current schema (a no-op for the existing table) then the
	// migration, which must add the column and backfill.
	conn, err := Open(dir)
	if err != nil {
		t.Fatalf("Open (migrate): %v", err)
	}
	defer conn.Close()

	has, err := hasColumn(conn, "characters", "tokens_updated_at")
	if err != nil {
		t.Fatalf("hasColumn: %v", err)
	}
	if !has {
		t.Fatal("tokens_updated_at column was not added by migration")
	}

	var updated string
	if err := conn.QueryRow(`SELECT tokens_updated_at FROM characters WHERE id = 1`).Scan(&updated); err != nil {
		t.Fatalf("read backfilled value: %v", err)
	}
	if updated != "2020-01-02T03:04:05.000Z" {
		t.Fatalf("tokens_updated_at = %q, want it backfilled from created_at", updated)
	}
}

// TestMigrateAddsReauthColumns checks that the same old database also gains
// client_id and reauth_reason, defaulting to empty strings for existing rows.
func TestMigrateAddsReauthColumns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "esi-proxy.db")

	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open old db: %v", err)
	}
	_, err = old.Exec(`
		CREATE TABLE characters (
			id                      INTEGER PRIMARY KEY,
			account_id              INTEGER NOT NULL,
			name                    TEXT NOT NULL,
			refresh_token_enc       BLOB NOT NULL,
			access_token_enc        BLOB NOT NULL,
			access_token_expires_at TEXT NOT NULL,
			scopes                  TEXT NOT NULL DEFAULT '',
			created_at              TEXT NOT NULL DEFAULT '2020-01-02T03:04:05.000Z',
			tokens_updated_at       TEXT
		);
		INSERT INTO characters (id, account_id, name, refresh_token_enc, access_token_enc, access_token_expires_at)
		VALUES (1, 1, 'Old Char', x'00', x'00', '2020-01-02T03:04:05.000Z');
	`)
	if err != nil {
		t.Fatalf("seed old schema: %v", err)
	}
	old.Close()

	conn, err := Open(dir)
	if err != nil {
		t.Fatalf("Open (migrate): %v", err)
	}
	defer conn.Close()

	var clientID, reason string
	if err := conn.QueryRow(`SELECT client_id, reauth_reason FROM characters WHERE id = 1`).Scan(&clientID, &reason); err != nil {
		t.Fatalf("read new columns: %v", err)
	}
	if clientID != "" || reason != "" {
		t.Fatalf("client_id = %q, reauth_reason = %q; want both empty", clientID, reason)
	}
}
