package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SaveSnapshot records one fetched page under its sha256. Content is
// deduplicated per (skill, page): fetching the same page again with the
// same hash only refreshes fetched_at.
func (s *Store) SaveSnapshot(ctx context.Context, skill, page string, fetchedAt time.Time, sha, content string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO snapshots(skill, page, fetched_at, sha256, content) VALUES(?,?,?,?,?)
ON CONFLICT(skill, page, sha256) DO UPDATE SET fetched_at = excluded.fetched_at`,
		skill, page, iso(fetchedAt), sha, content)
	if err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}
	return nil
}

// LatestSnapshot returns the most recent snapshot hash for a (skill,
// page), with ok=false when none exists.
func (s *Store) LatestSnapshot(ctx context.Context, skill, page string) (sha string, fetchedAt time.Time, ok bool, err error) {
	var fetched string
	err = s.db.QueryRowContext(ctx,
		"SELECT sha256, fetched_at FROM snapshots WHERE skill=? AND page=? ORDER BY fetched_at DESC LIMIT 1",
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
