package engine_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestUpkeepWrapperGoHandoffArgs checks that the Go hand-off receives
// normalised arguments: an explicit `check` subcommand, DEFAULT_SKILL_DIR
// appended when no positional dir was given, and flags preserved. A STUB
// `upkeep` executable stands in for the real binary (the real binary is
// never built inside a test).
func TestUpkeepWrapperGoHandoffArgs(t *testing.T) {
	root := repoRoot(t)

	stubDir := t.TempDir()
	stub := filepath.Join(stubDir, "upkeep")
	writeExecutable(t, stub, `#!/usr/bin/env bash
if [[ "$1" == "--version" ]]; then
  echo "upkeep 0.3.0"
else
  printf '%s\n' "$*"
fi
`)
	home := t.TempDir()

	skillDir := filepath.Join(t.TempDir(), "skills", "x")
	if err := os.MkdirAll(filepath.Join(skillDir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: x\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	copyFile(t, filepath.Join(root, "bin", "upkeep.sh"), filepath.Join(skillDir, "scripts", "upkeep.sh"))

	cwd := t.TempDir() // unrelated cwd

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no-args", nil, "check " + skillDir},
		{"strict-flag", []string{"--strict"}, "check --strict " + skillDir},
		{"dir", []string{skillDir}, "check " + skillDir},
		{"check-strict-no-dir", []string{"check", "--strict"}, "check --strict " + skillDir},
		{"check-dir", []string{"check", skillDir}, "check " + skillDir},
		{"check-threshold-dir", []string{"check", "--threshold", "7", skillDir}, "check --threshold 7 " + skillDir},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(filepath.Join(skillDir, "scripts", "upkeep.sh"), tc.args...)
			cmd.Dir = cwd
			cmd.Env = append(os.Environ(),
				"HOME="+home,
				"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
			)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			if got := strings.TrimSpace(string(out)); got != tc.want {
				t.Errorf("stub argv = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestUpkeepWrapperGoHandoffKeepsPythonSubcommands verifies that non-check
// subcommands never reach the Go binary: fetch/stamp/sync stay on the
// script path even with a Go binary present.
func TestUpkeepWrapperGoHandoffKeepsPythonSubcommands(t *testing.T) {
	root := repoRoot(t)

	stubDir := t.TempDir()
	stub := filepath.Join(stubDir, "upkeep")
	writeExecutable(t, stub, `#!/usr/bin/env bash
if [[ "$1" == "--version" ]]; then
  echo "upkeep 0.3.0"
else
  printf 'GO-GOT: %s\n' "$*"
fi
`)
	home := t.TempDir()

	skillDir := filepath.Join(t.TempDir(), "skills", "x")
	if err := os.MkdirAll(filepath.Join(skillDir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: x\nmetadata:\n  upkeep-source-base: \"https://example.com\"\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	copyFile(t, filepath.Join(root, "bin", "upkeep.sh"), filepath.Join(skillDir, "scripts", "upkeep.sh"))

	// `sync` is a Python-side subcommand; with a Go binary present it must
	// still be handled by the script and never reach the binary.
	cmd := exec.Command(filepath.Join(skillDir, "scripts", "upkeep.sh"), "sync", skillDir)
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "SYNCED") {
		t.Errorf("expected SYNCED output, got:\n%s", out)
	}
	if strings.Contains(string(out), "GO-GOT:") {
		t.Errorf("sync reached the Go binary:\n%s", out)
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o755); err != nil {
		t.Fatal(err)
	}
}
