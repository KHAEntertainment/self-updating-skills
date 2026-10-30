package engine

import (
	"context"
	"os"

	"github.com/KHAEntertainment/self-updating-skills/internal/store"
)

// UpdateOptions controls an update run.
type UpdateOptions struct {
	// DryRun prints the plan and writes nothing: no sidecar, no files,
	// no database, no lease.
	DryRun bool
	// Runner is the owner runner ("ci" or "local"; default "local").
	Runner string
}

// UpdateResult is the outcome of an update.
type UpdateResult struct {
	// SkippedLease is true when another process held the update lease;
	// the CLI prints "skipped: lease held" and exits 0.
	SkippedLease bool          `json:"skipped_lease,omitempty"`
	Plan         *UpdatePlan   `json:"plan,omitempty"`
	Skills       []SkillResult `json:"skills,omitempty"`
	RunID        int64         `json:"run_id,omitempty"`
	RunOutcome   string        `json:"run_outcome,omitempty"`
}

// Update runs the full update cycle for dirs: lease → poll → plan → apply
// → run record → release. The lease is taken before any work and held for
// the whole cycle, so the plan cannot be computed against state another
// writer is mutating. In dry-run mode nothing is persisted and no database
// is created.
func Update(ctx context.Context, dirs []string, opts UpdateOptions) (*UpdateResult, error) {
	if opts.Runner == "" {
		opts.Runner = "local"
	}
	if opts.DryRun {
		drift, err := Poll(ctx, dirs, PollOptions{Runner: opts.Runner}, nil)
		if err != nil {
			return nil, err
		}
		plan, err := Plan(ctx, drift, opts.Runner)
		if err != nil {
			return nil, err
		}
		return &UpdateResult{Plan: plan}, nil
	}

	path := store.DefaultStatePath()
	st, err := store.Open(path)
	if err != nil {
		return nil, &Error{Op: "open store", Path: path, Err: err}
	}
	defer st.Close()

	holder := store.NewHolder()
	ok, err := st.AcquireLease(ctx, "update", holder, store.LeaseTTL)
	if err != nil {
		return nil, &Error{Op: "lease", Path: path, Err: err}
	}
	if !ok {
		return &UpdateResult{SkippedLease: true}, nil
	}
	released := false
	defer func() {
		if !released {
			_ = st.ReleaseLease(ctx, "update", holder)
		}
	}()

	runID, err := st.StartRun(ctx, opts.Runner)
	if err != nil {
		return nil, &Error{Op: "run", Path: path, Err: err}
	}

	drift, err := Poll(ctx, dirs, PollOptions{Persist: true, WriteSidecar: true, Runner: opts.Runner}, st)
	if err != nil {
		return nil, &Error{Op: "poll", Path: path, Err: err}
	}
	plan, err := Plan(ctx, drift, opts.Runner)
	if err != nil {
		return nil, &Error{Op: "plan", Path: path, Err: err}
	}
	results, err := applyAdvancedSkills(ctx, st, plan, holder, store.LeaseTTL)
	if err != nil {
		return nil, &Error{Op: "apply", Path: path, Err: err}
	}

	outcome := "ok"
	for _, r := range results {
		if r.Status == "failed" {
			outcome = "failed"
			break
		}
	}
	report := reportJSON(struct {
		Plan    *UpdatePlan   `json:"plan"`
		Results []SkillResult `json:"results"`
	}{plan, results})
	if err := st.FinishRun(ctx, runID, outcome, report); err != nil {
		return nil, &Error{Op: "run", Path: path, Err: err}
	}
	if err := st.ReleaseLease(ctx, "update", holder); err != nil {
		return nil, &Error{Op: "lease", Path: path, Err: err}
	}
	released = true

	return &UpdateResult{Plan: plan, Skills: results, RunID: runID, RunOutcome: outcome}, nil
}

// StatusResult is the status view: the latest recorded runs.
type StatusResult struct {
	Runs []store.Run `json:"runs"`
}

// Status reports recent update runs from the state database. A missing
// database (no runs yet) is not an error.
func Status(ctx context.Context, path string) (*StatusResult, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return &StatusResult{}, nil
		}
		return nil, err
	}
	st, err := store.Open(path)
	if err != nil {
		return nil, &Error{Op: "open store", Path: path, Err: err}
	}
	defer st.Close()
	runs, err := st.RecentRuns(ctx, 20)
	if err != nil {
		return nil, &Error{Op: "status", Path: path, Err: err}
	}
	return &StatusResult{Runs: runs}, nil
}
