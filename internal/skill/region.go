// Region scanning and frontmatter editing for generated regions. All
// offsets are byte offsets into the RAW file (no newline normalization), so
// the renderer can splice on the original bytes while finding markers on
// fence-masked text, per docs/style-guide.md: mask by replacing characters,
// never deleting lines, so byte offsets stay aligned (including CRLF).

package skill

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	genOpenLineRe  = regexp.MustCompile(`^[ \t]*<!--\s*generated:([A-Za-z0-9_.-]+)\s*-->[ \t]*\r?$`)
	genCloseLineRe = regexp.MustCompile(`^[ \t]*<!--\s*/generated:([A-Za-z0-9_.-]+)\s*-->[ \t]*\r?$`)

	frozenOpenLineRe  = regexp.MustCompile(`^[ \t]*<!--\s*frozen:([A-Za-z0-9_.-]+)\s*-->[ \t]*\r?$`)
	frozenCloseLineRe = regexp.MustCompile(`^[ \t]*<!--\s*/frozen:([A-Za-z0-9_.-]+)\s*-->[ \t]*\r?$`)

	metadataLineRe = regexp.MustCompile(`^metadata:[ \t]*\r?$`)
	flatMetaKeyRe  = regexp.MustCompile(`^[ \t]{2}([A-Za-z0-9_.-]+):`)
)

// Region is a marker-delimited block (generated or frozen) with byte
// offsets into the raw file. LineStart/LineEnd delimit the whole region
// including both marker lines; BodyStart/BodyEnd delimit the spliceable
// body between the open marker's line ending and the close marker's line
// start.
type Region struct {
	ID        string
	LineStart int
	LineEnd   int
	BodyStart int
	BodyEnd   int
}

// FileEOL returns the line ending the file uses, taken from its first line.
// Files with mixed endings keep per-line endings; only newly inserted lines
// use the value returned here.
func FileEOL(raw []byte) string {
	if i := strings.IndexByte(string(raw), '\n'); i > 0 && raw[i-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

// FrontmatterEnd returns the byte offset just past the closing "---" line
// of a leading frontmatter block (including its line ending), mirroring
// Python's ^---\n(.*?)\n--- match on the raw bytes.
func FrontmatterEnd(raw []byte) (int, bool) {
	text := string(raw)
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return 0, false
	}
	pos := 0
	for pos < len(text) {
		nl := strings.IndexByte(text[pos:], '\n')
		if nl < 0 {
			// No newline after this line: a closing --- here ends the
			// frontmatter at EOF.
			if strings.TrimRight(text[pos:], "\r") == "---" {
				return len(text), true
			}
			return 0, false
		}
		lineEnd := pos + nl + 1
		if pos > 0 && strings.TrimRight(text[pos:lineEnd-1], "\r") == "---" {
			return lineEnd, true
		}
		pos = lineEnd
	}
	return 0, false
}

// ScanGeneratedRegions scans the raw SKILL.md bytes for generated regions,
// skipping frontmatter and ignoring markers inside fenced code blocks. It
// returns an error for unclosed, nested, duplicated or stray markers: the
// renderer must never guess where a region ends.
func ScanGeneratedRegions(raw []byte) ([]Region, error) {
	return scanMarkerRegions(raw, genOpenLineRe, genCloseLineRe, "generated", true)
}

// ScanFrozenSpans scans the raw bytes for frozen-block spans (frontmatter
// skipped, fences masked) purely to detect overlap with generated regions.
// Hashing stays in ScanFrozenBlocks. Unlike the generated scan it is
// lenient, mirroring the frozen regex semantics: stray or unclosed markers
// are ordinary prose, never errors.
func ScanFrozenSpans(raw []byte) ([]Region, error) {
	return scanMarkerRegions(raw, frozenOpenLineRe, frozenCloseLineRe, "frozen", false)
}

func scanMarkerRegions(raw []byte, openRe, closeRe *regexp.Regexp, what string, strict bool) ([]Region, error) {
	from, _ := FrontmatterEnd(raw)
	masked := StripFencedBlocks(string(raw))
	// StripFencedBlocks is length-preserving, so offsets in masked are
	// offsets in raw.
	if len(masked) != len(raw) {
		return nil, fmt.Errorf("internal error: fence masking changed byte length (%d != %d)", len(masked), len(raw))
	}

	type lineRef struct {
		start, end int // start of line, end including its \n
		text       string
	}
	var lines []lineRef
	pos := 0
	for pos <= len(masked) {
		nl := strings.IndexByte(masked[pos:], '\n')
		if nl < 0 {
			if pos == len(masked) {
				break
			}
			lines = append(lines, lineRef{start: pos, end: len(masked), text: masked[pos:]})
			break
		}
		lines = append(lines, lineRef{start: pos, end: pos + nl + 1, text: masked[pos : pos+nl]})
		pos += nl + 1
	}

	seen := map[string]bool{}
	var out []Region
	i := 0
	for i < len(lines) {
		ln := lines[i]
		om := openRe.FindStringSubmatch(ln.text)
		if om == nil {
			if cm := closeRe.FindStringSubmatch(ln.text); cm != nil && strict && ln.start >= from {
				return nil, fmt.Errorf("stray closing %s marker for %q", what, cm[1])
			}
			i++
			continue
		}
		if ln.start < from {
			// open marker inside the frontmatter: never scanned
			i++
			continue
		}
		id := om[1]
		if seen[id] && strict {
			return nil, fmt.Errorf("duplicate %s region %q", what, id)
		}
		// The closer search starts at i+2, mirroring the frozen regex's
		// lazy \n(.*?)\n: an adjacent closer line is body, never a closer.
		closeAt := -1
		for j := i + 2; j < len(lines); j++ {
			if openRe.MatchString(lines[j].text) {
				if strict {
					return nil, fmt.Errorf("nested %s marker inside region %q", what, id)
				}
				continue
			}
			if cm := closeRe.FindStringSubmatch(lines[j].text); cm != nil && cm[1] == id {
				closeAt = j
				break
			}
		}
		if closeAt < 0 {
			if strict {
				return nil, fmt.Errorf("%s region %q is never closed", what, id)
			}
			// Lenient (frozen): an open marker with no eligible closer is
			// ordinary prose, not a block.
			i++
			continue
		}
		seen[id] = true
		out = append(out, Region{
			ID:        id,
			LineStart: lines[i].start,
			LineEnd:   lines[closeAt].end,
			BodyStart: lines[i].end,
			BodyEnd:   lines[closeAt].start,
		})
		i = closeAt + 1
	}
	return out, nil
}

// CheckRegionOverlap rejects generated regions that overlap each other or
// any frozen span: the renderer would destroy frozen content, so render
// must refuse to run.
func CheckRegionOverlap(regions, frozen []Region) error {
	for _, r := range regions {
		for _, f := range frozen {
			if r.LineStart < f.LineEnd && f.LineStart < r.LineEnd {
				return fmt.Errorf("generated region %q overlaps frozen block %q", r.ID, f.ID)
			}
		}
		for _, o := range regions {
			if o.ID == r.ID {
				continue
			}
			if r.LineStart < o.LineEnd && o.LineStart < r.LineEnd {
				return fmt.Errorf("generated region %q overlaps generated region %q", r.ID, o.ID)
			}
		}
	}
	return nil
}

// isSpaceByte reports whether c is a space or tab.
func isSpaceByte(c byte) bool { return c == ' ' || c == '\t' }

// FlatKey is a flat metadata key/value pair written as `  key: "value"`.
type FlatKey struct {
	Key   string
	Value string
}

// PurgeSpec names frontmatter content to remove before setting keys: all
// flat lines whose key starts with KeyPrefix, and whole nested blocks named
// in NestedBlocks (their 2-space header line through the next sibling).
type PurgeSpec struct {
	KeyPrefix    string
	NestedBlocks []string
}

// fline is one frontmatter line with its original ending preserved.
type fline struct {
	text     string // line text without \r and \n
	cr       bool   // the line originally ended with \r\n
	inserted bool   // created by SetFlatKeys (uses eol, not a remembered \r)
}

// SetFlatKeys rewrites the frontmatter of raw so that each key in keys has
// exactly one flat metadata line with the given (quoted) value. Existing
// lines are replaced in place; missing keys are inserted at the end of the
// metadata block. Per-line endings (including CRLF) are preserved; newly
// inserted lines use eol. It returns the new raw bytes.
func SetFlatKeys(raw []byte, eol string, keys []FlatKey, purge *PurgeSpec) ([]byte, error) {
	fmEnd, ok := FrontmatterEnd(raw)
	if !ok {
		return nil, fmt.Errorf("SKILL.md has no frontmatter")
	}
	fm := string(raw[:fmEnd])
	trailingNL := strings.HasSuffix(fm, "\n")
	body := strings.TrimSuffix(fm, "\n")

	var flines []fline
	for _, s := range strings.Split(body, "\n") {
		flines = append(flines, fline{text: strings.TrimSuffix(s, "\r"), cr: strings.HasSuffix(s, "\r")})
	}
	if len(flines) == 0 {
		return nil, fmt.Errorf("empty frontmatter")
	}
	// The last frontmatter line is the closing ---.
	closing := len(flines) - 1

	metaBounds := func() (int, int, error) {
		metaIdx := -1
		for i, fl := range flines {
			if metadataLineRe.MatchString(fl.text) {
				metaIdx = i
				break
			}
		}
		if metaIdx < 0 {
			return 0, 0, fmt.Errorf("frontmatter has no metadata: block")
		}
		// The metadata block runs until the first line that starts a
		// top-level key (non-whitespace first character), mirroring
		// ParseFrontmatter's boundary. Blank lines stay inside.
		metaEnd := closing
		for j := metaIdx + 1; j < closing; j++ {
			if txt := flines[j].text; txt != "" && !isSpaceByte(txt[0]) {
				metaEnd = j
				break
			}
		}
		return metaIdx, metaEnd, nil
	}

	// Purge: drop lines named by the spec, including whole nested blocks.
	if purge != nil {
		purgeBlocks := map[string]bool{}
		for _, n := range purge.NestedBlocks {
			purgeBlocks[n] = true
		}
		kept := flines[:0]
		inPurgeBlock := false
		for _, fl := range flines {
			if inPurgeBlock {
				if isSiblingStart(fl.text) {
					inPurgeBlock = false
				} else {
					continue
				}
			}
			if m := flatMetaKeyRe.FindStringSubmatch(fl.text); m != nil {
				if purge.KeyPrefix != "" && strings.HasPrefix(m[1], purge.KeyPrefix) {
					continue
				}
				if purgeBlocks[m[1]] {
					inPurgeBlock = true
					continue
				}
			}
			kept = append(kept, fl)
		}
		flines = kept
		closing = len(flines) - 1
	}

	metaIdx, metaEnd, err := metaBounds()
	if err != nil {
		return nil, err
	}

	// Replace existing key lines in place.
	set := map[string]string{}
	for _, k := range keys {
		set[k.Key] = k.Value
	}
	replaced := map[string]bool{}
	for i := metaIdx + 1; i < metaEnd; i++ {
		if m := flatMetaKeyRe.FindStringSubmatch(flines[i].text); m != nil {
			if _, wanted := set[m[1]]; wanted {
				flines[i].text = fmt.Sprintf("  %s: %q", m[1], set[m[1]])
				replaced[m[1]] = true
			}
		}
	}
	// Insert missing keys at the end of the metadata block, in order.
	var insert []fline
	for _, k := range keys {
		if replaced[k.Key] {
			continue
		}
		fl := fline{text: fmt.Sprintf("  %s: %q", k.Key, k.Value), inserted: true}
		if eol == "\r\n" {
			fl.cr = true
		}
		insert = append(insert, fl)
	}
	if len(insert) > 0 {
		out := append([]fline(nil), flines[:metaEnd]...)
		out = append(out, insert...)
		out = append(out, flines[metaEnd:]...)
		flines = out
	}

	var sb strings.Builder
	for i, fl := range flines {
		sb.WriteString(fl.text)
		if fl.cr {
			sb.WriteByte('\r')
		}
		if i != len(flines)-1 || trailingNL {
			sb.WriteByte('\n')
		}
	}
	return []byte(sb.String() + string(raw[fmEnd:])), nil
}
