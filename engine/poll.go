package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/KHAEntertainment/self-updating-skills/internal/check"
	"github.com/KHAEntertainment/self-updating-skills/internal/render"
	"github.com/KHAEntertainment/self-updating-skills/internal/skill"
	"github.com/KHAEntertainment/self-updating-skills/internal/source"
	"github.com/KHAEntertainment/self-updating-skills/internal/store"
)

// ErrLeaseHeld is returned by mutating engine operations when another
// process holds the single-writer lease. Callers print
// "skipped: lease held" and exit 0.
var ErrLeaseHeld = errors.New("skipped: lease held")

// ErrLeaseLost is returned when the lease row is gone mid-run (expired
// and taken over, or released): the run must stop writing at once.
var ErrLeaseLost = errors.New("lease lost")

// PollOptions controls a poll run.
type PollOptions struct {
	// StatePath is the SQLite state database path ("" = the default
	// resolved by store.DefaultStatePath).
	StatePath string
	// SeedDocs fills missing docs.<page>.verified_sha baselines from the
	// current snapshot. It is an explicit, deliberate act — never implied
	// by a plain poll or update (upkeep-auto-advance never seeds).
	SeedDocs bool
	// Runner is the owner runner gating sidecar writes ("ci" or "local").
	Runner string
}

// Poll is the public poll operation: it opens the state database, takes
// the single-writer lease ("update" — the same lease `update` uses, so a
// poll and an update can never write the same skills at once), fetches the
// latest upstream versions and tracked doc pages, records snapshots, and
// writes the sidecar + rendered SKILL.md for every owned, non-blocked
// skill. On a held lease it returns ErrLeaseHeld and writes nothing.
//
// The lease is held for the whole poll; ownership is renewed after
// fetching and immediately before each skill's file writes, so an expired
// lease taken over by another process stops this poll at once
// (ErrLeaseLost).
func Poll(ctx context.Context, dirs []string, opts PollOptions) ([]Drift, error) {
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
		return nil, ErrLeaseHeld
	}
	released := false
	defer func() {
		if !released {
			_ = st.ReleaseLease(ctx, "update", holder)
		}
	}()

	drift, err := pollLocked(ctx, dirs, opts, st, holder)
	if err != nil {
		return nil, &Error{Op: "poll", Path: path, Err: err}
	}
	if err := st.ReleaseLease(ctx, "update", holder); err != nil {
		return nil, &Error{Op: "lease", Path: path, Err: err}
	}
	released = true
	return drift, nil
}

// pollLocked runs the poll body while the caller holds the update lease
// (st non-nil). It renews ownership before each skill's writes.
func pollLocked(ctx context.Context, dirs []string, opts PollOptions, st *store.Store, holder string) ([]Drift, error) {
	out := make([]Drift, 0, len(dirs))
	for _, dir := range dirs {
		dr, err := pollOne(ctx, dir, opts, st, holder)
		if err != nil {
			return nil, err
		}
		out = append(out, dr)
	}
	return out, nil
}

// pollDryRun computes drift without any persistence: no state database, no
// lease, no sidecar or file writes. It is the engine of `update --dry-run`.
func pollDryRun(ctx context.Context, dirs []string, opts PollOptions) ([]Drift, error) {
	out := make([]Drift, 0, len(dirs))
	for _, dir := range dirs {
		dr, err := pollOne(ctx, dir, opts, nil, "")
		if err != nil {
			return nil, err
		}
		out = append(out, dr)
	}
	return out, nil
}

func pollOne(ctx context.Context, dir string, opts PollOptions, st *store.Store, holder string) (Drift, error) {
	abs, err := resolveSkillDir(dir)
	if err != nil {
		return Drift{}, err
	}
	dr := Drift{SkillDir: abs}

	sk := filepath.Join(abs, render.SkillName)
	if _, err := os.Stat(sk); err != nil {
		if os.IsNotExist(err) {
			dr.SkipReason = "no SKILL.md"
			return dr, nil
		}
		return dr, err
	}
	cfg := skill.ParseFrontmatter(sk)
	sc, err := render.LoadSidecar(abs)
	if err != nil {
		dr.BlockReason = fmt.Sprintf("malformed sidecar: %v", err)
		return dr, nil
	}
	if sc == nil {
		dr.SkipReason = "no sidecar"
		return dr, nil
	}
	dr.Owner = sc.Owner
	dr.AutoAdvance = cfg.AutoAdvance

	owned := sc.Owner == opts.Runner && (sc.Owner == "ci" || sc.Owner == "local")
	if !owned {
		dr.SkipReason = fmt.Sprintf("owner %q does not match runner %q", sc.Owner, opts.Runner)
	}

	today := time.Now().Format("2006-01-02")

	// Resolve pins (manual pins have no upstream; they are reported as-is).
	shaPins := map[string]bool{}
	for _, pin := range cfg.Pins {
		if pin.Kind == "manual" {
			continue
		}
		pd := PinDrift{Name: pin.Name, Kind: pin.Kind}
		if p, ok := sc.Pins[pin.Name]; ok {
			pd.VerifiedFor = p.VerifiedFor
			pd.LatestSeen = p.LatestSeen
		}
		cur, rerr := source.ResolveCurrentE(ctx, pin)
		if rerr != nil {
			pd.Error = rerr.Error()
			dr.Pins = append(dr.Pins, pd)
			continue
		}
		sha := isSHA(pin.Version)
		if sha {
			if l := len(pin.Version); l < len(cur) {
				cur = cur[:l]
			}
		}
		if newerVersion(cur, pd.LatestSeen, sha) {
			pd.LatestSeen = cur
		}
		if sha {
			shaPins[pin.Name] = true
		}
		dr.Pins = append(dr.Pins, pd)
	}

	// Tracked pages: a declared upkeep-source-pages list is authoritative
	// (empty retires all); an absent key falls back to the sidecar docs
	// keys (C2).
	tracked := trackedPages(cfg, sc)
	pages := map[string]bool{}
	for _, p := range tracked {
		pages[p] = true
	}
	pageNames := make([]string, 0, len(pages))
	for p := range pages {
		pageNames = append(pageNames, p)
	}
	sort.Strings(pageNames)
	bodies := map[string][]byte{}
	for _, pg := range pageNames {
		dd := DocDrift{Page: pg}
		if d, ok := sc.Docs[pg]; ok {
			dd.VerifiedSHA = d.VerifiedSHA
		}
		body, ferr := source.FetchPage(ctx, cfg.Sources.BaseURL, pg, cfg.Sources.Ext)
		if ferr != nil {
			dd.Error = ferr.Error()
			dr.Docs = append(dr.Docs, dd)
			continue
		}
		norm := bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n"))
		bodies[pg] = norm
		sha := sha256Hex(norm)
		dd.LatestSHA = sha
		dd.Changed = dd.VerifiedSHA != "" && dd.VerifiedSHA != sha
		dr.Docs = append(dr.Docs, dd)
	}

	if st == nil {
		// Dry-run: nothing is ever written.
		return dr, nil
	}

	if dr.SkipReason != "" || dr.BlockReason != "" {
		// No writes of any kind for this skill — sidecar, files, or DB
		// snapshots (F2). A skipped/blocked skill is untouched.
		return dr, nil
	}

	// Pre-write gate: refuse to write to a skill that is TAMPERED or has
	// any DRIFT/marker item — render would overwrite a hand edit or a
	// frozen violation would be masked.
	if err := gatePreWrite(ctx, abs, cfg, sc); err != nil {
		dr.BlockReason = err.Error()
		return dr, nil
	}

	// Renew lease ownership immediately before this skill's writes: a
	// takeover during fetching stops us at once.
	ok, err := st.RenewLease(ctx, "update", holder, store.LeaseTTL)
	if err != nil {
		return dr, err
	}
	if !ok {
		return dr, ErrLeaseLost
	}

	sc2 := cloneSidecar(sc)
	for _, pd := range dr.Pins {
		p, ok := sc2.Pins[pd.Name]
		if !ok {
			p = render.PinData{Kind: pd.Kind}
		}
		p.CheckedAt = today
		if pd.Error == "" && pd.LatestSeen != "" && newerVersion(pd.LatestSeen, p.LatestSeen, shaPins[pd.Name]) {
			p.LatestSeen = pd.LatestSeen
		}
		if sc2.Pins == nil {
			sc2.Pins = map[string]render.PinData{}
		}
		sc2.Pins[pd.Name] = p
	}
	// Prune retired docs keys (declared list authoritative) and report them.
	var pruned []string
	for k := range sc2.Docs {
		if !pages[k] {
			pruned = append(pruned, k)
			delete(sc2.Docs, k)
		}
	}
	if len(pruned) > 0 {
		sort.Strings(pruned)
		dr.Pruned = pruned
	}
	for _, dd := range dr.Docs {
		d, ok := sc2.Docs[dd.Page]
		if !ok {
			d = render.DocData{}
		}
		if dd.Error == "" && dd.LatestSHA != "" {
			d.LatestSHA = dd.LatestSHA
		}
		if opts.SeedDocs && d.VerifiedSHA == "" && dd.Error == "" && dd.LatestSHA != "" {
			d.VerifiedSHA = dd.LatestSHA
			d.VerifiedAt = today
		}
		if sc2.Docs == nil {
			sc2.Docs = map[string]render.DocData{}
		}
		sc2.Docs[dd.Page] = d
	}

	raw, err := os.ReadFile(sk)
	if err != nil {
		return dr, err
	}
	out, err := render.Build(raw, sc2)
	if err != nil {
		dr.BlockReason = fmt.Sprintf("render refused: %v", err)
		return dr, nil
	}
	if err := writeWithRollback(ctx, abs, sc2, out); err != nil {
		return dr, err
	}

	// Store snapshot content after a successful write, reusing the bodies
	// fetched above.
	for _, dd := range dr.Docs {
		if dd.Error != "" || dd.LatestSHA == "" {
			continue
		}
		if err := st.SaveSnapshot(ctx, filepath.Base(abs), dd.Page, time.Now(), dd.LatestSHA, string(bodies[dd.Page])); err != nil {
			dr.BlockReason = fmt.Sprintf("store: %v", err)
			return dr, nil
		}
	}

	return dr, nil
}

// trackedPages returns the pages a poll tracks: a declared
// upkeep-source-pages list is authoritative (an empty declaration retires
// all pages); an absent key falls back to the sidecar's docs keys (C2).
func trackedPages(cfg skill.Config, sc *render.Sidecar) []string {
	if cfg.Sources.PagesDeclared {
		return append([]string(nil), cfg.Sources.Pages...)
	}
	out := make([]string, 0, len(sc.Docs))
	for k := range sc.Docs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// newerVersion reports whether cur should replace prev as latest_seen.
func newerVersion(cur, prev string, sha bool) bool {
	if prev == "" {
		return true
	}
	if sha {
		return cur != prev
	}
	return source.VerCmp(cur, prev) > 0
}

// gatePreWrite computes the pre-write check gate: TAMPERED or any DRIFT
// item (including marker/sidecar problems) refuses the write. CONTENT-LAG
// and STALE do NOT refuse — they are the normal state of a skill awaiting
// an update, not corruption.
func gatePreWrite(ctx context.Context, abs string, cfg skill.Config, sc *render.Sidecar) error {
	r, err := check.Compute(ctx, cfg, nil, abs, sc, nil)
	if err != nil {
		return fmt.Errorf("pre-write check: %w", err)
	}
	if r.Verdict == "TAMPERED" {
		return fmt.Errorf("TAMPERED — refused to write")
	}
	for _, it := range r.Items {
		if it.Kind == "DRIFT" {
			return fmt.Errorf("DRIFT (%s) — refused to write", it.Line)
		}
	}
	return nil
}

// cloneSidecar returns a deep-enough copy for mutation (maps copied).
func cloneSidecar(sc *render.Sidecar) *render.Sidecar {
	out := &render.Sidecar{Schema: sc.Schema, Owner: sc.Owner, Redirects: sc.Redirects}
	if sc.Pins != nil {
		out.Pins = make(map[string]render.PinData, len(sc.Pins))
		for k, v := range sc.Pins {
			out.Pins[k] = v
		}
	}
	if sc.Docs != nil {
		out.Docs = make(map[string]render.DocData, len(sc.Docs))
		for k, v := range sc.Docs {
			out.Docs[k] = v
		}
	}
	return out
}

// writeWithRollback writes the sidecar then SKILL.md, restoring both files
// to their prior bytes if any write fails — nothing half-advanced stays on
// disk. A failed restore is surfaced (not discarded): it is the worst case
// and must be loud.
func writeWithRollback(ctx context.Context, abs string, sc *render.Sidecar, out []byte) error {
	sidecarPath := filepath.Join(abs, render.SidecarName)
	origSC, err := os.ReadFile(sidecarPath)
	if err != nil {
		return err
	}
	skillPath := filepath.Join(abs, render.SkillName)
	origSK, err := os.ReadFile(skillPath)
	if err != nil {
		return err
	}
	if _, _, err := render.Apply(abs, sc, out); err != nil {
		var restoreErrs []string
		if e := render.WriteFileAtomic(sidecarPath, origSC); e != nil {
			restoreErrs = append(restoreErrs, "sidecar: "+e.Error())
		}
		if e := render.WriteFileAtomic(skillPath, origSK); e != nil {
			restoreErrs = append(restoreErrs, "skill: "+e.Error())
		}
		if len(restoreErrs) > 0 {
			return fmt.Errorf("write: %w; restore failed: %s", err, strings.Join(restoreErrs, "; "))
		}
		return err
	}
	return nil
}
