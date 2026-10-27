package engine_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KHAEntertainment/self-updating-skills/engine"
	"github.com/KHAEntertainment/self-updating-skills/internal/render"
)

// R5 regression: a GitHub tag pin whose recorded latest_seen is ahead of
// both verified_for and the live tag must report CONTENT-LAG, never FRESH.
// The git stub returns only tag v1.0.0 for this pin's repository.
const lagGitStub = `#!/usr/bin/env bash
if [[ "$1" != "ls-remote" ]]; then exit 2; fi
case "$*" in
  *gh-lag*)
    echo "1111111111111111111111111111111111111111 refs/tags/v1.0.0"
    exit 0;;
  *) exit 2;;
esac
`

func TestCheckGitHubTagRecordedLag(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(lagGitStub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	sk := filepath.Join(dir, "SKILL.md")
	doc := "---\nname: t\nmetadata:\n  upkeep-pins: \"gh-lag=github:owner/gh-lag@1.0.0\"\n---\n\n# T\n"
	if err := os.WriteFile(sk, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	sidecar := `{
  "schema": 1,
  "pins": {
    "gh-lag": {
      "kind": "github",
      "repo": "owner/gh-lag",
      "verified_for": "1.0.0",
      "latest_seen": "2.0.0"
    }
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "upkeep.data.json"), []byte(sidecar), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := engine.Check(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict == "FRESH" {
		t.Fatalf("verdict = FRESH, want CONTENT-LAG (recorded lag must never read as FRESH)")
	}
	found := false
	for _, it := range r.Items {
		if it.Kind == "CONTENT-LAG" && strings.Contains(it.Line, "pin:gh-lag") {
			found = true
		}
	}
	if !found {
		t.Errorf("no CONTENT-LAG item for the GitHub tag pin; items: %+v", r.Items)
	}
}

// H1 regression: an unresolvable live lookup reports UNKNOWN even when the
// sidecar records a newer version — never a latest_seen substitution, so
// the Go output matches the script exactly.
func TestCheckUnresolvedPinWithSidecar(t *testing.T) {
	setupStubPath(t) // npm stub: unres-pkg exits 7
	dir := t.TempDir()
	sk := filepath.Join(dir, "SKILL.md")
	doc := "---\nname: t\nmetadata:\n  upkeep-pins: \"unres-pkg=1.0.0\"\n---\n\n# T\n"
	if err := os.WriteFile(sk, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	sidecar := `{
  "schema": 1,
  "pins": {
    "unres-pkg": {
      "kind": "npm",
      "verified_for": "1.0.0",
      "latest_seen": "2.0.0"
    }
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "upkeep.data.json"), []byte(sidecar), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := engine.Check(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != "FRESH" {
		t.Errorf("verdict = %q, want FRESH (the script's verdict for an unresolved pin)", r.Verdict)
	}
	for _, it := range r.Items {
		if it.Kind != "UNKNOWN" || !strings.Contains(it.Line, "unres-pkg") {
			t.Errorf("unexpected item for the unresolved pin: %+v", it)
		}
	}
}

// L1 regression: with a sidecar, a malformed generated marker is a DRIFT
// item with a precise message and an overall STALE verdict — check still
// prints a VERDICT line and never errors out.
func TestCheckMalformedMarkerIsDriftVerdict(t *testing.T) {
	setupStubPath(t)
	dir := t.TempDir()
	sk := filepath.Join(dir, "SKILL.md")
	doc := "---\nname: t\nmetadata:\n  upkeep-pins: \"fresh-pkg=1.2.3\"\n---\n\n# T\n\n<!-- generated:pins -->\nnever closed\n"
	if err := os.WriteFile(sk, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	sidecar := `{
  "schema": 1,
  "pins": {
    "fresh-pkg": {
      "kind": "npm",
      "verified_for": "1.2.3",
      "latest_seen": "1.2.3"
    }
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "upkeep.data.json"), []byte(sidecar), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := engine.Check(ctx, dir)
	if err != nil {
		t.Fatalf("malformed markers must be a DRIFT item, not an error: %v", err)
	}
	if r.Verdict != "STALE" {
		t.Errorf("verdict = %q, want STALE", r.Verdict)
	}
	found := false
	for _, it := range r.Items {
		if it.Kind == "DRIFT" && strings.Contains(it.Line, "pins") {
			found = true
			fmt.Println(it.Line)
		}
	}
	if !found {
		t.Errorf("no DRIFT item naming the malformed region; items: %+v", r.Items)
	}
	// And a VERDICT line is always rendered.
	if !strings.Contains(engine.RenderText(r), "VERDICT: STALE") {
		t.Errorf("RenderText missing VERDICT line: %q", engine.RenderText(r))
	}
}

// CR2 regression: render, stamp and check refuse a malformed sidecar with
// a precise error, leaving both files unchanged; the committed retrofit
// sidecar still loads.
func TestMalformedSidecarRefusesMutations(t *testing.T) {
	root := repoRoot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	badSidecars := []struct {
		name, body string
	}{
		{"unknown-top-level", "{\n  \"schema\": 1,\n  \"future_key\": \"x\"\n}\n"},
		{"unknown-pin-key", "{\n  \"schema\": 1,\n  \"pins\": {\n    \"fresh-pkg\": {\n      \"verified_for\": \"1.2.3\",\n      \"extra\": true\n    }\n  }\n}\n"},
		{"schema-2", "{\n  \"schema\": 2\n}\n"},
	}
	for _, bc := range badSidecars {
		t.Run(bc.name, func(t *testing.T) {
			// A minimal in-repo-shaped skill, no stub lookups needed for
			// render/stamp; check only needs the error before compute.
			dir := t.TempDir()
			doc := "---\nname: t\nmetadata:\n  upkeep-pins: \"fresh-pkg=1.2.3\"\n---\n\n# T\n\n<!-- frozen:pol -->\nf\n<!-- /frozen:pol -->\n"
			skPath := filepath.Join(dir, "SKILL.md")
			scPath := filepath.Join(dir, "upkeep.data.json")
			if err := os.WriteFile(skPath, []byte(doc), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(scPath, []byte(bc.body), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := engine.Render(ctx, dir, false); err == nil {
				t.Error("render must fail on a malformed sidecar")
			}
			if _, err := engine.Stamp(ctx, dir); err == nil {
				t.Error("stamp must fail on a malformed sidecar")
			}
			if _, err := engine.Check(ctx, dir); err == nil {
				t.Error("check must fail on a malformed sidecar")
			}
			got, _ := os.ReadFile(skPath)
			if string(got) != doc {
				t.Error("SKILL.md changed")
			}
			gotSc, _ := os.ReadFile(scPath)
			if string(gotSc) != bc.body {
				t.Error("sidecar changed")
			}
		})
	}

	// The committed retrofit sidecar still loads.
	if _, err := render.LoadSidecar(filepath.Join(root, "skills", "claude-code-authoring")); err != nil {
		t.Errorf("committed retrofit sidecar must load: %v", err)
	}
}
