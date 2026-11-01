// Package store opens the engine's local SQLite state and applies embedded
// migrations gated by PRAGMA user_version.
//
// The schema (migration v1) is the four Phase 3 tables: runs, snapshots,
// row_verdicts and lease. Every connection gets a busy timeout via the DSN
// pragma, so a losing writer waits briefly and then reads as "skipped:
// lease held" — never a surfaced SQLITE_BUSY error.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// migrations holds ordered schema steps. Each step i is user_version i+1.
// Steps run inside their own BEGIN IMMEDIATE transaction, and user_version
// is re-read after the write lock is acquired: two processes opening the
// same fresh database migrate serially instead of racing.
var migrations = []string{
	// v1: Phase 3 operational state (runs, snapshots, row_verdicts,
	// lease).
	`CREATE TABLE IF NOT EXISTS runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		runner TEXT NOT NULL,
		started_at TEXT NOT NULL,
		finished_at TEXT,
		outcome TEXT NOT NULL,
		report_json TEXT NOT NULL DEFAULT ''
	);
CREATE TABLE IF NOT EXISTS snapshots (
		skill TEXT NOT NULL,
		page TEXT NOT NULL,
		fetched_at TEXT NOT NULL,
		sha256 TEXT NOT NULL,
		content TEXT NOT NULL,
		UNIQUE(skill, page, sha256)
	);
CREATE INDEX IF NOT EXISTS idx_snapshots_lookup ON snapshots(skill, page, fetched_at);
CREATE TABLE IF NOT EXISTS row_verdicts (
		run_id INTEGER NOT NULL REFERENCES runs(id),
		skill TEXT NOT NULL,
		row_json TEXT NOT NULL,
		checks_json TEXT NOT NULL DEFAULT '',
		outcome TEXT NOT NULL,
		quote TEXT NOT NULL DEFAULT '',
		snapshot_sha TEXT NOT NULL DEFAULT ''
	);
CREATE TABLE IF NOT EXISTS lease (
		name TEXT PRIMARY KEY,
		holder TEXT NOT NULL,
		acquired_at TEXT NOT NULL,
		expires_at TEXT NOT NULL
	)`,
}

// DefaultStatePath returns the state database path: UPKEEP_STATE_DB
// overrides; otherwise $RUNNER_TEMP/upkeep/state.db in CI, else
// ~/.agents/upkeep/state.db locally.
func DefaultStatePath() string {
	if p := os.Getenv("UPKEEP_STATE_DB"); p != "" {
		return p
	}
	if rt := os.Getenv("RUNNER_TEMP"); rt != "" {
		return filepath.Join(rt, "upkeep", "state.db")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".agents", "upkeep", "state.db")
	}
	return filepath.Join(os.TempDir(), "upkeep-state.db")
}

// LeaseTTL is how long a lease stays valid without renewal.
const LeaseTTL = 30 * time.Minute

// Store wraps the SQLite handle with the engine's state operations.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path and applies
// any pending embedded migrations. The busy timeout is set on every
// connection through the DSN, so it cannot be forgotten on pooled
// connections.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("store dir: %w", err)
	}
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	if err := Migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the underlying handle (used by tests).
func (s *Store) DB() *sql.DB { return s.db }

// Migrate applies embedded migrations in order, tracking progress in
// PRAGMA user_version. It is idempotent and safe to call repeatedly.
func Migrate(db *sql.DB) error {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	for i, step := range migrations {
		if err := migrateStep(ctx, conn, i+1, step); err != nil {
			return err
		}
	}
	return nil
}

// migrateStep applies one migration under BEGIN IMMEDIATE, re-reading
// user_version once the write lock is held so concurrent migrators on a
// fresh database serialize instead of double-applying.
func migrateStep(ctx context.Context, conn *sql.Conn, ver int, step string) error {
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin migration %d: %w", ver, err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()
	var v int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	if v >= ver {
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return fmt.Errorf("commit migration %d: %w", ver, err)
		}
		committed = true
		return nil
	}
	if _, err := conn.ExecContext(ctx, step); err != nil {
		return fmt.Errorf("apply migration %d: %w", ver, err)
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", ver)); err != nil {
		return fmt.Errorf("set user_version %d: %w", ver, err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit migration %d: %w", ver, err)
	}
	committed = true
	return nil
}

// dsn builds the SQLite URI for path, escaping path characters (? # %
// space) while keeping the pragma options.
func dsn(path string) string {
	esc := strings.ReplaceAll(url.PathEscape(path), "%2F", "/")
	return "file:" + esc + "?_pragma=busy_timeout(10000)"
}

// isBusy reports whether err is a SQLite locking condition (the losing
// writer's BEGIN IMMEDIATE after the busy timeout).
func isBusy(err error) bool {
	if err == nil {
		return false
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "database is locked") || strings.Contains(m, "busy")
}

// NewHolder returns a fresh lease holder identity for this process.
func NewHolder() string {
	var b [8]byte
	if _, err := randRead(b[:]); err != nil {
		return fmt.Sprintf("%d", os.Getpid())
	}
	return fmt.Sprintf("%d-%x", os.Getpid(), b)
}

// iso formats t as RFC3339 (the storage format for all timestamps).
func iso(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// nowUTC returns the current time in UTC.
func nowUTC() time.Time { return time.Now().UTC() }

// parseISO parses a stored RFC3339 timestamp.
func parseISO(s string) (time.Time, error) { return time.Parse(time.RFC3339, s) }
