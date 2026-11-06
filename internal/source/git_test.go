package source

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// gitFixture builds a local docs repository: docs/config.md first says
// "--legacy-flag", a later commit replaces it with "--modern-flag".
func gitFixture(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "docs-repo")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	run("init", "-q", "-b", "main")
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, "docs", "config.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Start the server with --legacy-flag.\n")
	run("add", ".")
	run("commit", "-qm", "one")
	write("Start the server with --modern-flag.\n")
	run("commit", "-qam", "two")
	return dir
}

func withLocalGit(t *testing.T) {
	t.Helper()
	AllowLocalGitRepo = "source-test"
	t.Cleanup(func() { AllowLocalGitRepo = "" })
}

func TestGitLogSFindsHistory(t *testing.T) {
	withLocalGit(t)
	repo := gitFixture(t)
	cache := t.TempDir()
	ctx := context.Background()
	found, err := GitLogS(ctx, cache, repo, "docs/config.md", "--legacy-flag", nil, nil)
	if err != nil || !found {
		t.Fatalf("old flag must be found in history: %v %v", found, err)
	}
	found, err = GitLogS(ctx, cache, repo, "docs/config.md", "--never-existed", nil, nil)
	if err != nil || found {
		t.Fatalf("absent string: %v %v", found, err)
	}
	found, err = GitLogS(ctx, cache, repo, "other/path.md", "--legacy-flag", nil, nil)
	if err != nil || found {
		t.Fatalf("path scoping: %v %v", found, err)
	}
	// Second call reuses (fetches) the cached clone.
	entries, _ := os.ReadDir(cache)
	if len(entries) != 1 {
		t.Fatalf("expected one cached clone, got %d", len(entries))
	}
}

func TestGitLogSRefusesNonHTTPS(t *testing.T) {
	ctx := context.Background()
	repo := gitFixture(t)
	hosts := []string{"github.com"}
	for name, u := range map[string]string{
		"local path without seam": repo,
		"file url":                "file://" + repo,
		"ssh":                     "ssh://git@github.com/o/r.git",
		"scp form":                "git@github.com:o/r.git",
		"http":                    "http://github.com/o/r.git",
		"foreign host":            "https://evil.example/o/r.git",
		"credentials":             "https://u:p@github.com/o/r.git",
		"ext transport":           "ext::sh -c touch% /tmp/pwned",
		"empty":                   "",
	} {
		if _, err := GitLogS(ctx, t.TempDir(), u, "docs", "x", hosts, nil); err == nil {
			t.Errorf("%s: %q accepted", name, u)
		}
	}
}

func TestGitLogSIgnoresInheritedGitEnv(t *testing.T) {
	withLocalGit(t)
	repo := gitFixture(t)
	// A hostile environment must not redirect git or run an external diff.
	marker := filepath.Join(t.TempDir(), "ext-diff-ran")
	t.Setenv("GIT_EXTERNAL_DIFF", "sh -c 'touch "+marker+"'")
	t.Setenv("GIT_DIR", t.TempDir())
	found, err := GitLogS(context.Background(), t.TempDir(), repo, "docs/config.md", "--legacy-flag", nil, nil)
	if err != nil || !found {
		t.Fatalf("lookup under hostile env: %v %v", found, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("external diff ran")
	}
}

func TestGitLogSEmptyNeedle(t *testing.T) {
	if _, err := GitLogS(context.Background(), t.TempDir(), "https://github.com/o/r.git", "", "", []string{"github.com"}, nil); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("got %v", err)
	}
}

// -S matches substrings: history holding only "--legacy-flag" is a -S hit
// for "--legacy". The patch-line matcher decides; a whole-identifier
// matcher must report not found, the default (substring) matcher found.
func TestGitLogSMatchesPatchLines(t *testing.T) {
	withLocalGit(t)
	repo := gitFixture(t)
	ctx := context.Background()
	whole := regexp.MustCompile(`(^|[^A-Za-z0-9_.-])--legacy($|[^A-Za-z0-9_-])`)
	found, err := GitLogS(ctx, t.TempDir(), repo, "docs/config.md", "--legacy", nil, whole.MatchString)
	if err != nil || found {
		t.Fatalf("whole-identifier match on --legacy-flag history: %v %v", found, err)
	}
	found, err = GitLogS(ctx, t.TempDir(), repo, "docs/config.md", "--legacy", nil, nil)
	if err != nil || !found {
		t.Fatalf("default substring match: %v %v", found, err)
	}
}

func TestIsPatchChange(t *testing.T) {
	for line, want := range map[string]bool{"+a": true, "-a": true, "+++ b/x": false, "--- a/x": false, " ctx": false, "": false, "abc123": false} {
		if got := isPatchChange(line); got != want {
			t.Errorf("%q: got %v", line, got)
		}
	}
}
