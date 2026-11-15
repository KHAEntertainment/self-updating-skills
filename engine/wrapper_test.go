package engine_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestUpkeepWrapperGoHandoffArgs checks that the Go hand-off receives
// normalised arguments: an explicit `check` subcommand, DEFAULT_SKILL_DIR
// appended when no positional dir was given, flag values consumed (--threshold
// N and --threshold=N), and exit 0. A STUB `upkeep` executable stands in for
// the real binary (the real binary is never built inside a test). The script
// is run explicitly under /bin/bash (macOS bash 3.2, where an empty
// "${arr[@]}" under set -u crashes) and under PATH `bash`, when available.
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
	dirWithSpace := filepath.Join(t.TempDir(), "skill dir with space")

	shells := []struct {
		name string
		path string
	}{
		{"bash-path", "bash"},
	}
	if _, err := os.Stat("/bin/bash"); err == nil {
		shells = append(shells, struct {
			name string
			path string
		}{"bin-bash-3.2", "/bin/bash"})
	}

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no-args", nil, "check " + skillDir},
		{"check", []string{"check"}, "check " + skillDir},
		{"check-debug", []string{"check", "--debug"}, "check " + skillDir},
		{"strict-flag", []string{"--strict"}, "check --strict " + skillDir},
		{"check-strict", []string{"check", "--strict"}, "check --strict " + skillDir},
		{"check-threshold", []string{"check", "--threshold", "7"}, "check --threshold 7 " + skillDir},
		{"threshold-shorthand", []string{"--threshold", "7"}, "check --threshold 7 " + skillDir},
		{"check-threshold-strict", []string{"check", "--threshold", "7", "--strict"}, "check --threshold 7 --strict " + skillDir},
		{"threshold-equals", []string{"check", "--threshold=7"}, "check --threshold=7 " + skillDir},
		{"check-threshold-leading-zero", []string{"check", "--threshold", "010"}, "check --threshold 010 " + skillDir},
		{"threshold-leading-zero-shorthand", []string{"--threshold", "010"}, "check --threshold 010 " + skillDir},
		{"check-threshold-equals-leading-zero", []string{"check", "--threshold=08"}, "check --threshold=08 " + skillDir},
		{"dir", []string{skillDir}, "check " + skillDir},
		{"check-dir", []string{"check", skillDir}, "check " + skillDir},
		{"check-threshold-dir", []string{"check", "--threshold", "7", skillDir}, "check --threshold 7 " + skillDir},
		{"check-threshold-dir-with-space", []string{"check", "--threshold", "7", dirWithSpace}, "check --threshold 7 " + dirWithSpace},
	}

	for _, sh := range shells {
		sh := sh
		for _, tc := range cases {
			tc := tc
			t.Run(sh.name+"/"+tc.name, func(t *testing.T) {
				cmd := exec.Command(sh.path, filepath.Join(skillDir, "scripts", "upkeep.sh"))
				cmd.Args = append(cmd.Args, tc.args...)
				cmd.Dir = cwd
				cmd.Env = append(os.Environ(),
					"HOME="+home,
					"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				)
				out, err := cmd.Output()
				if err != nil {
					t.Fatalf("run: %v", err)
				}
				if got := strings.TrimSpace(string(out)); got != tc.want {
					t.Errorf("stub argv = %q, want %q", got, tc.want)
				}
			})
		}
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
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: x\n---\n"), 0o644); err != nil {
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

// TestUpkeepWrapperDelegationKeepsSkillDir covers Issue #12: when a vendored
// script hands off to a canonical ~/.agents/upkeep/bin/upkeep.sh of a
// different lib version, the canonical copy must work on the caller's skill
// dir, not on its own parent (~/.agents/upkeep). `fetch` and a bare `check`
// take no directory argument, so both depend on it.
func TestUpkeepWrapperDelegationKeepsSkillDir(t *testing.T) {
	root := repoRoot(t)

	// A stub `upkeep` below 0.3.0 keeps `check` off the Go hand-off, whatever
	// is installed on the machine running the test.
	stubDir := t.TempDir()
	writeExecutable(t, filepath.Join(stubDir, "upkeep"), "#!/usr/bin/env bash\necho \"upkeep 0.1.0\"\n")

	script, err := os.ReadFile(filepath.Join(root, "bin", "upkeep.sh"))
	if err != nil {
		t.Fatal(err)
	}
	libLine := regexp.MustCompile(`(?m)^# skill-upkeep-lib: \S+`)
	canonical := libLine.ReplaceAllString(string(script), "# skill-upkeep-lib: v99.0.0")
	if canonical == string(script) {
		t.Fatal("no skill-upkeep-lib line found in bin/upkeep.sh")
	}

	// homeWithCanonical holds a canonical script of a different version, so
	// the vendored copy delegates. homeBare holds none.
	homeWithCanonical := t.TempDir()
	canonDir := filepath.Join(homeWithCanonical, ".agents", "upkeep", "bin")
	if err := os.MkdirAll(canonDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(canonDir, "upkeep.sh"), canonical)
	homeBare := t.TempDir()

	skillDir := filepath.Join(t.TempDir(), "skills", "x")
	if err := os.MkdirAll(filepath.Join(skillDir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	skillDoc := "---\nname: x\nmetadata:\n  upkeep-source-base: \"https://example.invalid/docs\"\n  upkeep-source-pages: \"alpha, beta\"\n---\n\n# X\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	vendored := filepath.Join(skillDir, "scripts", "upkeep.sh")
	copyFile(t, filepath.Join(root, "bin", "upkeep.sh"), vendored)

	tmp := t.TempDir()
	run := func(home string, extraEnv []string, args ...string) string {
		t.Helper()
		cmd := exec.Command(vendored, args...)
		cmd.Dir = t.TempDir() // unrelated cwd
		cmd.Env = append(os.Environ(),
			"HOME="+home,
			"TMPDIR="+tmp,
			"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		)
		cmd.Env = append(cmd.Env, extraEnv...)
		out, _ := cmd.CombinedOutput() // check exits 0; fetch --list exits 0
		return string(out)
	}

	t.Run("delegated-fetch", func(t *testing.T) {
		out := run(homeWithCanonical, nil, "fetch", "--list", "--debug")
		if !strings.Contains(out, "(delegate)") {
			t.Fatalf("expected the hand-off to the canonical script:\n%s", out)
		}
		if strings.Contains(out, "no SKILL.md") || !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
			t.Errorf("delegated fetch did not resolve the caller's skill dir:\n%s", out)
		}
	})
	t.Run("delegated-bare-check", func(t *testing.T) {
		out := run(homeWithCanonical, nil, "--debug")
		if !strings.Contains(out, "(delegate)") {
			t.Fatalf("expected the hand-off to the canonical script:\n%s", out)
		}
		if strings.Contains(out, "no SKILL.md") || !strings.Contains(out, "VERDICT:") {
			t.Errorf("delegated bare check did not resolve the caller's skill dir:\n%s", out)
		}
	})
	t.Run("no-delegation-unchanged", func(t *testing.T) {
		out := run(homeWithCanonical, []string{"UPKEEP_NO_DELEGATE=1"}, "fetch", "--list", "--debug")
		if strings.Contains(out, "(delegate)") || !strings.Contains(out, "alpha") {
			t.Errorf("UPKEEP_NO_DELEGATE=1 must stay on the vendored script and its own dir:\n%s", out)
		}
	})
	t.Run("stray-variable-ignored", func(t *testing.T) {
		// Outside a delegated call the variable is not trusted.
		out := run(homeBare, []string{"UPKEEP_SKILL_DIR=" + filepath.Join(tmp, "nowhere")}, "fetch", "--list")
		if strings.Contains(out, "no SKILL.md") || !strings.Contains(out, "alpha") {
			t.Errorf("a stray UPKEEP_SKILL_DIR redirected the script:\n%s", out)
		}
	})
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
