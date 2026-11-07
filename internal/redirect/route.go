package redirect

import (
	"fmt"
	"strings"
)

// Row and skill outcomes, ranked from best to worst.
const (
	OutcomeAuto     = "auto"
	OutcomePR       = "pr"
	OutcomeFallback = "fallback"
	OutcomeHardStop = "hard_stop"
)

var outcomeRank = map[string]int{OutcomeAuto: 0, OutcomePR: 1, OutcomeFallback: 2, OutcomeHardStop: 3}

// RowResult is one row with its checks and outcome. It is what the run
// report and row_verdicts record.
type RowResult struct {
	Row         Row     `json:"row"`
	Checks      []Check `json:"checks"`
	Outcome     string  `json:"outcome"`
	Reason      string  `json:"reason,omitempty"`
	SnapshotSHA string  `json:"snapshot_sha,omitempty"`
}

// Passed reports whether the named check ran and passed.
func (rr RowResult) Passed(name string) bool {
	for _, c := range rr.Checks {
		if c.Name == name {
			return c.Ran && c.Pass
		}
	}
	return false
}

// FrozenBody is one frozen block's text, for the hard-stop rule.
type FrozenBody struct {
	ID   string
	Body string
}

// RouteRow decides one row's outcome. Precedence:
//  1. hard stop: old literally appears inside a frozen block (the frozen
//     block wins; MVP detection is a literal match);
//  2. fallback: the row was rejected by sanitization (rejected != "") or Q
//     did not pass;
//  3. pr: the row edits an existing row (same id or same old);
//  4. auto: Q plus S or D passed; otherwise pr (Q only).
func RouteRow(rr RowResult, rejected string, existing []ExistingRow, frozen []FrozenBody) RowResult {
	for _, fb := range frozen {
		if rr.Row.Old != "" && strings.Contains(fb.Body, rr.Row.Old) {
			rr.Outcome = OutcomeHardStop
			rr.Reason = fmt.Sprintf("old appears in frozen block %q; the frozen block wins", fb.ID)
			return rr
		}
	}
	if rejected != "" {
		rr.Outcome = OutcomeFallback
		rr.Reason = "rejected: " + rejected
		return rr
	}
	if !rr.Passed("Q") {
		rr.Outcome = OutcomeFallback
		rr.Reason = "quote check failed"
		return rr
	}
	for _, ex := range existing {
		if ex.ID == rr.Row.ID || ex.Old == rr.Row.Old {
			rr.Outcome = OutcomePR
			rr.Reason = fmt.Sprintf("edits existing row %q", ex.ID)
			return rr
		}
	}
	if rr.Passed("S") || rr.Passed("D") {
		rr.Outcome = OutcomeAuto
		return rr
	}
	rr.Outcome = OutcomePR
	rr.Reason = "only the quote check passed"
	return rr
}

// SkillOutcome is the worst row outcome (hard_stop > fallback > pr >
// auto). A draft with zero rows is pr: "nothing changed" cannot be checked
// against a quote. Only an auto outcome may write.
func SkillOutcome(rows []RowResult) string {
	if len(rows) == 0 {
		return OutcomePR
	}
	worst := OutcomeAuto
	for _, r := range rows {
		o := r.Outcome
		if _, known := outcomeRank[o]; !known {
			// An unrouted row can never let the skill write.
			o = OutcomeFallback
		}
		if outcomeRank[o] > outcomeRank[worst] {
			worst = o
		}
	}
	return worst
}
