package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// requiredChecksFixture builds a repo with the named skills and a stub
// upkeep binary: alpha is fresh, beta is stale, gamma is tampered and
// delta drifts when rendered.
func requiredChecksFixture(t *testing.T, names ...string) (repo, stub string) {
	t.Helper()
	repo = t.TempDir()
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.email", "t@example.com")
	git(t, repo, "config", "user.name", "t")
	git(t, repo, "config", "commit.gpgsign", "false")
	for _, name := range names {
		dir := filepath.Join(repo, "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// A sidecar, so the render --check pass covers the skill.
		if err := os.WriteFile(filepath.Join(dir, "upkeep.data.json"), []byte("{\n  \"schema\": 1\n}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "initial")

	stub = filepath.Join(t.TempDir(), "upkeep-stub")
	script := `#!/bin/sh
case "$1" in
  check)
    case "$3" in
      *alpha/*) echo "VERDICT: FRESH — alpha"; exit 0;;
      *beta/*)  echo "VERDICT: STALE — beta"; exit 1;;
      *gamma/*) echo "VERDICT: TAMPERED — gamma"; exit 1;;
      *) echo "VERDICT: FRESH — $3"; exit 0;;
    esac
    ;;
  render)
    case "$3" in
      *delta/*) echo "VERDICT: DRIFT — delta"; exit 1;;
    esac
    echo "VERDICT: UNCHANGED — $3"; exit 0;;
esac
exit 0
`
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return repo, stub
}

func runRequiredChecks(t *testing.T, repo, stub string, env ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join(repoRoot(t), "scripts", "required-checks.sh"), stub, "main")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
		err = nil
	}
	if err != nil {
		t.Fatalf("required-checks.sh: %v\n%s", err, out)
	}
	return string(out), code
}

func TestRequiredChecksIgnoresAnUntouchedStaleSkill(t *testing.T) {
	repo, stub := requiredChecksFixture(t, "alpha", "beta")
	out, code := runRequiredChecks(t, repo, stub)
	if code != 0 {
		t.Errorf("exit code = %d, want 0:\n%s", code, out)
	}
}

func TestRequiredChecksFailsOnATouchedStaleSkill(t *testing.T) {
	repo, stub := requiredChecksFixture(t, "alpha", "beta")
	git(t, repo, "checkout", "-b", "pr")
	if err := os.WriteFile(filepath.Join(repo, "skills", "beta", "SKILL.md"), []byte("---\nname: beta\n---\n\nedited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "touch beta")
	out, code := runRequiredChecks(t, repo, stub)
	if code != 1 {
		t.Errorf("exit code = %d, want 1 (the PR touches a stale skill):\n%s", code, out)
	}
}

func TestRequiredChecksCanRequireEverySkill(t *testing.T) {
	repo, stub := requiredChecksFixture(t, "alpha", "beta")
	out, code := runRequiredChecks(t, repo, stub, "UPKEEP_REQUIRE_ALL_SKILLS=1")
	if code != 1 {
		t.Errorf("exit code = %d, want 1 (every skill must be fresh):\n%s", code, out)
	}
}

func TestRequiredChecksFailsOnATamperedSkill(t *testing.T) {
	repo, stub := requiredChecksFixture(t, "alpha", "gamma")
	out, code := runRequiredChecks(t, repo, stub)
	if code != 1 {
		t.Errorf("exit code = %d, want 1 (a tampered frozen block fails anywhere):\n%s", code, out)
	}
	if !strings.Contains(out, "tampered") {
		t.Errorf("the output does not name the tampered skill:\n%s", out)
	}
}

func TestRequiredChecksFailsOnRenderDrift(t *testing.T) {
	repo, stub := requiredChecksFixture(t, "alpha", "delta")
	out, code := runRequiredChecks(t, repo, stub)
	if code != 1 {
		t.Errorf("exit code = %d, want 1 (render drift fails the job):\n%s", code, out)
	}
	if !strings.Contains(out, "render drift") {
		t.Errorf("the output does not name the render drift:\n%s", out)
	}
}
