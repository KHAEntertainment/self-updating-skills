package engine_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/KHAEntertainment/self-updating-skills/engine"
)

// TestCheckReadErrorUnreadableReference verifies that a read failure on an
// existing reference file returns a structured engine.Error (not a report).
func TestCheckReadErrorUnreadableReference(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod-based unreadable files do not fail when running as root")
	}
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "SKILL.md"), "---\nname: t\n---\n# body\n")
	refDir := filepath.Join(dir, "references")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ref := filepath.Join(refDir, "a.md")
	mustWrite(t, ref, "Verified: 2020-01-01\n")
	if err := os.Chmod(ref, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(ref, 0o644)

	report, err := engine.Check(context.Background(), dir)
	if err == nil {
		t.Fatalf("expected error for unreadable reference, got report %+v", report)
	}
	var ee *engine.Error
	if !errors.As(err, &ee) {
		t.Fatalf("error is not an engine.Error: %T %v", err, err)
	}
	if ee.Op != "read" {
		t.Errorf("Op = %q, want read", ee.Op)
	}
	resolvedRef, err := filepath.EvalSymlinks(ref)
	if err != nil {
		t.Fatal(err)
	}
	if ee.Path != resolvedRef {
		t.Errorf("Path = %q, want %q", ee.Path, resolvedRef)
	}
}

// TestCheckCancelledContext verifies that a cancelled ctx yields ctx.Err()
// (wrapped) instead of a report.
func TestCheckCancelledContext(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "SKILL.md"), "---\nname: t\n---\n# body\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, err := engine.Check(ctx, dir)
	if err == nil {
		t.Fatalf("expected error for cancelled ctx, got report %+v", report)
	}
	if report != nil {
		t.Errorf("report = %+v, want nil", report)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled (errors.Is)", err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
