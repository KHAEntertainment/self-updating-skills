package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/KHAEntertainment/self-updating-skills/internal/render"
	"github.com/KHAEntertainment/self-updating-skills/internal/skill"
	"github.com/KHAEntertainment/self-updating-skills/internal/store"
)

// ApplyOptions controls an Apply run.
type ApplyOptions struct {
	// StatePath is the SQLite state database path ("" = default).
	StatePath string
	// Runner is the owner runner re-checked under the lease ("ci" or
	// "local"; default "local").
	Runner string
}

// ApplyResult is the outcome of applying a plan: per-skill outcomes plus
// the (Phase 3 empty) PR/fallback collections the planned API reserves.
type ApplyResult struct {
	SkippedLease bool          `json:"skipped_lease,omitempty"`
	Skills       []SkillResult `json:"skills"`
	Prs          []any         `json:"prs"`
	Fallbacks    []any         `json:"fallbacks"`
}

// Apply is the public apply operation: it opens the state database, takes
// the single-writer lease, and applies the advancement steps of the plan.
// Because the lease is taken after the plan was made, every advance entry
// carries the expected verified_for/latest_seen; Apply re-reads each
// skill's sidecar and metadata under the lease and re-runs the per-skill
// plan decision, skipping (with a reported cause) any skill whose
// authorization no longer yields the same advance. A held lease returns an
// ApplyResult with SkippedLease set (not an error). A lease lost mid-run
// (expired and taken over) stops the whole apply at once with
// ErrLeaseLost.
func Apply(ctx context.Context, plan *UpdatePlan, opts ApplyOptions) (*ApplyResult, error) {
	path := opts.StatePath
	if path == "" {
		path = store.DefaultStatePath()
	}
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
		return &ApplyResult{SkippedLease: true}, nil
	}
	released := false
	defer func() {
		if !released {
			_ = st.ReleaseLease(ctx, "update", holder)
		}
	}()

	runner := opts.Runner
	if runner == "" {
		runner = "local"
	}
	results, err := applyPlan(ctx, st, holder, plan, runner)
	if err != nil {
		return &ApplyResult{Skills: results}, err
	}
	if err := st.ReleaseLease(ctx, "update", holder); err != nil {
		return &ApplyResult{Skills: results}, &Error{Op: "lease", Path: path, Err: err}
	}
	released = true
	return &ApplyResult{Skills: results, Prs: []any{}, Fallbacks: []any{}}, nil
}

// applyPlan executes a plan while the caller holds the update lease. It
// renews ownership before every skill (writing or not) so a lease lost to
// expiry takeover stops the whole cycle at once — no further writes for
// any skill. Individual skill failures continue the cycle.
func applyPlan(ctx context.Context, st *store.Store, holder string, plan *UpdatePlan, runner string) ([]SkillResult, error) {
	results := make([]SkillResult, 0, len(plan.Skills))
	for i := range plan.Skills {
		sp := &plan.Skills[i]
		ok, err := st.RenewLease(ctx, "update", holder, store.LeaseTTL)
		if err != nil {
			return results, err
		}
		if !ok {
			results = append(results, SkillResult{SkillDir: sp.SkillDir, Status: "failed", Reason: "lease lost"})
			return results, ErrLeaseLost
		}
		if sp.Status != "advance" {
			results = append(results, SkillResult{SkillDir: sp.SkillDir, Status: sp.Status, Reason: sp.Reason, DraftPins: sp.DraftPins, DraftPages: sp.DraftPages})
			continue
		}
		sr, err := applyOne(ctx, sp, runner)
		if err != nil {
			sr.Status = "failed"
			sr.Error = err.Error()
		}
		results = append(results, sr)
	}
	return results, nil
}

// applyOne applies one advance skill: pre-write gate, a full re-run of the
// per-skill plan decision under the lease (stale plan → skipped), write
// sidecar → render → check --strict, with full byte restore of both files
// on any failure. Restore errors are surfaced in the returned error.
func applyOne(ctx context.Context, sp *SkillPlan, runner string) (SkillResult, error) {
	sr := SkillResult{SkillDir: sp.SkillDir, Advances: sp.Advances}
	abs := sp.SkillDir
	sk := filepath.Join(abs, render.SkillName)
	cfg := skill.ParseFrontmatter(sk)
	sc, err := render.LoadSidecar(abs)
	if err != nil {
		return sr, fmt.Errorf("load sidecar: %w", err)
	}
	if sc == nil {
		return sr, fmt.Errorf("no sidecar")
	}
	if err := gatePreWrite(ctx, abs, cfg, sc); err != nil {
		return sr, err
	}

	// Stale-plan guard (F1): the plan was authorized under an earlier
	// lease against an earlier policy/evidence. Re-read the sidecar and
	// SKILL.md metadata and re-run the same per-skill plan decision; only
	// if it still yields exactly the same advance (same From→To) do we
	// write. Otherwise skip with a report — nothing is advanced after the
	// policy or evidence changed.
	replanned := planSkill(rebuildDrift(abs, cfg, sc), runner)
	if replanned.Status != "advance" || !sameAdvances(replanned.Advances, sp.Advances) {
		sr.Status = "skipped"
		sr.Reason = fmt.Sprintf("plan stale: re-check under lease yields %s (%s)", replanned.Status, replanned.Reason)
		return sr, nil
	}
	// The original From values still match the re-checked advance.
	for _, adv := range sp.Advances {
		p, ok := sc.Pins[adv.Pin]
		if !ok {
			sr.Status = "skipped"
			sr.Reason = fmt.Sprintf("plan stale: pin %s missing from sidecar", adv.Pin)
			return sr, nil
		}
		if p.VerifiedFor != adv.From {
			sr.Status = "skipped"
			sr.Reason = fmt.Sprintf("plan stale: pin %s verified_for %q != plan from %q", adv.Pin, p.VerifiedFor, adv.From)
			return sr, nil
		}
	}

	sidecarPath := filepath.Join(abs, render.SidecarName)
	origSC, err := os.ReadFile(sidecarPath)
	if err != nil {
		return sr, err
	}
	origSK, err := os.ReadFile(sk)
	if err != nil {
		return sr, err
	}
	restore := func() error {
		var restoreErrs []string
		if e := render.WriteFileAtomic(sidecarPath, origSC); e != nil {
			restoreErrs = append(restoreErrs, "sidecar: "+e.Error())
		}
		if e := render.WriteFileAtomic(sk, origSK); e != nil {
			restoreErrs = append(restoreErrs, "skill: "+e.Error())
		}
		if len(restoreErrs) > 0 {
			return fmt.Errorf("restore failed: %s", strings.Join(restoreErrs, "; "))
		}
		return nil
	}
	fail := func(primary error) (SkillResult, error) {
		if re := restore(); re != nil {
			return sr, fmt.Errorf("%v; %w", primary, re)
		}
		return sr, primary
	}

	today := time.Now().Format("2006-01-02")
	for _, adv := range sp.Advances {
		p := sc.Pins[adv.Pin]
		p.VerifiedFor = adv.To
		p.VerifiedAt = today
		p.CheckedAt = today
		sc.Pins[adv.Pin] = p
	}

	raw, err := os.ReadFile(sk)
	if err != nil {
		return fail(err)
	}
	out, err := render.Build(raw, sc)
	if err != nil {
		return fail(fmt.Errorf("render refused: %w", err))
	}
	if _, _, err := render.Apply(abs, sc, out); err != nil {
		return fail(err)
	}

	// Post-write strict check: an advance must land FRESH. Anything else
	// (STALE, CONTENT-LAG, TAMPERED, UNKNOWN) rolls everything back.
	rep, err := Check(ctx, abs)
	if err != nil {
		return fail(err)
	}
	if rep.Verdict != "FRESH" {
		return fail(fmt.Errorf("post-write check verdict %s (want FRESH)", rep.Verdict))
	}
	sr.Status = "written"
	return sr, nil
}

// rebuildDrift reconstructs the per-skill drift from the current sidecar
// and SKILL.md metadata — the inputs the plan decision consumed — without
// network polling (F1). Latest facts already persist in the sidecar.
func rebuildDrift(abs string, cfg skill.Config, sc *render.Sidecar) Drift {
	dr := Drift{SkillDir: abs, Owner: sc.Owner, AutoAdvance: cfg.AutoAdvance}
	for _, pin := range cfg.Pins {
		if pin.Kind == "manual" {
			continue
		}
		pd := PinDrift{Name: pin.Name, Kind: pin.Kind}
		if p, ok := sc.Pins[pin.Name]; ok {
			pd.VerifiedFor = p.VerifiedFor
			pd.LatestSeen = p.LatestSeen
		}
		dr.Pins = append(dr.Pins, pd)
	}
	for _, pg := range trackedPages(cfg, sc) {
		dd := DocDrift{Page: pg}
		if d, ok := sc.Docs[pg]; ok {
			dd.VerifiedSHA = d.VerifiedSHA
			dd.LatestSHA = d.LatestSHA
			dd.Changed = d.VerifiedSHA != "" && d.VerifiedSHA != d.LatestSHA
		}
		dr.Docs = append(dr.Docs, dd)
	}
	return dr
}

// sameAdvances reports whether two advance lists name the same pins with
// the same From→To transitions (order-independent).
func sameAdvances(a, b []Advance) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, adv := range a {
		seen[adv.Pin+"\x00"+adv.From+"\x00"+adv.To] = true
	}
	for _, adv := range b {
		if !seen[adv.Pin+"\x00"+adv.From+"\x00"+adv.To] {
			return false
		}
	}
	return true
}
