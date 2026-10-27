package render

import (
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testSidecar() *Sidecar {
	return &Sidecar{
		Schema: 1,
		Owner:  "ci",
		Pins: map[string]PinData{
			"@anthropic-ai/claude-code": {
				Kind:        "npm",
				VerifiedFor: "2.1.310",
				LatestSeen:  "2.1.312",
				CheckedAt:   "2026-10-27",
				VerifiedAt:  "2026-10-24",
			},
		},
		Docs: map[string]DocData{},
	}
}

const skillWithRegions = `---
name: t
metadata:
  version: "1.0.0"
  upkeep-pins: "@anthropic-ai/claude-code=2.1.310"
  upkeep-verified: "2026-10-24"
---

# T

<!-- generated:pins -->
stale
table
<!-- /generated:pins -->

<!-- generated:freshness -->
old
<!-- /generated:freshness -->

<!-- generated:redirects -->
old redirects
<!-- /generated:redirects -->

prose stays
`

func TestBuildSplicesRegionsAndKeys(t *testing.T) {
	out, err := Build([]byte(skillWithRegions), testSidecar())
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "| @anthropic-ai/claude-code | 2.1.310 | 2.1.312 | 2026-10-27 |") {
		t.Errorf("pins table missing:\n%s", s)
	}
	if !strings.Contains(s, "Verified 2026-10-24 for @anthropic-ai/claude-code@2.1.310") {
		t.Errorf("freshness line missing:\n%s", s)
	}
	if !strings.Contains(s, "| stale habit → current | since | source |") {
		t.Errorf("redirects header missing:\n%s", s)
	}
	if !strings.Contains(s, "prose stays") || !strings.Contains(s, `version: "1.0.0"`) {
		t.Errorf("author content lost:\n%s", s)
	}
	for _, marker := range []string{"<!-- generated:pins -->", "<!-- /generated:pins -->"} {
		if !strings.Contains(s, marker) {
			t.Errorf("marker %s lost", marker)
		}
	}
	// Owned keys unchanged in this fixture: no drift, so second Build is a
	// fixed point.
	out2, err := Build(out, testSidecar())
	if err != nil {
		t.Fatal(err)
	}
	if string(out2) != string(out) {
		t.Errorf("Build not idempotent:\n---1---\n%s\n---2---\n%s", out, out2)
	}
}

func TestBuildCRLFPreservesLineEndingsAndTrailingNewline(t *testing.T) {
	raw := strings.ReplaceAll(skillWithRegions, "\n", "\r\n")
	out, err := Build([]byte(raw), testSidecar())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(out), "\r\n") {
		t.Error("trailing newline lost")
	}
	for i := 0; i < len(out); i++ {
		if out[i] == '\n' && (i == 0 || out[i-1] != '\r') {
			t.Errorf("lone LF at offset %d", i)
		}
	}
	// Decoy markers inside fenced code must survive untouched.
	doc := raw + "```" + "\r\n" + "<!-- generated:pins -->" + "\r\n" + "decoy" + "\r\n" + "<!-- /generated:pins -->" + "\r\n" + "```" + "\r\n"
	out2, err := Build([]byte(doc), testSidecar())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out2), "decoy") {
		t.Error("fenced decoy content lost")
	}
}

func TestBuildRejectsRegionOverlappingFrozen(t *testing.T) {
	doc := `---
name: t
metadata:
  upkeep-pins: "x=1"
---

# T

<!-- generated:pins -->
body
<!-- frozen:pol -->
f
<!-- /generated:pins -->
<!-- /frozen:pol -->
`
	_, err := Build([]byte(doc), testSidecar())
	if err == nil {
		t.Fatal("expected overlap error")
	}
	// The error must name both ids.
	if !strings.Contains(err.Error(), "pins") || !strings.Contains(err.Error(), "pol") {
		t.Errorf("error does not name the overlapping ids: %v", err)
	}
}

func TestBuildRejectsUnknownRegion(t *testing.T) {
	doc := `---
name: t
metadata:
  upkeep-pins: "x=1"
---

<!-- generated:custom -->
x
<!-- /generated:custom -->
`
	if _, err := Build([]byte(doc), testSidecar()); err == nil {
		t.Fatal("expected unknown-region error")
	}
}

func TestBuildRejectsBadSidecarFields(t *testing.T) {
	cases := []func(sc *Sidecar){
		func(sc *Sidecar) { sc.Redirects = []Redirect{{ID: "r", Old: strings.Repeat("x", 121), New: "y"}} },
		func(sc *Sidecar) { sc.Redirects = []Redirect{{ID: "r", Old: "a\nb", New: "y"}} },
		func(sc *Sidecar) { sc.Redirects = []Redirect{{ID: "r", Old: "a <!-- b", New: "y"}} },
		func(sc *Sidecar) { sc.Redirects = []Redirect{{ID: "r", Old: "a```b", New: "y"}} },
		func(sc *Sidecar) {
			sc.Redirects = []Redirect{{ID: "r", Old: "a", New: "y", Source: "http://x.example"}}
		},
		func(sc *Sidecar) { sc.Pins["bad\nname"] = PinData{VerifiedFor: "1"} },
	}
	for i, mutate := range cases {
		sc := testSidecar()
		mutate(sc)
		if _, err := Build([]byte(skillWithRegions), sc); err == nil {
			t.Errorf("case %d: expected validation error", i)
		}
	}
}

func TestRegionBodyRedirectsSortedAndEscaped(t *testing.T) {
	sc := testSidecar()
	sc.Redirects = []Redirect{
		{ID: "b", Old: "old `x`", New: "new", Since: "1.0.0"},
		{ID: "a", Old: "plain", New: "n|ew", Since: "1.0.0"},
		{ID: "c", Old: "z", New: "z", Since: "0.9.0"},
	}
	body, err := RegionBody(RegionRedirects, sc)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(body, "\n")
	// rows sorted by since then id: c (0.9.0), a, b (1.0.0)
	if !strings.HasPrefix(lines[2], "| `z` → `z`") {
		t.Errorf("row order wrong:\n%s", body)
	}
	if !strings.Contains(body, "`` old `x` ``") {
		t.Errorf("backticks not escaped:\n%s", body)
	}
	if !strings.Contains(body, "`n\\|ew`") {
		t.Errorf("pipe not escaped:\n%s", body)
	}
}

func TestRegionBodyFreshnessEmpty(t *testing.T) {
	sc := testSidecar()
	sc.Pins = map[string]PinData{"x": {VerifiedFor: "1"}}
	body, err := RegionBody(RegionFreshness, sc)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		t.Errorf("body = %q, want empty", body)
	}
}

func TestDriftTargets(t *testing.T) {
	sc := testSidecar()
	raw := []byte(skillWithRegions)
	// No drift on a rendered file.
	out, err := Build(raw, sc)
	if err != nil {
		t.Fatal(err)
	}
	if tg, err := DriftTargets(out, sc); err != nil || len(tg) != 0 {
		t.Fatalf("targets = %v (err %v), want none", tg, err)
	}
	// Hand edit inside the pins region (the latest-seen cell only exists
	// in the region body, not the frontmatter).
	bad := strings.Replace(string(out), "2.1.312", "9.9.9", 1)
	tg, err := DriftTargets([]byte(bad), sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(tg) != 1 || tg[0] != "region:pins" {
		t.Errorf("targets = %v, want [region:pins]", tg)
	}
	// Stale owned frontmatter key.
	bad2 := strings.Replace(string(out), `upkeep-pins: "@anthropic-ai/claude-code=2.1.310"`, `upkeep-pins: "@anthropic-ai/claude-code=0.0.1"`, 1)
	tg, err = DriftTargets([]byte(bad2), sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(tg) != 1 || tg[0] != "frontmatter:upkeep-pins" {
		t.Errorf("targets = %v, want [frontmatter:upkeep-pins]", tg)
	}
	// A pin removed from the sidecar: the file's key is now unbacked.
	sc2 := testSidecar()
	sc2.Pins = map[string]PinData{}
	tg, err = DriftTargets(out, sc2)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range tg {
		if x == "frontmatter:upkeep-pins" || x == "frontmatter:upkeep-verified" {
			found = true
		}
	}
	if !found {
		t.Errorf("targets = %v, want an unbacked owned key", tg)
	}
	// ... and Build removes the unbacked keys.
	out2, err := Build(out, sc2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out2), "upkeep-pins") || strings.Contains(string(out2), "upkeep-verified") {
		t.Errorf("unbacked keys not removed:\n%s", out2)
	}
}

func TestSidecarCanonicalSortedAndStable(t *testing.T) {
	b1 := testSidecar().Canonical()
	b2 := testSidecar().Canonical()
	if string(b1) != string(b2) {
		t.Fatal("canonical bytes not stable")
	}
	s := string(b1)
	if !strings.HasPrefix(s, "{\n  \"schema\": 1") {
		t.Errorf("unexpected canonical form:\n%s", s)
	}
	if !strings.HasSuffix(s, "}\n") {
		t.Error("missing trailing newline")
	}
	// Round-trip.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, SidecarName), b1, 0o644); err != nil {
		t.Fatal(err)
	}
	sc, err := LoadSidecar(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(sc.Canonical()) != string(b1) {
		t.Error("sidecar round-trip not byte-identical")
	}
}

func TestLoadSidecarMissingAndBadSchema(t *testing.T) {
	dir := t.TempDir()
	sc, err := LoadSidecar(dir)
	if err != nil || sc != nil {
		t.Errorf("missing sidecar = (%v, %v), want (nil, nil)", sc, err)
	}
	if err := os.WriteFile(filepath.Join(dir, SidecarName), []byte(`{"schema": 2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSidecar(dir); err == nil {
		t.Error("schema 2 must be rejected")
	}
}

func TestApplyWriteOrderAndSkipUnchanged(t *testing.T) {
	dir := t.TempDir()
	sc := testSidecar()
	out, err := Build([]byte(skillWithRegions), sc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SkillName), []byte(skillWithRegions), 0o644); err != nil {
		t.Fatal(err)
	}
	sk, sd, err := Apply(dir, sc, out)
	if err != nil || !sk || !sd {
		t.Fatalf("Apply = (%v, %v, %v), want both changed", sk, sd, err)
	}
	gotSkill, err := os.ReadFile(filepath.Join(dir, SkillName))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotSkill) != string(out) {
		t.Error("SKILL.md bytes not applied")
	}
	// Second apply: nothing changes.
	sk, sd, err = Apply(dir, sc, out)
	if err != nil || sk || sd {
		t.Fatalf("second Apply = (%v, %v, %v), want no changes", sk, sd, err)
	}
}

func TestApplySecondRenameFailureConverges(t *testing.T) {
	dir := t.TempDir()
	sc := testSidecar()
	out, err := Build([]byte(skillWithRegions), sc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SkillName), []byte(skillWithRegions), 0o644); err != nil {
		t.Fatal(err)
	}
	// A non-canonical sidecar so the first write actually happens.
	if err := os.WriteFile(filepath.Join(dir, SidecarName), []byte(`{"schema":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := renameFn
	renameFn = func(oldpath, newpath string) error {
		if strings.HasSuffix(newpath, SkillName) {
			return os.ErrPermission
		}
		return os.Rename(oldpath, newpath)
	}
	defer func() { renameFn = orig }()

	_, _, err = Apply(dir, sc, out)
	if err == nil {
		t.Fatal("expected the SKILL.md rename failure to surface")
	}
	// Sidecar was written first: it is canonical now.
	got, err := os.ReadFile(filepath.Join(dir, SidecarName))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(sc.Canonical()) {
		t.Error("sidecar not written before the failed SKILL.md rename")
	}
	// SKILL.md untouched: check would report DRIFT here.
	gotSkill, _ := os.ReadFile(filepath.Join(dir, SkillName))
	if string(gotSkill) != skillWithRegions {
		t.Error("SKILL.md changed despite the failed rename")
	}
	tg, err := DriftTargets(gotSkill, sc)
	if err != nil || len(tg) == 0 {
		t.Errorf("drift targets after partial apply = %v (err %v), want non-empty", tg, err)
	}
	// The next render converges.
	renameFn = os.Rename
	if err := os.WriteFile(filepath.Join(dir, SkillName), gotSkill, 0o644); err != nil {
		t.Fatal(err)
	}
	sk, sd, err := Apply(dir, sc, out)
	if err != nil || !sk || sd {
		t.Fatalf("converging Apply = (%v, %v, %v), want only SKILL.md changed", sk, sd, err)
	}
	gotSkill, _ = os.ReadFile(filepath.Join(dir, SkillName))
	if string(gotSkill) != string(out) {
		t.Error("render did not converge")
	}
}

func TestWriteFileAtomicKeepsMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SkillName)
	if err := os.WriteFile(path, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("y")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640", info.Mode().Perm())
	}
	// No temp files left behind.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("dir has %d entries, want 1", len(entries))
	}
}

func TestDiff(t *testing.T) {
	if d := Diff("a\nb\nc\n", "a\nb\nc\n"); d != "" {
		t.Errorf("identical input: %q", d)
	}
	d := Diff("one\ntwo\nthree\n", "one\nTWO\nthree\nfour\n")
	if !strings.Contains(d, "-two") || !strings.Contains(d, "+TWO") || !strings.Contains(d, "+four") {
		t.Errorf("diff missing changes:\n%s", d)
	}
	// Huge input: bounded, no panic.
	var big string
	for i := 0; i < 6000; i++ {
		big += strings.Repeat("x", 10) + "\n"
	}
	if d := Diff(big, big+"y\n"); !strings.Contains(d, "diff omitted") {
		t.Errorf("huge diff not omitted: %.40s", d)
	}
}

func TestRenderFuzzDecoyAndCRLFStability(t *testing.T) {
	// Seeded pseudo-random loop (not go test -fuzz): decoy markers inside
	// fenced code, real regions, frozen blocks, LF or CRLF endings, with
	// and without a trailing newline. Rendered bytes must be stable across
	// runs and preserve line endings and the trailing newline.
	rng := rand.New(rand.NewSource(20261027))

	prose := []string{
		"# Skill", "", "Some prose with `code` and pipes | here.",
		"<!-- looks like a marker but isn't -->", "trailing spaces   ",
	}
	for iter := 0; iter < 200; iter++ {
		eol := "\n"
		if rng.Intn(2) == 0 {
			eol = "\r\n"
		}
		var sb strings.Builder
		write := func(s string) { sb.WriteString(s + eol) }
		write("---")
		write("name: fuzz")
		write("metadata:")
		write("  upkeep-pins: \"pkg-a=1.0.0\"")
		write("---")
		for _, p := range rng.Perm(len(prose))[:rng.Intn(3)] {
			_ = p
			write(prose[rng.Intn(len(prose))])
		}
		// A fenced block with decoy markers.
		write("```")
		write("<!-- generated:pins -->")
		write("<!-- /generated:freshness -->")
		write("<!-- frozen:zzz -->")
		write("```")
		// Real regions with random bodies.
		write("<!-- generated:pins -->")
		bodyLines := 0
		for n := rng.Intn(3); n > 0; n-- {
			write("hand " + prose[rng.Intn(len(prose))])
			bodyLines++
		}
		if bodyLines == 0 {
			write("") // empty region: blank line between markers, as render writes
		}
		write("<!-- /generated:pins -->")
		write("<!-- generated:freshness -->")
		if rng.Intn(2) == 0 {
			write("stale")
		} else {
			write("")
		}
		write("<!-- /generated:freshness -->")
		// A frozen block that must survive untouched.
		write("<!-- frozen:pol -->")
		write("frozen body stays")
		write("<!-- /frozen:pol -->")
		if rng.Intn(2) == 0 {
			write("tail prose")
		}
		raw := sb.String()
		if rng.Intn(4) == 0 {
			raw = strings.TrimSuffix(raw, eol) // no trailing newline
		}

		sc := testSidecar()
		sc.Pins["pkg-a"] = PinData{Kind: "npm", VerifiedFor: "1.0.0", VerifiedAt: "2026-10-24"}
		sc.Redirects = []Redirect{{ID: "r1", Old: "old", New: "new", Since: "1.0", Source: "https://x.example/a"}}

		out1, err := Build([]byte(raw), sc)
		if err != nil {
			t.Fatalf("iter %d (eol=%q): Build: %v", iter, eol, err)
		}
		out2, err := Build(out1, sc)
		if err != nil {
			t.Fatalf("iter %d: second Build: %v", iter, err)
		}
		if string(out1) != string(out2) {
			t.Fatalf("iter %d: render not stable across runs", iter)
		}
		// Trailing newline preserved.
		if strings.HasSuffix(raw, "\n") && !strings.HasSuffix(string(out1), eol) {
			t.Fatalf("iter %d: trailing newline lost", iter)
		}
		if !strings.HasSuffix(raw, "\n") && strings.HasSuffix(string(out1), "\n") {
			t.Fatalf("iter %d: trailing newline added", iter)
		}
		// EOL class preserved: no lone LF in a CRLF file, no CR in an LF file.
		for i := 0; i < len(out1); i++ {
			if eol == "\r\n" && out1[i] == '\n' && (i == 0 || out1[i-1] != '\r') {
				t.Fatalf("iter %d: lone LF at %d", iter, i)
			}
			if eol == "\n" && out1[i] == '\r' {
				t.Fatalf("iter %d: CR in an LF file at %d", iter, i)
			}
		}
		// Fenced decoys and frozen content untouched.
		if !strings.Contains(string(out1), "<!-- frozen:zzz -->") {
			t.Fatalf("iter %d: fenced decoy frozen marker lost", iter)
		}
		if !strings.Contains(string(out1), "frozen body stays") {
			t.Fatalf("iter %d: frozen body lost", iter)
		}
	}
}
