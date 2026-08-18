// Package db opens the proxy's SQLite database and applies its schema.
package db

import (
	"database/sql"
	_ "embed"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

// Open opens (creating if necessary) the SQLite database in dataDir and
// applies the schema. The returned *sql.DB is safe for concurrent use.
func Open(dataDir string) (*sql.DB, error) {
	path := filepath.Join(dataDir, "esi-proxy.db")

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

	return conn, nil
}
