package engine

import (
	"context"
	"time"

	"github.com/KHAEntertainment/self-updating-skills/internal/store"
)

// releaseLeaseBounded releases the named lease using a bounded, cancellation-
// independent context, so a canceled run still cleans up (L4). It surfaces
// release errors, including a 0-row release (ErrLeaseLostRow).
func releaseLeaseBounded(ctx context.Context, st *store.Store, name, holder, path string) error {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := st.ReleaseLease(cctx, name, holder); err != nil {
		if isLeaseLostRow(err) {
			// Already gone (taken over or released): that IS the cleanup
			// outcome the holder wants; not an error to surface to callers.
			return nil
		}
		return &Error{Op: "lease", Path: path, Err: err}
	}
	return nil
}

func isLeaseLostRow(err error) bool {
	if err == nil {
		return false
	}
	return err.Error() == "lease lost: holder not found"
}
