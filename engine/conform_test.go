package engine_test

import "strings"

// mapGoLines normalizes Go-only verdict and item-kind tokens to their shell
// script equivalents for conformance comparison. There are two documented
// Go-only classes (the script never reads sidecars): CONTENT-LAG maps to
// STALE per decision P1 — the CONTENT-LAG pin item and verdict line map
// onto their STALE counterparts, with the suffix text identical by
// construction — and DRIFT items make Go's verdict STALE where the script
// reports FRESH, which the fixture matrix avoids by never giving a
// script-compared fixture a drifting sidecar state. Every other line must
// be byte-identical.
//
// This is the one comparator shared by the fixture conformance test and the
// UPKEEP_LIVE=1 repo-skills test.
func mapGoLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "CONTENT-LAG pin:"):
			out[i] = "STALE    " + l[len("CONTENT-LAG "):]
		case strings.HasPrefix(l, "CONTENT-LAG page:"):
			// Go-only (the frozen script never reads sidecars). Mapped so a
			// mixed pin+page lag fixture compares line-for-line on the lines
			// both implementations produce.
			out[i] = "STALE    " + l[len("CONTENT-LAG "):]
		case strings.HasPrefix(l, "VERDICT: CONTENT-LAG "):
			out[i] = "VERDICT: STALE" + l[len("VERDICT: CONTENT-LAG"):]
		default:
			out[i] = l
		}
	}
	return out
}
