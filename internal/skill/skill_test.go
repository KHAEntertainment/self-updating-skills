package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripFencedBlocks(t *testing.T) {
	in := "line1\n```go\nfenced\n```\nline2\n~~~\nfenced2\n~~~\nline3"
	want := "line1\n     \n      \n   \nline2\n   \n       \n   \nline3"
	if got := StripFencedBlocks(in); got != want {
		t.Errorf("StripFencedBlocks:\ngot  %q\nwant %q", got, want)
	}
}

func TestStripFencedBlocksByteLength(t *testing.T) {
	inputs := []string{
		"a\n```\nx\ny\n```\nb",
		"# 日本語\n```markdown\né… text\n```\nafter\n",
		"~~~\nfenced\n~~~\n",
	}
	for _, in := range inputs {
		out := StripFencedBlocks(in)
		if len(out) != len(in) {
			t.Errorf("masked length %d != original length %d for %q", len(out), len(in), in)
		}
	}
}

func TestStripFencedBlocksOffsetAfterNonASCIIFence(t *testing.T) {
	in := "```markdown\né日本語\n```\n<!-- frozen:x -->\n"
	out := StripFencedBlocks(in)
	marker := "<!-- frozen:x -->"
	wantIdx := strings.Index(in, marker)
	gotIdx := strings.Index(out, marker)
	if wantIdx < 0 {
		t.Fatalf("marker not found in original")
	}
	if gotIdx != wantIdx {
		t.Errorf("marker offset = %d, want %d (original offset)", gotIdx, wantIdx)
	}
}

func TestStripFencedBlocksKeepsLineCount(t *testing.T) {
	in := "a\n```\nx\ny\n```\nb"
	out := StripFencedBlocks(in)
	if len(strings.Split(in, "\n")) != len(strings.Split(out, "\n")) {
		t.Errorf("line count changed: got %d want %d", len(strings.Split(out, "\n")), len(strings.Split(in, "\n")))
	}
}

func TestScanFrozenBlocks(t *testing.T) {
	dir := t.TempDir()
	sk := "---\n" +
		"name: t\n" +
		"metadata:\n" +
		"  upkeep-threshold-days: \"60\"\n" +
		"---\n" +
		"# Title\n\n" +
		"<!-- frozen:pol -->\n" +
		"Some policy text\n" +
		"with a second line\n" +
		"<!-- /frozen:pol -->\n\n" +
		"```markdown\n" +
		"<!-- frozen:inside -->\n" +
		"hidden\n" +
		"<!-- /frozen:inside -->\n" +
		"```\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(sk), 0o644); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(dir, "references"), 0o755)
	if err := os.WriteFile(filepath.Join(dir, "references", "a.md"), []byte("<!-- frozen:ref -->\nref body\n<!-- /frozen:ref -->\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	blocks, err := ScanFrozenBlocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2 (fenced one ignored): %+v", len(blocks), blocks)
	}
	if blocks[0].ID != "pol" || blocks[0].Rel != "SKILL.md" {
		t.Errorf("block0 = %+v", blocks[0])
	}
	if blocks[1].ID != "ref" || blocks[1].Rel != "references/a.md" {
		t.Errorf("block1 = %+v", blocks[1])
	}
	if blocks[0].Digest != "sha256:"+hash16(blocks[0].Body) {
		t.Errorf("digest mismatch: %s vs %s", blocks[0].Digest, hash16(blocks[0].Body))
	}
}

func TestScanFrozenBlocksMissingClose(t *testing.T) {
	dir := t.TempDir()
	sk := "---\nname: t\n---\n\n<!-- frozen:pol -->\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(sk), 0o644); err != nil {
		t.Fatal(err)
	}
	blocks, err := ScanFrozenBlocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 0 {
		t.Errorf("unclosed block should be ignored, got %+v", blocks)
	}
}

func TestScanFrozenBlocksDuplicateSameFile(t *testing.T) {
	dir := t.TempDir()
	sk := "---\nname: t\n---\n\n" +
		"<!-- frozen:x -->\n" +
		"first body\n" +
		"<!-- /frozen:x -->\n\n" +
		"<!-- frozen:x -->\n" +
		"second body\n" +
		"<!-- /frozen:x -->\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(sk), 0o644); err != nil {
		t.Fatal(err)
	}
	blocks, err := ScanFrozenBlocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1 (last value wins): %+v", len(blocks), blocks)
	}
	if blocks[0].Body != "second body" {
		t.Errorf("block body = %q, want %q (last value wins)", blocks[0].Body, "second body")
	}
}

func TestScanFrozenBlocksDuplicateAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	sk := "---\nname: t\n---\n\n" +
		"<!-- frozen:y -->\n" +
		"skill body\n" +
		"<!-- /frozen:y -->\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(sk), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "references", "a.md"), []byte("<!-- frozen:y -->\nreference body\n<!-- /frozen:y -->\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	blocks, err := ScanFrozenBlocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1: %+v", len(blocks), blocks)
	}
	if blocks[0].Rel != "references/a.md" {
		t.Errorf("Rel = %q, want references/a.md (last value wins)", blocks[0].Rel)
	}
	if blocks[0].Body != "reference body" {
		t.Errorf("Body = %q, want reference body", blocks[0].Body)
	}
}

func TestScanFrozenBlocksNestedMarker(t *testing.T) {
	dir := t.TempDir()
	sk := "---\nname: t\n---\n\n" +
		"<!-- frozen:a -->\n" +
		"outer body\n" +
		"<!-- frozen:b -->\n" +
		"inner body\n" +
		"<!-- /frozen:b -->\n" +
		"still outer\n" +
		"<!-- /frozen:a -->\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(sk), 0o644); err != nil {
		t.Fatal(err)
	}
	blocks, err := ScanFrozenBlocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1 (inner marker consumed by outer block)", len(blocks))
	}
	for _, want := range []string{"outer body", "<!-- frozen:b -->", "inner body", "still outer"} {
		if !strings.Contains(blocks[0].Body, want) {
			t.Errorf("outer block body missing %q: %q", want, blocks[0].Body)
		}
	}
}

func TestNormalizeNewlines(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"a\r\nb\r\n", "a\nb\n"},
		{"a\rb\n", "a\nb\n"},
		{"a\n", "a\n"},
		{"", ""},
	}
	for _, c := range cases {
		if got := NormalizeNewlines(c.in); got != c.want {
			t.Errorf("NormalizeNewlines(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseFrontmatterFlatAndNested(t *testing.T) {
	dir := t.TempDir()
	sk := `---
name: sample
metadata:
  author: KHAEntertainment
  upkeep-pins: "a=1.0.0, b=github:o/r@v2.0.0, c=manual:3.0.0, d=npm:4.0.0"
  upkeep-threshold-days: "90"
  upkeep-verified: "2020-01-01"
  pins:
    - nested: 5.0.0
  frozen:
    ff: "sha256:abc"
  sources:
    base_url: "https://example.com"
    pages:
      - one
      - two
---
`
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(sk), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := ParseFrontmatter(filepath.Join(dir, "SKILL.md"))
	if cfg.ThresholdDays != 90 {
		t.Errorf("ThresholdDays = %d, want 90", cfg.ThresholdDays)
	}
	if cfg.Verified != "2020-01-01" {
		t.Errorf("Verified = %q", cfg.Verified)
	}
	if len(cfg.Pins) != 5 {
		t.Fatalf("Pins = %+v", cfg.Pins)
	}
	if cfg.Pins[0] != (Pin{Name: "a", Version: "1.0.0", Kind: "npm"}) {
		t.Errorf("pin0 = %+v", cfg.Pins[0])
	}
	if cfg.Pins[1].Kind != "github" || cfg.Pins[1].Repo != "o/r" || cfg.Pins[1].Version != "v2.0.0" {
		t.Errorf("pin1 = %+v", cfg.Pins[1])
	}
	if cfg.Pins[2].Kind != "manual" {
		t.Errorf("pin2 = %+v", cfg.Pins[2])
	}
	if len(cfg.Frozen) != 1 || cfg.Frozen[0].Hash != "sha256:abc" {
		t.Errorf("Frozen = %+v", cfg.Frozen)
	}
	if cfg.Sources.BaseURL != "https://example.com" {
		t.Errorf("BaseURL = %q", cfg.Sources.BaseURL)
	}
	if len(cfg.Sources.Pages) != 2 || cfg.Sources.Pages[0] != "one" {
		t.Errorf("Pages = %+v", cfg.Sources.Pages)
	}
}

func hash16(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])[:16]
}

func TestParseFrontmatterNoFrontmatter(t *testing.T) {
	dir := t.TempDir()
	sk := "# just body\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(sk), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := ParseFrontmatter(filepath.Join(dir, "SKILL.md"))
	if cfg.ThresholdDays != 60 {
		t.Errorf("ThresholdDays = %d, want default 60", cfg.ThresholdDays)
	}
	if cfg.SkillName != filepath.Base(dir) {
		t.Errorf("SkillName = %q", cfg.SkillName)
	}
}
