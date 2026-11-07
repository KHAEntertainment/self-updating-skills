package engine

import (
	"context"
	"encoding/json"
	"os"

	"github.com/KHAEntertainment/self-updating-skills/internal/store"
)

// PendingResult lists what needs a person after the latest runs: drafted
// rows that went to a PR, the agent fallback or a hard stop (for every
// owner in Phase 4a; ci-owned PR and issue creation is Phase 4b), and the
// skills that failed in the most recent run, with their cause.
type PendingResult struct {
	Rows     []store.RowVerdict `json:"pending_rows"`
	Failures []SkillResult      `json:"failures"`
}

// Pending reads the pending items from the state database. A missing
// database is not an error.
func Pending(ctx context.Context, path string) (*PendingResult, error) {
	res := &PendingResult{Rows: []store.RowVerdict{}, Failures: []SkillResult{}}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return res, nil
		}
		return nil, err
	}
	st, err := store.Open(path)
	if err != nil {
		return nil, &Error{Op: "open store", Path: path, Err: err}
	}
	defer st.Close()
	rows, err := st.PendingRowVerdicts(ctx)
	if err != nil {
		return nil, &Error{Op: "status", Path: path, Err: err}
	}
	if rows != nil {
		res.Rows = rows
	}
	runs, err := st.RecentRuns(ctx, 1)
	if err != nil {
		return nil, &Error{Op: "status", Path: path, Err: err}
	}
	if len(runs) == 1 && runs[0].Report != "" {
		var rep struct {
			Results []SkillResult `json:"results"`
		}
		// A report that does not parse (an early error report) simply has
		// no per-skill failures to list; the run's outcome still shows.
		if json.Unmarshal([]byte(runs[0].Report), &rep) == nil {
			for _, r := range rep.Results {
				if r.Status == "failed" {
					r.Rows = nil
					res.Failures = append(res.Failures, r)
				}
			}
		}
	}
	return res, nil
}
