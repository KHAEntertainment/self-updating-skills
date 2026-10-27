package store

import (
	"path/filepath"
	"testing"
)

func TestOpenAndMigrateIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != 0 {
		t.Errorf("user_version = %d, want 0 (no migrations in Phase 1)", v)
	}

	// Migrate is idempotent and safe to call again.
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
}
