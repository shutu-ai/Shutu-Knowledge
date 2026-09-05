// Package storage owns the Knowledge persistence boundary: a single SQLite
// database (business state, chunks, FTS, vectors, jobs) plus the raw source
// store. Agent state never crosses this boundary.
package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DB wraps the SQLite handle with Knowledge-specific helpers.
type DB struct {
	*sql.DB
	path string
}

// Open creates the parent directory if needed, opens the database in WAL
// mode, and applies pending migrations.
func Open(path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create data dir: %w", err)
		}
	}
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", filepath.ToSlash(path))
	handle, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite handles one writer; a small pool avoids lock churn while the
	// job manager and web handlers share the file.
	handle.SetMaxOpenConns(1)
	db := &DB{DB: handle, path: path}
	if err := db.Ping(); err != nil {
		_ = handle.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := Migrate(handle); err != nil {
		_ = handle.Close()
		return nil, err
	}
	return db, nil
}

// Close releases the database handle.
func (db *DB) Close() error { return db.DB.Close() }

// Path returns the database file path.
func (db *DB) Path() string { return db.path }
