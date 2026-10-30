package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// AcquireLease attempts to acquire the named lease on behalf of holder. It
// returns true when acquired. An unexpired lease held by another holder
// returns false (the caller prints "skipped: lease held" and exits 0). A
// busy BEGIN IMMEDIATE — a concurrent writer holding the database lock past
// the busy timeout — is treated the same way, so SQLITE_BUSY never
// surfaces to the caller. Expired leases are taken over atomically under
// the same write lock.
func (s *Store) AcquireLease(ctx context.Context, name, holder string, ttl time.Duration) (bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		if isBusy(err) {
			return false, nil
		}
		return false, fmt.Errorf("begin lease: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	now := nowUTC()
	expires := now.Add(ttl)
	var curHolder, curExpires string
	err = conn.QueryRowContext(ctx, "SELECT holder, expires_at FROM lease WHERE name = ?", name).Scan(&curHolder, &curExpires)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO lease(name, holder, acquired_at, expires_at) VALUES(?,?,?,?)",
			name, holder, iso(now), iso(expires)); err != nil {
			return false, err
		}
	case err != nil:
		return false, err
	default:
		exp, perr := parseISO(curExpires)
		if perr != nil {
			return false, fmt.Errorf("lease expires_at: %w", perr)
		}
		if exp.After(now) {
			// Held by another (or our own) unexpired lease.
			return false, nil
		}
		res, err := conn.ExecContext(ctx,
			"UPDATE lease SET holder=?, acquired_at=?, expires_at=? WHERE name=? AND expires_at=?",
			holder, iso(now), iso(expires), name, curExpires)
		if err != nil {
			return false, err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			// Raced with a concurrent takeover; treat as held.
			return false, nil
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return false, err
	}
	committed = true
	return true, nil
}

// ReleaseLease releases a lease held by holder. Only the holder may
// release: the DELETE is scoped by holder, so a stale process can never
// clear another process's lease.
func (s *Store) ReleaseLease(ctx context.Context, name, holder string) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM lease WHERE name=? AND holder=?", name, holder); err != nil {
		return err
	}
	return nil
}

// RenewLease extends a lease held by holder. It returns false — and the
// caller must stop writing — when the lease row is gone (expired and
// taken over by another holder, or released).
func (s *Store) RenewLease(ctx context.Context, name, holder string, ttl time.Duration) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		"UPDATE lease SET expires_at=? WHERE name=? AND holder=?",
		iso(nowUTC().Add(ttl)), name, holder)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}
