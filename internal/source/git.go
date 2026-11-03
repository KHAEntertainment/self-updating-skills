package source

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// AllowLocalGitRepo is a test-only seam: when non-empty, GitLogS accepts a
// local repository path and the git file transport. It is intentionally
// NOT bound to an environment variable; release builds leave it empty, so
// only https remotes are ever cloned or fetched.
var AllowLocalGitRepo string

const (
	gitCloneTimeout = 10 * time.Minute
	gitLogTimeout   = 2 * time.Minute
)

// GitLogS reports whether needle was ever added or removed under path in
// the history of the default branch of repoURL (`git log -S`), for the D
// check on git-hosted docs. The repository is kept as a bare clone under
// cacheRoot/<sha256(repoURL)> and fetched on later calls.
//
// Hardening: repoURL must be https and its host one of allowedHosts; git
// runs with no shell, protocol.allow=never plus https only, no system or
// global config, no terminal prompt, no hooks, and no external diff or
// textconv drivers. The needle is its own argv entry, so a value starting
// with "-" is never read as an option.
func GitLogS(ctx context.Context, cacheRoot, repoURL, path, needle string, allowedHosts []string) (bool, error) {
	if needle == "" {
		return false, fmt.Errorf("git history: empty search string")
	}
	if err := checkRepoURL(repoURL, allowedHosts); err != nil {
		return false, err
	}
	if cacheRoot == "" {
		return false, fmt.Errorf("git history: no cache directory")
	}
	sum := sha256.Sum256([]byte(repoURL))
	dir := filepath.Join(cacheRoot, hex.EncodeToString(sum[:16])+".git")
	if err := syncBare(ctx, dir, repoURL); err != nil {
		return false, err
	}
	lctx, cancel := context.WithTimeout(ctx, gitLogTimeout)
	defer cancel()
	args := []string{"-C", dir, "log", "--no-ext-diff", "--no-textconv", "--format=%H", "-S", needle, "HEAD"}
	if p := strings.Trim(path, "/"); p != "" {
		args = append(args, "--", p)
	}
	out, err := runGit(lctx, args...)
	if err != nil {
		return false, fmt.Errorf("git history: %w", err)
	}
	return strings.TrimSpace(out) != "", nil
}

// checkRepoURL enforces https on an allowed host (or, under the test seam,
// a local path).
func checkRepoURL(repoURL string, allowedHosts []string) error {
	if strings.TrimSpace(repoURL) == "" {
		return fmt.Errorf("git history: no docs repository configured")
	}
	if AllowLocalGitRepo != "" && filepath.IsAbs(repoURL) {
		return nil
	}
	u, err := url.Parse(repoURL)
	if err != nil {
		return fmt.Errorf("git history: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("git history: non-https repository %q refused", repoURL)
	}
	if u.User != nil {
		return fmt.Errorf("git history: repository URL must not carry credentials")
	}
	if !HostAllowed(u.Hostname(), allowedHosts) {
		return fmt.Errorf("git history: host %q is not the package's repository host", u.Hostname())
	}
	return nil
}

// syncBare clones repoURL into dir (via a temp dir and rename, so a
// failed clone leaves no partial cache) or fetches an existing clone.
func syncBare(ctx context.Context, dir, repoURL string) error {
	cctx, cancel := context.WithTimeout(ctx, gitCloneTimeout)
	defer cancel()
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		if _, err := runGit(cctx, "-C", dir, "fetch", "--quiet", "--prune", "--", repoURL, "+HEAD:refs/heads/upkeep-head"); err != nil {
			return fmt.Errorf("git history: fetch: %w", err)
		}
		if _, err := runGit(cctx, "-C", dir, "symbolic-ref", "HEAD", "refs/heads/upkeep-head"); err != nil {
			return fmt.Errorf("git history: fetch: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return fmt.Errorf("git history: %w", err)
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".clone-")
	if err != nil {
		return fmt.Errorf("git history: %w", err)
	}
	defer os.RemoveAll(tmp)
	dst := filepath.Join(tmp, "repo.git")
	if _, err := runGit(cctx, "clone", "--bare", "--quiet", "--", repoURL, dst); err != nil {
		return fmt.Errorf("git history: clone: %w", err)
	}
	if err := os.Rename(dst, dir); err != nil {
		return fmt.Errorf("git history: %w", err)
	}
	return nil
}

// runGit runs git with the hardened configuration and returns stdout.
// stderr is folded into the error (bounded), never into stdout.
func runGit(ctx context.Context, args ...string) (string, error) {
	base := []string{
		"-c", "protocol.allow=never",
		"-c", "protocol.https.allow=always",
		"-c", "core.hooksPath=" + os.DevNull,
		"-c", "core.fsmonitor=false",
		"-c", "credential.helper=",
	}
	if AllowLocalGitRepo != "" {
		base = append(base, "-c", "protocol.file.allow=always")
	}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	// Inherited GIT_* variables (GIT_DIR, GIT_EXTERNAL_DIFF, GIT_SSH_COMMAND,
	// …) could redirect or extend git; drop them all.
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		if msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	return stdout.String(), nil
}
