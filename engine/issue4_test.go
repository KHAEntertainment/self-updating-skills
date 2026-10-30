package engine_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KHAEntertainment/self-updating-skills/engine"
	"github.com/KHAEntertainment/self-updating-skills/internal/store"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// writeSkillFiles writes a minimal skill dir from explicit file contents.
func writeSkillFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// pagesServer serves /docs/<page>.md with the given bodies.
func pagesServer(t *testing.T, bodies map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/docs/")
		body, ok := bodies[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
}

func shaOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(h[:])
}

const headPage = "# Hello\n"

var headPageSHA = shaOf(headPage)

const updateSkillBase = `---
name: t
metadata:
  upkeep-pins: "stale-pkg=1.0.0"
  upkeep-source-base: "%s/docs"
  upkeep-source-ext: ".md"
  upkeep-source-pages: "intro"
---

# T

<!-- generated:pins -->
| package | verified for | latest seen | checked |
| --- | --- | --- | --- |
| stale-pkg | 1.0.0 | 1.0.0 | 2026-10-27 |
<!-- /generated:pins -->
`

func updateSkillSidecar(owner string, latestSeen string, docBaseline bool) string {
	doc := ""
	if docBaseline {
		doc = fmt.Sprintf(",\n  \"docs\": {\n    \"intro\": { \"verified_sha\": %q, \"latest_sha\": %q }\n  }", headPageSHA, headPageSHA)
	}
	return fmt.Sprintf(`{
  "schema": 1,
  "owner": %q,
  "pins": {
    "stale-pkg": {
      "kind": "npm",
      "verified_for": "1.0.0",
      "latest_seen": %q,
      "checked_at": "2026-10-27"
    }
  }%s
}
`, owner, latestSeen, doc)
}

func setStatePath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	t.Setenv("UPKEEP_STATE_DB", path)
	return path
}

// buildUpkeep returns the real binary built once in TestMain with the
// real PATH and -buildvcs=false (F4).
func buildUpkeep(t *testing.T) string {
	t.Helper()
	if upkeepBin == "" {
		t.Fatal("upkeep binary not built by TestMain")
	}
	return upkeepBin
}

// runCmd runs a command and returns its combined output and exit code.
func runCmd(t *testing.T, name string, args ...string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = os.Environ()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("command timed out: %s %v", name, args)
	}
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run %s: %v", name, err)
		}
	}
	return buf.String(), code
}

// ---------------------------------------------------------------------------
// Acceptances: check verdicts on fixtures (script + Go)
// ---------------------------------------------------------------------------

// AC2a: pin behind AND page changed → Go CONTENT-LAG (with pin and page
// items), script STALE, and the pin line byte-maps between the two.
func TestACPinBehindPageChanged(t *testing.T) {
	setupStubPath(t)
	root := repoRoot(t)
	dir := filepath.Join(root, "testdata", "conformance", "docs-lag-pin-page")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	gl, report := goLines(t, ctx, dir)
	if report.Verdict != "CONTENT-LAG" {
		t.Fatalf("Go verdict = %q, want CONTENT-LAG", report.Verdict)
	}
	var goPin, goPage bool
	for _, it := range report.Items {
		if it.Kind == "CONTENT-LAG" && strings.Contains(it.Line, "pin:stale-pkg") {
			goPin = true
		}
		if it.Kind == "CONTENT-LAG" && strings.Contains(it.Line, "page:intro") {
			goPage = true
		}
	}
	if !goPin || !goPage {
		t.Fatalf("expected pin and page CONTENT-LAG items, got %+v", report.Items)
	}

	mapped := mapGoLines(gl)
	pinLine := "STALE    pin:stale-pkg  pinned 1.0.0 → current 2.0.0"
	foundMapped := false
	for _, l := range mapped {
		if l == pinLine {
			foundMapped = true
		}
	}
	if !foundMapped {
		t.Errorf("mapped Go output missing pin line %q; got %q", pinLine, mapped)
	}

	sl, sc := shLines(t, root, dir, false)
	if sc != 0 {
		t.Errorf("script exit = %d, want 0", sc)
	}
	pinLineFound := false
	verdictCount := 0
	verdictOK := false
	for _, l := range sl {
		if l == pinLine {
			pinLineFound = true
		}
		if strings.HasPrefix(l, "VERDICT: ") {
			verdictCount++
			if strings.HasPrefix(l, "VERDICT: STALE — ") && len(l) > len("VERDICT: STALE — ") {
				verdictOK = true
			}
		}
	}
	if !pinLineFound {
		t.Errorf("script output missing pin line %q; got %q", pinLine, sl)
	}
	if verdictCount != 1 || !verdictOK {
		t.Errorf("script must print exactly one real STALE verdict line; got %q", sl)
	}
	if s := goStrictExit(report); s != 1 {
		t.Errorf("Go strict exit = %d, want 1", s)
	}
}

// AC2b: docs-only change with no version move → Go CONTENT-LAG (page
// item only). Covered by Go alone; the frozen script has no sidecar access
// and reports FRESH (recorded as a known deviation).
func TestACDocsOnlyChange(t *testing.T) {
	setupStubPath(t)
	root := repoRoot(t)
	dir := filepath.Join(root, "testdata", "conformance", "docs-lag-only")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, report := goLines(t, ctx, dir)
	if report.Verdict != "CONTENT-LAG" {
		t.Fatalf("Go verdict = %q, want CONTENT-LAG", report.Verdict)
	}
	pageItem := false
	for _, it := range report.Items {
		if it.Kind == "CONTENT-LAG" && strings.Contains(it.Line, "page:intro") {
			pageItem = true
		}
		if it.Kind == "CONTENT-LAG" && strings.Contains(it.Line, "pin:") {
			t.Errorf("docs-only change must not produce a pin lag item: %+v", report.Items)
		}
	}
	if !pageItem {
		t.Errorf("missing page CONTENT-LAG item: %+v", report.Items)
	}
}

// ---------------------------------------------------------------------------
// Poll
// ---------------------------------------------------------------------------

func TestPollUpdatesSidecarAndSnapshots(t *testing.T) {
	setupStubPath(t)
	srv := pagesServer(t, map[string]string{"intro.md": headPage})
	defer srv.Close()

	dir := writeSkillFiles(t, map[string]string{
		"SKILL.md":         fmt.Sprintf(updateSkillBase, srv.URL),
		"upkeep.data.json": updateSkillSidecar("ci", "1.0.0", false),
	})
	statePath := setStatePath(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	drift, err := engine.Poll(ctx, []string{dir}, engine.PollOptions{Runner: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) != 1 {
		t.Fatalf("drift = %d skills", len(drift))
	}
	d := drift[0]
	if d.SkipReason != "" || d.BlockReason != "" {
		t.Fatalf("poll blocked: %+v", d)
	}
	if len(d.Pins) != 1 || d.Pins[0].LatestSeen != "2.0.0" {
		t.Fatalf("pin drift = %+v", d.Pins)
	}
	if len(d.Docs) != 1 || d.Docs[0].LatestSHA != headPageSHA {
		t.Fatalf("doc drift = %+v", d.Docs)
	}

	// Sidecar on disk reflects the poll.
	scBody, err := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"latest_seen": "2.0.0"`, `"latest_sha": "` + headPageSHA + `"`, `"checked_at": "` + time.Now().Format("2006-01-02") + `"`} {
		if !strings.Contains(string(scBody), want) {
			t.Errorf("sidecar missing %s: %s", want, scBody)
		}
	}
	// Rendered pins region reflects latest_seen.
	skBody, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if !strings.Contains(string(skBody), "| stale-pkg | 1.0.0 | 2.0.0 |") {
		t.Errorf("pins region not rendered from poll: %s", skBody)
	}
	// Snapshot persisted, deduplicated.
	st, err := store.Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sha, _, ok, err := st.LatestSnapshot(ctx, filepath.Base(dir), "intro")
	if err != nil || !ok || sha != headPageSHA {
		t.Errorf("snapshot = %q ok=%v err=%v", sha, ok, err)
	}
	var n int
	if err := st.DB().QueryRow("SELECT count(*) FROM snapshots WHERE skill=? AND page='intro'", filepath.Base(dir)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("snapshot rows = %d, want 1 (dedup)", n)
	}
}

func TestPollGatesTamperedAndDrift(t *testing.T) {
	setupStubPath(t)
	srv := pagesServer(t, map[string]string{"intro.md": headPage})
	defer srv.Close()

	// Unstamped frozen block → TAMPERED.
	dir := writeSkillFiles(t, map[string]string{
		"SKILL.md":         "---\nname: t\nmetadata:\n  upkeep-pins: \"stale-pkg=1.0.0\"\n  upkeep-source-base: \"" + srv.URL + "/docs\"\n  upkeep-source-ext: \".md\"\n  upkeep-source-pages: \"intro\"\n---\n\n# T\n\n<!-- frozen:pol -->\nf\n<!-- /frozen:pol -->\n",
		"upkeep.data.json": updateSkillSidecar("ci", "1.0.0", false),
	})
	scBefore, _ := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
	skBefore, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	drift, err := engine.Poll(ctx, []string{dir}, engine.PollOptions{Runner: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	if drift[0].BlockReason == "" {
		t.Fatal("expected BlockReason for a TAMPERED skill")
	}
	assertFilesUnchanged(t, dir, scBefore, skBefore)

	// Hand-edited generated region → DRIFT.
	dir2 := writeSkillFiles(t, map[string]string{
		"SKILL.md": strings.Replace(fmt.Sprintf(updateSkillBase, srv.URL),
			"| stale-pkg | 1.0.0 | 1.0.0 | 2026-10-27 |",
			"| stale-pkg | 9.9.9 | 1.0.0 | 2026-10-27 |", 1),
		"upkeep.data.json": updateSkillSidecar("ci", "1.0.0", false),
	})
	sc2Before, _ := os.ReadFile(filepath.Join(dir2, "upkeep.data.json"))
	sk2Before, _ := os.ReadFile(filepath.Join(dir2, "SKILL.md"))
	drift2, err := engine.Poll(ctx, []string{dir2}, engine.PollOptions{Runner: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	if drift2[0].BlockReason == "" || !strings.Contains(drift2[0].BlockReason, "DRIFT") {
		t.Fatalf("expected DRIFT BlockReason, got %+v", drift2[0])
	}
	assertFilesUnchanged(t, dir2, sc2Before, sk2Before)
}

func assertFilesUnchanged(t *testing.T, dir string, scBefore, skBefore []byte) {
	t.Helper()
	scAfter, _ := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
	skAfter, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if !bytes.Equal(scAfter, scBefore) {
		t.Error("sidecar changed despite the gate")
	}
	if !bytes.Equal(skAfter, skBefore) {
		t.Error("SKILL.md changed despite the gate")
	}
}

func TestPollOwnerMismatchSkipsWrites(t *testing.T) {
	setupStubPath(t)
	srv := pagesServer(t, map[string]string{"intro.md": headPage})
	defer srv.Close()

	dir := writeSkillFiles(t, map[string]string{
		"SKILL.md":         fmt.Sprintf(updateSkillBase, srv.URL),
		"upkeep.data.json": updateSkillSidecar("ci", "1.0.0", false),
	})
	setStatePath(t)
	scBefore, _ := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
	skBefore, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	drift, err := engine.Poll(ctx, []string{dir}, engine.PollOptions{Runner: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if drift[0].SkipReason == "" {
		t.Fatal("expected SkipReason for an owner mismatch")
	}
	assertFilesUnchanged(t, dir, scBefore, skBefore)
}

func TestSeedDocsPreservesExistingBaseline(t *testing.T) {
	setupStubPath(t)
	srv := pagesServer(t, map[string]string{"intro.md": headPage})
	defer srv.Close()

	const oldSHA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sidecar := `{
  "schema": 1,
  "owner": "ci",
  "pins": {
    "stale-pkg": {
      "kind": "npm",
      "verified_for": "1.0.0",
      "latest_seen": "1.0.0",
      "checked_at": "2026-10-27"
    }
  },
  "docs": {
    "intro": { "verified_sha": "` + oldSHA + `" }
  }
}
`
	dir := writeSkillFiles(t, map[string]string{
		"SKILL.md":         fmt.Sprintf(updateSkillBase, srv.URL),
		"upkeep.data.json": sidecar,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := engine.Poll(ctx, []string{dir}, engine.PollOptions{SeedDocs: true, Runner: "ci"}); err != nil {
		t.Fatal(err)
	}
	scBody, _ := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
	if !strings.Contains(string(scBody), `"verified_sha": "`+oldSHA+`"`) {
		t.Errorf("seed overwrote an existing baseline: %s", scBody)
	}
	if !strings.Contains(string(scBody), `"latest_sha": "`+headPageSHA+`"`) {
		t.Errorf("seed did not record latest_sha: %s", scBody)
	}
}

func TestSeedDocsOnlyFillsMissingBaseline(t *testing.T) {
	setupStubPath(t)
	srv := pagesServer(t, map[string]string{"intro.md": headPage})
	defer srv.Close()

	sidecar := `{
  "schema": 1,
  "owner": "ci",
  "pins": {
    "stale-pkg": {
      "kind": "npm",
      "verified_for": "1.0.0",
      "latest_seen": "1.0.0",
      "checked_at": "2026-10-27"
    }
  },
  "docs": {
    "intro": {}
  }
}
`
	dir := writeSkillFiles(t, map[string]string{
		"SKILL.md":         fmt.Sprintf(updateSkillBase, srv.URL),
		"upkeep.data.json": sidecar,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Without the flag, the baseline stays empty.
	if _, err := engine.Poll(ctx, []string{dir}, engine.PollOptions{Runner: "ci"}); err != nil {
		t.Fatal(err)
	}
	scBody, _ := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
	if strings.Contains(string(scBody), `"verified_sha"`) {
		t.Fatal("poll without --seed-docs must not write verified_sha")
	}

	// With the explicit flag, only the missing baseline is filled.
	if _, err := engine.Poll(ctx, []string{dir}, engine.PollOptions{SeedDocs: true, Runner: "ci"}); err != nil {
		t.Fatal(err)
	}
	scBody, _ = os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
	if !strings.Contains(string(scBody), `"verified_sha": "`+headPageSHA+`"`) {
		t.Errorf("seed did not write verified_sha: %s", scBody)
	}
}

// F1/T2: poll takes the same lease as update; on a held lease it writes
// nothing and the CLI prints "skipped: lease held" and exits 0.
func TestPollSkippedWhenUpdateLeaseHeld(t *testing.T) {
	setupStubPath(t)
	srv := pagesServer(t, map[string]string{"intro.md": headPage})
	defer srv.Close()

	dir := writeSkillFiles(t, map[string]string{
		"SKILL.md":         fmt.Sprintf(updateSkillBase, srv.URL),
		"upkeep.data.json": updateSkillSidecar("ci", "1.0.0", false),
	})
	statePath := setStatePath(t)
	scBefore, _ := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
	skBefore, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := store.Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ok, err := st.AcquireLease(ctx, "update", "held-holder", store.LeaseTTL)
	if err != nil || !ok {
		t.Fatalf("holder acquire: ok=%v err=%v", ok, err)
	}

	// Engine level: ErrLeaseHeld.
	_, err = engine.Poll(ctx, []string{dir}, engine.PollOptions{Runner: "ci"})
	if !errors.Is(err, engine.ErrLeaseHeld) {
		t.Fatalf("Poll err = %v, want ErrLeaseHeld", err)
	}
	assertFilesUnchanged(t, dir, scBefore, skBefore)

	// CLI level: exit 0, "skipped: lease held".
	bin := buildUpkeep(t)
	out, code := runCmd(t, bin, "poll", "--runner", "ci", dir)
	if code != 0 || !strings.Contains(out, "skipped: lease held") {
		t.Errorf("poll CLI = exit %d, %q; want 0 with skipped message", code, out)
	}
	assertFilesUnchanged(t, dir, scBefore, skBefore)
}

// ---------------------------------------------------------------------------
// Plan rules (pure; table-driven)
// ---------------------------------------------------------------------------

func driftOf(pins []engine.PinDrift, docs []engine.DocDrift, owner, aa string) engine.Drift {
	return engine.Drift{Owner: owner, AutoAdvance: aa, Pins: pins, Docs: docs}
}

func TestPlanRules(t *testing.T) {
	patch := engine.PinDrift{Name: "p", VerifiedFor: "1.0.0", LatestSeen: "1.0.1"}
	minor := engine.PinDrift{Name: "p", VerifiedFor: "1.0.0", LatestSeen: "1.1.0"}
	major := engine.PinDrift{Name: "p", VerifiedFor: "1.0.0", LatestSeen: "2.0.0"}
	pre := engine.PinDrift{Name: "p", VerifiedFor: "1.0.0", LatestSeen: "2.0.0-beta.1"}
	sha := engine.PinDrift{Name: "p", VerifiedFor: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", LatestSeen: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	unmoved := engine.PinDrift{Name: "p", VerifiedFor: "1.0.0", LatestSeen: "1.0.0"}
	behind := engine.PinDrift{Name: "p", VerifiedFor: "1.0.0", LatestSeen: "0.9.9"}
	errPin := engine.PinDrift{Name: "p", VerifiedFor: "1.0.0", Error: "npm view failed"}
	unchanged := []engine.DocDrift{{Page: "intro", VerifiedSHA: "a", LatestSHA: "a"}}
	changed := []engine.DocDrift{{Page: "intro", VerifiedSHA: "a", LatestSHA: "b", Changed: true}}
	nobaseline := []engine.DocDrift{{Page: "intro", LatestSHA: "b"}}

	cases := []struct {
		name   string
		drift  engine.Drift
		runner string
		status string
		check  func(*testing.T, engine.SkillPlan)
	}{
		{"owner-not-runner", driftOf(nil, nil, "ci", ""), "local", "skip", nil},
		{"owner-invalid", driftOf(nil, nil, "KHAEntertainment", ""), "ci", "skip", nil},
		{"owner-empty", driftOf(nil, nil, "", ""), "ci", "skip", nil},
		{"pin-poll-error-blocks", driftOf([]engine.PinDrift{errPin}, nil, "ci", ""), "ci", "blocked", nil},
		{"changed-page-drafts-even-no-version", driftOf([]engine.PinDrift{unmoved}, changed, "ci", ""), "ci", "draft", func(t *testing.T, sp engine.SkillPlan) {
			if len(sp.DraftPages) != 1 || sp.DraftPages[0] != "intro" {
				t.Errorf("DraftPages = %v", sp.DraftPages)
			}
		}},
		{"no-moved-pin", driftOf([]engine.PinDrift{unmoved}, unchanged, "ci", ""), "ci", "no-change", nil},
		{"latest-behind-reported", driftOf([]engine.PinDrift{behind}, unchanged, "ci", ""), "ci", "no-change", func(t *testing.T, sp engine.SkillPlan) {
			if !strings.Contains(sp.Reason, "behind") {
				t.Errorf("reason = %q", sp.Reason)
			}
		}},
		{"no-change-missing-baseline-remedy", driftOf([]engine.PinDrift{unmoved}, nobaseline, "ci", ""), "ci", "no-change", func(t *testing.T, sp engine.SkillPlan) {
			if !strings.Contains(sp.Reason, "poll --seed-docs") {
				t.Errorf("reason = %q, want seed remedy", sp.Reason)
			}
		}},
		{"auto-none-drafts", driftOf([]engine.PinDrift{patch}, unchanged, "ci", "none"), "ci", "draft", nil},
		{"auto-invalid-treated-as-none", driftOf([]engine.PinDrift{patch}, unchanged, "ci", "sometimes"), "ci", "draft", func(t *testing.T, sp engine.SkillPlan) {
			if !strings.Contains(sp.Reason, "invalid") {
				t.Errorf("reason = %q", sp.Reason)
			}
		}},
		{"missing-baseline-drafts", driftOf([]engine.PinDrift{patch}, nobaseline, "ci", ""), "ci", "draft", nil},
		{"rule1-minor-advances", driftOf([]engine.PinDrift{minor}, unchanged, "ci", ""), "ci", "advance", nil},
		{"rule1-major-advances", driftOf([]engine.PinDrift{major}, unchanged, "ci", ""), "ci", "advance", nil},
		{"rule1-patch-advances", driftOf([]engine.PinDrift{patch}, unchanged, "ci", ""), "ci", "advance", nil},
		{"prerelease-never-advances", driftOf([]engine.PinDrift{pre}, unchanged, "ci", ""), "ci", "draft", nil},
		{"sha-pin-drafts", driftOf([]engine.PinDrift{sha}, unchanged, "ci", ""), "ci", "draft", nil},
		{"rule2-patch-advances", driftOf([]engine.PinDrift{patch}, nil, "ci", ""), "ci", "advance", nil},
		{"rule2-major-drafts", driftOf([]engine.PinDrift{major}, nil, "ci", ""), "ci", "draft", nil},
		{"multi-pin-all-or-nothing", driftOf([]engine.PinDrift{patch, pre}, unchanged, "ci", ""), "ci", "draft", func(t *testing.T, sp engine.SkillPlan) {
			if len(sp.Advances) != 0 {
				t.Errorf("advances = %v, want none", sp.Advances)
			}
			if len(sp.DraftPins) != 2 {
				t.Errorf("DraftPins = %v", sp.DraftPins)
			}
		}},
		{"multi-pin-all-stable-advance", driftOf([]engine.PinDrift{patch, minor}, unchanged, "ci", ""), "ci", "advance", func(t *testing.T, sp engine.SkillPlan) {
			if len(sp.Advances) != 2 {
				t.Errorf("advances = %v", sp.Advances)
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := engine.Plan(context.Background(), []engine.Drift{tc.drift}, tc.runner)
			if err != nil {
				t.Fatal(err)
			}
			sp := plan.Skills[0]
			if sp.Status != tc.status {
				t.Fatalf("status = %q, want %q (reason %q)", sp.Status, tc.status, sp.Reason)
			}
			if tc.check != nil {
				tc.check(t, sp)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Update cycle
// ---------------------------------------------------------------------------

func TestUpdateDryRunWritesNothing(t *testing.T) {
	setupStubPath(t)
	srv := pagesServer(t, map[string]string{"intro.md": headPage})
	defer srv.Close()

	dir := writeSkillFiles(t, map[string]string{
		"SKILL.md":         fmt.Sprintf(updateSkillBase, srv.URL),
		"upkeep.data.json": updateSkillSidecar("ci", "1.0.0", true),
	})
	statePath := setStatePath(t)
	origSC, _ := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
	origSK, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := engine.Update(ctx, []string{dir}, engine.UpdateOptions{DryRun: true, Runner: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Plan == nil || len(res.Plan.Skills) != 1 || res.Plan.Skills[0].Status != "advance" {
		t.Fatalf("plan = %+v", res.Plan)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Errorf("dry-run must not create the state database (err=%v)", err)
	}
	scBody, _ := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
	skBody, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if !bytes.Equal(scBody, origSC) {
		t.Error("dry-run wrote the sidecar")
	}
	if !bytes.Equal(skBody, origSK) {
		t.Error("dry-run wrote SKILL.md")
	}
}

func TestUpdateAdvanceEndToEnd(t *testing.T) {
	setupStubPath(t)
	srv := pagesServer(t, map[string]string{"intro.md": headPage})
	defer srv.Close()

	dir := writeSkillFiles(t, map[string]string{
		"SKILL.md":         fmt.Sprintf(updateSkillBase, srv.URL),
		"upkeep.data.json": updateSkillSidecar("ci", "1.0.0", true),
	})
	setStatePath(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := engine.Update(ctx, []string{dir}, engine.UpdateOptions{Runner: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	if res.SkippedLease {
		t.Fatal("unexpected skipped lease")
	}
	if res.RunOutcome != "ok" {
		t.Fatalf("run outcome = %q: %+v", res.RunOutcome, res.Skills)
	}
	if len(res.Skills) != 1 || res.Skills[0].Status != "written" {
		t.Fatalf("skills = %+v", res.Skills)
	}

	scBody, _ := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
	today := time.Now().Format("2006-01-02")
	for _, want := range []string{`"verified_for": "2.0.0"`, `"verified_at": "` + today + `"`} {
		if !strings.Contains(string(scBody), want) {
			t.Errorf("sidecar missing %s: %s", want, scBody)
		}
	}
	skBody, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if !strings.Contains(string(skBody), "| stale-pkg | 2.0.0 | 2.0.0 |") {
		t.Errorf("pins region not advanced: %s", skBody)
	}

	// Post-update check is FRESH.
	_, report := goLines(t, ctx, dir)
	if report.Verdict != "FRESH" {
		t.Errorf("post-update verdict = %q, want FRESH", report.Verdict)
	}

	// The lease is released: a second update proceeds.
	res2, err := engine.Update(ctx, []string{dir}, engine.UpdateOptions{Runner: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	if res2.SkippedLease {
		t.Error("lease not released after the first update")
	}

	// Runs recorded.
	st, err := store.Open(os.Getenv("UPKEEP_STATE_DB"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	runs, err := st.RecentRuns(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].Outcome != "ok" {
		t.Errorf("runs = %+v", runs)
	}
}

func TestUpdateLeaseSkippedWhenHeld(t *testing.T) {
	setupStubPath(t)
	srv := pagesServer(t, map[string]string{"intro.md": headPage})
	defer srv.Close()

	dir := writeSkillFiles(t, map[string]string{
		"SKILL.md":         fmt.Sprintf(updateSkillBase, srv.URL),
		"upkeep.data.json": updateSkillSidecar("ci", "1.0.0", true),
	})
	statePath := setStatePath(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	st, err := store.Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ok, err := st.AcquireLease(ctx, "update", "other-process", store.LeaseTTL)
	if err != nil || !ok {
		t.Fatalf("holder acquire: ok=%v err=%v", ok, err)
	}
	res, err := engine.Update(ctx, []string{dir}, engine.UpdateOptions{Runner: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.SkippedLease {
		t.Fatal("update must be skipped while another process holds the lease")
	}
	if err := st.ReleaseLease(ctx, "update", "other-process"); err != nil {
		t.Fatal(err)
	}
	res2, err := engine.Update(ctx, []string{dir}, engine.UpdateOptions{Runner: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	if res2.SkippedLease {
		t.Fatal("update must proceed after release")
	}
}

// The lease concurrency test at the store primitive: two holders racing on
// the same real SQLite file via separate connections. A start barrier
// releases both at once; exactly one acquires.
func TestLeaseBarrierConcurrency(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.db")
	ctx := context.Background()

	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			st, err := store.Open(statePath)
			if err != nil {
				t.Error(err)
				return
			}
			defer st.Close()
			ok, err := st.AcquireLease(ctx, "update", store.NewHolder(), store.LeaseTTL)
			if err != nil {
				t.Error(err)
				return
			}
			results <- ok
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	winners := 0
	for ok := range results {
		if ok {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want exactly 1", winners)
	}
}

// T1: two real `upkeep update` processes against one state.db. The winner
// is held inside its cycle by a stub doc server that blocks until the
// loser has exited; exactly one runs, the loser prints "skipped: lease
// held" and exits 0.
func TestUpdateBarrierProcesses(t *testing.T) {
	setupStubPath(t)

	entered := make(chan struct{})
	release := make(chan struct{})
	enteredOnce := sync.Once{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enteredOnce.Do(func() { close(entered) })
		<-release
		w.Write([]byte(headPage))
	}))
	defer srv.Close()

	dir := writeSkillFiles(t, map[string]string{
		"SKILL.md":         fmt.Sprintf(updateSkillBase, srv.URL),
		"upkeep.data.json": updateSkillSidecar("ci", "1.0.0", true),
	})
	setStatePath(t)

	bin := buildUpkeep(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var aOut bytes.Buffer
	a := exec.CommandContext(ctx, bin, "update", "--runner", "ci", dir)
	a.Env = os.Environ()
	a.Stdout = &aOut
	a.Stderr = &aOut
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}

	// Wait until the winner is inside its doc fetch (holding the lease),
	// then start the loser.
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("winner never entered the fetch")
	}

	outB, codeB := runCmd(t, bin, "update", "--runner", "ci", dir)
	if codeB != 0 || !strings.Contains(outB, "skipped: lease held") {
		t.Errorf("loser = exit %d, %q; want 0 with skipped message", codeB, outB)
	}

	close(release)
	if err := a.Wait(); err != nil {
		t.Errorf("winner exit error: %v (out: %s)", err, aOut.String())
	}
	if strings.Contains(aOut.String(), "skipped: lease held") {
		t.Errorf("winner must not print skipped: %s", aOut.String())
	}

	st, err := store.Open(os.Getenv("UPKEEP_STATE_DB"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	runs, err := st.RecentRuns(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want exactly 1 (only the winner records)", len(runs))
	}
	if runs[0].Outcome != "ok" {
		t.Errorf("winner run outcome = %q, want ok", runs[0].Outcome)
	}
}

// T3 (update): a holder replaced during a doc fetch stops the whole cycle:
// no writes for any skill, run outcome failed, error returned.
func TestTakeoverDuringPollStopsUpdate(t *testing.T) {
	setupStubPath(t)

	takeoverOnce := sync.Once{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		takeoverOnce.Do(func() {
			st, err := store.Open(os.Getenv("UPKEEP_STATE_DB"))
			if err == nil {
				_, _ = st.DB().Exec("UPDATE lease SET holder='replacement-holder' WHERE name='update'")
				st.Close()
			}
		})
		w.Write([]byte(headPage))
	}))
	defer srv.Close()

	dir1 := writeSkillFiles(t, map[string]string{
		"SKILL.md":         fmt.Sprintf(updateSkillBase, srv.URL),
		"upkeep.data.json": updateSkillSidecar("ci", "1.0.0", false),
	})
	dir2 := writeSkillFiles(t, map[string]string{
		"SKILL.md":         fmt.Sprintf(updateSkillBase, srv.URL),
		"upkeep.data.json": updateSkillSidecar("ci", "1.0.0", false),
	})
	setStatePath(t)
	sc1Before, _ := os.ReadFile(filepath.Join(dir1, "upkeep.data.json"))
	sk1Before, _ := os.ReadFile(filepath.Join(dir1, "SKILL.md"))
	sc2Before, _ := os.ReadFile(filepath.Join(dir2, "upkeep.data.json"))
	sk2Before, _ := os.ReadFile(filepath.Join(dir2, "SKILL.md"))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, err := engine.Update(ctx, []string{dir1, dir2}, engine.UpdateOptions{Runner: "ci"})
	if err == nil {
		t.Fatal("Update must report the lost lease")
	}
	if !errors.Is(err, engine.ErrLeaseLost) {
		t.Fatalf("Update err = %v, want ErrLeaseLost", err)
	}
	assertFilesUnchanged(t, dir1, sc1Before, sk1Before)
	assertFilesUnchanged(t, dir2, sc2Before, sk2Before)

	st, err := store.Open(os.Getenv("UPKEEP_STATE_DB"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	runs, err := st.RecentRuns(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Outcome != "failed" {
		t.Errorf("runs = %+v, want one failed run", runs)
	}
}

// T3 (standalone poll): same takeover leaves the poll CLI exiting non-zero
// with nothing written.
func TestTakeoverDuringPollStopsStandalonePoll(t *testing.T) {
	setupStubPath(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st, err := store.Open(os.Getenv("UPKEEP_STATE_DB"))
		if err == nil {
			_, _ = st.DB().Exec("UPDATE lease SET holder='replacement-holder' WHERE name='update'")
			st.Close()
		}
		w.Write([]byte(headPage))
	}))
	defer srv.Close()

	dir := writeSkillFiles(t, map[string]string{
		"SKILL.md":         fmt.Sprintf(updateSkillBase, srv.URL),
		"upkeep.data.json": updateSkillSidecar("ci", "1.0.0", false),
	})
	setStatePath(t)
	scBefore, _ := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
	skBefore, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))

	bin := buildUpkeep(t)
	out, code := runCmd(t, bin, "poll", "--runner", "ci", dir)
	if code == 0 {
		t.Errorf("poll CLI = exit 0, want non-zero on lease loss: %q", out)
	}
	if !strings.Contains(out, "lease lost") {
		t.Errorf("poll CLI output missing lease-lost cause: %q", out)
	}
	assertFilesUnchanged(t, dir, scBefore, skBefore)
}

// F5: a failed restore is surfaced, not discarded. On a read-only skill
// directory the write and its restore both fail; Poll must report it.
func TestRestoreFailureSurfaced(t *testing.T) {
	setupStubPath(t)
	srv := pagesServer(t, map[string]string{"intro.md": headPage})
	defer srv.Close()

	dir := writeSkillFiles(t, map[string]string{
		"SKILL.md":         fmt.Sprintf(updateSkillBase, srv.URL),
		"upkeep.data.json": updateSkillSidecar("ci", "1.0.0", false),
	})
	setStatePath(t)
	scBefore, _ := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
	skBefore, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := engine.Poll(ctx, []string{dir}, engine.PollOptions{Runner: "ci"})
	if err == nil {
		t.Fatal("Poll must fail when the write and its restore both fail")
	}
	if !strings.Contains(err.Error(), "restore failed") {
		t.Errorf("error must surface the failed restore: %v", err)
	}
	assertFilesUnchanged(t, dir, scBefore, skBefore)
}

// T5: Apply with a stale plan skips the skill with a report, writing
// nothing.
func TestApplyStalePlanSkips(t *testing.T) {
	// gatePreWrite runs the full check computation, so npm must be stubbed.
	setupStubPath(t)
	dir := writeSkillFiles(t, map[string]string{
		"SKILL.md": "---\nname: t\nmetadata:\n  upkeep-pins: \"stale-pkg=1.0.1\"\n---\n\n# T\n",
		"upkeep.data.json": `{
  "schema": 1,
  "owner": "ci",
  "pins": {
    "stale-pkg": {
      "kind": "npm",
      "verified_for": "1.0.1",
      "latest_seen": "1.0.1",
      "checked_at": "2026-10-27"
    }
  }
}
`,
	})
	setStatePath(t)
	scBefore, _ := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
	skBefore, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}

	plan := &engine.UpdatePlan{Skills: []engine.SkillPlan{{
		SkillDir: dir,
		Status:   "advance",
		Advances: []engine.Advance{{Pin: "stale-pkg", From: "1.0.0", To: "1.0.1"}},
	}}}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := engine.Apply(ctx, plan, engine.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skills) != 1 || res.Skills[0].Status != "skipped" {
		t.Fatalf("result = %+v, want skipped", res.Skills)
	}
	if !strings.Contains(res.Skills[0].Reason, "plan stale") {
		t.Errorf("reason = %q, want stale-plan cause", res.Skills[0].Reason)
	}
	assertFilesUnchanged(t, dir, scBefore, skBefore)
}

// Q4: a failed advance rolls both files back to their exact prior bytes.
// The post-write check fails on an author-owned stale Verified date in
// references/ (unrelated to the pin), so the advance is rolled back; the
// poll facts (latest_seen, checked_at) stay.
func TestApplyRollbackOnPostWriteCheckFail(t *testing.T) {
	stub := `#!/usr/bin/env bash
if [[ "$1" == "view" && "$3" == "version" ]]; then
  case "$2" in
    roll-pkg) echo "1.0.1"; exit 0;;
    *) exit 2;;
  esac
fi
exit 2
`
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	old := time.Now().AddDate(-2, 0, 0).Format("2006-01-02")
	dir := writeSkillFiles(t, map[string]string{
		"SKILL.md": "---\nname: t\nmetadata:\n  upkeep-pins: \"roll-pkg=1.0.0\"\n  upkeep-threshold-days: \"1\"\n---\n\n# T\n",
		"upkeep.data.json": `{
  "schema": 1,
  "owner": "ci",
  "pins": {
    "roll-pkg": {
      "kind": "npm",
      "verified_for": "1.0.0",
      "latest_seen": "1.0.0",
      "checked_at": "2026-10-27"
    }
  }
}
`,
		"references/a.md": "Verified " + old + " for roll-pkg\n",
	})
	setStatePath(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// The poll phase legitimately persists fetch facts (latest_seen,
	// checked_at) before the advance is attempted. Capture the exact bytes
	// after poll — that is the state the advance's rollback must restore.
	if _, err := engine.Poll(ctx, []string{dir}, engine.PollOptions{Runner: "ci"}); err != nil {
		t.Fatal(err)
	}
	scPoll, _ := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
	skPoll, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))

	res, err := engine.Update(ctx, []string{dir}, engine.UpdateOptions{Runner: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	if res.RunOutcome != "failed" {
		t.Fatalf("run outcome = %q, want failed (%+v)", res.RunOutcome, res.Skills)
	}
	if len(res.Skills) != 1 || res.Skills[0].Status != "failed" {
		t.Fatalf("skills = %+v, want one failed skill", res.Skills)
	}
	if res.Skills[0].Error == "" {
		t.Error("failed skill must carry a cause")
	}
	scAfter, _ := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
	skAfter, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if !bytes.Equal(scAfter, scPoll) {
		t.Errorf("sidecar does not match the pre-advance bytes after rollback:\n%s", scAfter)
	}
	if !bytes.Equal(skAfter, skPoll) {
		t.Errorf("SKILL.md does not match the pre-advance bytes after rollback:\n%s", skAfter)
	}
	if !strings.Contains(string(scAfter), `"verified_for": "1.0.0"`) {
		t.Errorf("advance survived rollback: %s", scAfter)
	}
	if !strings.Contains(string(scAfter), `"latest_seen": "1.0.1"`) {
		t.Errorf("poll facts lost: %s", scAfter)
	}
}

// ---------------------------------------------------------------------------
// Fix batch 2 regressions
// ---------------------------------------------------------------------------

// F1: public Apply must re-run the per-skill plan decision under its lease.
// Policy, owner, verified_for and docs-baseline changes between Plan and
// Apply all skip the skill; the unmutated control advances.
func TestApplyStalePlanRegressions(t *testing.T) {
	setupStubPath(t)

	const baseSidecar = `{
  "schema": 1,
  "owner": "ci",
  "pins": {
    "fresh-pkg": {
      "kind": "npm",
      "verified_for": "1.2.3",
      "latest_seen": "1.2.4",
      "checked_at": "2026-10-27"
    }
  }
}
`
	baseSK := "---\nname: t\nmetadata:\n  upkeep-pins: \"fresh-pkg=1.2.3\"\n---\n\n# T\n"

	cases := []struct {
		name   string
		mutate func(t *testing.T, dir string)
		want   string
	}{
		{"baseline-advance", nil, "written"},
		{"policy-changed-to-none", func(t *testing.T, dir string) {
			sk := "---\nname: t\nmetadata:\n  upkeep-pins: \"fresh-pkg=1.2.3\"\n  upkeep-auto-advance: \"none\"\n---\n\n# T\n"
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(sk), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "skipped"},
		{"owner-changed", func(t *testing.T, dir string) {
			sc := strings.Replace(baseSidecar, `"owner": "ci"`, `"owner": "local"`, 1)
			if err := os.WriteFile(filepath.Join(dir, "upkeep.data.json"), []byte(sc), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "skipped"},
		{"verified-for-changed", func(t *testing.T, dir string) {
			// Change verified_for consistently in both the sidecar and the
			// frontmatter so the skill stays drift-free; the plan decision
			// then flips to no-change (verified_for == latest_seen).
			sk := "---\nname: t\nmetadata:\n  upkeep-pins: \"fresh-pkg=1.2.4\"\n---\n\n# T\n"
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(sk), 0o644); err != nil {
				t.Fatal(err)
			}
			sc := strings.Replace(baseSidecar, `"verified_for": "1.2.3"`, `"verified_for": "1.2.4"`, 1)
			if err := os.WriteFile(filepath.Join(dir, "upkeep.data.json"), []byte(sc), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "skipped"},
		{"docs-baseline-changed", func(t *testing.T, dir string) {
			sk := "---\nname: t\nmetadata:\n  upkeep-pins: \"fresh-pkg=1.2.3\"\n  upkeep-source-based: \"https://example.invalid/docs\"\n  upkeep-source-pages: \"intro\"\n---\n\n# T\n"
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(sk), 0o644); err != nil {
				t.Fatal(err)
			}
			sc := `{
  "schema": 1,
  "owner": "ci",
  "pins": {
    "fresh-pkg": {
      "kind": "npm",
      "verified_for": "1.2.3",
      "latest_seen": "1.2.4",
      "checked_at": "2026-10-27"
    }
  },
  "docs": {
    "intro": { "verified_sha": "sha256:aaa", "latest_sha": "sha256:bbb" }
  }
}
`
			if err := os.WriteFile(filepath.Join(dir, "upkeep.data.json"), []byte(sc), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "skipped"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeSkillFiles(t, map[string]string{
				"SKILL.md":         baseSK,
				"upkeep.data.json": baseSidecar,
			})
			if tc.mutate != nil {
				tc.mutate(t, dir)
			}
			plan := &engine.UpdatePlan{Skills: []engine.SkillPlan{{
				SkillDir: dir,
				Status:   "advance",
				Advances: []engine.Advance{{Pin: "fresh-pkg", From: "1.2.3", To: "1.2.4"}},
			}}}
			scBefore, _ := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
			skBefore, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
			res, err := engine.Apply(ctx, plan, engine.ApplyOptions{Runner: "ci"})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Skills) != 1 || res.Skills[0].Status != tc.want {
				t.Fatalf("status = %+v, want %s", res.Skills, tc.want)
			}
			scAfter, _ := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
			skAfter, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
			switch tc.want {
			case "written":
				if !strings.Contains(string(scAfter), `"verified_for": "1.2.4"`) {
					t.Errorf("advance not persisted: %s", scAfter)
				}
			default:
				if !bytes.Equal(scAfter, scBefore) || !bytes.Equal(skAfter, skBefore) {
					t.Errorf("files changed on a skipped stale plan (sidecar equal=%v, skill equal=%v)", bytes.Equal(scAfter, scBefore), bytes.Equal(skAfter, skBefore))
				}
				if !strings.Contains(res.Skills[0].Reason, "plan stale") {
					t.Errorf("reason = %q, want stale-plan cause", res.Skills[0].Reason)
				}
			}
		})
	}
}

// F2: a skipped-owner skill gets zero mutations of any kind — sidecar,
// files, or DB snapshots — even when the lease holder is replaced during
// its doc fetch.
func TestTakeoverDuringPollSkippedOwnerWritesNoSnapshots(t *testing.T) {
	setupStubPath(t)

	takeoverOnce := sync.Once{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		takeoverOnce.Do(func() {
			st, err := store.Open(os.Getenv("UPKEEP_STATE_DB"))
			if err == nil {
				_, _ = st.DB().Exec("UPDATE lease SET holder='replacement-holder' WHERE name='update'")
				st.Close()
			}
		})
		w.Write([]byte(headPage))
	}))
	defer srv.Close()

	dir := writeSkillFiles(t, map[string]string{
		"SKILL.md":         fmt.Sprintf(updateSkillBase, srv.URL),
		"upkeep.data.json": updateSkillSidecar("ci", "1.0.0", false),
	})
	statePath := setStatePath(t)
	scBefore, _ := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
	skBefore, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	drift, err := engine.Poll(ctx, []string{dir}, engine.PollOptions{Runner: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if drift[0].SkipReason == "" {
		t.Fatalf("expected owner-mismatch skip, got %+v", drift[0])
	}
	assertFilesUnchanged(t, dir, scBefore, skBefore)

	st, err := store.Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var n int
	if err := st.DB().QueryRow("SELECT count(*) FROM snapshots WHERE skill=?", filepath.Base(dir)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("snapshots = %d, want 0 for a skipped-owner skill", n)
	}
}

// F3: cancellation mid-poll still finalizes the run (outcome=failed,
// finished_at set) and releases the lease.
func TestCancelDuringPollFinalizesRunAndReleasesLease(t *testing.T) {
	setupStubPath(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancelOnce := sync.Once{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancelOnce.Do(func() { cancel() })
		w.Write([]byte(headPage))
	}))
	defer srv.Close()

	dir := writeSkillFiles(t, map[string]string{
		"SKILL.md":         fmt.Sprintf(updateSkillBase, srv.URL),
		"upkeep.data.json": updateSkillSidecar("ci", "1.0.0", true),
	})
	statePath := setStatePath(t)

	_, err := engine.Update(ctx, []string{dir}, engine.UpdateOptions{Runner: "ci"})
	if err == nil {
		t.Fatal("Update must fail when canceled mid-poll")
	}

	bg := context.Background()
	st, err := store.Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	runs, err := st.RecentRuns(bg, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	if runs[0].Outcome != "failed" || runs[0].FinishedAt == "" {
		t.Errorf("run = %+v, want failed with finished_at", runs[0])
	}
	var leaseCount int
	if err := st.DB().QueryRow("SELECT count(*) FROM lease WHERE name='update'").Scan(&leaseCount); err != nil {
		t.Fatal(err)
	}
	if leaseCount != 0 {
		t.Errorf("lease rows = %d, want 0 after cancellation", leaseCount)
	}
}

// C2: a declared upkeep-source-pages list is authoritative. Keys not
// listed are pruned and reported; an empty declaration retires all pages;
// an absent key falls back to the sidecar docs keys.
func TestPollRetiresUndeclaredDocsKeys(t *testing.T) {
	setupStubPath(t)
	srv := pagesServer(t, map[string]string{"intro.md": headPage, "legacy.md": headPage, "only.md": headPage})
	defer srv.Close()

	sidecarWithLegacy := `{
  "schema": 1,
  "owner": "ci",
  "pins": {
    "stale-pkg": {
      "kind": "npm",
      "verified_for": "1.0.0",
      "latest_seen": "1.0.0",
      "checked_at": "2026-10-27"
    }
  },
  "docs": {
    "intro": { "verified_sha": "` + headPageSHA + `", "latest_sha": "` + headPageSHA + `" },
    "legacy": { "verified_sha": "` + headPageSHA + `", "latest_sha": "` + headPageSHA + `" }
  }
}
`

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("declared-subset-prunes-and-reports", func(t *testing.T) {
		dir := writeSkillFiles(t, map[string]string{
			"SKILL.md":         "---\nname: t\nmetadata:\n  upkeep-pins: \"stale-pkg=1.0.0\"\n  upkeep-source-base: \"" + srv.URL + "/docs\"\n  upkeep-source-ext: \".md\"\n  upkeep-source-pages: \"intro\"\n---\n\n# T\n",
			"upkeep.data.json": sidecarWithLegacy,
		})
		drift, err := engine.Poll(ctx, []string{dir}, engine.PollOptions{Runner: "ci"})
		if err != nil {
			t.Fatal(err)
		}
		if len(drift[0].Pruned) != 1 || drift[0].Pruned[0] != "legacy" {
			t.Errorf("Pruned = %v, want [legacy]", drift[0].Pruned)
		}
		scBody, _ := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
		if strings.Contains(string(scBody), `"legacy"`) {
			t.Errorf("legacy docs key not pruned: %s", scBody)
		}
		if !strings.Contains(string(scBody), `"intro"`) {
			t.Errorf("intro docs key lost: %s", scBody)
		}
	})

	t.Run("declared-empty-retires-all", func(t *testing.T) {
		dir := writeSkillFiles(t, map[string]string{
			"SKILL.md":         "---\nname: t\nmetadata:\n  upkeep-pins: \"stale-pkg=1.0.0\"\n  upkeep-source-base: \"" + srv.URL + "/docs\"\n  upkeep-source-ext: \".md\"\n  upkeep-source-pages: \"\"\n---\n\n# T\n",
			"upkeep.data.json": sidecarWithLegacy,
		})
		drift, err := engine.Poll(ctx, []string{dir}, engine.PollOptions{Runner: "ci"})
		if err != nil {
			t.Fatal(err)
		}
		if len(drift[0].Pruned) != 2 {
			t.Errorf("Pruned = %v, want both pages retired", drift[0].Pruned)
		}
		scBody, _ := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
		if strings.Contains(string(scBody), `"docs"`) {
			t.Errorf("docs map not emptied: %s", scBody)
		}
	})

	t.Run("absent-falls-back-to-sidecar-keys", func(t *testing.T) {
		dir := writeSkillFiles(t, map[string]string{
			"SKILL.md": "---\nname: t\nmetadata:\n  upkeep-pins: \"stale-pkg=1.0.0\"\n  upkeep-source-base: \"" + srv.URL + "/docs\"\n  upkeep-source-ext: \".md\"\n---\n\n# T\n",
			"upkeep.data.json": `{
  "schema": 1,
  "owner": "ci",
  "pins": {
    "stale-pkg": {
      "kind": "npm",
      "verified_for": "1.0.0",
      "latest_seen": "1.0.0",
      "checked_at": "2026-10-27"
    }
  },
  "docs": {
    "only": { "verified_sha": "` + headPageSHA + `", "latest_sha": "` + headPageSHA + `" }
  }
}
`,
		})
		drift, err := engine.Poll(ctx, []string{dir}, engine.PollOptions{Runner: "ci"})
		if err != nil {
			t.Fatal(err)
		}
		if len(drift[0].Pruned) != 0 {
			t.Errorf("Pruned = %v, want none when the key is absent", drift[0].Pruned)
		}
		if len(drift[0].Docs) != 1 || drift[0].Docs[0].Page != "only" {
			t.Errorf("docs drift = %+v, want only tracked", drift[0].Docs)
		}
	})
}
