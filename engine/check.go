// Package engine is the public API of the skill engine. `check` is the only
// operation Phase 1 implements; it returns a JSON-tagged CheckReport, and
// the CLI renders text from the same struct.
package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/KHAEntertainment/self-updating-skills/internal/check"
	"github.com/KHAEntertainment/self-updating-skills/internal/render"
	"github.com/KHAEntertainment/self-updating-skills/internal/skill"
)

// Error is a structured engine error: the failing operation, the path it
// happened on, and the underlying error. It supports errors.Is/errors.As
// through Unwrap.
type Error struct {
	Op   string
	Path string
	Err  error
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s %s: %v", e.Op, e.Path, e.Err)
}

// Unwrap returns the underlying error.
func (e *Error) Unwrap() error { return e.Err }

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
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, &Error{Op: "stat", Path: dir, Err: err}
	}
	// The script resolves its skill path (pathlib.Path.resolve), which
	// follows symlinks (e.g. /var -> /private/var on macOS); mirror that so
	// the "UNKNOWN — no SKILL.md under ..." line is byte-identical.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if err := ctx.Err(); err != nil {
		return nil, &Error{Op: "check", Path: abs, Err: err}
	}
	sk := filepath.Join(abs, "SKILL.md")
	if _, err := os.Stat(sk); err != nil {
		if os.IsNotExist(err) {
			return &CheckReport{
				MissingSkill: true,
				Items:        []Item{{Kind: "UNKNOWN", Line: fmt.Sprintf("UNKNOWN — no SKILL.md under %s", abs)}},
			}, nil
		}
		return nil, &Error{Op: "stat", Path: sk, Err: err}
	}
	cfg := skill.ParseFrontmatter(sk)
	sc, err := render.LoadSidecar(abs)
	if err != nil {
		// A malformed sidecar (unknown keys, bad schema, invalid fields)
		// must not blank the gate line: check reports it as a DRIFT item
		// with the error and the overall STALE verdict, so a delegated
		// scripts/upkeep.sh check never injects an empty VERDICT line.
		// render and stamp keep refusing to run.
		return &CheckReport{
			Verdict: "STALE",
			Summary: "drift=1",
			Items:   []Item{{Kind: "DRIFT", Line: fmt.Sprintf("DRIFT    sidecar (upkeep.data.json)  %v — repair the sidecar; render and stamp refuse to run", err)}},
		}, nil
	}
	r, err := check.Compute(ctx, cfg, threshold, abs, sc)
	if err != nil {
		var re *skill.ReadError
		if errors.As(err, &re) {
			return nil, &Error{Op: "read", Path: re.Path, Err: re.Err}
		}
		return nil, &Error{Op: "read", Path: abs, Err: err}
	}
	// Cancellation past the point of a completed computation still wins over
	// a report.
	if err := ctx.Err(); err != nil {
		return nil, &Error{Op: "check", Path: abs, Err: err}
	}
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
	case "STALE", "CONTENT-LAG":
		suffix = ". Consult live docs before relying on cached references."
	}
	fmt.Fprintf(&sb, "VERDICT: %s — %s%s\n", r.Verdict, r.Summary, suffix)
	return sb.String()
}
