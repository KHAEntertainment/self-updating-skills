package skill

import (
	"strings"
	"testing"
)

func TestFileEOL(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"lf", "a\nb\n", "\n"},
		{"crlf", "a\r\nb\r\n", "\r\n"},
		{"mixed-first-crlf", "a\r\nb\n", "\r\n"},
		{"mixed-first-lf", "a\nb\r\n", "\n"},
		{"no-newline", "abc", "\n"},
		{"empty", "", "\n"},
	}
	for _, tc := range cases {
		if got := FileEOL([]byte(tc.in)); got != tc.want {
			t.Errorf("%s: FileEOL = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestFrontmatterEnd(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
		ok   bool
	}{
		{"basic", "---\nname: x\n---\nbody", 16, true},
		{"crlf", "---\r\nname: x\r\n---\r\nbody", 19, true},
		{"no-trailing", "---\nname: x\n---", 15, true},
		{"none", "# just prose\n", 0, false},
		{"not-first-line", "x\n---\n---\n", 0, false},
	}
	for _, tc := range cases {
		got, ok := FrontmatterEnd([]byte(tc.in))
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s: FrontmatterEnd = (%d, %v), want (%d, %v)", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

const sampleSkill = `---
name: sample
metadata:
  version: "1.0.0"
  upkeep-pins: "pkg=1.0.0"
  upkeep-verified: "2026-01-02"
---

# Sample

<!-- generated:pins -->
old content
<!-- /generated:pins -->

prose stays

<!-- frozen:pol -->
frozen body
<!-- /frozen:pol -->
`

const decoySkill = "---\nname: sample\nmetadata:\n  upkeep-pins: \"pkg=1.0.0\"\n---\n\n# Sample\n\n```\n<!-- generated:pins -->\ndecoy in fence\n<!-- /generated:pins -->\n```\n\n<!-- generated:pins -->\nreal body\n<!-- /generated:pins -->\n"

func TestScanGeneratedRegionsBasic(t *testing.T) {
	rs, err := ScanGeneratedRegions([]byte(sampleSkill))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].ID != "pins" {
		t.Fatalf("regions = %+v, want one pins region", rs)
	}
	r := rs[0]
	if got := sampleSkill[r.BodyStart:r.BodyEnd]; got != "old content\n" {
		t.Errorf("body = %q, want %q", got, "old content\n")
	}
	fs, err := ScanFrozenSpans([]byte(sampleSkill))
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].ID != "pol" {
		t.Fatalf("frozen spans = %+v", fs)
	}
}

func TestScanGeneratedRegionsIgnoresFencedDecoys(t *testing.T) {
	rs, err := ScanGeneratedRegions([]byte(decoySkill))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 {
		t.Fatalf("regions = %d, want 1 (fence decoys ignored)", len(rs))
	}
	if got := decoySkill[rs[0].BodyStart:rs[0].BodyEnd]; got != "real body\n" {
		t.Errorf("body = %q, want %q", got, "real body\n")
	}
}

func TestScanGeneratedRegionsCRLF(t *testing.T) {
	raw := strings.ReplaceAll(sampleSkill, "\n", "\r\n")
	rs, err := ScanGeneratedRegions([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 {
		t.Fatalf("regions = %d, want 1", len(rs))
	}
	if got := raw[rs[0].BodyStart:rs[0].BodyEnd]; got != "old content\r\n" {
		t.Errorf("body = %q, want %q", got, "old content\r\n")
	}
}

func TestScanGeneratedRegionsErrors(t *testing.T) {
	cases := []struct {
		name, doc string
	}{
		{"unclosed", "# t\n\n<!-- generated:pins -->\nbody\n"},
		{"stray-close", "# t\n\n<!-- /generated:pins -->\n"},
		{"duplicate", "# t\n\n<!-- generated:a -->\nx\n<!-- /generated:a -->\n\n<!-- generated:a -->\ny\n<!-- /generated:a -->\n"},
		{"nested", "# t\n\n<!-- generated:a -->\n<!-- generated:b -->\nx\n<!-- /generated:a -->\n<!-- /generated:b -->\n"},
	}
	for _, tc := range cases {
		if _, err := ScanGeneratedRegions([]byte(tc.doc)); err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
		}
	}
}

func TestScanGeneratedRegionsLenientFrozenStrays(t *testing.T) {
	doc := "# t\n\n<!-- /frozen:pol -->\nstray closer is prose\n\n<!-- frozen:pol -->\nunclosed open is prose\n"
	fs, err := ScanFrozenSpans([]byte(doc))
	if err != nil {
		t.Fatalf("lenient scan should not error: %v", err)
	}
	if len(fs) != 0 {
		t.Errorf("frozen spans = %d, want 0", len(fs))
	}
}

func TestCheckRegionOverlap(t *testing.T) {
	doc := "# t\n\n<!-- generated:pins -->\nbody\n<!-- frozen:pol -->\nfrozen\n<!-- /generated:pins -->\n<!-- /frozen:pol -->\n"
	rs, err := ScanGeneratedRegions([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	fs, err := ScanFrozenSpans([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckRegionOverlap(rs, fs); err == nil {
		t.Error("expected overlap error, got nil")
	}
	// A region and a frozen block side by side must be fine.
	doc2 := "# t\n\n<!-- generated:pins -->\nbody\n<!-- /generated:pins -->\n\n<!-- frozen:pol -->\nf\n<!-- /frozen:pol -->\n"
	rs2, _ := ScanGeneratedRegions([]byte(doc2))
	fs2, _ := ScanFrozenSpans([]byte(doc2))
	if err := CheckRegionOverlap(rs2, fs2); err != nil {
		t.Errorf("unexpected overlap error: %v", err)
	}
}

func TestSetFlatKeysReplaceInPlaceLF(t *testing.T) {
	out, err := SetFlatKeys([]byte(sampleSkill), "\n", []FlatKey{
		{Key: "upkeep-pins", Value: "pkg=2.0.0"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, `upkeep-pins: "pkg=1.0.0"`) {
		t.Error("old value still present")
	}
	if !strings.Contains(s, `upkeep-pins: "pkg=2.0.0"`) {
		t.Errorf("new value missing:\n%s", s)
	}
	// Author-owned keys and body stay untouched.
	if !strings.Contains(s, `version: "1.0.0"`) || !strings.Contains(s, "old content") {
		t.Error("unrelated content was modified")
	}
}

func TestSetFlatKeysInsertMissing(t *testing.T) {
	doc := "---\nname: x\nmetadata:\n  upkeep-pins: \"pkg=1.0.0\"\n---\nbody\n"
	out, err := SetFlatKeys([]byte(doc), "\n", []FlatKey{
		{Key: "upkeep-pins", Value: "pkg=1.0.0"},
		{Key: "upkeep-verified", Value: "2026-03-04"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "---\nname: x\nmetadata:\n  upkeep-pins: \"pkg=1.0.0\"\n  upkeep-verified: \"2026-03-04\"\n---\nbody\n"
	if string(out) != want {
		t.Errorf("out = %q, want %q", out, want)
	}
}

func TestSetFlatKeysCRLFPreserved(t *testing.T) {
	doc := "---\r\nname: x\r\nmetadata:\r\n  upkeep-pins: \"pkg=1.0.0\"\r\n---\r\nbody\r\n"
	out, err := SetFlatKeys([]byte(doc), "\r\n", []FlatKey{
		{Key: "upkeep-pins", Value: "pkg=2.0.0"},
		{Key: "upkeep-verified", Value: "2026-03-04"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "---\r\nname: x\r\nmetadata:\r\n  upkeep-pins: \"pkg=2.0.0\"\r\n  upkeep-verified: \"2026-03-04\"\r\n---\r\nbody\r\n"
	if string(out) != want {
		t.Errorf("out = %q, want %q", out, want)
	}
}

func TestSetFlatKeysNoTrailingNewline(t *testing.T) {
	doc := "---\nname: x\nmetadata:\n  a: \"1\"\n---"
	out, err := SetFlatKeys([]byte(doc), "\n", []FlatKey{{Key: "b", Value: "2"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "---\nname: x\nmetadata:\n  a: \"1\"\n  b: \"2\"\n---"
	if string(out) != want {
		t.Errorf("out = %q, want %q", out, want)
	}
}

func TestSetFlatKeysPurge(t *testing.T) {
	doc := "---\nname: x\nmetadata:\n  keep: \"1\"\n  upkeep-frozen-a: \"h1\"\n  upkeep-frozen-b: \"h2\"\n  frozen:\n    c: \"h3\"\n---\nbody\n"
	out, err := SetFlatKeys([]byte(doc), "\n", []FlatKey{{Key: "upkeep-frozen-a", Value: "h9"}}, &PurgeSpec{
		KeyPrefix:    "upkeep-frozen-",
		NestedBlocks: []string{"frozen"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, bad := range []string{"upkeep-frozen-b", "c: \"h3\"", "frozen:"} {
		if strings.Contains(s, bad) {
			t.Errorf("purge left %q behind:\n%s", bad, s)
		}
	}
	if !strings.Contains(s, `upkeep-frozen-a: "h9"`) || !strings.Contains(s, `keep: "1"`) {
		t.Errorf("purge damaged unrelated lines:\n%s", s)
	}
}

func TestSetFlatKeysStableAcrossRuns(t *testing.T) {
	doc := sampleSkill
	keys := []FlatKey{{Key: "upkeep-pins", Value: "pkg=9.9.9"}}
	out1, err := SetFlatKeys([]byte(doc), "\n", keys, nil)
	if err != nil {
		t.Fatal(err)
	}
	out2, err := SetFlatKeys(out1, "\n", keys, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(out1) != string(out2) {
		t.Errorf("SetFlatKeys not idempotent:\n%q\n%q", out1, out2)
	}
}
