package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestThresholdFlagDecimal verifies the --threshold flag parses base 10 like
// Python's int(): "010" is 10 and "08" is 8; invalid values error.
func TestThresholdFlagDecimal(t *testing.T) {
	v := checkCmd.Flags().Lookup("threshold").Value
	if err := v.Set("010"); err != nil {
		t.Fatalf("Set(010): %v", err)
	}
	if got := v.String(); got != "10" {
		t.Errorf("010 -> %s, want 10", got)
	}
	if err := v.Set("08"); err != nil {
		t.Fatalf("Set(08): %v", err)
	}
	if got := v.String(); got != "8" {
		t.Errorf("08 -> %s, want 8", got)
	}
	if err := v.Set("abc"); err == nil {
		t.Error("Set(abc) should fail")
	}
}

// TestCheckThresholdDecimalEndToEnd runs the check command through cobra
// with a date nine days old: threshold 010 (10) must be FRESH, threshold 8
// must be STALE, and an invalid value must error.
func TestCheckThresholdDecimalEndToEnd(t *testing.T) {
	dir := t.TempDir()
	d := time.Now().AddDate(0, 0, -9)
	sk := "---\nname: t\nmetadata:\n  upkeep-threshold-days: \"60\"\n---\n# t\n\nVerified: " + d.Format("2006-01-02") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(sk), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(threshold string) (string, error) {
		var buf bytes.Buffer
		rootCmd.SetOut(&buf)
		rootCmd.SetErr(&buf)
		rootCmd.SetArgs([]string{"check", "--threshold", threshold, dir})
		err := rootCmd.ExecuteContext(context.Background())
		return buf.String(), err
	}

	out, err := run("010")
	if err != nil {
		t.Fatalf("exec 010: %v\n%s", err, out)
	}
	if !strings.Contains(out, "VERDICT: FRESH") {
		t.Errorf("010 output = %q, want FRESH (age 9 <= 10)", out)
	}

	out, err = run("8")
	if err != nil {
		t.Fatalf("exec 8: %v\n%s", err, out)
	}
	if !strings.Contains(out, "VERDICT: STALE") {
		t.Errorf("8 output = %q, want STALE (age 9 > 8)", out)
	}

	if _, err = run("abc"); err == nil {
		t.Error("exec abc should error")
	}
}
