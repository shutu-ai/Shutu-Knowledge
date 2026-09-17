package storage

import (
	"context"
	"fmt"
	"os"
)

// MaintenanceResult reports storage work completed without exposing file
// contents or user data.
type MaintenanceResult struct {
	DatabaseBytes    int64 `json:"databaseBytes"`
	FTSOptimized     bool  `json:"ftsOptimized"`
	Vacuumed         bool  `json:"vacuumed"`
	DatabaseBytesEnd int64 `json:"databaseBytesEnd"`
}

// MaintainSQLite optimizes the external-content FTS index and vacuums when
// the main database reaches threshold bytes. threshold <= 0 forces a vacuum
// for explicit maintenance and tests.
func (db *DB) MaintainSQLite(optimizeFTS, vacuum bool, threshold int64) (MaintenanceResult, error) {
	return db.MaintainSQLiteContext(context.Background(), optimizeFTS, vacuum, threshold)
}

// MaintainSQLiteContext runs each SQLite maintenance mutation with the
// caller's deadline/cancellation. The legacy method above remains available
// to startup and test callers that do not have a request context.
func (db *DB) MaintainSQLiteContext(ctx context.Context, optimizeFTS, vacuum bool, threshold int64) (MaintenanceResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return MaintenanceResult{}, err
	}
	result := MaintenanceResult{}
	if info, err := os.Stat(db.path); err == nil {
		result.DatabaseBytes = info.Size()
	}
	if optimizeFTS {
		if _, err := db.ExecContext(ctx, `INSERT INTO chunk_fts(chunk_fts) VALUES ('optimize')`); err != nil {
			return result, fmt.Errorf("optimize full-text index: %w", err)
		}
		result.FTSOptimized = true
	}
	if vacuum && (threshold <= 0 || result.DatabaseBytes >= threshold) {
		if _, err := db.ExecContext(ctx, `VACUUM`); err != nil {
			return result, fmt.Errorf("vacuum database: %w", err)
		}
		result.Vacuumed = true
	}
	if info, err := os.Stat(db.path); err == nil {
		result.DatabaseBytesEnd = info.Size()
	}
	return result, nil
}
