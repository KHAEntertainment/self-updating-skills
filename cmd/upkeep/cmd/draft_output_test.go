package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/KHAEntertainment/self-updating-skills/engine"
	"github.com/KHAEntertainment/self-updating-skills/internal/redirect"
	"github.com/KHAEntertainment/self-updating-skills/internal/store"
)

// Row fields and reasons can carry model or upstream text. These tests pin
// that update and status print them on one line with control characters
// escaped, so a draft cannot move the cursor or forge output lines.

const hostile = "evil\x1b[2J\nFORGED LINE\rx"

func assertSafe(t *testing.T, out string, wantLines int) {
	t.Helper()
	if strings.ContainsAny(out, "\x1b\r") {
		t.Fatalf("raw control character in output: %q", out)
	}
	if strings.Contains(out, "\nFORGED LINE") {
		t.Fatalf("forged line in output: %q", out)
	}
	if n := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1; n != wantLines {
		t.Fatalf("got %d lines, want %d: %q", n, wantLines, out)
	}
}

func TestSafeLine(t *testing.T) {
	if got := safeLine("plain   text\nwrapped"); got != "plain text wrapped" {
		t.Fatalf("whitespace: %q", got)
	}
	got := safeLine(hostile)
	if strings.ContainsAny(got, "\x1b\r\n") || !strings.Contains(got, `\x1b`) {
		t.Fatalf("hostile: %q", got)
	}
}

func TestRenderUpdateEscapesDraftedRows(t *testing.T) {
	res := &engine.UpdateResult{Skills: []engine.SkillResult{
		{SkillDir: "/s/a", Status: "pr", Reason: "pr: " + hostile, Outcome: redirect.OutcomePR,
			Rows: []redirect.RowResult{{
				Row:     redirect.Row{ID: "id\x1b", Old: hostile, New: "new\nline"},
				Checks:  []redirect.Check{{Name: "Q", Ran: true, Pass: true}, {Name: "S", Detail: "not run"}},
				Outcome: redirect.OutcomePR, Reason: hostile,
			}}},
		{SkillDir: "/s/b", Status: "fallback", Reason: "fallback: " + hostile},
		{SkillDir: "/s/c", Status: "failed", Error: "hard stop: " + hostile, Outcome: redirect.OutcomeHardStop},
		{SkillDir: "/s/d", Status: "failed", Error: "drafting: model call: " + hostile},
	}}
	var buf bytes.Buffer
	c := &cobra.Command{}
	c.SetOut(&buf)
	renderUpdate(c, res)
	out := buf.String()
	// PR line + row line, FALLBACK line, FAILED line, FAILED line.
	assertSafe(t, out, 5)
	for _, want := range []string{"PR /s/a", "FALLBACK /s/b", "FAILED /s/c", "FAILED /s/d", "[Q pass, S not run]"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

func TestStatusEscapesPendingRows(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.db")
	t.Setenv("UPKEEP_STATE_DB", state)
	ctx := context.Background()
	st, err := store.Open(state)
	if err != nil {
		t.Fatal(err)
	}
	runID, err := st.StartRun(ctx, "ci")
	if err != nil {
		t.Fatal(err)
	}
	row, _ := json.Marshal(map[string]string{"id": "r1", "old": hostile, "new": "n", "reason": "quote check failed " + hostile})
	if err := st.SaveRowVerdict(ctx, runID, "skill-a", string(row), "[]", "fallback", "q", "sha256:x"); err != nil {
		t.Fatal(err)
	}
	skillRow, _ := json.Marshal(map[string]string{"reason": "pr: page hooks changed with no accepted row " + hostile})
	if err := st.SaveRowVerdict(ctx, runID, "skill-b", string(skillRow), "[]", "pr", "", ""); err != nil {
		t.Fatal(err)
	}
	rep, _ := json.Marshal(map[string]any{"results": []engine.SkillResult{{SkillDir: "/s/c", Status: "failed", Error: "drafting unavailable: " + hostile}}})
	if err := st.FinishRun(ctx, runID, "failed", string(rep)); err != nil {
		t.Fatal(err)
	}
	st.Close()

	var buf bytes.Buffer
	statusJSON = false
	statusCmd.SetOut(&buf)
	statusCmd.SetContext(context.Background())
	t.Cleanup(func() { statusCmd.SetOut(nil) })
	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// run line, FAILED line, FALLBACK row line, PR skill-level line.
	assertSafe(t, out, 4)
	for _, want := range []string{"FAILED /s/c", `FALLBACK skill-a (run 1) row "r1"`, `PR skill-b (run 1) — "pr: page hooks changed with no accepted row`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}
