package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The gate-text notes every skill rendered from the meta-skill template
// carries (Skill Engine Phase 5, issue #6). They are the two harness
// quirks measured in the Phase 2 harness checks (issue #1, comment
// 6458680218): OpenCode caches the skill body at session start, and
// Antigravity's view_file refuses a symlinked path that resolves outside
// its allowed directories.
const (
	openCodeGateNote    = "**OpenCode** — After `upkeep render`/`update` rewrites this skill, restart OpenCode to load the new content."
	antigravityGateNote = "**Antigravity** — If reading this skill's files is denied, read them with a shell command."
)

// TestGateNotesInTheMetaSkillTemplate checks the template a new skill is
// scaffolded from: it must carry both harness notes, so every skill
// rendered from it carries them too.
func TestGateNotesInTheMetaSkillTemplate(t *testing.T) {
	path := filepath.Join(repoRoot(t), "skills", "self-updating-skills", "SKILL.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	gate := string(b)
	for _, note := range []string{openCodeGateNote, antigravityGateNote} {
		if !strings.Contains(gate, note) {
			t.Errorf("%s does not carry the gate note %q", path, note)
		}
	}
}

// TestGateNotesInEveryRepoSkill checks that the skills in this repo —
// rendered from the meta-skill template — carry both notes in their gate
// text.
func TestGateNotesInEveryRepoSkill(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "skills")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name(), "SKILL.md")
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		found++
		for _, note := range []string{openCodeGateNote, antigravityGateNote} {
			if !strings.Contains(string(b), note) {
				t.Errorf("%s does not carry the gate note %q", path, note)
			}
		}
	}
	if found == 0 {
		t.Fatalf("no skills found under %s", dir)
	}
}
