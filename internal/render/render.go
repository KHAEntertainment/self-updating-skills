// The lossless renderer: splice generated-region bodies and renderer-owned
// frontmatter keys into the raw SKILL.md bytes, preserving every other
// byte (line endings, trailing newline, author prose).
package render

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/KHAEntertainment/self-updating-skills/internal/skill"
)

var (
	ownedKeyLineRe = regexp.MustCompile(`^[ \t]{2}(upkeep-pins|upkeep-verified):[ \t]*(.*)\r?$`)
	metadataLineRe = regexp.MustCompile(`^metadata:[ \t]*\r?$`)
)

// isSpaceByte reports whether c is a space or tab.
func isSpaceByte(c byte) bool { return c == ' ' || c == '\t' }

// Build renders the new SKILL.md bytes from raw and the sidecar. It fails
// (and the caller writes nothing) on: sidecar fields failing sanitization,
// malformed generated markers, unknown region ids, regions nesting or
// overlapping frozen blocks.
func Build(raw []byte, sc *Sidecar) ([]byte, error) {
	if err := sc.Validate(); err != nil {
		return nil, err
	}
	regions, err := skill.ScanGeneratedRegions(raw)
	if err != nil {
		return nil, err
	}
	frozen, err := skill.ScanFrozenSpans(raw)
	if err != nil {
		return nil, err
	}
	if err := skill.CheckRegionOverlap(regions, frozen); err != nil {
		return nil, err
	}
	for _, r := range regions {
		if !IsRegionID(r.ID) {
			return nil, fmt.Errorf("unknown generated region %q (built-ins: pins, freshness, redirects)", r.ID)
		}
	}

	eol := skill.FileEOL(raw)
	out := raw
	// Splice from the last region backwards so earlier offsets stay valid.
	for i := len(regions) - 1; i >= 0; i-- {
		r := regions[i]
		body, err := RegionBody(r.ID, sc)
		if err != nil {
			return nil, err
		}
		repl := []byte(toEOL(body, eol) + eol)
		out = append(out[:r.BodyStart:r.BodyStart],
			append(repl, out[r.BodyEnd:]...)...)
	}
	keys := ownedKeyState(sc)
	if keys[0].Value != "" || keys[1].Value != "" || hasOwnedKeyLine(raw) {
		out, err = skill.SetFlatKeys(out, eol, keys, nil)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// DriftTargets compares the on-disk SKILL.md against what the renderer
// would produce and names every part that differs: "region:<id>" for a
// generated region whose body differs from the sidecar and
// "frontmatter:<key>" for a renderer-owned key with a stale value (or one
// that is present but no longer backed by sidecar data).
//
// markerErr reports malformed generated markers (unclosed, nested, stray,
// duplicated, unknown, or overlapping frozen blocks): a DRIFT condition
// that check reports as an item, never a hard error — but render refuses
// such a file, so nothing may prescribe "run: upkeep render" for it. err
// is reserved for sidecar validation failures, which check surfaces as
// structured errors.
func DriftTargets(raw []byte, sc *Sidecar) (targets []string, markerErr error, err error) {
	if err := sc.Validate(); err != nil {
		return nil, nil, err
	}
	regions, err := skill.ScanGeneratedRegions(raw)
	if err != nil {
		return nil, err, nil
	}
	frozen, err := skill.ScanFrozenSpans(raw)
	if err != nil {
		return nil, err, nil
	}
	if err := skill.CheckRegionOverlap(regions, frozen); err != nil {
		return nil, err, nil
	}
	eol := skill.FileEOL(raw)
	for _, r := range regions {
		if !IsRegionID(r.ID) {
			return nil, fmt.Errorf("unknown generated region %q (built-ins: pins, freshness, redirects)", r.ID), nil
		}
		body, err := RegionBody(r.ID, sc)
		if err != nil {
			return nil, err, nil
		}
		want := toEOL(body, eol) + eol
		if string(raw[r.BodyStart:r.BodyEnd]) != want {
			targets = append(targets, "region:"+r.ID)
		}
	}
	// Owned frontmatter keys: read from the metadata block only, with a
	// presence flag, so check agrees with Build in every case — an absent
	// key and an empty one are different states.
	current := ownedKeyValues(raw)
	for _, k := range ownedKeyState(sc) {
		cur := current[k.Key]
		match := cur.present && k.Value != "" && cur.value == k.Value
		if !match && !(cur.present == false && k.Value == "") {
			targets = append(targets, "frontmatter:"+k.Key)
		}
	}
	return targets, nil, nil
}

// ownedKey is one renderer-owned frontmatter key: its current value and
// whether the key is present at all.
type ownedKey struct {
	value   string
	present bool
}

// ownedKeyState returns both renderer-owned frontmatter keys with their
// desired values from the sidecar; an empty value means the key must not
// exist in the frontmatter.
func ownedKeyState(sc *Sidecar) []skill.FlatKey {
	pins := ""
	if len(sc.Pins) > 0 {
		pins = sc.PinString()
	}
	return []skill.FlatKey{
		{Key: "upkeep-pins", Value: pins},
		{Key: "upkeep-verified", Value: sc.VerifiedDate()},
	}
}

// ownedKeyValues reads the current values of the renderer-owned flat keys
// from the metadata block only (the same scope the writer uses), keyed by
// key name; a key missing from the file has present=false. Values written
// by SetFlatKeys with %q are unquoted via strconv.Unquote, falling back to
// the raw value.
func ownedKeyValues(raw []byte) map[string]ownedKey {
	out := map[string]ownedKey{
		"upkeep-pins":     {},
		"upkeep-verified": {},
	}
	fmEnd, ok := skill.FrontmatterEnd(raw)
	if !ok {
		return out
	}
	inMeta := false
	for _, ln := range strings.Split(string(raw[:fmEnd]), "\n") {
		ln = strings.TrimSuffix(ln, "\r")
		if !inMeta {
			if metadataLineRe.MatchString(ln) {
				inMeta = true
			}
			continue
		}
		if ln != "" && !isSpaceByte(ln[0]) {
			break // metadata block ended
		}
		if m := ownedKeyLineRe.FindStringSubmatch(ln); m != nil {
			v := strings.TrimSpace(m[2])
			if u, err := strconv.Unquote(v); err == nil {
				v = u
			}
			out[m[1]] = ownedKey{value: v, present: true}
		}
	}
	return out
}

// hasOwnedKeyLine reports whether the metadata block carries any owned key
// line (so Build rewrites the frontmatter even when the sidecar has no
// pins: an owned key with no backing data is removed).
func hasOwnedKeyLine(raw []byte) bool {
	for _, k := range ownedKeyValues(raw) {
		if k.present {
			return true
		}
	}
	return false
}

// toEOL converts LF line breaks in body to the file's line ending.
func toEOL(body, eol string) string {
	if eol == "\n" || body == "" {
		return body
	}
	return strings.ReplaceAll(body, "\n", eol)
}
