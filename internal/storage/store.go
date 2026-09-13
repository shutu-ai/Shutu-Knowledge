// Package storage owns the Knowledge persistence boundary: a single SQLite
// database (business state, chunks, FTS, vectors, jobs) plus the raw source
// store. Agent state never crosses this boundary.
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DB wraps the SQLite handle with Knowledge-specific helpers.
type DB struct {
	*sql.DB
	path   string
	readDB *sql.DB
}

// Open creates the parent directory if needed, opens the database in WAL
// mode, and applies pending migrations.
func Open(path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create data dir: %w", err)
		}
	}
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", filepath.ToSlash(path))
	handle, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite handles one writer; a single connection keeps the import worker
	// pool deterministic and avoids SQLITE_BUSY during multi-file ingestion.
	// Long startup work is kept off the protocol path by App's deferred
	// recovery and non-blocking health snapshot.
	handle.SetMaxOpenConns(1)
	if err := handle.Ping(); err != nil {
		_ = handle.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := Migrate(handle); err != nil {
		_ = handle.Close()
		return nil, err
	}
	db := &DB{DB: handle, path: path}
	// Keep protocol/read paths independent from long write transactions. WAL
	// readers can observe the last committed snapshot while an importer is
	// replacing millions of chunks on the writer connection.
	readHandle, err := sql.Open("sqlite", dsn)
	if err != nil {
		_ = handle.Close()
		return nil, fmt.Errorf("open sqlite read connection: %w", err)
	}
	// Keep several read connections available. Startup recovery and large
	// document-list queries can legitimately take time; one shared reader lets
	// either operation starve every HTTP request even though WAL supports
	// concurrent readers.
	readHandle.SetMaxOpenConns(4)
	readHandle.SetMaxIdleConns(4)
	if err := readHandle.Ping(); err != nil {
		_ = readHandle.Close()
		_ = handle.Close()
		return nil, fmt.Errorf("ping sqlite read connection: %w", err)
	}
	// SQLite sidecars may be recreated by the driver; best-effort chmod keeps
	// the main database out of world/group-readable default locations.
	_ = os.Chmod(path, 0o600)
	db.readDB = readHandle
	return db, nil
}

// Close releases the database handle.
func (db *DB) Close() error {
	readErr := error(nil)
	if db.readDB != nil {
		readErr = db.readDB.Close()
	}
	writeErr := db.DB.Close()
	if writeErr != nil {
		return writeErr
	}
	return readErr
}

// ReadDB exposes the dedicated read connection for checks that accept a
// standard database handle, such as schema probes.
func (db *DB) ReadDB() *sql.DB { return db.readDB }

// Query routes read-only statements to the dedicated WAL reader. Writes and
// transactions continue to use the embedded writer connection below.
func (db *DB) Query(query string, args ...any) (*sql.Rows, error) {
	return db.readDB.Query(query, args...)
}

// QueryContext routes read-only statements to the dedicated WAL reader.
func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return db.readDB.QueryContext(ctx, query, args...)
}

// QueryRow routes read-only statements to the dedicated WAL reader.
func (db *DB) QueryRow(query string, args ...any) *sql.Row {
	return db.readDB.QueryRow(query, args...)
}

// QueryRowContext routes read-only statements to the dedicated WAL reader.
func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return db.readDB.QueryRowContext(ctx, query, args...)
}

// Ping routes readiness probes to the dedicated WAL reader.
func (db *DB) Ping() error { return db.readDB.Ping() }

// PingContext routes readiness probes to the dedicated WAL reader.
func (db *DB) PingContext(ctx context.Context) error { return db.readDB.PingContext(ctx) }

// Exec keeps all mutations on the single writer connection.
func (db *DB) Exec(query string, args ...any) (sql.Result, error) {
	return db.DB.Exec(query, args...)
}

// ExecContext keeps all mutations on the single writer connection.
func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return db.DB.ExecContext(ctx, query, args...)
}

// Begin starts a transaction on the single writer connection.
func (db *DB) Begin() (*sql.Tx, error) { return db.DB.Begin() }

// BeginTx starts a transaction on the single writer connection.
func (db *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return db.DB.BeginTx(ctx, opts)
}

// Path returns the database file path.
func (db *DB) Path() string { return db.path }
