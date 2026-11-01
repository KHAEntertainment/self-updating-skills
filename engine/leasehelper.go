package engine

import (
	"context"
	"time"

	"github.com/KHAEntertainment/self-updating-skills/internal/store"
)

// releaseLeaseBounded releases the named lease using a bounded, cancellation-
// independent context, so a canceled run still cleans up (L4). It is STRICT:
// a 0-row release propagates store.ErrLeaseLostRow so a normal completion can
// never mask final ownership loss (round 4 item 2). Only best-effort cleanup
// after a known failure may suppress that sentinel (releaseLeaseBestEffort).
func releaseLeaseBounded(ctx context.Context, st *store.Store, name, holder, path string) error {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := st.ReleaseLease(cctx, name, holder); err != nil {
		return &Error{Op: "lease", Path: path, Err: err}
	}
	return nil
}

// releaseLeaseBestEffort is the deferred-cleanup variant: it ignores
// errors entirely (an already-gone row is the expected outcome after a
// failure), so it is used only on error paths, never to decide success.
func releaseLeaseBestEffort(ctx context.Context, st *store.Store, name, holder, path string) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	_ = st.ReleaseLease(cctx, name, holder)
}

// renewLeaseBounded renews the lease with a bounded, cancellation-independent
// context. It is used immediately before any file write and after the
// post-write check, and inside the rollback path before any restore.
func renewLeaseBounded(ctx context.Context, st *store.Store, holder string) (bool, error) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return st.RenewLease(cctx, "update", holder, store.LeaseTTL)
}
