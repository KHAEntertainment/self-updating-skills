package engine_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// scanReport is the part of `scan.sh --json` the tests read.
type scanReport struct {
	Summary struct {
		Entries         int      `json:"entries"`
		SkillFolders    int      `json:"skill_folders"`
		Names           int      `json:"names"`
		DuplicatedNames int      `json:"duplicated_names"`
		SelfUpdating    int      `json:"self_updating"`
		BrokenLinks     []string `json:"broken_links"`
		Problems        []string `json:"problems"`
	} `json:"summary"`
	Skills []struct {
		Name            string   `json:"name"`
		Path            string   `json:"path"`
		Links           []string `json:"links"`
		Origin          string   `json:"origin"`
		Record          string   `json:"record"`
		Source          string   `json:"source"`
		State           string   `json:"state"`
		Detail          string   `json:"detail"`
		Copies          int      `json:"copies"`
		CopiesIdentical bool     `json:"copies_identical"`
	} `json:"skills"`
}

// rows keys each skill by name (plus its harness folder when the name has
// more than one copy) to "origin/record/source/state".
func (r scanReport) rows() map[string]string {
	out := map[string]string{}
	for _, s := range r.Skills {
		key := s.Name
		if s.Copies > 1 {
			key += "@" + filepath.Base(filepath.Dir(filepath.Dir(s.Path)))
		}
		out[key] = s.Origin + "/" + s.Record + "/" + s.Source + "/" + s.State
	}
	return out
}

func (r scanReport) detail(name string) string {
	for _, s := range r.Skills {
		if s.Name == name {
			return s.Detail
		}
	}
	return "<no row for " + name + ">"
}

// runScan runs skills/self-updating-skills/scripts/scan.sh --json with HOME
// set to the fixture, from cwd, and decodes the report. The script must
// exit 0 and finish well inside the timeout whatever it is given.
func runScan(t *testing.T, home, cwd string, env []string, args ...string) scanReport {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	script := filepath.Join(repoRoot(t), "skills", "self-updating-skills", "scripts", "scan.sh")
	cmd := exec.CommandContext(ctx, script, append([]string{"--json"}, args...)...)
	cmd.Dir = cwd
	cmd.Env = append(append(os.Environ(), "HOME="+home), env...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("scan.sh failed: %v\n%s", err, out)
	}
	var report scanReport
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("scan.sh --json is not valid JSON: %v\n%s", err, out)
	}
	return report
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// skillDoc is a SKILL.md: frontmatter (plus any extra frontmatter lines) and a body.
func skillDoc(name, frontmatter, body string) string {
	return "---\nname: " + name + "\ndescription: fixture\n" + frontmatter + "---\n\n" + body
}

func assertRows(t *testing.T, got, want map[string]string) {
	t.Helper()
	for key, w := range want {
		if got[key] != w {
			t.Errorf("%s = %q, want %q", key, got[key], w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("rows = %v, want exactly %d", got, len(want))
	}
}

// TestScanScriptOffline runs the scan against a fixture HOME with --offline
// (no network, no check) and checks the find and triage rules: symlinks
// resolve to one skill folder, a name with two different folders is a
// duplicate, the install record and --owner decide the origin, and a path
// given on the command line is walked in depth. The fixture must be
// byte-identical afterwards.
func TestScanScriptOffline(t *testing.T) {
	home := t.TempDir()
	extra := filepath.Join(t.TempDir(), "my repo")
	agents := filepath.Join(home, ".agents", "skills")
	claude := filepath.Join(home, ".claude", "skills")

	writeFile(t, filepath.Join(agents, "alpha", "SKILL.md"), skillDoc("alpha", "", "# Alpha\n"))
	writeFile(t, filepath.Join(agents, "mine", "SKILL.md"), skillDoc("mine", "", "# Mine\n"))
	writeFile(t, filepath.Join(agents, "pinned", "SKILL.md"), skillDoc("pinned", "metadata:\n  upkeep-pins: \"left-pad=1.0.0\"\n", "# Pinned\n"))
	writeFile(t, filepath.Join(claude, "mine", "SKILL.md"), skillDoc("mine", "", "# Mine, edited by hand\n"))
	writeFile(t, filepath.Join(claude, "notes.txt"), "not a skill\n")
	writeFile(t, filepath.Join(home, ".codex", "skills", "sig", "SKILL.md"),
		skillDoc("sig", "", "Run `npm install left-pad@1.2.0`.\n\n```json\n{ \"version\": \"9.9.9\" }\n```\n"))
	writeFile(t, filepath.Join(extra, ".claude", "skills", "deep", "SKILL.md"), skillDoc("deep", "", "# Deep\n"))
	writeFile(t, filepath.Join(home, ".agents", ".skill-lock.json"),
		`{"version":3,"skills":{"alpha":{"source":"someone/repo"},"mine":{"source":"Me/skills","sourceUrl":"https://github.com/Me/skills.git"}}}`)
	symlink(t, filepath.Join(agents, "alpha"), filepath.Join(claude, "alpha"))
	symlink(t, filepath.Join(agents, "gone"), filepath.Join(claude, "gone"))
	// A link back to its own parent: the in-depth walk must not follow it.
	symlink(t, extra, filepath.Join(extra, "loop"))

	before := treeDigest(t, home, extra)
	got := runScan(t, home, t.TempDir(), nil, "--offline", "--owner", "me", extra)

	s := got.Summary
	if s.Entries != 7 || s.SkillFolders != 6 || s.Names != 5 || s.DuplicatedNames != 1 || s.SelfUpdating != 1 {
		t.Errorf("summary = %+v, want entries 7, folders 6, names 5, duplicated 1, self-updating 1", s)
	}
	if len(s.BrokenLinks) != 1 || !strings.HasSuffix(s.BrokenLinks[0], filepath.Join(".claude", "skills", "gone")) {
		t.Errorf("broken links = %v, want the one dangling symlink", s.BrokenLinks)
	}
	assertRows(t, got.rows(), map[string]string{
		"alpha":        "installed/lock/someone/repo/NO-SIGNAL", // one folder, reached through two entries
		"mine@.agents": "yours/lock/Me/skills/NO-SIGNAL",        // --owner matches the record, whatever the case
		"mine@.claude": "unknown/none//NO-SIGNAL",               // same name, no record of its own
		"pinned":       "unknown/none//NOT-CHECKED",
		"sig":          "unknown/none//NOT-CHECKED",
		"deep":         "unknown/none//NO-SIGNAL",
	})
	if d := got.detail("sig"); d != "left-pad 1.2.0" {
		t.Errorf("sig detail = %q, want only the package signal (a JSON \"version\" key names no package)", d)
	}
	for _, r := range got.Skills {
		if r.Name == "alpha" && len(r.Links) != 2 {
			t.Errorf("alpha links = %v, want both entries", r.Links)
		}
		if r.Name == "mine" && (r.Copies != 2 || r.CopiesIdentical) {
			t.Errorf("mine copies = %d identical=%t, want 2 that differ", r.Copies, r.CopiesIdentical)
		}
	}
	if after := treeDigest(t, home, extra); after != before {
		t.Errorf("the scan changed the scanned tree:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestScanScriptHostileInput feeds the scan what a stranger's skill folder
// can contain. One bad input must not lose the report, and nothing a scanned
// folder says about itself may make it "yours": only a github.com origin or
// an install record for the folder that physically sits in the install
// location counts.
func TestScanScriptHostileInput(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	agents := filepath.Join(home, ".agents", "skills")
	claude := filepath.Join(home, ".claude", "skills")

	// The install slot is a link to a folder elsewhere; the record is stale.
	writeFile(t, filepath.Join(outside, "external", "slotlink", "SKILL.md"), skillDoc("slotlink", "", "# x\n"))
	symlink(t, filepath.Join(outside, "external", "slotlink"), filepath.Join(agents, "slotlink"))
	// A checkout whose origin only looks like owner/repo.
	writeFile(t, filepath.Join(home, "code", "elsewhere", ".git", "config"),
		"[remote \"origin\"]\n\turl = https://example.invalid/Me/repo.git\n")
	writeFile(t, filepath.Join(home, "code", "elsewhere", "skills", "nothub", "SKILL.md"), skillDoc("nothub", "", "# x\n"))
	symlink(t, filepath.Join(home, "code", "elsewhere", "skills", "nothub"), filepath.Join(claude, "nothub"))
	// A checkout that really is the owner's.
	writeFile(t, filepath.Join(home, "code", "own", ".git", "config"),
		"[core]\n\tsshCommand = touch "+filepath.Join(outside, "GIT_EXECUTED")+"\n[remote \"origin\"]\n\turl = git@github.com:Me/repo.git\n")
	writeFile(t, filepath.Join(home, "code", "own", "skills", "hub", "SKILL.md"), skillDoc("hub", "", "# x\n"))
	symlink(t, filepath.Join(home, "code", "own", "skills", "hub"), filepath.Join(claude, "hub"))
	// A checkout whose config is a FIFO: opening it would block forever.
	fifoRepo := filepath.Join(home, "code", "fifo")
	writeFile(t, filepath.Join(fifoRepo, "skills", "piped", "SKILL.md"), skillDoc("piped", "", "# x\n"))
	if err := os.MkdirAll(filepath.Join(fifoRepo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("mkfifo", filepath.Join(fifoRepo, ".git", "config")).Run(); err != nil {
		t.Logf("mkfifo unavailable, the FIFO case is not exercised: %v", err)
	}
	symlink(t, filepath.Join(fifoRepo, "skills", "piped"), filepath.Join(claude, "piped"))
	// A skill whose SKILL.md cannot be read, next to one that can.
	writeFile(t, filepath.Join(claude, "locked", "SKILL.md"), skillDoc("locked", "", "# x\n"))
	if err := os.Chmod(filepath.Join(claude, "locked", "SKILL.md"), 0o000); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(claude, "fine", "SKILL.md"), skillDoc("fine", "", "# x\n"))
	// Two copies over the 4 MB read limit that differ only past it. A cut-short
	// read would hash them alike and call them identical.
	padding := strings.Repeat("x", 4<<20)
	writeFile(t, filepath.Join(claude, "big", "SKILL.md"), skillDoc("big", "", padding+"\nNever do A.\n"))
	writeFile(t, filepath.Join(home, ".codex", "skills", "big", "SKILL.md"), skillDoc("big", "", padding+"\nAlways do A.\n"))

	want := map[string]string{
		"big@.claude": "unknown/none//UNREADABLE",
		"big@.codex":  "unknown/none//UNREADABLE",
		"slotlink":    "unknown/none//NO-SIGNAL",
		"nothub":      "unknown/none//NO-SIGNAL",
		"hub":         "yours/git/Me/repo/NO-SIGNAL",
		"piped":       "unknown/none//NO-SIGNAL",
		"locked":      "unknown/none//UNREADABLE",
		"fine":        "unknown/none//NO-SIGNAL",
	}
	if os.Geteuid() == 0 { // root reads through mode 000
		want["locked"] = "unknown/none//NO-SIGNAL"
	}

	for name, lock := range map[string]string{
		"stale-record":     `{"skills":{"slotlink":{"source":"Me/old-install"}}}`,
		"skills-is-a-list": `{"skills": []}`,
		"not-json":         `{"skills": `,
	} {
		t.Run(name, func(t *testing.T) {
			writeFile(t, filepath.Join(home, ".agents", ".skill-lock.json"), lock)
			got := runScan(t, home, t.TempDir(), nil, "--offline", "--owner", "me")
			assertRows(t, got.rows(), want)
			if os.Geteuid() != 0 && len(got.Summary.Problems) != 3 {
				t.Errorf("problems = %v, want the unreadable SKILL.md and the two oversized ones", got.Summary.Problems)
			}
			for _, r := range got.Skills {
				if r.Name == "big" && (r.Copies != 2 || r.CopiesIdentical) {
					t.Errorf("big copies = %d identical=%t, want 2 that are not called identical", r.Copies, r.CopiesIdentical)
				}
			}
		})
	}
	if _, err := os.Stat(filepath.Join(outside, "GIT_EXECUTED")); err == nil {
		t.Error("the scan ran git inside a scanned checkout (core.sshCommand fired)")
	}
}

// TestScanScriptCheckIsIsolated covers the online path without the internet:
// a local registry answers the lookups. The scan is started from a hostile
// working directory (modules that shadow the standard library, a GIT_DIR
// pointing at it) with stub `npm` and `git` first on PATH. It must report the
// same verdicts `upkeep.sh check` would, never run npm (which writes a cache
// and logs), run git only from / with the inherited GIT_* settings dropped,
// import nothing from the working directory, and leave every tree unchanged.
func TestScanScriptCheckIsIsolated(t *testing.T) {
	release := make(chan struct{})
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/left-pad/latest":
			fmt.Fprint(w, `{"version":"1.3.0"}`)
		case r.URL.Path == "/good/latest", manyPin.MatchString(r.URL.Path):
			fmt.Fprint(w, `{"version":"1.0.0"}`)
		case r.URL.Path == "/slow/latest": // never answers while the scan is waiting
			select {
			case <-release:
			case <-r.Context().Done():
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer registry.Close()
	defer close(release)

	home := t.TempDir()
	skills := filepath.Join(home, ".claude", "skills")
	writeFile(t, filepath.Join(skills, "npmpin", "SKILL.md"), skillDoc("npmpin", "metadata:\n  upkeep-pins: \"left-pad=1.0.0\"\n", "# x\n"))
	writeFile(t, filepath.Join(skills, "ghpin", "SKILL.md"), skillDoc("ghpin", "metadata:\n  upkeep-pins: \"tool=github:o/r@1.0.0\"\n", "# x\n"))
	writeFile(t, filepath.Join(skills, "behind", "SKILL.md"), skillDoc("behind", "", "Use `left-pad@1.2.0`.\n"))
	writeFile(t, filepath.Join(skills, "current", "SKILL.md"), skillDoc("current", "", "Use `good@1.0.0`.\n"))
	writeFile(t, filepath.Join(skills, "partial", "SKILL.md"), skillDoc("partial", "", "Use `good@1.0.0` with `missing@1.0.0`.\n"))
	writeFile(t, filepath.Join(skills, "ghost", "SKILL.md"), skillDoc("ghost", "metadata:\n  upkeep-pins: \"missing=1.0.0\"\n", "# x\n"))
	// 31 pins, the last one behind: stopping at the limit must not read as FRESH.
	var pins []string
	for i := 0; i < 30; i++ {
		pins = append(pins, fmt.Sprintf("p%d=1.0.0", i))
	}
	pins = append(pins, "p30=0.9.0")
	writeFile(t, filepath.Join(skills, "manypins", "SKILL.md"),
		skillDoc("manypins", "metadata:\n  upkeep-pins: \""+strings.Join(pins, ", ")+"\"\n", "# x\n"))
	// A reference file that is a FIFO: reading it would block forever.
	writeFile(t, filepath.Join(skills, "fifo", "SKILL.md"), skillDoc("fifo", "metadata:\n  upkeep-pins: \"good=1.0.0\"\n", "# x\n"))
	if err := os.MkdirAll(filepath.Join(skills, "fifo", "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	haveFifo := exec.Command("mkfifo", filepath.Join(skills, "fifo", "references", "blocked.md")).Run() == nil

	evidence := t.TempDir() // outside every digested tree
	stubs := t.TempDir()
	writeExecutable(t, filepath.Join(stubs, "npm"), "#!/usr/bin/env bash\ntouch \""+filepath.Join(evidence, "NPM_RAN")+"\"\necho 9.9.9\n")
	writeExecutable(t, filepath.Join(stubs, "git"), "#!/usr/bin/env bash\nprintf '%s|%s|%s\\n' \"$PWD\" \"${GIT_DIR:-unset}\" \"$*\" >> \""+
		filepath.Join(evidence, "git.log")+"\"\nprintf 'abc123\\trefs/tags/v1.2.0\\n'\n")
	cwd := t.TempDir()
	for _, mod := range []string{"datetime", "hashlib", "re", "subprocess", "json", "pathlib"} {
		writeFile(t, filepath.Join(cwd, mod+".py"), "open("+fmt.Sprintf("%q", filepath.Join(evidence, "IMPORTED"))+", 'w').close()\n")
	}

	before := treeDigest(t, home, cwd)
	got := runScan(t, home, cwd, []string{
		"SKILL_SCAN_NPM_REGISTRY=" + registry.URL,
		"PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH"),
		"GIT_DIR=" + cwd,
	})

	wantRows := map[string]string{
		"npmpin":   "unknown/none//STALE",
		"ghpin":    "unknown/none//STALE",
		"behind":   "unknown/none//GAP",
		"current":  "unknown/none//CURRENT",
		"partial":  "unknown/none//UNRESOLVED", // one package was not found: not CURRENT
		"ghost":    "unknown/none//FRESH",      // upkeep.sh's verdict; the detail names the unresolved pin
		"manypins": "unknown/none//UNKNOWN",
		"fifo":     "unknown/none//UNKNOWN",
	}
	wantDetails := map[string]string{
		"npmpin":   "pin:left-pad pinned 1.0.0 → current 1.3.0",
		"ghpin":    "pin:tool pinned 1.0.0 → current 1.2.0",
		"behind":   "left-pad 1.2.0 → 1.3.0",
		"partial":  "not found: missing 1.0.0",
		"ghost":    "pin:missing (pinned 1.0.0 — current unresolved)",
		"manypins": "check stopped: more than 30 pins",
		"fifo":     "check stopped: blocked.md: not a regular file",
	}
	if !haveFifo {
		t.Log("mkfifo unavailable, the FIFO case is not exercised")
		wantRows["fifo"], wantDetails["fifo"] = "unknown/none//FRESH", ""
	}
	assertRows(t, got.rows(), wantRows)
	for name, want := range wantDetails {
		if d := got.detail(name); d != want {
			t.Errorf("%s detail = %q, want %q", name, d, want)
		}
	}

	// A lookup that never answers: the deadline must end the check, and the
	// result must say so. (A swallowed deadline would wait out the lookup and
	// report the pin as merely unresolved.)
	slowDir := t.TempDir()
	writeFile(t, filepath.Join(slowDir, "slow", "SKILL.md"), skillDoc("slow", "metadata:\n  upkeep-pins: \"slow=1.0.0\"\n", "# x\n"))
	started := time.Now()
	slow := runScan(t, home, cwd, []string{"SKILL_SCAN_NPM_REGISTRY=" + registry.URL, "SKILL_SCAN_CHECK_SECONDS=1"}, "--only", slowDir)
	if d := slow.detail("slow"); d != "check stopped: timed out after 1 s" || slow.rows()["slow"] != "unknown/none//UNKNOWN" {
		t.Errorf("slow = %q, detail %q, want UNKNOWN and the timeout named", slow.rows()["slow"], d)
	}
	if took := time.Since(started); took > 6*time.Second {
		t.Errorf("the 1 s deadline took %s to end the check", took)
	}

	if _, err := os.Stat(filepath.Join(evidence, "NPM_RAN")); err == nil {
		t.Error("the scan ran the npm CLI")
	}
	if _, err := os.Stat(filepath.Join(evidence, "IMPORTED")); err == nil {
		t.Error("the scan imported a module from the working directory")
	}
	gitLog, err := os.ReadFile(filepath.Join(evidence, "git.log"))
	if err != nil {
		t.Fatalf("git was not run for the GitHub pin: %v", err)
	}
	calls := strings.Split(strings.TrimSpace(string(gitLog)), "\n")
	wantCall := "/|unset|-c protocol.allow=never -c protocol.https.allow=always ls-remote --tags --refs https://github.com/o/r.git"
	if len(calls) != 1 || calls[0] != wantCall {
		t.Errorf("git calls = %q, want exactly %q", calls, wantCall)
	}
	if after := treeDigest(t, home, cwd); after != before {
		t.Errorf("the scan changed the scanned tree or the working directory:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

var manyPin = regexp.MustCompile(`^/p\d+/latest$`)

// treeDigest lists every file, directory and symlink under the roots with a
// content hash (or the link target), so any write shows up as a difference.
// Files that cannot be read and special files are listed by mode.
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
			case !d.Type().IsRegular():
				lines = append(lines, path+" "+d.Type().String())
			default:
				b, err := os.ReadFile(path)
				if err != nil {
					lines = append(lines, path+" unreadable")
					return nil
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
