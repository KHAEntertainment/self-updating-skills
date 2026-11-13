package engine_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestScanScriptOffline runs skills/self-updating-skills/scripts/scan.sh
// against a fixture HOME with --offline (no network, no upkeep.sh check) and
// checks the find and triage rules: symlinks resolve to one skill folder, a
// name with two different folders is a duplicate, the install record and
// --owner decide the origin, and a path given on the command line is walked
// in depth. The fixture must be byte-identical afterwards: the scan never
// writes.
func TestScanScriptOffline(t *testing.T) {
	root := repoRoot(t)
	home := t.TempDir()
	extra := filepath.Join(t.TempDir(), "my repo")

	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	skill := func(name, body string) string {
		return "---\nname: " + name + "\ndescription: fixture\n" + body
	}
	agents := filepath.Join(home, ".agents", "skills")
	claude := filepath.Join(home, ".claude", "skills")
	write(filepath.Join(agents, "alpha", "SKILL.md"), skill("alpha", "---\n\n# Alpha\n"))
	write(filepath.Join(agents, "mine", "SKILL.md"), skill("mine", "---\n\n# Mine\n"))
	write(filepath.Join(agents, "pinned", "SKILL.md"), skill("pinned", "metadata:\n  upkeep-pins: \"left-pad=1.0.0\"\n---\n\n# Pinned\n"))
	write(filepath.Join(claude, "mine", "SKILL.md"), skill("mine", "---\n\n# Mine, edited by hand\n"))
	write(filepath.Join(claude, "notes.txt"), "not a skill\n")
	write(filepath.Join(home, ".codex", "skills", "sig", "SKILL.md"),
		skill("sig", "---\n\nRun `npm install left-pad@1.2.0`.\n\n```json\n{ \"version\": \"9.9.9\" }\n```\n"))
	write(filepath.Join(extra, ".claude", "skills", "deep", "SKILL.md"), skill("deep", "---\n\n# Deep\n"))
	write(filepath.Join(home, ".agents", ".skill-lock.json"),
		`{"version":3,"skills":{"alpha":{"source":"someone/repo"},"mine":{"source":"Me/skills"}}}`)
	if err := os.Symlink(filepath.Join(agents, "alpha"), filepath.Join(claude, "alpha")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(agents, "gone"), filepath.Join(claude, "gone")); err != nil {
		t.Fatal(err)
	}

	// A link back to its own parent: the in-depth walk must not follow it.
	if err := os.Symlink(extra, filepath.Join(extra, "loop")); err != nil {
		t.Fatal(err)
	}

	before := treeDigest(t, home, extra)

	cmd := exec.Command(filepath.Join(root, "skills", "self-updating-skills", "scripts", "scan.sh"),
		"--offline", "--json", "--owner", "me", extra)
	cmd.Dir = t.TempDir() // unrelated cwd
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("scan.sh failed: %v\n%s", err, out)
	}

	var got struct {
		Summary struct {
			Entries         int      `json:"entries"`
			SkillFolders    int      `json:"skill_folders"`
			Names           int      `json:"names"`
			DuplicatedNames int      `json:"duplicated_names"`
			SelfUpdating    int      `json:"self_updating"`
			BrokenLinks     []string `json:"broken_links"`
		} `json:"summary"`
		Skills []struct {
			Name            string   `json:"name"`
			Path            string   `json:"path"`
			Links           []string `json:"links"`
			Origin          string   `json:"origin"`
			Record          string   `json:"record"`
			Source          string   `json:"source"`
			SelfUpdating    bool     `json:"self_updating"`
			State           string   `json:"state"`
			Detail          string   `json:"detail"`
			Copies          int      `json:"copies"`
			CopiesIdentical bool     `json:"copies_identical"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("scan.sh --json is not valid JSON: %v\n%s", err, out)
	}

	s := got.Summary
	if s.Entries != 7 || s.SkillFolders != 6 || s.Names != 5 || s.DuplicatedNames != 1 || s.SelfUpdating != 1 {
		t.Errorf("summary = %+v, want entries 7, folders 6, names 5, duplicated 1, self-updating 1", s)
	}
	if len(s.BrokenLinks) != 1 || !strings.HasSuffix(s.BrokenLinks[0], filepath.Join(".claude", "skills", "gone")) {
		t.Errorf("broken links = %v, want the one dangling symlink", s.BrokenLinks)
	}

	// key: skill name, plus the harness folder for the name that has two copies
	rows := map[string]string{}
	for _, r := range got.Skills {
		key := r.Name
		if r.Copies > 1 {
			key += "@" + filepath.Base(filepath.Dir(filepath.Dir(r.Path)))
		}
		rows[key] = fmt.Sprintf("%s/%s/%s/%s/%d/%t/%d", r.Origin, r.Record, r.Source, r.State, r.Copies, r.CopiesIdentical, len(r.Links))
		if r.Name == "sig" && r.Detail != "left-pad 1.2.0" {
			t.Errorf("sig detail = %q, want only the package signal (a JSON \"version\" key names no package)", r.Detail)
		}
	}
	want := map[string]string{
		"alpha":        "installed/lock/someone/repo/NO-SIGNAL/1/true/2", // one folder, reached through two entries
		"mine@.agents": "yours/lock/Me/skills/NO-SIGNAL/2/false/1",       // --owner matches the record, whatever the case
		"mine@.claude": "unknown/none//NO-SIGNAL/2/false/1",              // same name, no record of its own
		"pinned":       "unknown/none//NOT-CHECKED/1/true/1",
		"sig":          "unknown/none//NOT-CHECKED/1/true/1",
		"deep":         "unknown/none//NO-SIGNAL/1/true/1",
	}
	for key, w := range want {
		if rows[key] != w {
			t.Errorf("%s = %q, want %q", key, rows[key], w)
		}
	}
	if len(rows) != len(want) {
		t.Errorf("rows = %v, want exactly %d", rows, len(want))
	}

	if after := treeDigest(t, home, extra); after != before {
		t.Errorf("the scan changed the scanned tree:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// treeDigest lists every file, directory and symlink under the roots with a
// content hash (or the link target), so any write shows up as a difference.
func treeDigest(t *testing.T, roots ...string) string {
	t.Helper()
	var lines []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			switch {
			case d.Type()&fs.ModeSymlink != 0:
				target, err := os.Readlink(path)
				if err != nil {
					return err
				}
				lines = append(lines, path+" -> "+target)
			case d.IsDir():
				lines = append(lines, path+"/")
			default:
				b, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				lines = append(lines, fmt.Sprintf("%s %x", path, sha256.Sum256(b)))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
