// The lossless renderer: splice generated-region bodies and renderer-owned
// frontmatter keys into the raw SKILL.md bytes, preserving every other
// byte (line endings, trailing newline, author prose).
package render

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/KHAEntertainment/self-updating-skills/internal/skill"
)

var ownedKeyLineRe = regexp.MustCompile(`^[ \t]{2}(upkeep-pins|upkeep-verified):[ \t]*(.*)\r?$`)

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
// that is present but no longer backed by sidecar data). Marker/sidecar
// validation errors are returned as errors: check must not guess.
func DriftTargets(raw []byte, sc *Sidecar) ([]string, error) {
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
	eol := skill.FileEOL(raw)
	var targets []string
	for _, r := range regions {
		if !IsRegionID(r.ID) {
			return nil, fmt.Errorf("unknown generated region %q (built-ins: pins, freshness, redirects)", r.ID)
		}
		body, err := RegionBody(r.ID, sc)
		if err != nil {
			return nil, err
		}
		want := toEOL(body, eol) + eol
		if string(raw[r.BodyStart:r.BodyEnd]) != want {
			targets = append(targets, "region:"+r.ID)
		}
	}
	// Owned frontmatter keys: current value must equal the desired value
	// ("" means the key must not exist at all).
	current := ownedKeyValues(raw)
	for _, k := range ownedKeyState(sc) {
		if current[k.Key] != k.Value {
			targets = append(targets, "frontmatter:"+k.Key)
		}
	}
	return targets, nil
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
// from the frontmatter (unquoted), keyed by key name; a key missing from
// the file maps to "".
func ownedKeyValues(raw []byte) map[string]string {
	out := map[string]string{"upkeep-pins": "", "upkeep-verified": ""}
	fmEnd, ok := skill.FrontmatterEnd(raw)
	if !ok {
		return out
	}
	for _, ln := range strings.Split(string(raw[:fmEnd]), "\n") {
		if m := ownedKeyLineRe.FindStringSubmatch(strings.TrimSuffix(ln, "\r")); m != nil {
			v := strings.TrimSpace(m[2])
			v = strings.TrimSuffix(strings.TrimPrefix(v, "\""), "\"")
			v = strings.TrimSuffix(strings.TrimPrefix(v, "'"), "'")
			out[m[1]] = v
		}
	}
	return out
}

// hasOwnedKeyLine reports whether the frontmatter carries any owned key
// line (so Build rewrites the frontmatter even when the sidecar has no
// pins: an owned key with no backing data is removed).
func hasOwnedKeyLine(raw []byte) bool {
	fmEnd, ok := skill.FrontmatterEnd(raw)
	if !ok {
		return false
	}
	for _, ln := range strings.Split(string(raw[:fmEnd]), "\n") {
		if ownedKeyLineRe.MatchString(strings.TrimSuffix(ln, "\r")) {
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
