// Package store opens the engine's local SQLite state and applies embedded
// migrations gated by PRAGMA user_version.
//
// Phase 1 defines no tables yet; the migration runner is the contract that
// later phases (runs, snapshots, row verdicts, lease) will build on.
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// migrations holds ordered schema steps. Each step i is version i+1.
// Phase 1 intentionally ships an empty set: the runner must be in place
// before any table is introduced.
var migrations = []string{}

// Open opens (creating if needed) the SQLite database at path and applies
// any pending embedded migrations.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	if err := Migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Migrate applies embedded migrations in order, tracking progress in
// PRAGMA user_version. It is idempotent and safe to call repeatedly.
func Migrate(db *sql.DB) error {
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	for i, step := range migrations {
		ver := i + 1
		if ver <= v {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", ver, err)
		}
		if _, err := tx.Exec(step); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", ver, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", ver)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("set user_version %d: %w", ver, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", ver, err)
		}
	}
	return nil
}
