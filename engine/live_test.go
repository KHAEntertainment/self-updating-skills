package engine_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestLiveRepoSkills compares the Go engine and the script on the real repo
// skills. It performs live npm/GitHub lookups, so it is not part of the
// default suite: run with UPKEEP_LIVE=1. Output is printed side-by-side for
// review and for the PR body.
func TestLiveRepoSkills(t *testing.T) {
	if os.Getenv("UPKEEP_LIVE") != "1" {
		t.Skip("set UPKEEP_LIVE=1 to run the live repo-skill comparison")
	}
	root := repoRoot(t)
	skillDirs, err := filepath.Glob(filepath.Join(root, "skills", "*"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(skillDirs)

	ran := false
	for _, sd := range skillDirs {
		if _, err := os.Stat(filepath.Join(sd, "SKILL.md")); err != nil {
			continue
		}
		ran = true
		name := filepath.Base(sd)
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			gl, _ := goLines(t, ctx, sd)
			sl, sc := shLines(t, root, sd, false)
			fmt.Printf("\n=== %s (script exit %d) ===\n", name, sc)
			fmt.Printf("GO:\n%s\nSCRIPT:\n%s\n", strings.Join(gl, "\n"), strings.Join(sl, "\n"))
			// Same comparator as the fixture conformance matrix (P1 mapping).
			if !equalLines(mapGoLines(gl), sl) {
				t.Errorf("mismatch for skill %s:\nGO=%q\nSH=%q", name, gl, sl)
			}
		})
	}
	if !ran {
		t.Fatal("no skills found under " + filepath.Join(root, "skills"))
	}
}
