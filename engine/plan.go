package engine

import (
	"context"
	"fmt"

	"github.com/KHAEntertainment/self-updating-skills/internal/source"
)

// Plan derives the update plan from poll drift, applying the auto-advance
// rules (P2) per skill in the settled order:
//
//	a. owner not exactly ci/local, or ≠ runner → skip + report
//	b. any pin or page poll error → blocked, no advance
//	c. any tracked page changed → needs drafting (even with no release)
//	d. no pin moved → no-change (latest_seen < verified_for reported)
//	e. upkeep-auto-advance: none (or invalid, treated as none) → drafting
//	f. any tracked page with no verified_sha baseline → drafting
//	g. tracked pages ≥1 and all unchanged → advance iff every moved pin is
//	   a stable 3-part semver increase (rule 1); zero tracked pages →
//	   advance iff every moved pin is a patch bump (rule 2); else drafting.
//
// Multi-pin skills are all-or-nothing: if one moved pin fails its rule,
// no pin advances.
func Plan(ctx context.Context, drift []Drift, runner string) (*UpdatePlan, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if runner == "" {
		runner = "local"
	}
	plan := &UpdatePlan{}
	for _, dr := range drift {
		plan.Skills = append(plan.Skills, planSkill(dr, runner))
	}
	return plan, nil
}

func planSkill(dr Drift, runner string) SkillPlan {
	sp := SkillPlan{SkillDir: dr.SkillDir}

	if dr.SkipReason != "" {
		sp.Status = "skip"
		sp.Reason = dr.SkipReason
		return sp
	}
	if dr.BlockReason != "" {
		sp.Status = "blocked"
		sp.Reason = dr.BlockReason
		return sp
	}
	// a. owner gate.
	if dr.Owner == "" || (dr.Owner != "ci" && dr.Owner != "local") || dr.Owner != runner {
		sp.Status = "skip"
		sp.Reason = fmt.Sprintf("owner %q does not match runner %q", dr.Owner, runner)
		return sp
	}
	// b. poll errors block.
	for _, p := range dr.Pins {
		if p.Error != "" {
			sp.Status = "blocked"
			sp.Reason = "poll error: " + p.Error
			return sp
		}
	}
	for _, d := range dr.Docs {
		if d.Error != "" {
			sp.Status = "blocked"
			sp.Reason = "poll error: " + d.Error
			return sp
		}
	}
	// c. any changed tracked page → drafting, even with no release.
	for _, d := range dr.Docs {
		if d.Changed {
			sp.Status = "draft"
			sp.Reason = "changed snapshot"
			sp.DraftPages = append(sp.DraftPages, d.Page)
		}
	}
	if sp.Status == "draft" {
		for _, p := range dr.Pins {
			if pinMoved(p) {
				sp.DraftPins = append(sp.DraftPins, p.Name)
			}
		}
		return sp
	}
	// d. no pin moved → no-change.
	var moved []PinDrift
	for _, p := range dr.Pins {
		if pinMoved(p) {
			moved = append(moved, p)
		}
	}
	if len(moved) == 0 {
		sp.Status = "no-change"
		for _, p := range dr.Pins {
			if p.VerifiedFor != "" && p.LatestSeen != "" && source.VerCmp(p.LatestSeen, p.VerifiedFor) < 0 {
				sp.Reason = "latest_seen behind verified_for"
				break
			}
		}
		// A no-change skill with a missing docs baseline still carries an
		// unverified snapshot (check reports CONTENT-LAG); give the lag a
		// visible way out.
		for _, d := range dr.Docs {
			if d.VerifiedSHA == "" {
				sp.Reason = combineReasons(sp.Reason, "missing docs baseline — run: upkeep poll --seed-docs")
				break
			}
		}
		return sp
	}
	// e. auto-advance setting (default "patch" when unset; any other
	// value is treated as "none" and reported).
	aa := dr.AutoAdvance
	if aa == "" {
		aa = "patch"
	}
	if aa != "patch" && aa != "none" {
		sp.Reason = fmt.Sprintf("invalid upkeep-auto-advance %q treated as none", aa)
		aa = "none"
	}
	if aa == "none" {
		sp.Status = "draft"
		sp.Reason = combineReasons(sp.Reason, "auto-advance disabled")
		for _, p := range moved {
			sp.DraftPins = append(sp.DraftPins, p.Name)
		}
		return sp
	}
	// f. missing verified baseline on any tracked page → drafting.
	for _, d := range dr.Docs {
		if d.VerifiedSHA == "" {
			sp.Status = "draft"
			sp.Reason = "missing verified baseline (page " + d.Page + ")"
			for _, p := range moved {
				sp.DraftPins = append(sp.DraftPins, p.Name)
			}
			return sp
		}
	}
	// g. rules 1 and 2.
	tracked := len(dr.Docs)
	for _, p := range moved {
		ok := false
		if tracked >= 1 {
			ok = isStableIncrease(p.VerifiedFor, p.LatestSeen)
		} else {
			ok = isPatch(p.VerifiedFor, p.LatestSeen)
		}
		if !ok {
			sp.Status = "draft"
			sp.Reason = "no auto-advance rule applies"
			for _, q := range moved {
				sp.DraftPins = append(sp.DraftPins, q.Name)
			}
			return sp
		}
	}
	sp.Status = "advance"
	for _, p := range moved {
		sp.Advances = append(sp.Advances, Advance{Pin: p.Name, From: p.VerifiedFor, To: p.LatestSeen})
	}
	sp.DraftPins = nil
	return sp
}

// pinMoved reports whether a pin's recorded latest_seen is ahead of its
// verified_for. SHA pins move by identity (a changed HEAD sha), not by
// version order.
func pinMoved(p PinDrift) bool {
	if p.VerifiedFor == "" || p.LatestSeen == "" {
		return false
	}
	if isSHA(p.VerifiedFor) {
		return p.LatestSeen != p.VerifiedFor
	}
	return source.VerCmp(p.LatestSeen, p.VerifiedFor) > 0
}

func combineReasons(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}
