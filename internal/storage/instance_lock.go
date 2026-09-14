package storage

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

// InstanceLock is an advisory, crash-safe lock for one data directory. A
// dedicated SQLite file lets supported platforms rely on the database lock
// instead of stale PID-file heuristics.
type InstanceLock struct {
	db          *sql.DB
	conn        *sql.Conn
	releaseOnce sync.Once
	releaseErr  error
}

// AcquireInstanceLock takes the exclusive data-directory lock. The DSN uses a
// short busy timeout so a duplicate process receives a clear startup error
// instead of silently waiting beside the owner.
func AcquireInstanceLock(path string) (*InstanceLock, error) {
	if filepath.Clean(path) == "." {
		return nil, fmt.Errorf("instance lock path is empty")
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(100)", filepath.ToSlash(path))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open instance lock: %w", err)
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect instance lock: %w", err)
	}
	lock := &InstanceLock{db: db, conn: conn}
	release := func() { _ = lock.Release() }
	if _, err := conn.ExecContext(context.Background(), `PRAGMA busy_timeout = 100`); err != nil {
		release()
		return nil, fmt.Errorf("configure instance lock: %w", err)
	}
	if _, err := conn.ExecContext(context.Background(), `PRAGMA locking_mode = EXCLUSIVE`); err != nil {
		release()
		return nil, fmt.Errorf("configure exclusive instance lock: %w", err)
	}
	if _, err := conn.ExecContext(context.Background(), `BEGIN EXCLUSIVE`); err != nil {
		release()
		return nil, fmt.Errorf("another Knowledge instance owns this data directory: %w", err)
	}
	return lock, nil
}

// Release ends the exclusive transaction and closes the lock database. It is
// safe to call more than once, including during startup-error cleanup.
func (l *InstanceLock) Release() error {
	if l == nil {
		return nil
	}
	l.releaseOnce.Do(func() {
		if l.conn != nil {
			_, _ = l.conn.ExecContext(context.Background(), `ROLLBACK`)
			if err := l.conn.Close(); err != nil && l.releaseErr == nil {
				l.releaseErr = err
			}
		}
		if l.db != nil {
			if err := l.db.Close(); err != nil && l.releaseErr == nil {
				l.releaseErr = err
			}
		}
	})
	return l.releaseErr
}
