// Package db opens the proxy's SQLite database and applies its schema.
package db

import (
	"database/sql"
	_ "embed"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/BOS-Development/esi-bridge/internal/buildinfo"
)

//go:embed schema.sql
var schema string

// FileName is the database's file name within the data directory.
// Development builds use their own database ("esi-proxy-dev.db"), so
// running one never touches the characters, grants or schema of a release
// install sharing the same data directory.
func FileName() string {
	if buildinfo.Dev {
		return "esi-proxy-dev.db"
	}
	return "esi-proxy.db"
}

// Open opens (creating if necessary) the SQLite database in dataDir and
// applies the schema. The returned *sql.DB is safe for concurrent use.
func Open(dataDir string) (*sql.DB, error) {
	path := filepath.Join(dataDir, FileName())

	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)", path)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// modernc.org/sqlite doesn't support concurrent writers well; a single
	// connection avoids SQLITE_BUSY without needing a separate pool/lock.
	conn.SetMaxOpenConns(1)

	if _, err := conn.Exec(schema); err != nil {
		conn.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	if err := migrate(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}

	return conn, nil
}

// migrate applies schema changes that can't be expressed with the idempotent
// CREATE TABLE IF NOT EXISTS statements in schema.sql — chiefly adding columns
// to tables that already exist in an older database.
func migrate(conn *sql.DB) error {
	// tokens_updated_at was added after the first release. ADD COLUMN can't use
	// schema.sql's strftime() default (SQLite only allows constant defaults for
	// ADD COLUMN), so add it nullable and backfill existing rows from created_at.
	has, err := hasColumn(conn, "characters", "tokens_updated_at")
	if err != nil {
		return err
	}
	if !has {
		if _, err := conn.Exec(`ALTER TABLE characters ADD COLUMN tokens_updated_at TEXT`); err != nil {
			return fmt.Errorf("add characters.tokens_updated_at: %w", err)
		}
		if _, err := conn.Exec(`UPDATE characters SET tokens_updated_at = created_at WHERE tokens_updated_at IS NULL`); err != nil {
			return fmt.Errorf("backfill characters.tokens_updated_at: %w", err)
		}
	}

	// client_id and reauth_reason were added later still. Both have constant
	// defaults, so ADD COLUMN can declare them NOT NULL directly.
	for _, col := range []struct{ name, def string }{
		{"client_id", `TEXT NOT NULL DEFAULT ''`},
		{"reauth_reason", `TEXT NOT NULL DEFAULT ''`},
	} {
		has, err := hasColumn(conn, "characters", col.name)
		if err != nil {
			return err
		}
		if !has {
			if _, err := conn.Exec(`ALTER TABLE characters ADD COLUMN ` + col.name + ` ` + col.def); err != nil {
				return fmt.Errorf("add characters.%s: %w", col.name, err)
			}
		}
	}
	return nil
}

// hasColumn reports whether a table already has a column of the given name.
func hasColumn(conn *sql.DB, table, column string) (bool, error) {
	rows, err := conn.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
