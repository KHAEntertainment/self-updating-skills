package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/KHAEntertainment/self-updating-skills/internal/check"
	"github.com/KHAEntertainment/self-updating-skills/internal/render"
	"github.com/KHAEntertainment/self-updating-skills/internal/skill"
	"github.com/KHAEntertainment/self-updating-skills/internal/source"
	"github.com/KHAEntertainment/self-updating-skills/internal/store"
)

// PollOptions controls a poll run.
type PollOptions struct {
	// Persist stores fetched page snapshots in the SQLite store (st must
	// be non-nil then).
	Persist bool
	// WriteSidecar writes the updated sidecar and renders SKILL.md.
	// Poll is the only writer of fetch facts (latest_seen, checked_at,
	// latest_sha); without it the poll is a read-only simulation.
	WriteSidecar bool
	// SeedDocs fills missing docs.<page>.verified_sha baselines from the
	// current snapshot. It is an explicit, deliberate act — never implied
	// by a plain poll or update (upkeep-auto-advance never seeds).
	SeedDocs bool
	// Runner is the owner runner gating sidecar writes ("ci" or "local").
	Runner string
}

// Poll fetches the latest upstream versions and tracked doc pages for each
// skill dir, and — per options — records snapshots and writes the sidecar
// + rendered SKILL.md. A skill whose sidecar write is gated off (owner
// mismatch, TAMPERED, DRIFT) or impossible (no sidecar) is reported with a
// SkipReason/BlockReason and is otherwise untouched.
func Poll(ctx context.Context, dirs []string, opts PollOptions, st *store.Store) ([]Drift, error) {
	out := make([]Drift, 0, len(dirs))
	for _, dir := range dirs {
		dr, err := pollOne(ctx, dir, opts, st)
		if err != nil {
			return nil, err
		}
		out = append(out, dr)
	}
	return out, nil
}

func pollOne(ctx context.Context, dir string, opts PollOptions, st *store.Store) (Drift, error) {
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

	// Tracked pages = union of frontmatter upkeep-source-pages and sidecar
	// docs keys.
	pages := map[string]bool{}
	for _, p := range cfg.Sources.Pages {
		pages[p] = true
	}
	for k := range sc.Docs {
		pages[k] = true
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

	if !opts.WriteSidecar || dr.SkipReason != "" || dr.BlockReason != "" {
		return dr, nil
	}

	// Pre-write gate: refuse to write to a skill that is TAMPERED or has
	// any DRIFT/marker item — render would overwrite a hand edit or a
	// frozen violation would be masked.
	if err := gatePreWrite(ctx, abs, cfg, sc); err != nil {
		dr.BlockReason = err.Error()
		return dr, nil
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

	// Store snapshot content after a successful write (persist path),
	// reusing the bodies fetched above.
	if opts.Persist && st != nil {
		for _, dd := range dr.Docs {
			if dd.Error != "" || dd.LatestSHA == "" {
				continue
			}
			if err := st.SaveSnapshot(ctx, filepath.Base(abs), dd.Page, time.Now(), dd.LatestSHA, string(bodies[dd.Page])); err != nil {
				// Snapshot persistence is best-effort; report on the drift.
				dr.BlockReason = fmt.Sprintf("store: %v", err)
				return dr, nil
			}
		}
	}

	return dr, nil
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
// disk.
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
		_ = render.WriteFileAtomic(sidecarPath, origSC)
		_ = render.WriteFileAtomic(skillPath, origSK)
		return err
	}
	return nil
}
