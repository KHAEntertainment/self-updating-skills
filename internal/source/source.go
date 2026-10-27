// Package source resolves current upstream versions for pins exactly like the
// script: by shelling out to `npm` and `git` found through PATH, with the
// same 15s timeout and the same failure behavior (any failure yields an
// empty current version, which the check renders as UNKNOWN). No HTTP client
// is used.
package source

import (
	"context"
	"io"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/KHAEntertainment/self-updating-skills/internal/skill"
)

// lookupTimeout mirrors the script's timeout=15 on every subprocess call.
const lookupTimeout = 15 * time.Second

var (
	shaPinRe    = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	semverTagRe = regexp.MustCompile(`^v?\d+\.\d+`)
	leadingVRe  = regexp.MustCompile(`^v`)
	verSplitRe  = regexp.MustCompile(`[._-]+`)
)

// ResolveCurrent returns the current upstream version for a pin, or ""
// when the lookup fails (mirroring the script's UNKNOWN path).
func ResolveCurrent(ctx context.Context, pin skill.Pin) string {
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
			out, ok := run(cctx, "git", "ls-remote", url, "HEAD")
			if !ok {
				return ""
			}
			fields := strings.Fields(out)
			if len(fields) == 0 {
				return ""
			}
			cur := fields[0]
			if l := len(pin.Version); l < len(cur) {
				cur = cur[:l]
			}
			return cur
		}
		out, ok := run(cctx, "git", "ls-remote", "--tags", "--refs", url)
		if !ok {
			return ""
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
			return ""
		}
		sort.SliceStable(useTags, func(i, j int) bool { return VerCmp(useTags[i], useTags[j]) < 0 })
		return leadingVRe.ReplaceAllString(useTags[len(useTags)-1], "")
	default:
		out, ok := run(cctx, "npm", "view", pin.Name, "version")
		if !ok {
			return ""
		}
		return strings.TrimSpace(out)
	}
}

// run executes a command via PATH lookup, discards stderr (like the script's
// stderr=subprocess.DEVNULL), and returns stdout.
func run(ctx context.Context, name string, args ...string) (string, bool) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = io.Discard
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

type verPart struct {
	digit bool
	n     int
	s     string
}

// VerCmp compares two version strings the way the script's ver_cmp does:
// leading "v" stripped, split on [._-], numeric parts compared numerically,
// numeric parts sort before non-numeric ones, and a shorter equal prefix
// sorts first.
func VerCmp(a, b string) int {
	pa := verParts(a)
	pb := verParts(b)
	n := len(pa)
	if len(pb) < n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		x, y := pa[i], pb[i]
		if x == y {
			continue
		}
		if x.digit && y.digit {
			if x.n < y.n {
				return -1
			}
			return 1
		}
		if x.digit != y.digit {
			if x.digit {
				return -1
			}
			return 1
		}
		if x.s < y.s {
			return -1
		}
		return 1
	}
	if len(pa) < len(pb) {
		return -1
	}
	if len(pa) > len(pb) {
		return 1
	}
	return 0
}

func verParts(v string) []verPart {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.TrimPrefix(v, "v")
	parts := verSplitRe.Split(v, -1)
	out := make([]verPart, 0, len(parts))
	for _, p := range parts {
		if n, err := strconv.Atoi(p); err == nil && p != "" {
			out = append(out, verPart{digit: true, n: n})
		} else {
			out = append(out, verPart{s: p})
		}
	}
	return out
}
