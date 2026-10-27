// The render and stamp operations. Render rewrites a skill's generated
// regions and renderer-owned frontmatter keys from its upkeep.data.json
// sidecar; Stamp records frozen-block hashes into the frontmatter.
package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/KHAEntertainment/self-updating-skills/internal/render"
	"github.com/KHAEntertainment/self-updating-skills/internal/skill"
)

// RenderResult is the result of a render. Diff is display-only: no
// behaviour depends on it.
type RenderResult struct {
	Changed bool   `json:"changed"`
	Diff    string `json:"diff"`
}

// resolveSkillDir mirrors Check's path resolution (EvalSymlinks, e.g.
// /var -> /private/var on macOS) so error paths are consistent.
func resolveSkillDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", &Error{Op: "stat", Path: dir, Err: err}
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return abs, nil
}

// Render renders dir's SKILL.md from its upkeep.data.json sidecar: the
// sidecar is canonicalized and written first, then SKILL.md, each only
// when the bytes differ (an unchanged file is never touched, so its mtime
// stays put). dryRun computes what would change without writing.
func Render(ctx context.Context, dir string, dryRun bool) (*RenderResult, error) {
	abs, err := resolveSkillDir(dir)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, &Error{Op: "render", Path: abs, Err: err}
	}
	sk := filepath.Join(abs, render.SkillName)
	if _, err := os.Stat(sk); err != nil {
		return nil, &Error{Op: "stat", Path: sk, Err: err}
	}
	sc, err := render.LoadSidecar(abs)
	if err != nil {
		return nil, &Error{Op: "read", Path: filepath.Join(abs, render.SidecarName), Err: err}
	}
	if sc == nil {
		return nil, &Error{Op: "render", Path: abs, Err: fmt.Errorf("no %s sidecar under %s", render.SidecarName, abs)}
	}
	raw, err := os.ReadFile(sk)
	if err != nil {
		return nil, &Error{Op: "read", Path: sk, Err: err}
	}
	// Shared owned-key preflight (F2): duplicate renderer-owned keys are
	// refused here too, with the same error shape stamp produces.
	if err := skill.OwnedKeyDuplicate(raw); err != nil {
		return nil, &Error{Op: "render", Path: sk, Err: err}
	}
	out, err := render.Build(raw, sc)
	if err != nil {
		return nil, &Error{Op: "render", Path: sk, Err: err}
	}
	if err := ctx.Err(); err != nil {
		return nil, &Error{Op: "render", Path: abs, Err: err}
	}

	changed := string(out) != string(raw)
	sidecarChanged := false
	if cur, err := os.ReadFile(filepath.Join(abs, render.SidecarName)); err == nil {
		sidecarChanged = string(cur) != string(sc.Canonical())
	} else if !os.IsNotExist(err) {
		return nil, &Error{Op: "read", Path: filepath.Join(abs, render.SidecarName), Err: err}
	}

	if !dryRun {
		// Sidecar first, then SKILL.md: the sidecar is the source of truth,
		// so a crash between the renames is repaired by the next render.
		if _, _, err := render.Apply(abs, sc, out); err != nil {
			return nil, &Error{Op: "write", Path: abs, Err: err}
		}
	}

	diff := ""
	if changed {
		diff = render.Diff(string(raw), string(out))
	}
	if sidecarChanged {
		if diff != "" {
			diff += "\n"
		}
		diff += fmt.Sprintf("(%s rewritten to canonical form)\n", render.SidecarName)
	}
	return &RenderResult{Changed: changed || sidecarChanged, Diff: diff}, nil
}

// StampResult is the result of a stamp. Lines carries the same stdout
// lines the shell script prints.
type StampResult struct {
	Lines []string `json:"lines"`
}

// Stamp records the SHA-256 digests of every frozen block in dir's
// frontmatter as flat upkeep-frozen-<id> keys, removing old flat frozen
// keys and any nested frozen: block first. It writes SKILL.md atomically
// only when the bytes change.
func Stamp(ctx context.Context, dir string) (*StampResult, error) {
	abs, err := resolveSkillDir(dir)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, &Error{Op: "stamp", Path: abs, Err: err}
	}
	sk := filepath.Join(abs, render.SkillName)
	if _, err := os.Stat(sk); err != nil {
		return nil, &Error{Op: "stat", Path: sk, Err: err}
	}
	// A malformed sidecar (unknown keys, bad schema) makes every mutating
	// command refuse to run: stamp rewrites SKILL.md, so it must not
	// operate on a skill in an unknown data state. A missing sidecar is
	// fine (nil, nil).
	sc, err := render.LoadSidecar(abs)
	if err != nil {
		return nil, &Error{Op: "read", Path: filepath.Join(abs, render.SidecarName), Err: err}
	}
	raw, err := os.ReadFile(sk)
	if err != nil {
		return nil, &Error{Op: "read", Path: sk, Err: err}
	}
	// Shared owned-key preflight (F2): with a sidecar, duplicate
	// upkeep-pins/upkeep-verified lines make stamp refuse exactly like
	// render, before any write.
	if sc != nil {
		if err := skill.OwnedKeyDuplicate(raw); err != nil {
			return nil, &Error{Op: "stamp", Path: sk, Err: err}
		}
	}
	blocks, err := skill.ScanFrozenBlocks(abs)
	if err != nil {
		var re *skill.ReadError
		if errors.As(err, &re) {
			return nil, &Error{Op: "read", Path: re.Path, Err: re.Err}
		}
		return nil, &Error{Op: "read", Path: abs, Err: err}
	}
	if len(blocks) == 0 {
		return &StampResult{Lines: []string{
			fmt.Sprintf("VERDICT: NONE — no <!-- frozen:id --> blocks found in %s", abs),
		}}, nil
	}
	keys := make([]skill.FlatKey, 0, len(blocks))
	for _, b := range blocks {
		keys = append(keys, skill.FlatKey{Key: "upkeep-frozen-" + b.ID, Value: b.Digest})
	}
	eol := skill.FileEOL(raw)
	out, err := skill.SetFlatKeys(raw, eol, keys, &skill.PurgeSpec{
		KeyPrefix:    "upkeep-frozen-",
		NestedBlocks: []string{"frozen"},
	})
	if err != nil {
		return nil, &Error{Op: "stamp", Path: sk, Err: err}
	}
	if string(out) != string(raw) {
		if err := render.WriteFileAtomic(sk, out); err != nil {
			return nil, &Error{Op: "write", Path: sk, Err: err}
		}
	}
	var lines []string
	for _, b := range blocks {
		lines = append(lines, fmt.Sprintf("STAMPED  frozen:%s (%s)  %s", b.ID, b.Rel, b.Digest))
	}
	lines = append(lines, fmt.Sprintf("VERDICT: INTACT — stamped %d frozen block(s) using agentskills.io-compliant flat keys", len(blocks)))
	return &StampResult{Lines: lines}, nil
}
