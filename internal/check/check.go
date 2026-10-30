// Package check computes skill check verdicts and item lines. It is a
// faithful Go port of the script's check subcommand (bin/upkeep.sh), so the
// rendered text stays byte-identical.
package check

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/KHAEntertainment/self-updating-skills/internal/render"
	"github.com/KHAEntertainment/self-updating-skills/internal/skill"
	"github.com/KHAEntertainment/self-updating-skills/internal/source"
)

var dateRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)

// Item is one verdict item line.
type Item struct {
	Kind string
	Line string
}

// Report is the computed check result before public rendering.
type Report struct {
	Verdict      string
	Summary      string
	Items        []Item
	MissingSkill bool
}

// DateItem is a found Verified date (label, date string).
type DateItem struct {
	Label string
	Date  string
}

// Compute runs the full check logic for a skill directory. threshold
// overrides the frontmatter freshness threshold when non-nil. sc is the
// skill's upkeep.data.json sidecar, or nil when the skill has none: with
// no sidecar the behaviour is byte-identical to Phase 1. sidecarErr (with
// sc == nil) reports a sidecar that failed to decode: the computation runs
// normally and the error is appended as a DRIFT item, so every other
// finding — TAMPERED included — keeps its natural verdict precedence.
func Compute(ctx context.Context, cfg skill.Config, threshold *int, skillDir string, sc *render.Sidecar, sidecarErr error) (Report, error) {
	thr := cfg.ThresholdDays
	if threshold != nil {
		thr = *threshold
	}
	today := dayNumber(time.Now())

	var items []Item
	totalPins, manualPins, stalePins, lagPins := 0, 0, 0, 0

	for _, pin := range cfg.Pins {
		totalPins++
		if pin.Kind == "manual" {
			items = append(items, Item{Kind: "MANUAL", Line: fmt.Sprintf("MANUAL   pin:%s  %s (check vendor releases)", pin.Name, pin.Version)})
			manualPins++
			continue
		}
		cur := source.ResolveCurrent(ctx, pin)
		// An unresolved live lookup reports UNKNOWN exactly like the script:
		// latest_seen is never substituted for it (parity with the frozen
		// fallback wins in this case).
		if cur == "" {
			pinned := pin.Version
			if pinned == "" {
				pinned = "?"
			}
			items = append(items, Item{Kind: "UNKNOWN", Line: fmt.Sprintf("UNKNOWN  pin:%s  (pinned %s — current unresolved)", pin.Name, pinned)})
			continue
		}
		// The live lookup stays authoritative; the sidecar's latest_seen is
		// the newest recorded upstream version (data-and-rendering "Field
		// rules": it changes on every poll that finds something new), so the
		// comparison current is the newer of the two. Where the pinned
		// version (verified_for, P1) is behind it, a sidecar-backed pin
		// reports CONTENT-LAG instead of STALE (D7). GitHub tag pins join
		// this comparison; only SHA pins keep the identity comparison (a
		// SHA has no version order, and the live HEAD is authoritative).
		var sd *render.PinData
		if sc != nil {
			if p, ok := sc.Pins[pin.Name]; ok {
				sd = &p
			}
		}
		isSHA := pin.Kind == "github" && isSha(pin.Version)
		eff := cur
		if sd != nil && sd.LatestSeen != "" && !isSHA && source.VerCmp(sd.LatestSeen, eff) > 0 {
			eff = sd.LatestSeen
		}
		if isSHA {
			if strings.EqualFold(pin.Version, eff) {
				items = append(items, Item{Kind: "FRESH", Line: fmt.Sprintf("FRESH    pin:%s  %s", pin.Name, eff)})
			} else if sd != nil {
				lagPins++
				items = append(items, Item{Kind: "CONTENT-LAG", Line: fmt.Sprintf("CONTENT-LAG pin:%s  pinned %s → current %s", pin.Name, pin.Version, eff)})
			} else {
				stalePins++
				items = append(items, Item{Kind: "STALE", Line: fmt.Sprintf("STALE    pin:%s  pinned %s → current %s", pin.Name, pin.Version, eff)})
			}
			continue
		}
		// Python compares ver_cmp(ver or "0", cur): an empty pinned version
		// compares as "0" for the verdict, but the displayed value stays the
		// empty string exactly as Python prints it.
		cmpVer := pin.Version
		if cmpVer == "" {
			cmpVer = "0"
		}
		switch c := source.VerCmp(cmpVer, eff); {
		case c == 0:
			items = append(items, Item{Kind: "FRESH", Line: fmt.Sprintf("FRESH    pin:%s  %s", pin.Name, eff)})
		case c < 0:
			if sd != nil {
				lagPins++
				items = append(items, Item{Kind: "CONTENT-LAG", Line: fmt.Sprintf("CONTENT-LAG pin:%s  pinned %s → current %s", pin.Name, pin.Version, eff)})
			} else {
				stalePins++
				items = append(items, Item{Kind: "STALE", Line: fmt.Sprintf("STALE    pin:%s  pinned %s → current %s", pin.Name, pin.Version, eff)})
			}
		default:
			items = append(items, Item{Kind: "AHEAD", Line: fmt.Sprintf("AHEAD    pin:%s  pinned %s → current %s", pin.Name, pin.Version, eff)})
		}
	}

	// Tracked doc pages: a page whose latest fetched snapshot (recorded by
	// poll in the sidecar) differs from its verified_sha reports
	// CONTENT-LAG (D7) — even when no version moved. A page with a latest
	// snapshot but no verified_sha baseline also reports CONTENT-LAG: it
	// can never produce a false FRESH, and Plan routes it to drafting
	// (never auto-accepted on missing evidence). A page with no recorded
	// latest_sha carries no signal at all.
	pageLag := 0
	totalPages, verifiedPages := 0, 0
	if sc != nil && len(sc.Docs) > 0 {
		pageNames := make([]string, 0, len(sc.Docs))
		for n := range sc.Docs {
			pageNames = append(pageNames, n)
		}
		sort.Strings(pageNames)
		for _, n := range pageNames {
			d := sc.Docs[n]
			if d.LatestSHA == "" {
				continue
			}
			totalPages++
			if d.VerifiedSHA != "" && d.VerifiedSHA == d.LatestSHA {
				verifiedPages++
				continue
			}
			verifiedLabel := d.VerifiedSHA
			if verifiedLabel == "" {
				verifiedLabel = "(none)"
			}
			pageLag++
			items = append(items, Item{Kind: "CONTENT-LAG", Line: fmt.Sprintf("CONTENT-LAG page:%s  verified %s → current %s", n, verifiedLabel, d.LatestSHA)})
		}
	}

	dateItems, err := scanDates(skillDir, cfg)
	if err != nil {
		return Report{}, err
	}
	staleDates := 0
	for _, di := range dateItems {
		dt, err := time.ParseInLocation("2006-01-02", di.Date, time.Local)
		if err != nil {
			items = append(items, Item{Kind: "??", Line: fmt.Sprintf("??       date:%s  %s (unparseable)", di.Label, di.Date)})
			continue
		}
		age := today - dayNumber(dt)
		if age > thr {
			staleDates++
			items = append(items, Item{Kind: "STALE", Line: fmt.Sprintf("STALE    date:%s  %s (%dd > %dd — RE-VERIFY)", di.Label, di.Date, age, thr)})
		} else {
			items = append(items, Item{Kind: "FRESH", Line: fmt.Sprintf("FRESH    date:%s  %s (%dd)", di.Label, di.Date, age)})
		}
	}

	blocks, err := skill.ScanFrozenBlocks(skillDir)
	if err != nil {
		return Report{}, err
	}
	recorded := cfg.Frozen
	recordedMap := cfg.FrozenMap()
	blockIDs := make(map[string]bool, len(blocks))
	for _, b := range blocks {
		blockIDs[b.ID] = true
	}
	tampered := 0
	for _, b := range blocks {
		rec, ok := recordedMap[b.ID]
		switch {
		case !ok || rec == "":
			tampered++
			items = append(items, Item{Kind: "UNSTAMPED", Line: fmt.Sprintf("UNSTAMPED frozen:%s (%s)  current %s (run: upkeep.sh stamp)", b.ID, b.Rel, b.Digest)})
		case rec != b.Digest:
			tampered++
			items = append(items, Item{Kind: "TAMPERED", Line: fmt.Sprintf("TAMPERED  frozen:%s (%s)  expected %s != current %s", b.ID, b.Rel, rec, b.Digest)})
		default:
			items = append(items, Item{Kind: "INTACT", Line: fmt.Sprintf("INTACT   frozen:%s (%s)  %s", b.ID, b.Rel, b.Digest)})
		}
	}
	for _, fe := range recorded {
		if !blockIDs[fe.ID] {
			tampered++
			items = append(items, Item{Kind: "MISSING", Line: fmt.Sprintf("MISSING  frozen:%s  expected %s — frozen block deleted!", fe.ID, fe.Hash)})
		}
	}

	// DRIFT: with a sidecar, a generated region or renderer-owned
	// frontmatter key that differs from what the renderer would produce.
	// Malformed generated markers and a malformed sidecar are also DRIFT
	// items with a precise message — check always prints a VERDICT line —
	// but render and stamp refuse such files, so the messages never
	// prescribe `upkeep render`.
	drift := 0
	if sidecarErr != nil {
		// The sidecar could not be decoded: run every other check and
		// carry the decode problem as a DRIFT item.
		drift++
		items = append(items, Item{Kind: "DRIFT", Line: fmt.Sprintf("DRIFT    sidecar (upkeep.data.json)  %v — repair the sidecar; render and stamp refuse to run", sidecarErr)})
	}
	if sc != nil {
		raw, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
		if err != nil {
			return Report{}, &skill.ReadError{Path: filepath.Join(skillDir, "SKILL.md"), Err: err}
		}
		targets, markerErr, err := render.DriftTargets(raw, sc)
		if err != nil {
			// Sidecar validation failure: same treatment as a malformed
			// sidecar load — an item, not a hard error.
			drift++
			items = append(items, Item{Kind: "DRIFT", Line: fmt.Sprintf("DRIFT    sidecar (upkeep.data.json)  %v — repair the sidecar; render and stamp refuse to run", err)})
		} else {
			if markerErr != nil {
				drift++
				items = append(items, Item{Kind: "DRIFT", Line: fmt.Sprintf("DRIFT    region (SKILL.md)  %v (upkeep render refuses this file; repair the markers)", markerErr)})
			}
			for _, tg := range targets {
				drift++
				items = append(items, Item{Kind: "DRIFT", Line: fmt.Sprintf("DRIFT    %s (SKILL.md)  differs from upkeep.data.json — run: upkeep render", tg)})
			}
		}
	}

	var parts []string
	autoPins := totalPins - manualPins
	if totalPins > 0 {
		s := fmt.Sprintf("pins=%d/%d fresh", autoPins-stalePins-lagPins, autoPins)
		if manualPins > 0 {
			s += fmt.Sprintf(" (+%d manual)", manualPins)
		}
		parts = append(parts, s)
	}
	if len(dateItems) > 0 {
		parts = append(parts, fmt.Sprintf("dates=%d/%d fresh (<=%dd)", len(dateItems)-staleDates, len(dateItems), thr))
	}
	if len(blocks) > 0 || len(recorded) > 0 {
		tot := len(blocks)
		if len(recorded) > tot {
			tot = len(recorded)
		}
		parts = append(parts, fmt.Sprintf("frozen=%d/%d intact", tot-tampered, tot))
	}
	if drift > 0 {
		parts = append(parts, fmt.Sprintf("drift=%d", drift))
	}
	if totalPages > 0 {
		parts = append(parts, fmt.Sprintf("pages=%d/%d verified", verifiedPages, totalPages))
	}
	summary := strings.Join(parts, ", ")
	if summary == "" {
		summary = "no pins, dates, or frozen blocks configured"
	}

	verdict := "FRESH"
	switch {
	case tampered > 0:
		verdict = "TAMPERED"
	case stalePins > 0 || staleDates > 0 || drift > 0:
		verdict = "STALE"
	case lagPins > 0 || pageLag > 0:
		// CONTENT-LAG sits between STALE and FRESH (D7): the upstream
		// version moved or a tracked snapshot changed, but the content is
		// verified against an older state.
		verdict = "CONTENT-LAG"
	case len(parts) == 0:
		verdict = "UNKNOWN"
	}
	return Report{Verdict: verdict, Summary: summary, Items: items}, nil
}

var shaRe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

func isSha(v string) bool {
	return shaRe.MatchString(v)
}

// dayNumber returns the calendar-day number of t (UTC date components), so
// age computations match the script's datetime.date arithmetic exactly.
func dayNumber(t time.Time) int {
	y, m, d := t.Date()
	return int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

// scanDates finds Verified dates in SKILL.md and references/*.md, mirroring
// the script's date scan exactly (substring "erified", 2-line window,
// dedupe by (label, date), then the metadata.verified fallback). A failed
// read of an existing file is returned as an error.
func scanDates(skillDir string, cfg skill.Config) ([]DateItem, error) {
	sk := filepath.Join(skillDir, "SKILL.md")
	files := []string{sk}
	if entries, err := os.ReadDir(filepath.Join(skillDir, "references")); err == nil && len(entries) > 0 {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				files = append(files, filepath.Join(skillDir, "references", e.Name()))
			}
		}
		sort.Strings(files[1:])
	}

	var out []DateItem
	seen := map[string]bool{}
	for _, f := range files {
		// Python skips references that do not exist (f.exists() follows
		// symlinks), so dangling symlinks are skipped; files that exist but
		// cannot be read still surface as a structured error.
		if _, err := os.Stat(f); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, &skill.ReadError{Path: f, Err: err}
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, &skill.ReadError{Path: f, Err: err}
		}
		text := skill.NormalizeNewlines(string(raw))
		label := "SKILL.md"
		if f == sk {
			text = skill.StripFrontmatter(text)
		} else {
			label = "references/" + filepath.Base(f)
		}
		cleaned := skill.StripFencedBlocks(text)
		lines := strings.Split(cleaned, "\n")
		for idx, line := range lines {
			if !strings.Contains(line, "erified") {
				continue
			}
			end := idx + 2
			if end > len(lines) {
				end = len(lines)
			}
			window := strings.Join(lines[idx:end], " ")
			for _, dm := range dateRe.FindAllString(window, -1) {
				key := label + "\x00" + dm
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, DateItem{Label: label, Date: dm})
			}
		}
	}
	if len(out) == 0 && cfg.Verified != "" {
		out = append(out, DateItem{Label: "SKILL.md(metadata.verified)", Date: cfg.Verified})
	}
	return out, nil
}
