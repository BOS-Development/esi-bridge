package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCreatesDatabaseAndSchema(t *testing.T) {
	dir := t.TempDir()

	conn, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()

	if _, err := os.Stat(filepath.Join(dir, "esi-proxy.db")); err != nil {
		t.Errorf("expected db file to be created: %v", err)
	}

	tables := []string{
		"accounts", "characters", "applications", "grants",
		"pending_grants", "app_permissions", "pending_app_permissions",
	}
	for _, table := range tables {
		if _, err := conn.Exec("SELECT COUNT(*) FROM " + table); err != nil {
			t.Errorf("table %q not present/queryable after Open: %v", table, err)
		}
	}
}

// TestOpenIsIdempotent exercises re-opening an existing database — the schema
// uses CREATE TABLE IF NOT EXISTS, so applying it again on every startup must
// be harmless.
func TestOpenIsIdempotent(t *testing.T) {
	dir := t.TempDir()

	conn1, err := Open(dir)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	conn1.Close()

	conn2, err := Open(dir)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	conn2.Close()
}

// TestOpenEnforcesForeignKeys confirms the foreign_keys pragma in the DSN is
// actually applied: an orphan character (no matching account) must be rejected.
func TestOpenEnforcesForeignKeys(t *testing.T) {
	dir := t.TempDir()
	conn, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()

	_, err = conn.Exec(`INSERT INTO characters
		(id, account_id, name, refresh_token_enc, access_token_enc, access_token_expires_at)
		VALUES (1, 999, 'orphan', x'00', x'00', '2030-01-01T00:00:00Z')`)
	if err == nil {
		t.Fatal("expected a foreign-key violation inserting a character with no account")
	}
}
