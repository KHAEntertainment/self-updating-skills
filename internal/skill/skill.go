// Package skill parses skill frontmatter and scans frozen blocks. It is a
// faithful Go port of the Python parsing in bin/upkeep.sh (the
// parse_skill_frontmatter and scan_frozen_blocks functions) so that verdict
// output stays byte-identical between the two implementations.
package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Pin is one version pin: metadata name, pinned version, kind
// (npm|github|manual) and repo (github only).
type Pin struct {
	Name    string
	Version string
	Kind    string
	Repo    string
}

// Sources mirrors the upkeep-source-* metadata block. Not used by `check`,
// but parsed for completeness and later phases.
type Sources struct {
	BaseURL    string
	Ext        string
	Cache      string
	TTLMinutes int
	IndexURL   string
	Pages      []string
	// PagesDeclared distinguishes a declared upkeep-source-pages list —
	// including an explicitly empty one, which retires all pages — from an
	// absent one, which falls back to the sidecar's docs keys (C2).
	PagesDeclared bool
}

// FrozenEntry is a recorded frozen-block id/hash pair, in metadata order.
type FrozenEntry struct {
	ID   string
	Hash string
}

// Config is the parsed skill configuration.
type Config struct {
	SkillName     string
	Pins          []Pin
	ThresholdDays int
	Verified      string
	Sources       Sources
	Frozen        []FrozenEntry
	// AutoAdvance is the upkeep-auto-advance metadata value: "patch",
	// "none", or "" when unset (meaning the default "patch").
	AutoAdvance string
}

// FrozenMap returns the recorded hashes keyed by frozen-block id.
func (c Config) FrozenMap() map[string]string {
	m := make(map[string]string, len(c.Frozen))
	for _, fe := range c.Frozen {
		m[fe.ID] = fe.Hash
	}
	return m
}

// DefaultConfig returns the configuration used when a skill has no (or an
// unreadable) frontmatter: the script's defaults.
func DefaultConfig(skillDir string) Config {
	name := filepath.Base(skillDir)
	return Config{
		SkillName:     name,
		ThresholdDays: 60,
		Sources:       defaultSources(name),
	}
}

func defaultSources(name string) Sources {
	return Sources{Ext: ".md", Cache: name + "-docs", TTLMinutes: 720}
}

// ReadError reports a failed read of an existing file.
type ReadError struct {
	Path string
	Err  error
}

func (e *ReadError) Error() string { return fmt.Sprintf("read %s: %v", e.Path, e.Err) }

// Unwrap returns the underlying error.
func (e *ReadError) Unwrap() error { return e.Err }

// NormalizeNewlines converts CRLF and lone CR line endings to LF, mirroring
// Python's universal-newline read_text. Views used for parsing (frontmatter,
// frozen markers, dates) go through this; the original file bytes are left
// untouched for later splicing.
func NormalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

var (
	fmRe        = regexp.MustCompile(`(?s)^---\n(.*?)\n---`)
	nameRe      = regexp.MustCompile(`(?m)^name:\s*(.+)$`)
	metaStartRe = regexp.MustCompile(`(?m)^metadata:[ \t]*$`)

	// flat metadata key: exactly two leading whitespace chars, then key: value
	flatKeyRe       = regexp.MustCompile(`^[ \t]{2}([A-Za-z0-9_.-]+):\s*(.+)$`)
	inlineCommentRe = regexp.MustCompile(`\s+#.*$`)

	// nested block starts (two-space indent): "pins:", "sources:", "frozen:"
	twoSpaceKeyRe = regexp.MustCompile(`^[ \t]{2}([A-Za-z0-9_.-]+):[ \t]*$`)
	// nested 4-space scalar lines: "  base_url: ..."
	fourSpaceKeyRe = regexp.MustCompile(`^[ \t]{4}([A-Za-z0-9_.-]+):\s*(.+)$`)

	pinKindRe = regexp.MustCompile(`kind:\s*(\w+)`)
	pinRepoRe = regexp.MustCompile(`repo:\s*([A-Za-z0-9_./-]+)`)
	pinVerRe  = regexp.MustCompile(`version:\s*([^\s,}]+)`)

	inlinePagesRe = regexp.MustCompile(`^[ \t]{4}pages:\s*\[(.*?)\]`)
	dateRe        = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)

	fenceRe       = regexp.MustCompile("^(`{3,}|~{3,})")
	frozenOpenRe  = regexp.MustCompile(`^[ \t]*<!--\s*frozen:([A-Za-z0-9_.-]+)\s*-->[ \t]*$`)
	frozenCloseRe = regexp.MustCompile(`^[ \t]*<!--\s*/frozen:([A-Za-z0-9_.-]+)\s*-->[ \t]*$`)
)

// ParseFrontmatter reads and parses a skill's SKILL.md frontmatter.
// It mirrors the script's behavior: when the file has no frontmatter block,
// defaults are returned.
func ParseFrontmatter(skPath string) Config {
	dir := filepath.Dir(skPath)
	cfg := DefaultConfig(dir)
	txt, err := os.ReadFile(skPath)
	if err != nil {
		return cfg
	}
	txt = []byte(NormalizeNewlines(string(txt)))
	m := fmRe.FindSubmatch(txt)
	if m == nil {
		return cfg
	}
	fm := string(m[1])

	if nm := nameRe.FindStringSubmatch(fm); nm != nil {
		cfg.SkillName = strings.Trim(strings.TrimSpace(nm[1]), "\"'")
	}

	// metadata region: lines after the `metadata:` line up to the first line
	// that starts with a non-whitespace character.
	lines := strings.Split(fm, "\n")
	metaStart := -1
	for i, ln := range lines {
		if metaStartRe.MatchString(ln) {
			metaStart = i
			break
		}
	}
	if metaStart < 0 {
		return cfg
	}
	metaEnd := metaStart + 1
	for metaEnd < len(lines) {
		ln := lines[metaEnd]
		if ln != "" && !unicode.IsSpace(rune(ln[0])) {
			break
		}
		metaEnd++
	}
	metaLines := lines[metaStart+1 : metaEnd]

	// flat key/value pairs
	flat := map[string]string{}
	var flatOrder []string
	for _, ln := range metaLines {
		mm := flatKeyRe.FindStringSubmatch(ln)
		if mm == nil {
			continue
		}
		k, v := mm[1], mm[2]
		v = strings.TrimSpace(v)
		if !strings.HasPrefix(v, `"`) && !strings.HasPrefix(v, `'`) {
			v = inlineCommentRe.ReplaceAllString(v, "")
		}
		v = strings.Trim(v, "\"'")
		if _, ok := flat[k]; !ok {
			flatOrder = append(flatOrder, k)
		}
		flat[k] = v
	}

	// pins: flat upkeep-pins first, then nested pins: block
	if v, ok := flat["upkeep-pins"]; ok {
		cfg.Pins = parseFlatPins(v)
	}
	if idx, ok := nestedBlockStart(metaLines, "pins"); ok {
		for _, ln := range metaLines[idx+1:] {
			if isSiblingStart(ln) {
				break
			}
			s := strings.TrimSpace(ln)
			if s == "" || strings.HasPrefix(s, "#") || !strings.Contains(s, ":") {
				continue
			}
			key, val, _ := strings.Cut(s, ":")
			key = strings.Trim(strings.TrimSpace(key), "\"' ")
			val = strings.Trim(strings.TrimSpace(val), "\"' ")
			if hasPin(cfg.Pins, key) {
				continue
			}
			if strings.HasPrefix(val, "{") {
				kind, repo, ver := "npm", "", ""
				if km := pinKindRe.FindStringSubmatch(val); km != nil {
					kind = km[1]
				}
				if rm := pinRepoRe.FindStringSubmatch(val); rm != nil {
					repo = rm[1]
				}
				if vm := pinVerRe.FindStringSubmatch(val); vm != nil {
					ver = strings.Trim(vm[1], "\"' ")
				}
				cfg.Pins = append(cfg.Pins, Pin{Name: key, Version: ver, Kind: kind, Repo: repo})
			} else {
				cfg.Pins = append(cfg.Pins, Pin{Name: key, Version: val, Kind: "npm"})
			}
		}
	}

	// sources block (parsed for later phases; check does not use it)
	srcLines := []string{}
	if idx, ok := nestedBlockStart(metaLines, "sources"); ok {
		for _, ln := range metaLines[idx+1:] {
			if isSiblingStart(ln) {
				break
			}
			srcLines = append(srcLines, ln)
		}
	}
	srcStr := strings.Join(srcLines, "\n")
	pages, pagesDeclared := srcPages(flat, srcStr)
	cfg.Sources = Sources{
		BaseURL:       srcScalar(flat, "upkeep-source-base", srcStr, "base_url", ""),
		Ext:           srcScalar(flat, "upkeep-source-ext", srcStr, "ext", ".md"),
		Cache:         srcScalar(flat, "upkeep-source-cache", srcStr, "cache_name", cfg.SkillName+"-docs"),
		IndexURL:      srcScalar(flat, "upkeep-source-index", srcStr, "index_url", ""),
		Pages:         pages,
		PagesDeclared: pagesDeclared,
	}
	ttl := srcScalar(flat, "upkeep-source-ttl", srcStr, "ttl_minutes", "720")
	if n, err := strconv.Atoi(ttl); err == nil {
		cfg.Sources.TTLMinutes = n
	} else {
		cfg.Sources.TTLMinutes = 720
	}
	if cfg.Sources.Ext != "" && !strings.HasPrefix(cfg.Sources.Ext, ".") {
		cfg.Sources.Ext = "." + cfg.Sources.Ext
	}

	// frozen: flat upkeep-frozen-<id> keys first, then nested frozen: block
	for _, k := range flatOrder {
		if strings.HasPrefix(k, "upkeep-frozen-") {
			cfg.Frozen = append(cfg.Frozen, FrozenEntry{ID: k[len("upkeep-frozen-"):], Hash: flat[k]})
		}
	}
	if idx, ok := nestedBlockStart(metaLines, "frozen"); ok {
		seen := cfg.FrozenMap()
		for _, ln := range metaLines[idx+1:] {
			if isSiblingStart(ln) {
				break
			}
			s := strings.TrimSpace(ln)
			if s == "" || strings.HasPrefix(s, "#") || !strings.Contains(s, ":") {
				continue
			}
			fk, fv, _ := strings.Cut(s, ":")
			fk = strings.Trim(strings.TrimSpace(fk), "\"' ")
			if _, exists := seen[fk]; exists {
				continue
			}
			fv = strings.Trim(strings.TrimSpace(fv), "\"' ")
			cfg.Frozen = append(cfg.Frozen, FrozenEntry{ID: fk, Hash: fv})
			seen[fk] = fv
		}
	}

	cfg.ThresholdDays = 60
	thr, ok := flat["upkeep-threshold-days"]
	if !ok {
		thr = flat["freshness_threshold_days"]
	}
	if thr == "" {
		thr = "60"
	}
	if n, err := strconv.Atoi(thr); err == nil {
		cfg.ThresholdDays = n
	}
	cfg.Verified = flat["verified"]
	if cfg.Verified == "" {
		cfg.Verified = flat["upkeep-verified"]
	}
	cfg.AutoAdvance = flat["upkeep-auto-advance"]
	return cfg
}

func parseFlatPins(s string) []Pin {
	var out []Pin
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" || !strings.Contains(item, "=") {
			continue
		}
		k, rhs, _ := strings.Cut(item, "=")
		k = strings.TrimSpace(k)
		rhs = strings.TrimSpace(rhs)
		switch {
		case strings.HasPrefix(rhs, "github:"):
			spec := strings.TrimPrefix(rhs, "github:")
			repo, ver, _ := strings.Cut(spec, "@")
			out = append(out, Pin{Name: k, Version: strings.TrimSpace(ver), Kind: "github", Repo: strings.TrimSpace(repo)})
		case strings.HasPrefix(rhs, "manual:"):
			out = append(out, Pin{Name: k, Version: strings.TrimSpace(strings.TrimPrefix(rhs, "manual:")), Kind: "manual"})
		case strings.HasPrefix(rhs, "npm:"):
			out = append(out, Pin{Name: k, Version: strings.TrimSpace(strings.TrimPrefix(rhs, "npm:")), Kind: "npm"})
		default:
			out = append(out, Pin{Name: k, Version: rhs, Kind: "npm"})
		}
	}
	return out
}

func hasPin(pins []Pin, name string) bool {
	for _, p := range pins {
		if p.Name == name {
			return true
		}
	}
	return false
}

// nestedBlockStart finds the first line starting a two-space-indented block
// with the given key (e.g. "pins:"). It returns the line index and true.
func nestedBlockStart(lines []string, key string) (int, bool) {
	for i, ln := range lines {
		mm := twoSpaceKeyRe.FindStringSubmatch(ln)
		if mm != nil && mm[1] == key {
			return i, true
		}
	}
	return -1, false
}

// isSiblingStart reports whether a line begins a sibling metadata key: up to
// two leading whitespace chars followed by a letter or underscore. This is
// the boundary the script's nested-block regexes stop at.
func isSiblingStart(ln string) bool {
	i := 0
	for i < len(ln) && i < 2 && (ln[i] == ' ' || ln[i] == '\t') {
		i++
	}
	if i >= len(ln) {
		return false
	}
	c := ln[i]
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
}

// srcScalar returns the flat metadata value for flatKey if non-empty;
// otherwise the nested 4-space scalar nestedKey within srcStr; otherwise def.
func srcScalar(flat map[string]string, flatKey, srcStr, nestedKey, def string) string {
	if v, ok := flat[flatKey]; ok && v != "" {
		return v
	}
	for _, ln := range strings.Split(srcStr, "\n") {
		mm := fourSpaceKeyRe.FindStringSubmatch(ln)
		if mm == nil || mm[1] != nestedKey {
			continue
		}
		v := mm[2]
		v = strings.TrimSpace(v)
		if strings.HasPrefix(v, "[") || strings.HasPrefix(v, "{") {
			return def
		}
		if !strings.HasPrefix(v, `"`) && !strings.HasPrefix(v, `'`) {
			v = inlineCommentRe.ReplaceAllString(v, "")
		}
		return strings.Trim(v, "\"'")
	}
	return def
}

var pagesBlockStartRe = regexp.MustCompile(`^[ \t]{4}pages:[ \t]*$`)

// srcPages parses the upkeep-source-pages declaration across every form the
// frontmatter parser supports (flat key, nested inline `pages: []`, and
// nested block bullets). It returns the page list and whether the key was
// declared at all — an explicitly empty declaration (flat "" or empty
// inline/block) is declared and retires all pages, while an absent key
// returns declared=false (callers fall back to the sidecar docs keys).
func srcPages(flat map[string]string, srcStr string) ([]string, bool) {
	if v, ok := flat["upkeep-source-pages"]; ok {
		var pages []string
		for _, p := range strings.Split(v, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				pages = append(pages, strings.Trim(p, "\"'"))
			}
		}
		return pages, true
	}
	for _, ln := range strings.Split(srcStr, "\n") {
		if mm := inlinePagesRe.FindStringSubmatch(ln); mm != nil {
			var pages []string
			for _, p := range strings.Split(mm[1], ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					pages = append(pages, strings.Trim(p, "\"'"))
				}
			}
			return pages, true
		}
	}
	// block form: 4-space "pages:" line followed by "- " bullets
	start := -1
	for i, ln := range strings.Split(srcStr, "\n") {
		if pagesBlockStartRe.MatchString(ln) {
			start = i
			break
		}
	}
	if start < 0 {
		return nil, false
	}
	blockLines := strings.Split(srcStr, "\n")[start+1:]
	var pages []string
	for _, ln := range blockLines {
		s := strings.TrimSpace(ln)
		if s == "" {
			continue
		}
		if isFourSpaceSibling(ln) {
			break
		}
		if strings.HasPrefix(s, "- ") {
			pages = append(pages, strings.Trim(strings.TrimSpace(s[2:]), "\"'"))
		}
	}
	return pages, true
}

// isFourSpaceSibling is the pages-block boundary: up to four leading
// whitespace chars followed by a letter or underscore.
func isFourSpaceSibling(ln string) bool {
	i := 0
	for i < len(ln) && i < 4 && (ln[i] == ' ' || ln[i] == '\t') {
		i++
	}
	if i >= len(ln) {
		return false
	}
	c := ln[i]
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
}

// StripFencedBlocks masks fenced code blocks (``` or ~~~ runs) by replacing
// every non-newline byte inside the fence — fence delimiter lines included —
// with a space. The result is byte-for-byte the same length as the input, so
// offsets of content after a fence are preserved.
func StripFencedBlocks(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, len(lines))
	inFence := false
	fenceMarker := ""
	for i, line := range lines {
		s := strings.TrimLeftFunc(line, unicode.IsSpace)
		if m := fenceRe.FindStringSubmatch(s); m != nil {
			marker := m[1]
			if !inFence {
				inFence = true
				fenceMarker = strings.Repeat(string(marker[0]), len(marker))
				out[i] = strings.Repeat(" ", len(line))
				continue
			}
			if strings.HasPrefix(s, fenceMarker) {
				inFence = false
				fenceMarker = ""
				out[i] = strings.Repeat(" ", len(line))
				continue
			}
		}
		if inFence {
			out[i] = strings.Repeat(" ", len(line))
		} else {
			out[i] = line
		}
	}
	return strings.Join(out, "\n")
}

// FrozenBlock is a scanned frozen block.
type FrozenBlock struct {
	ID     string
	Rel    string // "SKILL.md" or "references/<file>"
	Body   string
	Digest string
}

// ScanFrozenBlocks scans a skill directory (SKILL.md plus references/*.md)
// for frozen blocks, computing their hashes. Frontmatter and fenced code
// blocks are ignored, exactly like the script. Blocks are keyed by ID with
// Python dict semantics: the last value wins and the first insertion
// position is kept, across all files.
func ScanFrozenBlocks(skillDir string) ([]FrozenBlock, error) {
	sk := filepath.Join(skillDir, "SKILL.md")
	files := []string{sk}
	if entries, err := os.ReadDir(filepath.Join(skillDir, "references")); err == nil && len(entries) > 0 {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				files = append(files, filepath.Join(skillDir, "references", e.Name()))
			}
		}
		sort.Strings(files[1:])
	}

	order := []string{}
	blocks := map[string]*FrozenBlock{}
	for _, f := range files {
		// Python's scans skip references whose path does not exist
		// (f.exists() follows symlinks), so dangling symlinks are skipped;
		// files that exist but cannot be read still return a structured
		// error.
		if _, err := os.Stat(f); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, &ReadError{Path: f, Err: err}
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, &ReadError{Path: f, Err: err}
		}
		text := NormalizeNewlines(string(raw))
		rel := "SKILL.md"
		if f == sk {
			text = StripFrontmatter(text)
		} else {
			rel = "references/" + filepath.Base(f)
		}
		cleaned := StripFencedBlocks(text)
		lines := strings.Split(cleaned, "\n")
		i := 0
		for i < len(lines) {
			om := frozenOpenRe.FindStringSubmatch(lines[i])
			if om == nil {
				i++
				continue
			}
			fid := om[1]
			// Python's regex \n(.*?)\n never accepts the immediately following
			// line as the closer; it lazily continues to the next eligible
			// closer. So start the closer search at i+2: an adjacent closer is
			// never accepted, and a later one still forms the block.
			closeAt := -1
			for j := i + 2; j < len(lines); j++ {
				cm := frozenCloseRe.FindStringSubmatch(lines[j])
				if cm != nil && cm[1] == fid {
					closeAt = j
					break
				}
			}
			if closeAt < 0 {
				// No eligible closer (only adjacent or none): not a block.
				i++
				continue
			}
			body := strings.Join(lines[i+1:closeAt], "\n")
			body = strings.TrimSpace(body)
			bodyLines := strings.Split(body, "\n")
			for k, bl := range bodyLines {
				bodyLines[k] = strings.TrimRightFunc(bl, unicode.IsSpace)
			}
			body = strings.Join(bodyLines, "\n")
			sum := sha256.Sum256([]byte(body))
			digest := "sha256:" + hex.EncodeToString(sum[:])[:16]
			if _, ok := blocks[fid]; !ok {
				order = append(order, fid)
			}
			// last value wins; first insertion position is kept
			blocks[fid] = &FrozenBlock{ID: fid, Rel: rel, Body: body, Digest: digest}
			i = closeAt + 1
		}
	}
	out := make([]FrozenBlock, 0, len(order))
	for _, fid := range order {
		out = append(out, *blocks[fid])
	}
	return out, nil
}

// StripFrontmatter removes a leading YAML frontmatter block, mirroring the
// script's re.sub(r'^---\n.*?\n---\n?', ”, raw, count=1, flags=re.S).
func StripFrontmatter(text string) string {
	m := fmRe.FindStringSubmatch(text)
	if m == nil {
		return text
	}
	// m[0] is "---\n...\n---"; also drop one optional trailing newline.
	rest := text[len(m[0]):]
	rest = strings.TrimPrefix(rest, "\n")
	return rest
}
