package engine_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KHAEntertainment/self-updating-skills/engine"
)

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

const npmStub = `#!/usr/bin/env bash
if [[ "$1" == "view" && "$3" == "version" ]]; then
  case "$2" in
    fresh-pkg) echo "1.2.3"; exit 0;;
    stale-pkg) echo "2.0.0"; exit 0;;
    ahead-pkg) echo "0.9.0"; exit 0;;
    unres-pkg) exit 7;;
    *) exit 2;;
  esac
fi
exit 2
`

// gitStub parses the real `git ls-remote` argument shapes the implementations
// emit: `<url> HEAD` for SHA pins and `--tags --refs <url>` for tag pins.
const gitStub = `#!/usr/bin/env bash
if [[ "$1" != "ls-remote" ]]; then exit 2; fi
url=""
is_head=false
for a in "${@:2}"; do
  if [[ "$a" == "HEAD" ]]; then is_head=true; fi
  if [[ "$a" == https://* ]]; then url="$a"; fi
done
if [[ -z "$url" ]]; then exit 2; fi
if [[ "$is_head" == true ]]; then
  case "$url" in
    *gh-sha-stale*) echo "dddddddddddddddddddddddddddddddddddddd HEAD"; exit 0;;
    *gh-sha*) echo "cccccccccccccccccccccccccccccccccccccccc HEAD"; exit 0;;
    *gh-unres*) exit 7;;
    *) exit 2;;
  esac
fi
case "$url" in
  *gh-tags*)
    echo "1111111111111111111111111111111111111111 refs/tags/v1.0.0"
    echo "2222222222222222222222222222222222222222 refs/tags/v2.0.0"
    exit 0;;
  *gh-unres*) exit 7;;
  *) exit 2;;
esac
`

// setupStubPath writes fake npm/git executables and puts their directory
// first on PATH for both the in-process Go engine and the shell script.
func setupStubPath(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"npm": npmStub, "git": gitStub} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func goLines(t *testing.T, ctx context.Context, dir string) ([]string, *engine.CheckReport) {
	t.Helper()
	r, err := engine.Check(ctx, dir)
	if err != nil {
		t.Fatalf("engine.Check(%s): %v", dir, err)
	}
	return splitLines(engine.RenderText(r)), r
}

func shLines(t *testing.T, root, dir string, strict bool) ([]string, int) {
	t.Helper()
	args := []string{"check"}
	if strict {
		args = append(args, "--strict")
	}
	args = append(args, dir)
	cmd := exec.Command(filepath.Join(root, "bin", "upkeep.sh"), args...)
	cmd.Env = append(os.Environ(), "UPKEEP_NO_DELEGATE=1")
	out, err := cmd.Output()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
		err = nil
	}
	if err != nil {
		t.Fatalf("script check(%s): %v", dir, err)
	}
	return splitLines(string(out)), code
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// goStrictExit mirrors the CLI's --strict exit rule.
func goStrictExit(r *engine.CheckReport) int {
	if r.MissingSkill || r.Verdict == "STALE" || r.Verdict == "TAMPERED" {
		return 1
	}
	return 0
}

type conformanceCase struct {
	name       string
	fixture    string // under testdata/conformance; "" => missing-SKILL.md case
	verdict    string // intended verdict ("" for the missing-skill case)
	strictFail bool   // intended exit 1 under --strict
	countLine  string // if set, this exact line must appear exactly once
}

func conformanceCases() []conformanceCase {
	return []conformanceCase{
		{"fresh-date", "fresh-date", "FRESH", false, ""},
		{"stale-date", "stale-date", "STALE", true, ""},
		{"fresh-pin", "fresh-pin", "FRESH", false, ""},
		{"stale-pin", "stale-pin", "STALE", true, ""},
		{"ahead-pin", "ahead-pin", "FRESH", false, ""},
		{"sha-pin-fresh", "sha-pin-fresh", "FRESH", false, ""},
		{"sha-pin-stale", "sha-pin-stale", "STALE", true, ""},
		{"unresolved-pin", "unresolved-pin", "FRESH", false, ""},
		{"tampered", "tampered", "TAMPERED", true, ""},
		{"unstamped", "unstamped", "TAMPERED", true, ""},
		{"missing-block", "missing-block", "TAMPERED", true, ""},
		{"unconfigured", "unconfigured", "UNKNOWN", false, ""},
		{"fenced-frozen", "fenced-frozen", "UNKNOWN", false, ""},
		{"mixed", "mixed", "FRESH", false, ""},
		{"crlf", "crlf", "FRESH", false, ""},
		{"frozen-nested", "frozen-nested", "TAMPERED", true, "UNSTAMPED frozen:a"},
		{"frozen-dup-same", "frozen-dup-same", "TAMPERED", true, "UNSTAMPED frozen:x"},
		{"frozen-dup-across", "frozen-dup-across", "TAMPERED", true, "UNSTAMPED frozen:y"},
		{"trimmed-values", "trimmed-values", "STALE", true, "INTACT   frozen:pol"},
	}
}

// TestConformanceFixtures runs both implementations on every conformance
// fixture (default and --strict modes) and requires identical item lines,
// verdict line, and exit codes, plus the intended verdict.
func TestConformanceFixtures(t *testing.T) {
	root := repoRoot(t)
	setupStubPath(t)

	for _, tc := range conformanceCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(root, "testdata", "conformance", tc.fixture)
			abs, err := filepath.Abs(dir)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			gl, report := goLines(t, ctx, abs)
			if tc.verdict != "" && report.Verdict != tc.verdict {
				t.Errorf("Go verdict = %q, want %q", report.Verdict, tc.verdict)
			}
			sl, sc := shLines(t, root, abs, false)
			if !equalLines(gl, sl) {
				t.Errorf("mismatch (default) for fixture %s:\nGO=%q\nSH=%q", tc.name, gl, sl)
			}
			if sc != 0 {
				t.Errorf("script exit (default) = %d, want 0", sc)
			}

			// --strict mode
			sgl, sReport := goLines(t, ctx, abs)
			ssl, ssc := shLines(t, root, abs, true)
			if !equalLines(sgl, ssl) {
				t.Errorf("mismatch (strict) for fixture %s:\nGO=%q\nSH=%q", tc.name, sgl, ssl)
			}
			wantExit := goStrictExit(sReport)
			if ssc != wantExit {
				t.Errorf("script exit (strict) = %d, want %d (Go rule)", ssc, wantExit)
			}
			if tc.strictFail && wantExit != 1 {
				t.Errorf("expected strict failure for %s, got exit %d", tc.name, wantExit)
			}
			if !tc.strictFail && wantExit != 0 {
				t.Errorf("expected non-strict exit 0 for %s, got %d", tc.name, wantExit)
			}
			if tc.countLine != "" {
				n := 0
				for _, l := range gl {
					if l == tc.countLine {
						n++
					}
				}
				if n != 1 {
					t.Errorf("line %q appears %d times, want exactly 1", tc.countLine, n)
				}
			}
		})
	}
}

// TestConformanceMissingSkill covers the no-SKILL.md path in both modes.
func TestConformanceMissingSkill(t *testing.T) {
	root := repoRoot(t)
	setupStubPath(t)
	dir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	gl, report := goLines(t, ctx, dir)
	if !report.MissingSkill {
		t.Errorf("expected MissingSkill report, got %+v", report)
	}
	sl, sc := shLines(t, root, dir, false)
	if !equalLines(gl, sl) {
		t.Errorf("mismatch for missing SKILL.md:\nGO=%q\nSH=%q", gl, sl)
	}
	if sc != 0 {
		t.Errorf("script exit (default) = %d, want 0", sc)
	}
	ssl, ssc := shLines(t, root, dir, true)
	if ssc != 1 {
		t.Errorf("script exit (strict) = %d, want 1", ssc)
	}
	if len(ssl) != 1 {
		t.Errorf("strict missing-SKILL lines = %q, want 1 line", ssl)
	}
}
