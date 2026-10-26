// Package engine is the public API of the skill engine. `check` is the only
// operation Phase 1 implements; it returns a JSON-tagged CheckReport, and
// the CLI renders text from the same struct.
package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/KHAEntertainment/self-updating-skills/internal/check"
	"github.com/KHAEntertainment/self-updating-skills/internal/skill"
)

// Item is one verdict item line.
type Item struct {
	Kind string `json:"kind"`
	Line string `json:"line"`
}

// CheckReport is the result of a check. Text output is rendered from this
// struct; --json prints it as-is.
type CheckReport struct {
	Verdict string `json:"verdict"`
	Summary string `json:"summary"`
	Items   []Item `json:"items"`
	// MissingSkill is true when the target directory has no SKILL.md; the
	// script then prints a single "UNKNOWN — no SKILL.md under ..." line
	// and no VERDICT line.
	MissingSkill bool `json:"missing_skill,omitempty"`
}

// Check runs a check on the skill directory dir.
func Check(ctx context.Context, dir string) (*CheckReport, error) {
	return CheckWithThreshold(ctx, dir, nil)
}

// CheckWithThreshold runs a check with an explicit freshness-threshold
// override (in days); nil uses the skill's frontmatter threshold.
func CheckWithThreshold(ctx context.Context, dir string, threshold *int) (*CheckReport, error) {
	sk := filepath.Join(dir, "SKILL.md")
	if _, err := os.Stat(sk); err != nil {
		return &CheckReport{
			MissingSkill: true,
			Items:        []Item{{Kind: "UNKNOWN", Line: fmt.Sprintf("UNKNOWN — no SKILL.md under %s", dir)}},
		}, nil
	}
	cfg := skill.ParseFrontmatter(sk)
	r := check.Compute(ctx, cfg, threshold, dir)
	return &CheckReport{Verdict: r.Verdict, Summary: r.Summary, Items: items(r.Items)}, nil
}

func items(in []check.Item) []Item {
	out := make([]Item, len(in))
	for i, it := range in {
		out[i] = Item{Kind: it.Kind, Line: it.Line}
	}
	return out
}

// RenderText renders a report exactly like the script's stdout: one item
// line per item, then "VERDICT: X — summary".
func RenderText(r *CheckReport) string {
	if r.MissingSkill {
		if len(r.Items) > 0 {
			return r.Items[0].Line + "\n"
		}
		return ""
	}
	var sb strings.Builder
	for _, it := range r.Items {
		sb.WriteString(it.Line)
		sb.WriteByte('\n')
	}
	suffix := ""
	switch r.Verdict {
	case "TAMPERED":
		suffix = ". Revert owner-policy edits immediately!"
	case "STALE":
		suffix = ". Consult live docs before relying on cached references."
	}
	fmt.Fprintf(&sb, "VERDICT: %s — %s%s\n", r.Verdict, r.Summary, suffix)
	return sb.String()
}
