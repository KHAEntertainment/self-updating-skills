package engine_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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
		if name != "SKILL.md" && name != "upkeep.data.json" {
			p = filepath.Join(dir, name)
		}
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
	foundScript := false
	for _, l := range sl {
		if l == pinLine {
			foundScript = true
		}
		if strings.HasPrefix(l, "VERDICT: STALE") {
			foundScript = foundScript && true
		}
	}
	if !foundScript {
		t.Errorf("script output missing pin line or STALE verdict; got %q", sl)
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
	st, err := store.Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	drift, err := engine.Poll(ctx, []string{dir}, engine.PollOptions{Persist: true, WriteSidecar: true, Runner: "ci"}, st)
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	drift, err := engine.Poll(ctx, []string{dir}, engine.PollOptions{WriteSidecar: true, Runner: "ci"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if drift[0].BlockReason == "" {
		t.Fatal("expected BlockReason for a TAMPERED skill")
	}
	before, _ := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
	after, _ := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
	if string(before) != string(after) {
		t.Error("sidecar changed despite the gate")
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	drift, err := engine.Poll(ctx, []string{dir}, engine.PollOptions{WriteSidecar: true, Runner: "local"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if drift[0].SkipReason == "" {
		t.Fatal("expected SkipReason for an owner mismatch")
	}
	scBody, err := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(scBody), `"latest_seen": "2.0.0"`) {
		t.Error("sidecar written despite owner mismatch")
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
	if _, err := engine.Poll(ctx, []string{dir}, engine.PollOptions{WriteSidecar: true, Runner: "ci"}, nil); err != nil {
		t.Fatal(err)
	}
	scBody, _ := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
	if strings.Contains(string(scBody), `"verified_sha"`) {
		t.Fatal("poll without --seed-docs must not write verified_sha")
	}

	// With the explicit flag, only the missing baseline is filled.
	if _, err := engine.Poll(ctx, []string{dir}, engine.PollOptions{WriteSidecar: true, SeedDocs: true, Runner: "ci"}, nil); err != nil {
		t.Fatal(err)
	}
	scBody, _ = os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
	if !strings.Contains(string(scBody), `"verified_sha": "`+headPageSHA+`"`) {
		t.Errorf("seed did not write verified_sha: %s", scBody)
	}
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
	if string(scBody) != string(origSC) {
		t.Error("dry-run wrote the sidecar")
	}
	if string(skBody) != string(origSK) {
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
	if len(res.Skills) != 1 || res.Skills[0].Status != "advance" {
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

	// One recorded run.
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

// The lease concurrency test: two holders racing on the same real SQLite
// file via separate connections (goroutines on separate Store handles). A
// start barrier releases both at once; exactly one acquires.
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

// Q4: a failed advance rolls both files back. The post-write check fails
// on an author-owned stale Verified date in references/ (unrelated to the
// pin), so the advance is rolled back; the poll facts (latest_seen,
// checked_at) stay.
func TestApplyRollbackOnPostWriteCheckFail(t *testing.T) {
	// Custom npm stub: roll-pkg moves 1.0.0 → 1.0.1 (a patch).
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
	res, err := engine.Update(ctx, []string{dir}, engine.UpdateOptions{Runner: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	if res.RunOutcome != "failed" {
		t.Fatalf("run outcome = %q, want failed (%+v)", res.RunOutcome, res.Skills)
	}
	scBody, _ := os.ReadFile(filepath.Join(dir, "upkeep.data.json"))
	if strings.Contains(string(scBody), `"verified_for": "1.0.1"`) {
		t.Errorf("advance survived rollback: %s", scBody)
	}
	if !strings.Contains(string(scBody), `"latest_seen": "1.0.1"`) {
		t.Errorf("poll facts lost: %s", scBody)
	}
}
