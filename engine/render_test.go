package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KHAEntertainment/self-updating-skills/engine"
	"github.com/KHAEntertainment/self-updating-skills/internal/render"
)

// copyDir recursively copies src to dst (regular files and dirs).
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

// retrofittedSkill returns a temp copy of the repo's retrofitted
// claude-code-authoring skill plus its committed SKILL.md and sidecar bytes.
func retrofittedSkill(t *testing.T) (dir, skillPath, sidecarPath string, wantSkill, wantSidecar []byte) {
	t.Helper()
	root := repoRoot(t)
	src := filepath.Join(root, "skills", "claude-code-authoring")
	wantSkill, err := os.ReadFile(filepath.Join(src, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	wantSidecar, err = os.ReadFile(filepath.Join(src, render.SidecarName))
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "claude-code-authoring")
	copyDir(t, src, dst)
	return dst, filepath.Join(dst, "SKILL.md"), filepath.Join(dst, render.SidecarName), wantSkill, wantSidecar
}

// AC1: the meta-skill retrofit flow plus `upkeep render` reproduces the
// hand-retrofitted skills/claude-code-authoring file byte for byte.
func TestRenderRetrofitByteIdentical(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, skPath, scPath, wantSkill, wantSidecar := retrofittedSkill(t)

	// Pristine committed state: render is a no-op — proof that the committed
	// file is exactly the renderer's output.
	r, err := engine.Render(ctx, dir, false)
	if err != nil {
		t.Fatalf("render (pristine): %v", err)
	}
	if r.Changed {
		t.Fatalf("render on the committed retrofit changed bytes; diff:\n%s", r.Diff)
	}
	got, err := os.ReadFile(skPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(wantSkill) {
		t.Fatal("pristine render modified SKILL.md")
	}
	gotSc, err := os.ReadFile(scPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotSc) != string(wantSidecar) {
		t.Fatal("pristine render modified the sidecar (committed sidecar is not canonical)")
	}

	// Scramble the generated regions and the owned frontmatter keys, then
	// render: the output must be byte-identical to the committed file.
	scrambled := string(wantSkill)
	scrambled = strings.Replace(scrambled,
		"| @anthropic-ai/claude-code | 2.1.310 | 2.1.312 | 2026-10-27 |", "hand-junked pins", 1)
	scrambled = strings.Replace(scrambled,
		"Verified 2026-10-24 for @anthropic-ai/claude-code@2.1.310", "hand-junked freshness", 1)
	scrambled = strings.Replace(scrambled,
		"| stale habit → current | since | source |\n| --- | --- | --- |", "| junk | | |\n| --- | --- | --- |\n| bogus | row | here |", 1)
	scrambled = strings.Replace(scrambled,
		`upkeep-pins: "@anthropic-ai/claude-code=2.1.310"`, `upkeep-pins: "wrong=0.0.0"`, 1)
	scrambled = strings.Replace(scrambled,
		`upkeep-verified: "2026-10-24"`, `upkeep-verified: "1999-01-01"`, 1)
	if scrambled == string(wantSkill) {
		t.Fatal("scrambling failed to change anything")
	}
	if err := os.WriteFile(skPath, []byte(scrambled), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err = engine.Render(ctx, dir, false)
	if err != nil {
		t.Fatalf("render (scrambled): %v", err)
	}
	if !r.Changed {
		t.Fatal("render of scrambled input reported no change")
	}
	got, err = os.ReadFile(skPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(wantSkill) {
		t.Errorf("rendered output is not byte-identical to the hand-retrofitted file")
		// Show the first divergence for triage.
		a, b := string(wantSkill), string(got)
		for i := 0; i < len(a) && i < len(b); i++ {
			if a[i] != b[i] {
				lo := i - 60
				if lo < 0 {
					lo = 0
				}
				hi := i + 60
				t.Errorf("first divergence at byte %d:\nwant: %q\ngot:  %q", i, a[lo:min(hi, len(a))], b[lo:min(hi, len(b))])
				break
			}
		}
	}
}

// AC2: a second render writes nothing — the file mtime is unchanged.
func TestRenderSecondRunNoOp(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, skPath, _, _, _ := retrofittedSkill(t)

	r, err := engine.Render(ctx, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Changed {
		t.Fatalf("first render on the committed retrofit should be a no-op, diff:\n%s", r.Diff)
	}
	fixed := time.Date(2000, 1, 2, 3, 4, 5, 600000000, time.UTC)
	if err := os.Chtimes(skPath, fixed, fixed); err != nil {
		t.Fatal(err)
	}
	r, err = engine.Render(ctx, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Changed {
		t.Fatal("second render reported a change")
	}
	info, err := os.Stat(skPath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(fixed) {
		t.Errorf("mtime changed on the no-op render: %v != %v", info.ModTime(), fixed)
	}
}

// AC7: the rendered file keeps its original file mode.
func TestRenderKeepsFileMode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, skPath, _, wantSkill, _ := retrofittedSkill(t)
	if err := os.Chmod(skPath, 0o640); err != nil {
		t.Fatal(err)
	}
	// Scramble the pins region so the render actually rewrites the file.
	scrambled := strings.Replace(string(wantSkill),
		"| @anthropic-ai/claude-code | 2.1.310 | 2.1.312 | 2026-10-27 |", "junk", 1)
	if err := os.WriteFile(skPath, []byte(scrambled), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Render(ctx, dir, false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(skPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640", info.Mode().Perm())
	}
}

// AC4: a region overlapping a frozen block makes render fail without
// changing any file.
func TestRenderRejectsFrozenOverlapEngine(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir := t.TempDir()
	doc := "---\nname: t\nmetadata:\n  upkeep-pins: \"stale-pkg=1.0.0\"\n---\n\n# T\n\n<!-- generated:pins -->\nbody\n<!-- frozen:pol -->\nf\n<!-- /generated:pins -->\n<!-- /frozen:pol -->\n"
	sk := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(sk, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	sidecar := "{\n  \"schema\": 1,\n  \"pins\": {\n    \"stale-pkg\": {\n      \"verified_for\": \"1.0.0\"\n    }\n  }\n}\n"
	sc := filepath.Join(dir, render.SidecarName)
	if err := os.WriteFile(sc, []byte(sidecar), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := engine.Render(ctx, dir, false)
	if err == nil {
		t.Fatal("expected overlap error")
	}
	got, _ := os.ReadFile(sk)
	if string(got) != doc {
		t.Error("SKILL.md changed despite the render failure")
	}
	gotSc, _ := os.ReadFile(sc)
	if string(gotSc) != sidecar {
		t.Error("sidecar changed despite the render failure")
	}
}

// Sanitization guard: redirect fields failing validation make render exit
// non-zero and write nothing at all.
func TestRenderSanitizationFailureWritesNothing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, skPath, _, _, _ := retrofittedSkill(t)
	// A hostile sidecar: a redirect row with a line break and a non-https
	// source. Validation must fail before any write.
	sc, err := render.LoadSidecar(dir)
	if err != nil {
		t.Fatal(err)
	}
	sc.Redirects = []render.Redirect{
		{ID: "bad", Old: "a\nb", New: "c"},
		{ID: "bad2", Old: "x", New: "y", Source: "http://evil.example/"},
	}
	if err := os.WriteFile(filepath.Join(dir, render.SidecarName), sc.Canonical(), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(skPath)
	if _, err := engine.Render(ctx, dir, false); err == nil {
		t.Fatal("expected sanitization error")
	}
	after, _ := os.ReadFile(skPath)
	if string(after) != string(before) {
		t.Error("SKILL.md changed despite validation failure")
	}
}

// AC3: a hand edit inside a generated region is reported as DRIFT with
// overall verdict STALE by check; render --check (dryRun) sees the drift.
func TestCheckDriftOnHandEdit(t *testing.T) {
	setupStubPath(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir := t.TempDir()
	doc := "---\nname: t\nmetadata:\n  upkeep-pins: \"stale-pkg=1.0.0\"\n---\n\n# T\n\n<!-- generated:pins -->\njunk\n<!-- /generated:pins -->\n"
	sk := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(sk, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	scBytes := "{\n  \"schema\": 1,\n  \"owner\": \"ci\",\n  \"pins\": {\n    \"stale-pkg\": {\n      \"kind\": \"npm\",\n      \"verified_for\": \"1.0.0\",\n      \"latest_seen\": \"2.0.0\",\n      \"checked_at\": \"2026-01-01\"\n    }\n  }\n}\n"
	if err := os.WriteFile(filepath.Join(dir, render.SidecarName), []byte(scBytes), 0o644); err != nil {
		t.Fatal(err)
	}

	// check: the hand-edited region is DRIFT, the lagging pin is
	// CONTENT-LAG, and drift forces the overall STALE verdict.
	r, err := engine.Check(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	drift, lag := 0, 0
	for _, it := range r.Items {
		if it.Kind == "DRIFT" && strings.Contains(it.Line, "region:pins") {
			drift++
		}
		if it.Kind == "CONTENT-LAG" {
			lag++
		}
	}
	if drift != 1 {
		t.Errorf("DRIFT region items = %d, want 1 (items: %+v)", drift, r.Items)
	}
	if lag != 1 {
		t.Errorf("CONTENT-LAG items = %d, want 1", lag)
	}
	if r.Verdict != "STALE" {
		t.Errorf("verdict = %q, want STALE (DRIFT forces STALE over CONTENT-LAG)", r.Verdict)
	}

	// render --check: dry-run sees the drift (the CLI exits 1 on it).
	res, err := engine.Render(ctx, dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed {
		t.Error("dry-run render should detect drift")
	}
	// And the actual render repairs it; a following check is CONTENT-LAG
	// (the pin lags) with no DRIFT items.
	if _, err := engine.Render(ctx, dir, false); err != nil {
		t.Fatal(err)
	}
	r, err = engine.Check(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != "CONTENT-LAG" {
		t.Errorf("verdict after render = %q, want CONTENT-LAG", r.Verdict)
	}
	for _, it := range r.Items {
		if it.Kind == "DRIFT" {
			t.Errorf("unexpected DRIFT item after render: %s", it.Line)
		}
	}
}

// Concurrency: two renders racing on the same directory must both succeed
// and leave the canonical bytes (atomic renames, deterministic output).
func TestRenderConcurrent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, skPath, _, wantSkill, _ := retrofittedSkill(t)
	// Scramble so both renders have real work to do.
	scrambled := strings.Replace(string(wantSkill),
		"| @anthropic-ai/claude-code | 2.1.310 | 2.1.312 | 2026-10-27 |", "junk", 1)
	if err := os.WriteFile(skPath, []byte(scrambled), 0o644); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := engine.Render(ctx, dir, false); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent render: %v", err)
	}
	got, err := os.ReadFile(skPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(wantSkill) {
		t.Error("concurrent renders left non-canonical bytes")
	}
}
