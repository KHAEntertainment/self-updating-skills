package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenAndMigrateV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	var v int
	if err := st.DB().QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != 1 {
		t.Errorf("user_version = %d, want 1", v)
	}

	for _, table := range []string{"runs", "snapshots", "row_verdicts", "lease"} {
		var n int
		if err := st.DB().QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("table %s missing after migration v1", table)
		}
	}

	// Migrate is idempotent and safe to call again.
	if err := Migrate(st.DB()); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().Ping(); err != nil {
		t.Fatal(err)
	}
}

func TestLeaseAcquireRelease(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	ok, err := st.AcquireLease(ctx, "update", "a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("first acquire must succeed")
	}
	// Second acquire within TTL by another holder is refused.
	ok, err = st.AcquireLease(ctx, "update", "b", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("second acquire must be refused while the lease is unexpired")
	}
	// Only the holder can release.
	if err := st.ReleaseLease(ctx, "update", "b"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.AcquireLease(ctx, "update", "c", time.Minute); ok {
		t.Fatal("release by a non-holder must not free the lease")
	}
	if err := st.ReleaseLease(ctx, "update", "a"); err != nil {
		t.Fatal(err)
	}
	ok, err = st.AcquireLease(ctx, "update", "c", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("acquire after release must succeed")
	}
}

func TestLeaseExpiryTakeover(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if ok, _ := st.AcquireLease(ctx, "update", "a", -time.Second); !ok {
		t.Fatal("acquire with a TTL in the past must succeed")
	}
	// The lease is already expired: a new holder takes it over.
	ok, err := st.AcquireLease(ctx, "update", "b", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expired lease must be taken over")
	}
}

func TestLeaseRenewLost(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if ok, _ := st.AcquireLease(ctx, "update", "a", -time.Second); !ok {
		t.Fatal("acquire failed")
	}
	// Renewal by the holder succeeds even though the lease is expired
	// (the row exists and is theirs).
	ok, err := st.RenewLease(ctx, "update", "a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("holder renewal must succeed")
	}
	// A non-holder's renewal finds zero rows.
	ok, err = st.RenewLease(ctx, "update", "b", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("non-holder renewal must report lost")
	}
}

func TestSnapshotDedupAndLatest(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.SaveSnapshot(ctx, "s", "p", time.Now(), "sha256:aaa", "body1"); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := st.SaveSnapshot(ctx, "s", "p", later, "sha256:aaa", "body1"); err != nil {
		t.Fatal(err)
	}
	sha, _, ok, err := st.LatestSnapshot(ctx, "s", "p")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || sha != "sha256:aaa" {
		t.Errorf("latest snapshot = %q ok=%v, want sha256:aaa true", sha, ok)
	}
	// Different content (different sha) coexists.
	if err := st.SaveSnapshot(ctx, "s", "p", later.Add(time.Minute), "sha256:bbb", "body2"); err != nil {
		t.Fatal(err)
	}
	sha, _, ok, _ = st.LatestSnapshot(ctx, "s", "p")
	if !ok || sha != "sha256:bbb" {
		t.Errorf("latest snapshot = %q ok=%v, want sha256:bbb true", sha, ok)
	}
	if _, _, ok, _ := st.LatestSnapshot(ctx, "s", "other"); ok {
		t.Error("expected no snapshot for a different page")
	}
}

func TestRuns(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	id, err := st.StartRun(ctx, "ci")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.FinishRun(ctx, id, "ok", `{"a":1}`); err != nil {
		t.Fatal(err)
	}
	runs, err := st.RecentRuns(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	if runs[0].Outcome != "ok" || runs[0].Report != `{"a":1}` {
		t.Errorf("run = %+v", runs[0])
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}
