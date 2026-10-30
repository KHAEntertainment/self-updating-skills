package check

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KHAEntertainment/self-updating-skills/internal/render"
	"github.com/KHAEntertainment/self-updating-skills/internal/skill"
)

const npmStub = `#!/usr/bin/env bash
if [[ "$1" == "view" && "$3" == "version" ]]; then
  case "$2" in
    fresh-pkg) echo "1.2.3"; exit 0;;
    *) exit 2;;
  esac
fi
exit 2
`

// setupStubPath writes a fake npm executable so the suite never touches
// the real registry.
func setupStubPath(t *testing.T) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte(npmStub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// writeSkill writes a minimal skill with a frontmatter pin and a sidecar.
func writeSkill(t *testing.T, sidecar string) (string, skill.Config) {
	t.Helper()
	dir := t.TempDir()
	doc := "---\nname: t\nmetadata:\n  upkeep-pins: \"fresh-pkg=1.2.3\"\n---\n\n# T\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if sidecar != "" {
		if err := os.WriteFile(filepath.Join(dir, "upkeep.data.json"), []byte(sidecar), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir, skill.ParseFrontmatter(filepath.Join(dir, "SKILL.md"))
}

const freshSidecar = `{
  "schema": 1,
  "owner": "local"
}
`

func TestComputeDocsLag(t *testing.T) {
	setupStubPath(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dir, cfg := writeSkill(t, `{
  "schema": 1,
  "pins": {
    "fresh-pkg": {
      "kind": "npm",
      "verified_for": "1.2.3",
      "latest_seen": "1.2.3",
      "checked_at": "2026-10-27"
    }
  },
  "docs": {
    "skills": { "verified_sha": "sha256:aaa", "latest_sha": "sha256:bbb" }
  }
}
`)
	sc, err := render.LoadSidecar(dir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Compute(ctx, cfg, nil, dir, sc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != "CONTENT-LAG" {
		t.Errorf("verdict = %q, want CONTENT-LAG", r.Verdict)
	}
	found := false
	for _, it := range r.Items {
		if it.Kind == "CONTENT-LAG" && strings.HasPrefix(it.Line, "CONTENT-LAG page:skills") {
			found = true
		}
	}
	if !found {
		t.Errorf("no page CONTENT-LAG item: %+v", r.Items)
	}
	if !strings.Contains(r.Summary, "pages=0/1 verified") {
		t.Errorf("summary = %q, want pages segment", r.Summary)
	}
}

func TestComputeDocsUnchangedFresh(t *testing.T) {
	setupStubPath(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dir, cfg := writeSkill(t, `{
  "schema": 1,
  "pins": {
    "fresh-pkg": {
      "kind": "npm",
      "verified_for": "1.2.3",
      "latest_seen": "1.2.3",
      "checked_at": "2026-10-27"
    }
  },
  "docs": {
    "skills": { "verified_sha": "sha256:aaa", "latest_sha": "sha256:aaa" }
  }
}
`)
	sc, _ := render.LoadSidecar(dir)
	r, err := Compute(ctx, cfg, nil, dir, sc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != "FRESH" {
		t.Errorf("verdict = %q, want FRESH", r.Verdict)
	}
	if !strings.Contains(r.Summary, "pages=1/1 verified") {
		t.Errorf("summary = %q, want pages=1/1", r.Summary)
	}
}

func TestComputeDocsNoSignal(t *testing.T) {
	setupStubPath(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// No latest_sha yet: no signal, no lag.
	dir, cfg := writeSkill(t, `{
  "schema": 1,
  "pins": {
    "fresh-pkg": {
      "kind": "npm",
      "verified_for": "1.2.3",
      "latest_seen": "1.2.3",
      "checked_at": "2026-10-27"
    }
  },
  "docs": {
    "skills": { "verified_sha": "sha256:aaa" }
  }
}
`)
	sc, _ := render.LoadSidecar(dir)
	r, err := Compute(ctx, cfg, nil, dir, sc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != "FRESH" {
		t.Errorf("verdict = %q, want FRESH (missing evidence is not lag)", r.Verdict)
	}
}

func TestComputeDocsLatestWithoutBaseline(t *testing.T) {
	setupStubPath(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A latest snapshot with no verified_sha baseline reads CONTENT-LAG,
	// never FRESH: content exists that has never been verified.
	dir, cfg := writeSkill(t, `{
  "schema": 1,
  "pins": {
    "fresh-pkg": {
      "kind": "npm",
      "verified_for": "1.2.3",
      "latest_seen": "1.2.3",
      "checked_at": "2026-10-27"
    }
  },
  "docs": {
    "skills": { "latest_sha": "sha256:bbb" }
  }
}
`)
	sc, _ := render.LoadSidecar(dir)
	r, err := Compute(ctx, cfg, nil, dir, sc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != "CONTENT-LAG" {
		t.Errorf("verdict = %q, want CONTENT-LAG", r.Verdict)
	}
}

func TestComputeNoSidecarPhase1(t *testing.T) {
	setupStubPath(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dir, cfg := writeSkill(t, "")
	r, err := Compute(ctx, cfg, nil, dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != "FRESH" {
		t.Errorf("verdict = %q, want FRESH (Phase 1 behaviour)", r.Verdict)
	}
}
