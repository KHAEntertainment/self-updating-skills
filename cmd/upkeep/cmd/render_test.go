package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KHAEntertainment/self-updating-skills/engine"
)

// renderFixture creates a minimal skill with a sidecar and a scrambled pins
// region, so render has real work to do.
func renderFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	doc := "---\nname: t\nmetadata:\n  upkeep-pins: \"stale-pkg=1.0.0\"\n---\n\n# T\n\n<!-- generated:pins -->\njunk\n<!-- /generated:pins -->\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	sidecar := "{\n  \"schema\": 1,\n  \"pins\": {\n    \"stale-pkg\": {\n      \"verified_for\": \"1.0.0\"\n    }\n  }\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "upkeep.data.json"), []byte(sidecar), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runRender(t *testing.T, args ...string) string {
	t.Helper()
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(append([]string{"render"}, args...))
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("exec render %v: %v\n%s", args, err, buf.String())
	}
	return buf.String()
}

func TestRenderCommandEndToEnd(t *testing.T) {
	dir := renderFixture(t)
	out := runRender(t, dir)
	if !strings.Contains(out, "VERDICT: RENDERED") {
		t.Errorf("render output = %q, want RENDERED", out)
	}
	sk, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sk), "| stale-pkg | 1.0.0 |  |  |") {
		t.Errorf("pins region not rendered:\n%s", sk)
	}
	// Second run: unchanged, exit 0 (no os.Exit path triggered).
	out = runRender(t, dir)
	if !strings.Contains(out, "VERDICT: UNCHANGED") {
		t.Errorf("second render output = %q, want UNCHANGED", out)
	}
}

func TestRenderCommandJSON(t *testing.T) {
	dir := renderFixture(t)
	out := runRender(t, "--json", dir)
	if !strings.Contains(out, `"changed": true`) {
		t.Errorf("json output = %q, want changed true", out)
	}
}

func TestRenderVerdictRules(t *testing.T) {
	cases := []struct {
		changed, check bool
		wantLine       string
		wantExit       int
	}{
		{true, true, "DRIFT", 1},
		{false, true, "UNCHANGED", 0},
		{true, false, "RENDERED", 0},
		{false, false, "UNCHANGED", 0},
	}
	for _, tc := range cases {
		line, exit := renderVerdict(&engine.RenderResult{Changed: tc.changed}, tc.check)
		if exit != tc.wantExit || !strings.Contains(line, tc.wantLine) {
			t.Errorf("renderVerdict(%v, %v) = (%q, %d), want line containing %q and exit %d",
				tc.changed, tc.check, line, exit, tc.wantLine, tc.wantExit)
		}
	}
}
