package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SaveSnapshot records one fetched page under its sha256. Content is
// deduplicated per (skill, page). INSERT OR REPLACE re-inserts a matching
// row with a fresh rowid, so a re-fetch of an old hash becomes the newest
// row — a page that changes A→B→A within one second reads A as latest
// (C6), never the stale intermediate B.
func (s *Store) SaveSnapshot(ctx context.Context, skill, page string, fetchedAt time.Time, sha, content string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT OR REPLACE INTO snapshots(skill, page, fetched_at, sha256, content) VALUES(?,?,?,?,?)`,
		skill, page, iso(fetchedAt), sha, content)
	if err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}
	return nil
}

// LatestSnapshot returns the most recent snapshot hash for a (skill,
// page), with ok=false when none exists. Within an equal fetched_at
// second, the newest rowid wins (the re-fetch order).
func (s *Store) LatestSnapshot(ctx context.Context, skill, page string) (sha string, fetchedAt time.Time, ok bool, err error) {
	var fetched string
	err = s.db.QueryRowContext(ctx,
		"SELECT sha256, fetched_at FROM snapshots WHERE skill=? AND page=? ORDER BY fetched_at DESC, rowid DESC LIMIT 1",
		skill, page).Scan(&sha, &fetched)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, fmt.Errorf("latest snapshot: %w", err)
	}
	t, perr := parseISO(fetched)
	if perr != nil {
		return "", time.Time{}, false, fmt.Errorf("snapshot fetched_at: %w", perr)
	}
	return sha, t, true, nil
}

// SnapshotContent returns the stored text of one (skill, page) snapshot by
// its sha256, with ok=false when this database never fetched it (a fresh
// database, or a verified_sha recorded on another machine). Drafting and
// the Q/D verifiers read snapshots only through this lookup, so evidence
// is always tied to an exact hash.
func (s *Store) SnapshotContent(ctx context.Context, skill, page, sha string) (content string, ok bool, err error) {
	if sha == "" {
		return "", false, nil
	}
	err = s.db.QueryRowContext(ctx,
		"SELECT content FROM snapshots WHERE skill=? AND page=? AND sha256=? LIMIT 1",
		skill, page, sha).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("snapshot content: %w", err)
	}
	return content, true, nil
}
