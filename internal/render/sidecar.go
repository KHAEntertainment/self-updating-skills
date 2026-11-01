// Package render implements the upkeep.data.json sidecar, the built-in
// region templates and the lossless, byte-stable renderer for SKILL.md
// generated regions (architecture D5, D7, D10, P1, P4).
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SidecarName is the file name of the data sidecar, next to SKILL.md.
const SidecarName = "upkeep.data.json"

// PinData is one version pin in the sidecar.
type PinData struct {
	Kind        string `json:"kind,omitempty"`
	Repo        string `json:"repo,omitempty"` // github pins only
	VerifiedFor string `json:"verified_for"`
	LatestSeen  string `json:"latest_seen,omitempty"`
	CheckedAt   string `json:"checked_at,omitempty"`
	VerifiedAt  string `json:"verified_at,omitempty"`
}

// DocData is one tracked doc page in the sidecar. VerifiedSHA is the
// snapshot the content was verified against (moved only by an explicit
// seed or an advance); LatestSHA is the newest fetched snapshot, recorded
// by every successful poll — the docs analogue of PinData.LatestSeen
// (decision P1), and the field `check` compares for CONTENT-LAG.
type DocData struct {
	VerifiedSHA string `json:"verified_sha,omitempty"`
	LatestSHA   string `json:"latest_sha,omitempty"`
	VerifiedAt  string `json:"verified_at,omitempty"`
}

// Redirect is one stale-habit → current-equivalent row.
type Redirect struct {
	ID       string   `json:"id"`
	Old      string   `json:"old"`
	New      string   `json:"new"`
	Kind     string   `json:"kind,omitempty"`
	Since    string   `json:"since,omitempty"`
	Source   string   `json:"source,omitempty"`
	Accepted string   `json:"accepted,omitempty"`
	Checks   []string `json:"checks,omitempty"`
	Evidence string   `json:"evidence,omitempty"`
}

// Sidecar is the parsed upkeep.data.json. It is the single source the
// renderer reads; SKILL.md is derived from it.
type Sidecar struct {
	Schema    int                `json:"schema"`
	Owner     string             `json:"owner,omitempty"`
	Pins      map[string]PinData `json:"pins,omitempty"`
	Docs      map[string]DocData `json:"docs,omitempty"`
	Redirects []Redirect         `json:"redirects,omitempty"`
}

// LoadSidecar reads the sidecar in dir. A missing file returns (nil, nil):
// the skill then has no sidecar and Phase 1 behaviour applies exactly. A
// present sidecar is decoded strictly — unknown keys at any nesting level
// and any schema other than 1 are rejected with an error naming the
// problem — so a malformed sidecar can never be silently rewritten
// (Canonical() would drop undeclared keys, which is data loss).
func LoadSidecar(dir string) (*Sidecar, error) {
	path := filepath.Join(dir, SidecarName)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var sc Sidecar
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&sc); err != nil {
		return nil, fmt.Errorf("%s: %w", SidecarName, err)
	}
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return nil, fmt.Errorf("%s: unexpected trailing data", SidecarName)
	}
	if sc.Schema != 1 {
		return nil, fmt.Errorf("%s: unsupported schema %d (want 1)", SidecarName, sc.Schema)
	}
	return &sc, nil
}

// Canonical returns the sidecar's canonical bytes: two-space indented JSON
// with map keys sorted (encoding/json sorts them), one trailing newline.
func (sc *Sidecar) Canonical() []byte {
	b, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		// Sidecar contains only strings, ints and maps; MarshalIndent
		// cannot fail.
		panic(fmt.Sprintf("marshal sidecar: %v", err))
	}
	return append(b, '\n')
}

// PinNames returns the pin package names sorted by name.
func (sc *Sidecar) PinNames() []string {
	names := make([]string, 0, len(sc.Pins))
	for n := range sc.Pins {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// PinString returns the canonical flat `upkeep-pins` value rendered from
// the sidecar's verified_for fields (decision P1).
func (sc *Sidecar) PinString() string {
	var parts []string
	for _, n := range sc.PinNames() {
		p := sc.Pins[n]
		ver := p.VerifiedFor
		var spec string
		switch p.Kind {
		case "github":
			spec = "github:" + p.Repo + "@" + ver
		case "manual":
			spec = "manual:" + ver
		default:
			spec = ver
		}
		parts = append(parts, n+"="+spec)
	}
	return strings.Join(parts, ", ")
}

// VerifiedDate returns the newest verified_at across pins and docs
// (ISO dates sort lexicographically), or "" when none is set.
func (sc *Sidecar) VerifiedDate() string {
	best := ""
	for _, p := range sc.Pins {
		if p.VerifiedAt > best {
			best = p.VerifiedAt
		}
	}
	for _, d := range sc.Docs {
		if d.VerifiedAt > best {
			best = d.VerifiedAt
		}
	}
	return best
}
