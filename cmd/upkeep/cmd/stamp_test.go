package cmd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KHAEntertainment/self-updating-skills/engine"
	"github.com/KHAEntertainment/self-updating-skills/internal/skill"
)

const stampSkill = `---
name: t
metadata:
  version: "1.0.0"
---

# T

<!-- frozen:pol -->
frozen body
<!-- /frozen:pol -->
`

func runStamp(t *testing.T, args ...string) string {
	t.Helper()
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(append([]string{"stamp"}, args...))
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("exec stamp %v: %v\n%s", args, err, buf.String())
	}
	return buf.String()
}

// repoRoot walks up from the test working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	t.Fatal("go.mod not found above " + wd)
	return ""
}

func TestStampCommand(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(stampSkill), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runStamp(t, dir)
	if !strings.Contains(out, "STAMPED  frozen:pol (SKILL.md)  sha256:") {
		t.Errorf("stamp output = %q", out)
	}
	if !strings.Contains(out, "VERDICT: INTACT — stamped 1 frozen block(s) using agentskills.io-compliant flat keys") {
		t.Errorf("stamp verdict line = %q", out)
	}
	cfg := skill.ParseFrontmatter(filepath.Join(dir, "SKILL.md"))
	if len(cfg.Frozen) != 1 || cfg.Frozen[0].ID != "pol" || !strings.HasPrefix(cfg.Frozen[0].Hash, "sha256:") {
		t.Fatalf("frontmatter frozen keys = %+v", cfg.Frozen)
	}
	// Second stamp: idempotent output, no file change.
	before, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	out2 := runStamp(t, dir)
	if out != out2 {
		t.Errorf("second stamp output differs:\n%s\n%s", out, out2)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if string(before) != string(after) {
		t.Error("second stamp rewrote the file")
	}
}

// TestStampMatchesScript compares the Go stamp's stdout with the shell
// script's stamp on identical copies of the same skill.
func TestStampMatchesScript(t *testing.T) {
	root := repoRoot(t)
	goDir := t.TempDir()
	shDir := t.TempDir()
	for _, d := range []string{goDir, shDir} {
		if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(stampSkill), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	goOut := runStamp(t, goDir)
	cmd := exec.Command(filepath.Join(root, "bin", "upkeep.sh"), "stamp", shDir)
	cmd.Env = append(os.Environ(), "UPKEEP_NO_DELEGATE=1")
	shOut, err := cmd.Output()
	if err != nil {
		t.Fatalf("script stamp: %v\n%s", err, shOut)
	}
	if goOut != string(shOut) {
		t.Errorf("Go stamp output differs from the script:\nGO:\n%s\nSH:\n%s", goOut, shOut)
	}
}

func TestStampMissingSkill(t *testing.T) {
	// The missing-SKILL.md path exits 2 via os.Exit, which cannot run
	// in-process; the underlying engine error is asserted instead.
	if _, err := engine.Stamp(context.Background(), t.TempDir()); err == nil {
		t.Fatal("expected error for missing SKILL.md")
	}
}
