package engine_test

import "strings"

// mapGoLines normalizes Go-only verdict and item-kind tokens to their shell
// script equivalents for conformance comparison. Per decision P1 the frozen
// script shows STALE exactly where the Go engine shows CONTENT-LAG, so the
// CONTENT-LAG pin item and verdict line map onto their STALE counterparts
// (the suffix text is identical by construction). Every other line must be
// byte-identical.
//
// This is the one comparator shared by the fixture conformance test and the
// UPKEEP_LIVE=1 repo-skills test.
func mapGoLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "CONTENT-LAG pin:"):
			out[i] = "STALE    " + l[len("CONTENT-LAG "):]
		case strings.HasPrefix(l, "VERDICT: CONTENT-LAG "):
			out[i] = "VERDICT: STALE" + l[len("VERDICT: CONTENT-LAG"):]
		default:
			out[i] = l
		}
	}
	return out
}
