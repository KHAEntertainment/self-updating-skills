package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/KHAEntertainment/self-updating-skills/internal/render"
	"github.com/KHAEntertainment/self-updating-skills/internal/skill"
)

// applyAdvancedSkills executes the advance steps of a plan: for each
// advance-status skill, renew the lease, run the pre-write gate, write the
// sidecar (verified_for := latest_seen, verified_at/checked_at := today),
// render, then run check --strict. A post-write check that is not FRESH —
// or any write error — restores both files to their prior bytes and marks
// the skill failed. Nothing half-advanced stays on disk.
func applyAdvancedSkills(ctx context.Context, st leaseStore, plan *UpdatePlan, holder string, ttl time.Duration) ([]SkillResult, error) {
	results := make([]SkillResult, 0, len(plan.Skills))
	for i := range plan.Skills {
		sp := &plan.Skills[i]
		sr := SkillResult{SkillDir: sp.SkillDir, Status: sp.Status, Reason: sp.Reason, Advances: sp.Advances}
		if sp.Status != "advance" {
			results = append(results, sr)
			continue
		}
		ok, err := st.RenewLease(ctx, "update", holder, ttl)
		if err != nil {
			return nil, err
		}
		if !ok {
			sr.Status = "failed"
			sr.Reason = "lease lost"
			results = append(results, sr)
			continue
		}
		if err := applyOne(ctx, sp); err != nil {
			sr.Status = "failed"
			sr.Reason = err.Error()
		}
		results = append(results, sr)
	}
	return results, nil
}

// leaseStore is the slice of the store the apply loop needs, so it is
// testable without a real database.
type leaseStore interface {
	RenewLease(ctx context.Context, name, holder string, ttl time.Duration) (bool, error)
}

func applyOne(ctx context.Context, sp *SkillPlan) error {
	abs := sp.SkillDir
	sk := filepath.Join(abs, render.SkillName)
	cfg := skill.ParseFrontmatter(sk)
	sc, err := render.LoadSidecar(abs)
	if err != nil {
		return fmt.Errorf("load sidecar: %w", err)
	}
	if sc == nil {
		return fmt.Errorf("no sidecar")
	}
	if err := gatePreWrite(ctx, abs, cfg, sc); err != nil {
		return err
	}

	sidecarPath := filepath.Join(abs, render.SidecarName)
	origSC, err := os.ReadFile(sidecarPath)
	if err != nil {
		return err
	}
	origSK, err := os.ReadFile(sk)
	if err != nil {
		return err
	}
	restore := func() {
		_ = render.WriteFileAtomic(sidecarPath, origSC)
		_ = render.WriteFileAtomic(sk, origSK)
	}

	today := time.Now().Format("2006-01-02")
	for _, adv := range sp.Advances {
		p, ok := sc.Pins[adv.Pin]
		if !ok {
			restore()
			return fmt.Errorf("pin %s missing from sidecar", adv.Pin)
		}
		if p.VerifiedFor != adv.From {
			restore()
			return fmt.Errorf("pin %s verified_for %q != plan from %q", adv.Pin, p.VerifiedFor, adv.From)
		}
		p.VerifiedFor = adv.To
		p.VerifiedAt = today
		p.CheckedAt = today
		sc.Pins[adv.Pin] = p
	}

	raw, err := os.ReadFile(sk)
	if err != nil {
		restore()
		return err
	}
	out, err := render.Build(raw, sc)
	if err != nil {
		restore()
		return fmt.Errorf("render refused: %w", err)
	}
	if _, _, err := render.Apply(abs, sc, out); err != nil {
		restore()
		return err
	}

	// Post-write strict check: an advance must land FRESH. Anything else
	// (STALE, CONTENT-LAG, TAMPERED, UNKNOWN) rolls everything back.
	rep, err := Check(ctx, abs)
	if err != nil {
		restore()
		return err
	}
	if rep.Verdict != "FRESH" {
		restore()
		return fmt.Errorf("post-write check verdict %s (want FRESH)", rep.Verdict)
	}
	return nil
}
