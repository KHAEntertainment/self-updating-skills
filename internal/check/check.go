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
// overrides the frontmatter freshness threshold when non-nil.
func Compute(ctx context.Context, cfg skill.Config, threshold *int, skillDir string) (Report, error) {
	thr := cfg.ThresholdDays
	if threshold != nil {
		thr = *threshold
	}
	today := dayNumber(time.Now())

	var items []Item
	totalPins, manualPins, stalePins := 0, 0, 0

	for _, pin := range cfg.Pins {
		totalPins++
		if pin.Kind == "manual" {
			items = append(items, Item{Kind: "MANUAL", Line: fmt.Sprintf("MANUAL   pin:%s  %s (check vendor releases)", pin.Name, pin.Version)})
			manualPins++
			continue
		}
		cur := source.ResolveCurrent(ctx, pin)
		if cur == "" {
			pinned := pin.Version
			if pinned == "" {
				pinned = "?"
			}
			items = append(items, Item{Kind: "UNKNOWN", Line: fmt.Sprintf("UNKNOWN  pin:%s  (pinned %s — current unresolved)", pin.Name, pinned)})
			continue
		}
		isSHA := pin.Kind == "github" && isSha(pin.Version)
		if isSHA {
			if strings.EqualFold(pin.Version, cur) {
				items = append(items, Item{Kind: "FRESH", Line: fmt.Sprintf("FRESH    pin:%s  %s", pin.Name, cur)})
			} else {
				stalePins++
				items = append(items, Item{Kind: "STALE", Line: fmt.Sprintf("STALE    pin:%s  pinned %s → current %s", pin.Name, pin.Version, cur)})
			}
			continue
		}
		switch c := source.VerCmp(pin.Version, cur); {
		case c == 0:
			items = append(items, Item{Kind: "FRESH", Line: fmt.Sprintf("FRESH    pin:%s  %s", pin.Name, cur)})
		case c < 0:
			stalePins++
			items = append(items, Item{Kind: "STALE", Line: fmt.Sprintf("STALE    pin:%s  pinned %s → current %s", pin.Name, pin.Version, cur)})
		default:
			items = append(items, Item{Kind: "AHEAD", Line: fmt.Sprintf("AHEAD    pin:%s  pinned %s → current %s", pin.Name, pin.Version, cur)})
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
		case !ok:
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

	var parts []string
	autoPins := totalPins - manualPins
	if totalPins > 0 {
		s := fmt.Sprintf("pins=%d/%d fresh", autoPins-stalePins, autoPins)
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
	summary := strings.Join(parts, ", ")
	if summary == "" {
		summary = "no pins, dates, or frozen blocks configured"
	}

	verdict := "FRESH"
	switch {
	case tampered > 0:
		verdict = "TAMPERED"
	case stalePins > 0 || staleDates > 0:
		verdict = "STALE"
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
