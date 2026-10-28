// Built-in region templates (architecture D5: no user-supplied templates in
// v1) and the sanitization guards for anything rendered from the sidecar
// (D10: only bounded row fields are rendered, never raw doc text).
package render

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"
)

// Built-in region ids.
const (
	RegionPins      = "pins"
	RegionFreshness = "freshness"
	RegionRedirects = "redirects"
)

// IsRegionID reports whether id is a built-in region kind.
func IsRegionID(id string) bool {
	switch id {
	case RegionPins, RegionFreshness, RegionRedirects:
		return true
	}
	return false
}

// maxFieldLen is the bound for any sidecar field rendered into a region
// (sanitization rule 1: old/new are at most 120 characters; the same bound
// is applied to every rendered field as a guard). The limit counts
// characters (runes), not bytes.
const maxFieldLen = 120

// validateField rejects a sidecar field that must never reach a skill file:
// too long, multi-line, or containing region/frozen markers or a run of
// three or more backticks.
func validateField(what, s string) error {
	if n := utf8.RuneCountInString(s); n > maxFieldLen {
		return fmt.Errorf("%s: %d characters exceeds the %d-character limit", what, n, maxFieldLen)
	}
	if strings.ContainsAny(s, "\n\r") {
		return fmt.Errorf("%s: contains a line break", what)
	}
	for _, bad := range []string{"<!--", "-->", "```"} {
		if strings.Contains(s, bad) {
			return fmt.Errorf("%s: contains %q", what, bad)
		}
	}
	return nil
}

// cell escapes a plain table cell: pipes would break the table.
func cell(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}

// codeSpan renders s as a CommonMark code span with the content contained:
// the delimiter run is one backtick longer than the longest backtick run
// inside s, and space padding is added when the content begins or ends
// with a backtick (sanitization rule 1; validation caps interior runs at
// two). Pipes are escaped so the cell cannot break the table.
func codeSpan(s string) string {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	delim := strings.Repeat("`", longest+1)
	pad := ""
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		pad = " "
	}
	return delim + pad + strings.ReplaceAll(s, "|", `\|`) + pad + delim
}

// Validate checks every field the renderer would output. Render refuses to
// run (and writes nothing) when any field fails.
func (sc *Sidecar) Validate() error {
	for _, n := range sc.PinNames() {
		p := sc.Pins[n]
		for _, f := range []struct {
			what, val string
		}{
			{"pin " + n + " name", n},
			{"pin " + n + " kind", p.Kind},
			{"pin " + n + " repo", p.Repo},
			{"pin " + n + " verified_for", p.VerifiedFor},
			{"pin " + n + " latest_seen", p.LatestSeen},
			{"pin " + n + " checked_at", p.CheckedAt},
			{"pin " + n + " verified_at", p.VerifiedAt},
		} {
			if err := validateField(f.what, f.val); err != nil {
				return err
			}
		}
	}
	for n, d := range sc.Docs {
		for _, f := range []struct {
			what, val string
		}{
			{"doc " + n + " name", n},
			{"doc " + n + " verified_sha", d.VerifiedSHA},
			{"doc " + n + " verified_at", d.VerifiedAt},
		} {
			if err := validateField(f.what, f.val); err != nil {
				return err
			}
		}
	}
	for _, r := range sc.Redirects {
		for _, f := range []struct {
			what, val string
		}{
			{"redirect " + r.ID + " id", r.ID},
			{"redirect " + r.ID + " old", r.Old},
			{"redirect " + r.ID + " new", r.New},
			{"redirect " + r.ID + " since", r.Since},
			{"redirect " + r.ID + " source", r.Source},
		} {
			if err := validateField(f.what, f.val); err != nil {
				return err
			}
		}
		if r.Source != "" {
			// Sanitization rule 2: source must be an https URL (the host
			// allowlist is deferred to the drafting phase, Issue #11).
			u, err := url.Parse(r.Source)
			if err != nil || u.Scheme != "https" || u.Host == "" {
				return fmt.Errorf("redirect %s source: %q is not an https URL", r.ID, r.Source)
			}
		}
	}
	return nil
}

// RegionBody renders the LF body for one built-in region id, WITHOUT a
// trailing newline: the splice writes the body plus the file's line ending
// (an empty body becomes a single blank line between the markers).
func RegionBody(id string, sc *Sidecar) (string, error) {
	switch id {
	case RegionPins:
		var lines []string
		lines = append(lines, "| package | verified for | latest seen | checked |", "| --- | --- | --- | --- |")
		for _, n := range sc.PinNames() {
			p := sc.Pins[n]
			lines = append(lines, fmt.Sprintf("| %s | %s | %s | %s |", cell(n), cell(p.VerifiedFor), cell(p.LatestSeen), cell(p.CheckedAt)))
		}
		return strings.Join(lines, "\n"), nil
	case RegionFreshness:
		var lines []string
		for _, n := range sc.PinNames() {
			p := sc.Pins[n]
			if p.VerifiedAt == "" {
				continue
			}
			lines = append(lines, fmt.Sprintf("Verified %s for %s@%s", p.VerifiedAt, n, p.VerifiedFor))
		}
		if len(lines) == 0 {
			return "", nil
		}
		return strings.Join(lines, "\n"), nil
	case RegionRedirects:
		var lines []string
		lines = append(lines, "| stale habit → current | since | source |", "| --- | --- | --- |")
		rows := append([]Redirect(nil), sc.Redirects...)
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].Since != rows[j].Since {
				return rows[i].Since < rows[j].Since
			}
			return rows[i].ID < rows[j].ID
		})
		for _, r := range rows {
			lines = append(lines, fmt.Sprintf("| %s → %s | %s | %s |", codeSpan(r.Old), codeSpan(r.New), cell(r.Since), codeSpan(r.Source)))
		}
		return strings.Join(lines, "\n"), nil
	default:
		return "", fmt.Errorf("unknown generated region %q (built-ins: pins, freshness, redirects)", id)
	}
}
