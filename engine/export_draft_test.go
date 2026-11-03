package engine

import (
	"context"
	"encoding/json"

	"github.com/KHAEntertainment/self-updating-skills/internal/redirect"
	"github.com/KHAEntertainment/self-updating-skills/internal/store"
)

// SetDrafter replaces the drafting client factory for a test and returns
// the restore function.
func SetDrafter(f func() (redirect.Drafter, error)) func() {
	old := newDrafter
	newDrafter = f
	return func() { newDrafter = old }
}

// ApplyPlanForRun applies plan under a fresh update lease inside a recorded
// run, so drafted rows' verdicts are persisted under that run, and finishes
// the run with the same report shape Update writes.
func ApplyPlanForRun(ctx context.Context, statePath string, plan *UpdatePlan, runner string) ([]SkillResult, int64, error) {
	st, err := store.Open(statePath)
	if err != nil {
		return nil, 0, err
	}
	defer st.Close()
	holder := store.NewHolder()
	if ok, err := st.AcquireLease(ctx, "update", holder, store.LeaseTTL); err != nil || !ok {
		return nil, 0, err
	}
	defer releaseLeaseBestEffort(ctx, st, "update", holder, statePath)
	runID, err := st.StartRun(ctx, runner)
	if err != nil {
		return nil, 0, err
	}
	results, aerr := applyPlanRun(ctx, st, holder, plan, runner, runID)
	outcome := "ok"
	for _, r := range results {
		if r.Status == "failed" {
			outcome = "failed"
		}
	}
	if aerr != nil {
		outcome = "failed"
	}
	rep, _ := json.Marshal(struct {
		Results []SkillResult `json:"results"`
	}{results})
	if err := st.FinishRun(context.WithoutCancel(ctx), runID, outcome, string(rep)); err != nil {
		return results, runID, err
	}
	return results, runID, aerr
}
