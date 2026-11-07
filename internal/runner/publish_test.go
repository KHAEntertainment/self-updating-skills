package runner

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// ciPublishFixture builds a git repo with one skill, a bare "origin" and a
// stub gh that records every call, so the publish script never touches
// GitHub.
func ciPublishFixture(t *testing.T) (work, ghDir, ghLog string) {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	work = filepath.Join(root, "work")
	git(t, root, "init", "--bare", "-b", "main", origin)
	git(t, root, "clone", origin, work)
	git(t, work, "config", "user.email", "t@example.com")
	git(t, work, "config", "user.name", "t")
	git(t, work, "config", "commit.gpgsign", "false")

	skill := filepath.Join(work, "skills", "alpha")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: alpha\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "upkeep.data.json"), []byte("{\n  \"schema\": 1,\n  \"owner\": \"ci\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "-A")
	git(t, work, "commit", "-q", "-m", "initial")
	git(t, work, "push", "-q", "origin", "main")

	ghDir = filepath.Join(root, "bin")
	if err := os.MkdirAll(ghDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ghLog = filepath.Join(root, "gh.log")
	stub := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + ghLog + "\n" +
		"prev=\"\"\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$prev\" = \"--body-file\" ]; then cp \"$a\" " + filepath.Join(root, "gh-body") + ".$(basename \"$a\"); fi\n" +
		"  prev=\"$a\"\n" +
		"done\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(ghDir, "gh"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	return work, ghDir, ghLog
}

// updateJSON builds the JSON "upkeep update --runner ci --json" prints.
func updateJSON(t *testing.T, path string, skills []map[string]any) {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"run_id":      42,
		"run_outcome": "ok",
		"skills":      skills,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCIPublishOpensOnePRPerSkill(t *testing.T) {
	work, ghDir, ghLog := ciPublishFixture(t)
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	alpha := filepath.Join(work, "skills", "alpha")
	beta := filepath.Join(work, "skills", "beta")
	if err := os.MkdirAll(beta, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonPath := filepath.Join(t.TempDir(), "update.json")
	updateJSON(t, jsonPath, []map[string]any{
		{
			"skill_dir": alpha,
			"status":    "written",
			"outcome":   "auto",
			"rows": []map[string]any{{
				"row": map[string]any{
					"id":    "r1",
					"old":   "--old-flag",
					"new":   "--new-flag",
					"quote": "Use --new-flag now.",
				},
				"checks": []map[string]any{
					{"name": "Q", "ran": true, "pass": true},
					{"name": "S", "ran": true, "pass": true},
					{"name": "D", "ran": false, "detail": "not run: no history"},
				},
				"outcome": "auto",
			}},
		},
		{
			"skill_dir": beta,
			"status":    "pr",
			"reason":    "no row was accepted",
			"rows": []map[string]any{{
				"row": map[string]any{
					"id":    "r2",
					"old":   "old-key",
					"new":   "new-key",
					"quote": "Set new-key.",
				},
				"checks": []map[string]any{
					{"name": "Q", "ran": true, "pass": true},
					{"name": "S", "ran": false, "detail": "not run: no schema"},
				},
				"outcome": "pr",
			}},
		},
	})

	// The updater changed the skill in the working tree.
	if err := os.WriteFile(filepath.Join(alpha, "SKILL.md"), []byte("---\nname: alpha\n---\n\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "-A")

	script := filepath.Join(repoRoot(t), "scripts", "ci-publish.sh")
	cmd := exec.Command("bash", script, "--update-json", jsonPath, "--repo", work, "--base", "main")
	cmd.Env = append(os.Environ(), "PATH="+ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ci-publish.sh: %v\n%s", err, out)
	}

	log, err := os.ReadFile(ghLog)
	if err != nil {
		t.Fatalf("gh was never called: %v", err)
	}
	calls := string(log)
	if n := strings.Count(calls, "pr create"); n != 2 {
		t.Errorf("gh pr create was called %d times, want 2:\n%s", n, calls)
	}
	if n := strings.Count(calls, "merge --auto --squash"); n != 1 {
		t.Errorf("gh pr merge --auto --squash was called %d times, want 1 (only the written skill):\n%s", n, calls)
	}
	if !strings.Contains(calls, "upkeep/alpha-") || !strings.Contains(calls, "upkeep/beta-") {
		t.Errorf("the branch names are wrong:\n%s", calls)
	}

	// The PR body lists each row's checks and its evidence quote.
	bodies, err := filepath.Glob(filepath.Join(filepath.Dir(ghLog), "gh-body.*"))
	if err != nil || len(bodies) != 2 {
		t.Fatalf("the PR bodies were not captured: %v %v", bodies, err)
	}
	var joined string
	for _, b := range bodies {
		content, err := os.ReadFile(b)
		if err != nil {
			t.Fatal(err)
		}
		joined += string(content)
	}
	for _, want := range []string{
		"Q pass",
		"S pass",
		"D not run",
		"Use --new-flag now.",
		"Q pass",
		"S not run",
		"Set new-key.",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the PR body does not contain %q:\n%s", want, joined)
		}
	}
	// The review PR is not auto-merged.
	for _, b := range bodies {
		content, err := os.ReadFile(b)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(content), "upkeep/beta-") && !strings.Contains(string(content), "not** auto-merged") {
			t.Errorf("the review PR does not say it is not auto-merged:\n%s", content)
		}
	}
}

func TestCIPublishWithNothingToDo(t *testing.T) {
	work, ghDir, ghLog := ciPublishFixture(t)
	jsonPath := filepath.Join(t.TempDir(), "update.json")
	updateJSON(t, jsonPath, []map[string]any{
		{"skill_dir": filepath.Join(work, "skills", "alpha"), "status": "skipped", "reason": "owner does not match"},
	})
	cmd := exec.Command("bash", filepath.Join(repoRoot(t), "scripts", "ci-publish.sh"),
		"--update-json", jsonPath, "--repo", work, "--base", "main")
	cmd.Env = append(os.Environ(), "PATH="+ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ci-publish.sh: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "nothing to publish") {
		t.Errorf("output = %q, want nothing to publish", out)
	}
	if _, err := os.Stat(ghLog); !os.IsNotExist(err) {
		t.Errorf("gh was called even though there was nothing to publish")
	}
}
