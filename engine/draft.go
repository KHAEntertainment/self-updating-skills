package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/KHAEntertainment/self-updating-skills/internal/redirect"
	"github.com/KHAEntertainment/self-updating-skills/internal/render"
	"github.com/KHAEntertainment/self-updating-skills/internal/skill"
	"github.com/KHAEntertainment/self-updating-skills/internal/source"
	"github.com/KHAEntertainment/self-updating-skills/internal/store"
)

// newDrafter builds the drafting client. Tests replace it with a stub; the
// production value reads ANTHROPIC_API_KEY from the environment only.
var newDrafter = redirect.FromEnv

// draftTimeout bounds one model call, well under store.LeaseTTL.
const draftTimeout = 5 * time.Minute

// changelogPageRe marks tracked pages given to the drafter as context.
var changelogPageRe = regexp.MustCompile(`(?i)(changelog|migrat|release-notes|releases)`)

// draftedPage is one page as drafted against: its snapshot hashes and text.
type draftedPage struct {
	name        string
	latestSHA   string
	verifiedSHA string
	text        string
	prev        string
	hasPrev     bool
}

// draftOne runs the Phase 4 gate for one skill whose plan status is
// "draft", under the caller's update lease: draft rows from the changed
// snapshots, sanitize and verify each row (Q/S/D), route it, and write
// only when every row is auto-accepted. Nothing is written for pr,
// fallback or hard_stop; the skill keeps its pin (CONTENT-LAG). Every row
// result is recorded in row_verdicts (when runID != 0) after the final
// decision.
func draftOne(ctx context.Context, st *store.Store, holder string, sp *SkillPlan, runner string, runID int64) (SkillResult, error) {
	sr := SkillResult{SkillDir: sp.SkillDir, Status: "draft", Reason: sp.Reason, DraftPins: sp.DraftPins, DraftPages: sp.DraftPages}
	abs := sp.SkillDir
	cfg := skill.ParseFrontmatter(filepath.Join(abs, render.SkillName))
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
	// Stale-plan guard (F1): re-run the plan decision under the lease and
	// draft for what it yields now, not for the plan handed in.
	re := planSkill(rebuildDrift(abs, cfg, sc), runner)
	if re.Status != "draft" {
		sr.Status = "skipped"
		sr.Reason = fmt.Sprintf("plan stale: re-check under lease yields %s (%s)", re.Status, re.Reason)
		return sr, nil
	}
	sr.Reason, sr.DraftPins, sr.DraftPages = re.Reason, re.DraftPins, re.DraftPages

	key := filepath.Base(abs)
	pages, err := draftPages(ctx, st, key, cfg, sc)
	if err != nil {
		return sr, err
	}
	in := redirect.Input{Skill: cfg.SkillName}
	for _, n := range re.DraftPins {
		p := sc.Pins[n]
		in.Pins = append(in.Pins, redirect.PinChange{Name: n, VerifiedFor: p.VerifiedFor, LatestSeen: p.LatestSeen})
	}
	for _, r := range sc.Redirects {
		in.Existing = append(in.Existing, redirect.ExistingRow{ID: r.ID, Old: r.Old, New: r.New})
	}
	changed := false
	for _, p := range pages {
		pi := redirect.PageInput{Name: p.name, SHA: p.latestSHA, New: p.text, HasPrevious: p.hasPrev, Changelog: changelogPageRe.MatchString(p.name)}
		if p.hasPrev {
			pi.Diff = render.Diff(p.prev, p.text)
		}
		if p.latestSHA != p.verifiedSHA {
			changed = true
		}
		in.Pages = append(in.Pages, pi)
	}

	var rows []redirect.RowResult
	parseErr := ""
	if !changed {
		// Nothing changed to draft from (a version moved with no doc change
		// the rules could not auto-advance): no model call, zero rows → PR.
		sr.Reason = "zero rows: no changed page to draft from"
	} else if n := redirect.InputSize(in); n > redirect.MaxDraftInputBytes {
		// Too much page text for one bounded model call: no call (and no
		// API key needed), the skill goes to a PR for a human to draft.
		sr.Reason = fmt.Sprintf("pr: drafting input exceeds %d bytes; no model call", redirect.MaxDraftInputBytes)
	} else {
		d, err := newDrafter()
		if err != nil {
			return sr, err
		}
		dctx, cancel := context.WithTimeout(ctx, draftTimeout)
		raw, err := d.Draft(dctx, in)
		cancel()
		if err != nil {
			return sr, fmt.Errorf("drafting: %w", err)
		}
		if ok, err := renewLeaseBounded(ctx, st, holder); err != nil {
			return sr, err
		} else if !ok {
			sr.Status = "failed"
			sr.Reason = "lease lost"
			return sr, ErrLeaseLost
		}
		draft, err := redirect.ParseDraft(raw)
		if err != nil {
			parseErr = err.Error()
		} else {
			rows, err = verifyRows(ctx, st, cfg, draft.Rows, pages, in.Existing, abs)
			if err != nil {
				return sr, err
			}
			if len(rows) == 0 {
				sr.Reason = "zero rows: the draft found nothing to change"
			}
		}
	}

	sr.Rows = rows
	sr.Outcome = redirect.SkillOutcome(rows)
	if parseErr != "" {
		sr.Outcome = redirect.OutcomeFallback
		sr.Reason = "invalid model output: " + parseErr
	}
	// F1: an all-auto draft may advance a page's verified_sha only when an
	// accepted row carries evidence from that page. A changed page with no
	// accepted row sends the skill to a PR, like a zero-row draft.
	coverage := ""
	if sr.Outcome == redirect.OutcomeAuto {
		if p := uncoveredPage(rows, pages); p != "" {
			sr.Outcome = redirect.OutcomePR
			coverage = "page " + p + " changed with no accepted row"
		}
	}
	final := sr.Outcome
	switch sr.Outcome {
	case redirect.OutcomeHardStop:
		sr.Status = "failed"
		sr.Error = "hard stop: " + firstReason(rows, redirect.OutcomeHardStop)
		sr.Reason = "frozen block wins; nothing written"
	case redirect.OutcomeFallback:
		sr.Status = "fallback"
		if parseErr == "" {
			sr.Reason = "fallback: " + firstReason(rows, redirect.OutcomeFallback)
		}
	case redirect.OutcomePR:
		sr.Status = "pr"
		switch {
		case coverage != "":
			sr.Reason = "pr: " + coverage
		case len(rows) > 0:
			sr.Reason = "pr: " + firstReason(rows, redirect.OutcomePR)
		}
	case redirect.OutcomeAuto:
		var werr error
		sr, werr = commitDraft(ctx, st, holder, abs, runner, sr, pages, in.Pins)
		switch sr.Status {
		case "written":
		case "skipped":
			// Stale plan: nothing was written or restored.
			final = "skipped"
		default:
			// Lease loss, render refusal or a non-FRESH strict check: the
			// write did not stand.
			final = "rolled_back"
		}
		if rerr := saveVerdicts(ctx, st, runID, key, sr, final, parseErr); rerr != nil {
			return sr, errors.Join(werr, rerr)
		}
		return sr, werr
	}
	if err := saveVerdicts(ctx, st, runID, key, sr, final, parseErr); err != nil {
		return sr, err
	}
	return sr, nil
}

// draftPages loads every tracked page that has a newest snapshot: the
// newest text (sanitized again) and, when this database holds it, the
// previous verified text.
func draftPages(ctx context.Context, st *store.Store, key string, cfg skill.Config, sc *render.Sidecar) ([]draftedPage, error) {
	var out []draftedPage
	for _, name := range trackedPages(cfg, sc) {
		dd, ok := sc.Docs[name]
		if !ok || dd.LatestSHA == "" {
			continue
		}
		if dd.LatestSHA == dd.VerifiedSHA && !changelogPageRe.MatchString(name) {
			continue
		}
		text, ok, err := st.SnapshotContent(ctx, key, name, dd.LatestSHA)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("snapshot %s of page %s is not in the state database", dd.LatestSHA, name)
		}
		p := draftedPage{name: name, latestSHA: dd.LatestSHA, verifiedSHA: dd.VerifiedSHA, text: string(source.SanitizeDoc([]byte(text)))}
		if dd.VerifiedSHA != "" && dd.VerifiedSHA != dd.LatestSHA {
			prev, ok, err := st.SnapshotContent(ctx, key, name, dd.VerifiedSHA)
			if err != nil {
				return nil, err
			}
			p.prev, p.hasPrev = string(source.SanitizeDoc([]byte(prev))), ok
		} else if dd.VerifiedSHA == dd.LatestSHA {
			p.prev, p.hasPrev = p.text, true
		}
		out = append(out, p)
	}
	return out, nil
}

// verifyRows sanitizes, verifies and routes every drafted row.
func verifyRows(ctx context.Context, st *store.Store, cfg skill.Config, drafted []redirect.Row, pages []draftedPage, existing []redirect.ExistingRow, abs string) ([]redirect.RowResult, error) {
	hosts := redirect.AllowedHosts(cfg)
	byName := map[string]draftedPage{}
	inPages := map[string]bool{}
	newText := map[string]string{}
	for _, p := range pages {
		byName[p.name] = p
		inPages[p.name] = true
		newText[p.name] = p.text
	}
	blocks, err := skill.ScanFrozenBlocks(abs)
	if err != nil {
		return nil, fmt.Errorf("frozen blocks: %w", err)
	}
	var frozen []redirect.FrozenBody
	for _, b := range blocks {
		frozen = append(frozen, redirect.FrozenBody{ID: b.ID, Body: b.Body})
	}

	// The S schema is fetched once per draft, only when a config-key row
	// needs it.
	var cs *redirect.ConfigSchema
	schemaErr := ""
	schemaLoaded := false
	loadSchema := func() {
		if schemaLoaded {
			return
		}
		schemaLoaded = true
		if cfg.Sources.SchemaURL == "" {
			return
		}
		b, err := source.FetchSchema(ctx, cfg.Sources.SchemaURL, hosts)
		if err != nil {
			schemaErr = err.Error()
			return
		}
		if cs, err = redirect.CompileConfigSchema(b); err != nil {
			schemaErr = err.Error()
		}
	}

	seenIDs := map[string]bool{}
	out := make([]redirect.RowResult, 0, len(drafted))
	for _, r := range drafted {
		rr := redirect.RowResult{Row: r, SnapshotSHA: byName[r.Page].latestSHA}
		rejected := ""
		if err := redirect.Sanitize(r, hosts, inPages); err != nil {
			rejected = err.Error()
		} else if seenIDs[r.ID] {
			rejected = fmt.Sprintf("duplicate id %q in the draft", r.ID)
		}
		seenIDs[r.ID] = true
		q := redirect.CheckQ(r, newText)
		rr.Checks = append(rr.Checks, q)
		if rejected == "" && q.Pass {
			if r.Kind == redirect.KindConfigKey {
				loadSchema()
			}
			// One history lookup feeds both S (old-existence evidence for a
			// refused old key) and D.
			h, err := history(ctx, st, cfg, byName[r.Page], r, hosts)
			if err != nil {
				return nil, err
			}
			rr.Checks = append(rr.Checks, redirect.CheckS(r, cs, schemaErr, h))
			rr.Checks = append(rr.Checks, redirect.CheckD(r, byName[r.Page].text, h))
		}
		out = append(out, redirect.RouteRow(rr, rejected, existing, frozen))
	}
	// Two rows rewriting the same old, or a row rewriting the old of a row
	// already in the sidecar, would leave the redirect table ambiguous: a
	// human picks (PR). Olds compare in Canon form.
	for i, r := range out {
		if r.Outcome != redirect.OutcomeAuto {
			continue
		}
		if dup := duplicateOld(out, i, existing); dup {
			out[i].Outcome = redirect.OutcomePR
			out[i].Reason = "duplicate old: " + r.Row.Old
		}
	}
	return out, nil
}

// duplicateOld reports whether row i's old, in Canon form, is also the old
// of another drafted row or of an existing sidecar row.
func duplicateOld(rows []redirect.RowResult, i int, existing []redirect.ExistingRow) bool {
	old := redirect.Canon(rows[i].Row.Old)
	if old == "" {
		return false
	}
	for j, r := range rows {
		if j != i && redirect.Canon(r.Row.Old) == old {
			return true
		}
	}
	for _, ex := range existing {
		if redirect.Canon(ex.Old) == old {
			return true
		}
	}
	return false
}

// history gathers the D check's evidence: the previous snapshot of the
// row's page and, when it does not show old and the skill names a docs
// repository, a `git log -S` lookup in that repository.
func history(ctx context.Context, st *store.Store, cfg skill.Config, p draftedPage, r redirect.Row, hosts []string) (redirect.History, error) {
	h := redirect.History{Previous: p.prev, HasPrevious: p.hasPrev && p.verifiedSHA != p.latestSHA}
	if h.HasPrevious && redirect.ContainsIdentifier(h.Previous, r.Old) {
		return h, nil
	}
	if cfg.Sources.DocsRepo == "" {
		return h, nil
	}
	file := path.Join(strings.Trim(cfg.Sources.DocsPath, "/"), r.Page+cfg.Sources.Ext)
	found, err := source.GitLogS(ctx, filepath.Join(st.Dir(), "repos"), cfg.Sources.DocsRepo, file, r.Old, hosts,
		func(line string) bool { return redirect.ContainsIdentifier(line, r.Old) })
	if err != nil {
		h.GitErr = err.Error()
		return h, nil
	}
	h.GitRan, h.GitFound = true, found
	return h, nil
}

// commitDraft writes an all-auto draft: the rows (accepted "auto", with
// their passing checks and snapshot hash as evidence), every moved pin's
// verified_for := latest_seen, and every drafted page's verified_sha :=
// the snapshot drafted against. Before writing, the sidecar is re-read and
// must still carry exactly the hashes and versions drafted against;
// otherwise the skill is skipped as a stale plan. The write itself goes
// through commitWrite (L1/L2 renewals, strict check, rollback).
func commitDraft(ctx context.Context, st *store.Store, holder, abs, runner string, sr SkillResult, pages []draftedPage, pins []redirect.PinChange) (SkillResult, error) {
	sc, err := render.LoadSidecar(abs)
	if err != nil {
		return sr, fmt.Errorf("load sidecar: %w", err)
	}
	if sc == nil {
		return sr, fmt.Errorf("no sidecar")
	}
	cfg := skill.ParseFrontmatter(filepath.Join(abs, render.SkillName))
	if err := gatePreWrite(ctx, abs, cfg, sc); err != nil {
		return sr, err
	}
	for _, p := range pages {
		dd := sc.Docs[p.name]
		if dd.LatestSHA != p.latestSHA || dd.VerifiedSHA != p.verifiedSHA {
			sr.Status = "skipped"
			sr.Reason = fmt.Sprintf("plan stale: page %s changed while drafting", p.name)
			return sr, nil
		}
	}
	// The pins to advance are exactly the moved pins drafted for, with the
	// same verified_for and latest_seen; anything else is a stale plan.
	re := planSkill(rebuildDrift(abs, cfg, sc), runner)
	if re.Status != "draft" || !sameStrings(re.DraftPins, sr.DraftPins) {
		sr.Status = "skipped"
		sr.Reason = "plan stale: pins changed while drafting"
		return sr, nil
	}
	var advances []Advance
	for _, pc := range pins {
		pd, ok := sc.Pins[pc.Name]
		if !ok || pd.VerifiedFor != pc.VerifiedFor || pd.LatestSeen != pc.LatestSeen {
			sr.Status = "skipped"
			sr.Reason = fmt.Sprintf("plan stale: pin %s changed while drafting", pc.Name)
			return sr, nil
		}
		advances = append(advances, Advance{Pin: pc.Name, From: pc.VerifiedFor, To: pc.LatestSeen})
	}
	ids := map[string]bool{}
	for _, r := range sc.Redirects {
		ids[r.ID] = true
	}
	for _, rr := range sr.Rows {
		if ids[rr.Row.ID] {
			sr.Status = "skipped"
			sr.Reason = fmt.Sprintf("plan stale: row %s now exists", rr.Row.ID)
			return sr, nil
		}
	}
	// Re-assert at the write: no two written rows share an old.
	for i, rr := range sr.Rows {
		if duplicateOld(sr.Rows, i, nil) {
			sr.Status = "skipped"
			sr.Reason = fmt.Sprintf("refused: duplicate old %q in the draft", rr.Row.Old)
			return sr, nil
		}
	}

	sr.Advances = advances
	return commitWrite(ctx, st, holder, abs, sc, sr, func(sc *render.Sidecar) {
		today := time.Now().Format("2006-01-02")
		for _, rr := range sr.Rows {
			var checks []string
			for _, c := range rr.Checks {
				if c.Ran && c.Pass {
					checks = append(checks, c.Name)
				}
			}
			sc.Redirects = append(sc.Redirects, render.Redirect{
				ID: rr.Row.ID, Old: rr.Row.Old, New: rr.Row.New, Kind: rr.Row.Kind,
				Since: rr.Row.Since, Source: rr.Row.Source,
				Accepted: "auto", Checks: checks, Evidence: rr.SnapshotSHA,
			})
		}
		for _, adv := range advances {
			p := sc.Pins[adv.Pin]
			p.VerifiedFor = adv.To
			p.VerifiedAt = today
			p.CheckedAt = today
			sc.Pins[adv.Pin] = p
		}
		for _, pg := range pages {
			dd := sc.Docs[pg.name]
			dd.VerifiedSHA = pg.latestSHA
			dd.VerifiedAt = today
			sc.Docs[pg.name] = dd
		}
	})
}

// sameStrings reports whether a and b hold the same names, ignoring order.
func sameStrings(a, b []string) bool {
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	return strings.Join(x, "\x00") == strings.Join(y, "\x00")
}

// uncoveredPage returns the first changed page (newest snapshot differs
// from the verified one) that no auto-accepted row quotes, or "".
func uncoveredPage(rows []redirect.RowResult, pages []draftedPage) string {
	for _, p := range pages {
		if p.latestSHA == p.verifiedSHA {
			continue
		}
		covered := false
		for _, r := range rows {
			if r.Outcome == redirect.OutcomeAuto && r.Row.Page == p.name {
				covered = true
				break
			}
		}
		if !covered {
			return p.name
		}
	}
	return ""
}

func firstReason(rows []redirect.RowResult, outcome string) string {
	for _, r := range rows {
		if r.Outcome == outcome {
			if r.Row.ID != "" {
				return r.Row.ID + ": " + r.Reason
			}
			return r.Reason
		}
	}
	return outcome
}

// verdictRow is the row_json shape: the row without its quote (the quote
// has its own column) plus the routing reason.
type verdictRow struct {
	redirect.Row
	Reason string `json:"reason,omitempty"`
}

// saveVerdicts records every row's verdict for the run. A draft with no
// rows (zero rows, or output that failed the schema) is recorded as one
// skill-level verdict with an empty row, so status can list it. runID 0
// (the public Apply, which has no run record) records nothing.
func saveVerdicts(ctx context.Context, st *store.Store, runID int64, key string, sr SkillResult, final, parseErr string) error {
	if runID == 0 {
		return nil
	}
	vs, err := buildVerdicts(sr, final, parseErr)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return st.SaveRowVerdicts(cctx, runID, key, vs)
}

// buildVerdicts lists the verdicts saveVerdicts records, in order.
func buildVerdicts(sr SkillResult, final, parseErr string) ([]store.RowVerdictInput, error) {
	skillLevel := func(reason string) (store.RowVerdictInput, error) {
		b, err := json.Marshal(verdictRow{Reason: reason})
		return store.RowVerdictInput{RowJSON: string(b), ChecksJSON: "[]", Outcome: sr.Outcome}, err
	}
	if len(sr.Rows) == 0 {
		reason := sr.Reason
		if parseErr != "" {
			reason = "invalid model output: " + parseErr
		}
		v, err := skillLevel(reason)
		return []store.RowVerdictInput{v}, err
	}
	var vs []store.RowVerdictInput
	for _, rr := range sr.Rows {
		row := rr.Row
		row.Quote = ""
		b, err := json.Marshal(verdictRow{Row: row, Reason: rr.Reason})
		if err != nil {
			return nil, err
		}
		cb, err := json.Marshal(rr.Checks)
		if err != nil {
			return nil, err
		}
		outcome := rr.Outcome
		if final == "rolled_back" || final == "skipped" {
			outcome = final
		}
		vs = append(vs, store.RowVerdictInput{RowJSON: string(b), ChecksJSON: string(cb), Outcome: outcome, Quote: rr.Row.Quote, SnapshotSHA: rr.SnapshotSHA})
	}
	// A skill outcome no row carries (all rows auto, but a changed page was
	// left uncovered) is recorded as one skill-level verdict, so status
	// lists the pending item.
	if sr.Outcome != redirect.OutcomeAuto && final == sr.Outcome {
		for _, rr := range sr.Rows {
			if rr.Outcome == sr.Outcome {
				return vs, nil
			}
		}
		v, err := skillLevel(sr.Reason)
		if err != nil {
			return nil, err
		}
		vs = append(vs, v)
	}
	return vs, nil
}
