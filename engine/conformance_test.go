package engine_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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

const gitStub = `#!/usr/bin/env bash
if [[ "$1" != "ls-remote" ]]; then exit 2; fi
url="${@: -1}"
case "$url" in
  *gh-tags*)
    echo "1111111111111111111111111111111111111111 refs/tags/v1.0.0"
    echo "2222222222222222222222222222222222222222 refs/tags/v2.0.0"
    exit 0;;
  *gh-sha-stale*)
    echo "dddddddddddddddddddddddddddddddddddddd HEAD"
    exit 0;;
  *gh-sha*)
    echo "cccccccccccccccccccccccccccccccccccccccc HEAD"
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

func goLines(t *testing.T, ctx context.Context, dir string) []string {
	t.Helper()
	r, err := engine.Check(ctx, dir)
	if err != nil {
		t.Fatalf("engine.Check(%s): %v", dir, err)
	}
	return splitLines(engine.RenderText(r))
}

func shLines(t *testing.T, root, dir string) ([]string, int) {
	t.Helper()
	cmd := exec.Command(filepath.Join(root, "bin", "upkeep.sh"), "check", dir)
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

// TestConformanceFixtures runs both implementations on every conformance
// fixture and requires identical item lines and verdict line.
func TestConformanceFixtures(t *testing.T) {
	root := repoRoot(t)
	setupStubPath(t)

	fixturesDir := filepath.Join(root, "testdata", "conformance")
	entries, err := os.ReadDir(fixturesDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		name := name
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(fixturesDir, name)
			abs, err := filepath.Abs(dir)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			gl := goLines(t, ctx, abs)
			sl, _ := shLines(t, root, abs)
			if !equalLines(gl, sl) {
				t.Errorf("mismatch for fixture %s:\nGO=%q\nSH=%q", name, gl, sl)
			}
		})
	}
}

// TestConformanceMissingSkill covers the no-SKILL.md path.
func TestConformanceMissingSkill(t *testing.T) {
	root := repoRoot(t)
	setupStubPath(t)
	dir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	gl := goLines(t, ctx, dir)
	sl, _ := shLines(t, root, dir)
	if !equalLines(gl, sl) {
		t.Errorf("mismatch for missing SKILL.md:\nGO=%q\nSH=%q", gl, sl)
	}
}
