// Package source resolves current upstream versions for pins exactly like the
// script: by shelling out to `npm` and `git` found through PATH, with the
// same 15s timeout and the same failure behavior (any failure yields an
// empty current version, which the check renders as UNKNOWN). No HTTP client
// is used.
package source

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/KHAEntertainment/self-updating-skills/internal/skill"
)

// lookupTimeout mirrors the script's timeout=15 on every subprocess call.
const lookupTimeout = 15 * time.Second

var (
	shaPinRe    = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	semverTagRe = regexp.MustCompile(`^v?[0-9]+\.[0-9]+`)
	leadingVRe  = regexp.MustCompile(`^v`)
	verSplitRe  = regexp.MustCompile(`[._-]+`)
	verRunRe    = regexp.MustCompile(`[0-9]+|[^0-9]+`)
)

// Part classes, in sort order (the script's ver_parts uses the same numbers).
const (
	partPre   = 0 // pre-release word: dev < a < b < rc
	partEnd   = 1 // end of the version
	partNum   = 2 // number
	partPost  = 3 // post-release word
	partOther = 4 // any other word, above every number
)

var preRank = map[string]int{"dev": 0, "a": 1, "alpha": 1, "b": 2, "beta": 2, "c": 3, "rc": 3, "pre": 3, "preview": 3}

var postWords = map[string]bool{"post": true, "rev": true, "r": true}

// ResolveCurrent returns the current upstream version for a pin, or ""
// when the lookup fails (mirroring the script's UNKNOWN path).
func ResolveCurrent(ctx context.Context, pin skill.Pin) string {
	cur, _ := ResolveCurrentE(ctx, pin)
	return cur
}

// ResolveCurrentE is ResolveCurrent with the lookup error surfaced, so the
// updater can distinguish a failed poll from an unchanged one.
func ResolveCurrentE(ctx context.Context, pin skill.Pin) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()

	switch pin.Kind {
	case "github":
		repo := pin.Repo
		if repo == "" {
			repo = pin.Name
		}
		url := "https://github.com/" + repo + ".git"
		if shaPinRe.MatchString(pin.Version) {
			out, err := run(cctx, "git", "ls-remote", url, "HEAD")
			if err != nil {
				return "", err
			}
			fields := strings.Fields(out)
			if len(fields) == 0 {
				return "", fmt.Errorf("git ls-remote returned no HEAD")
			}
			cur := fields[0]
			if l := len(pin.Version); l < len(cur) {
				cur = cur[:l]
			}
			return cur, nil
		}
		out, err := run(cctx, "git", "ls-remote", "--tags", "--refs", url)
		if err != nil {
			return "", err
		}
		var tags []string
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "refs/tags/") {
				parts := strings.Split(line, "refs/tags/")
				tags = append(tags, strings.TrimSpace(parts[len(parts)-1]))
			}
		}
		var semver []string
		for _, t := range tags {
			if semverTagRe.MatchString(t) {
				semver = append(semver, t)
			}
		}
		useTags := semver
		if len(useTags) == 0 {
			useTags = tags
		}
		if len(useTags) == 0 {
			return "", fmt.Errorf("no tags found for %s", url)
		}
		sort.SliceStable(useTags, func(i, j int) bool { return VerCmp(useTags[i], useTags[j]) < 0 })
		return leadingVRe.ReplaceAllString(useTags[len(useTags)-1], ""), nil
	default:
		out, err := run(cctx, "npm", "view", pin.Name, "version")
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(out), nil
	}
}

// run executes a command via PATH lookup, discards stderr (like the script's
// stderr=subprocess.DEVNULL), and returns stdout.
func run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = io.Discard
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

type verPart struct {
	class int
	rank  int    // pre-release rank
	num   string // digits without leading zeros
	s     string // word
}

// VerCmp compares two version strings the way the script's ver_cmp does:
// leading "v" stripped, split on [._-] and on digit/letter boundaries,
// numbers compared numerically, a pre-release word (dev, a, b, rc) below the
// release it precedes, a post-release word (post, rev, r) above it, any
// other word above every number, and a shorter equal prefix first
// (1.0.0rc1 < 1.0.0 < 1.0.0.post1; 1.2 < 1.2.0). Only the first word is a
// pre/post marker; later words are identifiers inside it, so
// alpha < alpha.1 < alpha.beta as SemVer requires.
func VerCmp(a, b string) int {
	pa := verParts(a)
	pb := verParts(b)
	n := len(pa)
	if len(pb) < n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		if c := cmpPart(pa[i], pb[i]); c != 0 {
			return c
		}
	}
	if len(pa) < len(pb) {
		return -1
	}
	if len(pa) > len(pb) {
		return 1
	}
	return 0
}

func cmpPart(x, y verPart) int {
	if x.class != y.class {
		if x.class < y.class {
			return -1
		}
		return 1
	}
	switch x.class {
	case partPre:
		return cmpInt(x.rank, y.rank)
	case partNum:
		if c := cmpInt(len(x.num), len(y.num)); c != 0 {
			return c
		}
		return strings.Compare(x.num, y.num)
	case partOther:
		return strings.Compare(x.s, y.s)
	}
	return 0
}

func cmpInt(x, y int) int {
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}

func verParts(v string) []verPart {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.TrimPrefix(v, "v")
	var out []verPart
	seenWord := false
	for _, tok := range verSplitRe.Split(v, -1) {
		if tok == "" {
			out = append(out, verPart{class: partOther})
			continue
		}
		for _, p := range verRunRe.FindAllString(tok, -1) {
			if p[0] >= '0' && p[0] <= '9' {
				out = append(out, verPart{class: partNum, num: strings.TrimLeft(p, "0")})
				continue
			}
			if seenWord {
				out = append(out, verPart{class: partOther, s: p})
				continue
			}
			seenWord = true
			if r, ok := preRank[p]; ok {
				out = append(out, verPart{class: partPre, rank: r})
			} else if postWords[p] {
				out = append(out, verPart{class: partPost})
			} else {
				out = append(out, verPart{class: partOther, s: p})
			}
		}
	}
	return append(out, verPart{class: partEnd})
}
