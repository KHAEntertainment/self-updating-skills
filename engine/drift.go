// Package engine is the public API of the skill engine. Phase 3 adds the
// deterministic updater: Poll (fetch upstream versions and tracked pages
// into SQLite snapshots, recording latest_seen/checked_at/latest_sha in
// the sidecar), Plan (the auto-advance rules, P2), Apply (the write
// cycle: sidecar → render → check --strict, with rollback), Status, and
// the single-writer lease.
package engine

import (
	"fmt"

	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/KHAEntertainment/self-updating-skills/internal/source"
)

// PinDrift is one pin's poll result.
type PinDrift struct {
	Name        string `json:"name"`
	Kind        string `json:"kind,omitempty"`
	VerifiedFor string `json:"verified_for,omitempty"`
	LatestSeen  string `json:"latest_seen,omitempty"`
	Error       string `json:"error,omitempty"`
}

// DocDrift is one tracked doc page's poll result.
type DocDrift struct {
	Page        string `json:"page"`
	VerifiedSHA string `json:"verified_sha,omitempty"`
	LatestSHA   string `json:"latest_sha,omitempty"`
	Changed     bool   `json:"changed"`
	Error       string `json:"error,omitempty"`
}

// Drift is the poll result for one skill directory.
type Drift struct {
	SkillDir    string     `json:"skill_dir"`
	Owner       string     `json:"owner,omitempty"`
	AutoAdvance string     `json:"auto_advance,omitempty"`
	SkipReason  string     `json:"skip_reason,omitempty"`
	BlockReason string     `json:"block_reason,omitempty"`
	Pins        []PinDrift `json:"pins,omitempty"`
	Docs        []DocDrift `json:"docs,omitempty"`
	// Pruned lists sidecar docs keys retired by a declared
	// upkeep-source-pages list and removed from the sidecar on this poll
	// (C2). SQLite snapshot history is retained.
	Pruned []string `json:"pruned,omitempty"`
}

// Advance is one pin to advance.
type Advance struct {
	Pin  string `json:"pin"`
	From string `json:"from"`
	To   string `json:"to"`
}

// SkillPlan is the plan for one skill directory.
type SkillPlan struct {
	SkillDir   string    `json:"skill_dir"`
	Status     string    `json:"status"` // advance|draft|blocked|skip|no-change
	Reason     string    `json:"reason,omitempty"`
	Advances   []Advance `json:"advances,omitempty"`
	DraftPins  []string  `json:"draft_pins,omitempty"`
	DraftPages []string  `json:"draft_pages,omitempty"`
}

// UpdatePlan is the full plan for a set of skill directories.
type UpdatePlan struct {
	Skills []SkillPlan `json:"skills"`
}

// SkillResult is one skill's apply outcome. Status is "written" for a
// successful advance, "skipped" (stale plan, lease lost) or "failed" for
// an error; non-advance plan statuses pass through unchanged.
type SkillResult struct {
	SkillDir   string    `json:"skill_dir"`
	Status     string    `json:"status"`
	Reason     string    `json:"reason,omitempty"`
	Advances   []Advance `json:"advances,omitempty"`
	DraftPins  []string  `json:"draft_pins,omitempty"`
	DraftPages []string  `json:"draft_pages,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// shaRe matches the SHA pin identity form (same shape as check.isSha).
var shaRe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// isSHA reports whether v is a git SHA pin/version.
func isSHA(v string) bool { return shaRe.MatchString(v) }

// sha256Hex returns the "sha256:<hex>" snapshot hash of b.
func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

// stableSemverRe matches a stable three-part numeric version (leading "v"
// allowed). Prereleases and build metadata never match, so they can never
// auto-advance.
var stableSemverRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

// stableTriplet parses a stable semver into (major, minor, patch) using
// strconv; any parse failure (including decimal overflow) means not
// stable (C4).
func stableTriplet(v string) (int, int, int, bool) {
	m := stableSemverRe.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return 0, 0, 0, false
	}
	a, errA := strconv.Atoi(m[1])
	b, errB := strconv.Atoi(m[2])
	c, errC := strconv.Atoi(m[3])
	if errA != nil || errB != nil || errC != nil {
		return 0, 0, 0, false
	}
	return a, b, c, true
}

// isPatch reports whether from → to is a semver patch bump: same
// major.minor, higher numeric patch, both stable.
func isPatch(from, to string) bool {
	fa, fb, fc, ok1 := stableTriplet(from)
	ta, tb, tc, ok2 := stableTriplet(to)
	return ok1 && ok2 && fa == ta && fb == tb && tc > fc
}

// isStableIncrease reports whether to is a stable semver increase over
// from (rule 1: any stable three-part increase — patch, minor or major).
func isStableIncrease(from, to string) bool {
	_, _, _, ok1 := stableTriplet(from)
	_, _, _, ok2 := stableTriplet(to)
	return ok1 && ok2 && source.VerCmp(to, from) > 0
}

// reportJSON marshals a run report.
func reportJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("{\"error\":%q}", err.Error())
	}
	return string(b)
}
