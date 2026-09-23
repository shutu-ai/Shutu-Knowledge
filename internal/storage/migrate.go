package storage

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Current storage-format envelope. Future migrations must update the persisted
// singleton and these constants in the same release that changes the format.
// These are compatibility contract versions, not migration counts.
const (
	CurrentStorageFormatVersion = 2
	CurrentStorageReaderVersion = 9
	CurrentStorageWriterVersion = 9
	MinStorageReaderVersion     = 9
	MinStorageWriterVersion     = 9
)

var (
	// ErrSchemaTooNew means the database contains a migration this binary does
	// not know. It must never be auto-downgraded or opened for recovery.
	ErrSchemaTooNew = errors.New("storage schema is newer than this binary")
	// ErrStorageTooNew means the persisted compatibility envelope requires a
	// newer reader or writer than this binary provides.
	ErrStorageTooNew = errors.New("storage format is newer than this binary")
	// ErrStorageUnavailable means metadata is absent or not at a consistent
	// migration state, so the database is not a safe rollback point.
	ErrStorageUnavailable = errors.New("storage format is unavailable")
)

// StorageFormatInfo is the persisted rollback and compatibility envelope.
type StorageFormatInfo struct {
	FormatVersion       int    `json:"formatVersion"`
	MinReaderVersion    int    `json:"minReaderVersion"`
	MinWriterVersion    int    `json:"minWriterVersion"`
	MigrationStatus     string `json:"migrationStatus"`
	SchemaVersion       int    `json:"schemaVersion"`
	SupportedMigrations int    `json:"supportedMigrations"`
}

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	var out []migration
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		var version int
		var label string
		if _, err := fmt.Sscanf(name, "%d_%s", &version, &label); err != nil {
			return nil, fmt.Errorf("migration file %s must be named NNNN_label.sql", name)
		}
		data, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}
		out = append(out, migration{version: version, name: strings.TrimSuffix(name, ".sql"), sql: string(data)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// Migrate applies pending migrations inside transactions and records them in
// schema_migrations. It is idempotent: already-applied versions are skipped.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	supported := len(migrations)
	var currentVersion int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&currentVersion); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if currentVersion > supported {
		return fmt.Errorf("%w: database version %d, supported version %d", ErrSchemaTooNew, currentVersion, supported)
	}
	for _, m := range migrations {
		var applied int
		if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.version).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %d: %w", m.version, err)
		}
		if applied > 0 {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", m.version, err)
		}
		if _, err := tx.Exec(m.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", m.name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, m.version, m.name); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.version, err)
		}
	}
	return validateStorageFormat(db)
}

// SchemaVersion reports the highest applied migration version (0 when none).
func SchemaVersion(db *sql.DB) (int, error) {
	return SchemaVersionContext(context.Background(), db)
}

// SchemaVersionContext reports the highest applied migration version while
// honoring the caller's cancellation.
func SchemaVersionContext(ctx context.Context, db *sql.DB) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var v int
	err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v)
	return v, err
}

// StorageFormat returns the persisted compatibility envelope and the highest
// applied migration for startup, health, doctor, and rollback drills.
func StorageFormat(db *sql.DB) (StorageFormatInfo, error) {
	return StorageFormatContext(context.Background(), db)
}

// StorageFormatContext returns the persisted compatibility envelope while
// honoring the caller's cancellation.
func StorageFormatContext(ctx context.Context, db *sql.DB) (StorageFormatInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var info StorageFormatInfo
	err := db.QueryRowContext(ctx, `SELECT format_version, min_reader_version, min_writer_version,
		migration_status FROM storage_format WHERE id = 1`).Scan(
		&info.FormatVersion, &info.MinReaderVersion, &info.MinWriterVersion,
		&info.MigrationStatus)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return StorageFormatInfo{}, ctxErr
		}
		return StorageFormatInfo{}, fmt.Errorf("%w: %s", ErrStorageUnavailable, err)
	}
	info.SchemaVersion, err = SchemaVersionContext(ctx, db)
	if err != nil {
		return StorageFormatInfo{}, err
	}
	migrations, err := loadMigrations()
	if err != nil {
		return StorageFormatInfo{}, err
	}
	info.SupportedMigrations = len(migrations)
	return info, nil
}

func validateStorageFormat(db *sql.DB) error {
	info, err := StorageFormat(db)
	if err != nil {
		return err
	}
	if info.MigrationStatus != "ready" {
		return fmt.Errorf("%w: migration status %q", ErrStorageUnavailable, info.MigrationStatus)
	}
	if info.FormatVersion > CurrentStorageFormatVersion ||
		info.MinReaderVersion > CurrentStorageReaderVersion || info.MinWriterVersion > CurrentStorageWriterVersion {
		return fmt.Errorf("%w: format=%d min_reader=%d min_writer=%d",
			ErrStorageTooNew, info.FormatVersion, info.MinReaderVersion, info.MinWriterVersion)
	}
	if info.SchemaVersion > info.SupportedMigrations {
		return fmt.Errorf("%w: database version %d, supported version %d",
			ErrSchemaTooNew, info.SchemaVersion, info.SupportedMigrations)
	}
	return nil
}


