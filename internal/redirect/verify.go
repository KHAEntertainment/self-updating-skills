package redirect

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Check is one verifier's result for one row. Ran is false when the check
// does not apply or its evidence is unavailable; a check that did not run
// never counts as a pass.
type Check struct {
	Name   string `json:"name"` // Q, S or D
	Ran    bool   `json:"ran"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

func notRun(name, why string) Check { return Check{Name: name, Detail: "not run: " + why} }

// Normalize collapses every whitespace run to one space and trims, so
// line-wrapping and indentation differences do not defeat a quote match.
func Normalize(s string) string { return strings.Join(strings.Fields(s), " ") }

// CheckQ is the required quote check: after whitespace normalization the
// row's quote appears in the newest snapshot of its page, and the row's new
// value appears in the quote. newText maps page → newest snapshot text.
func CheckQ(r Row, newText map[string]string) Check {
	c := Check{Name: "Q", Ran: true}
	snap, ok := newText[r.Page]
	if !ok {
		c.Detail = fmt.Sprintf("page %q has no snapshot", r.Page)
		return c
	}
	q := Normalize(r.Quote)
	if q == "" {
		c.Detail = "empty quote"
		return c
	}
	if !strings.Contains(Normalize(snap), q) {
		c.Detail = "quote not found in the snapshot"
		return c
	}
	if n := Normalize(r.New); n == "" || !strings.Contains(q, n) {
		c.Detail = "new value not in the quote"
		return c
	}
	c.Pass = true
	return c
}

// ConfigSchema is a compiled harness config schema for the S check.
type ConfigSchema struct {
	root *jsonschema.Schema
	SHA  string // sha256:<hex> of the schema bytes, recorded as evidence
}

// CompileConfigSchema compiles a fetched config schema. The compiler has no
// URL loader, so a remote $ref can never trigger a fetch; only refs inside
// the document resolve.
func CompileConfigSchema(b []byte) (*ConfigSchema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("config schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	c.UseLoader(jsonschema.SchemeURLLoader{})
	if err := c.AddResource("upkeep:config-schema.json", doc); err != nil {
		return nil, fmt.Errorf("config schema: %w", err)
	}
	sch, err := c.Compile("upkeep:config-schema.json")
	if err != nil {
		return nil, fmt.Errorf("config schema: %w", err)
	}
	sum := sha256.Sum256(b)
	return &ConfigSchema{root: sch, SHA: "sha256:" + hex.EncodeToString(sum[:])}, nil
}

var keyPathRe = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*$`)

// keyPath extracts a dotted config key path from a row value such as
// "model_instructions_file" or "`features.web_search`". Only the bare key
// path form is verifiable: an assignment ("key = value") carries a value
// this check does not validate, so it is reported as not a key path
// (assign=true) and S does not run.
func keyPath(v string) (path []string, assign, ok bool) {
	v = strings.Trim(strings.TrimSpace(v), "`\"' ")
	if strings.Contains(v, "=") {
		return nil, true, false
	}
	if !keyPathRe.MatchString(v) {
		return nil, false, false
	}
	return strings.Split(v, "."), false, true
}

// The S check is an allowlist (owner decision, PR #18 round 3): it only
// judges schemas it can read completely. A key path is followed through
// plain `properties` of plain objects; the final property must resolve to a
// plain type schema, optionally through local $ref / allOf members that are
// themselves plain. Every other construct — a false schema, not,
// anyOf/oneOf, if/then/else, patternProperties, dependentSchemas,
// $dynamicRef, deprecated, a $ref/allOf on an intermediate object, or
// resolution deeper than maxRefDepth or cyclic — makes S "not run", which
// never passes. A non-local $ref cannot compile (the compiler has no URL
// loader), so it never reaches the walk.

// maxRefDepth bounds $ref/allOf resolution of a leaf.
const maxRefDepth = 4

// keyState is what the walk established about one key path.
type keyState int

const (
	// keyComplex: the path crosses a schema outside the allowlist.
	keyComplex keyState = iota
	// keyRejected: a plain closed object (additionalProperties false, no
	// patternProperties) has no such property.
	keyRejected
	// keyOpen: a plain object without additionalProperties: false has no
	// such property, so the name may still be accepted.
	keyOpen
	// keyDeclared: every segment is a declared plain property.
	keyDeclared
)

// walkResult is the outcome of following one key path.
type walkResult struct {
	state keyState
	// leafPlain: for keyDeclared, the final property resolves to a plain
	// type schema. retired: that resolution carries "deprecated": true
	// (proves an old key retired). marked: the keyword, or a description
	// beginning "Deprecated" (blocks a new key).
	leafPlain bool
	retired   bool
	marked    bool
}

func (cs *ConfigSchema) walk(path []string) walkResult {
	cur := cs.root
	for i, seg := range path {
		if !plainObject(cur) {
			return walkResult{state: keyComplex}
		}
		p, ok := cur.Properties[seg]
		if !ok {
			if ap, isBool := cur.AdditionalProperties.(bool); isBool && !ap {
				return walkResult{state: keyRejected}
			}
			return walkResult{state: keyOpen}
		}
		if i == len(path)-1 {
			ok, retired, marked := plainLeaf(p, 0, map[*jsonschema.Schema]bool{})
			return walkResult{state: keyDeclared, leafPlain: ok, retired: retired, marked: marked}
		}
		cur = p
	}
	return walkResult{state: keyComplex}
}

// plainObject reports whether s is an object the walk may descend through:
// no boolean schema, no $ref/allOf, no applicator or conditional keyword,
// no patternProperties, not deprecated.
func plainObject(s *jsonschema.Schema) bool {
	if s == nil || s.Bool != nil || s.Ref != nil || len(s.AllOf) > 0 || markedDeprecated(s) {
		return false
	}
	return !hasForbidden(s)
}

// hasForbidden reports the keywords the allowlist never accepts anywhere.
func hasForbidden(s *jsonschema.Schema) bool {
	return s.Not != nil || len(s.AnyOf) > 0 || len(s.OneOf) > 0 ||
		s.If != nil || s.Then != nil || s.Else != nil ||
		len(s.PatternProperties) > 0 || len(s.DependentSchemas) > 0 || len(s.Dependencies) > 0 ||
		s.DynamicRef != nil || s.RecursiveRef != nil ||
		s.UnevaluatedProperties != nil || s.PropertyNames != nil
}

// plainLeaf resolves a leaf property. It is plain when it uses only type,
// format, description, title, enum, const, default, examples, minLength,
// maxLength and pattern, plus local $ref / allOf members that are plain in
// turn, within maxRefDepth and without a cycle. retired and marked report
// retiredKeyword and markedDeprecated anywhere in the resolved set.
func plainLeaf(s *jsonschema.Schema, depth int, seen map[*jsonschema.Schema]bool) (ok, retired, marked bool) {
	if s == nil || s.Bool != nil || depth > maxRefDepth || seen[s] {
		return false, false, false
	}
	seen[s] = true
	defer delete(seen, s)
	if hasForbidden(s) || len(s.Properties) > 0 || s.AdditionalProperties != nil ||
		len(s.DependentRequired) > 0 || len(s.Required) > 0 || s.MinProperties != nil || s.MaxProperties != nil ||
		s.Items != nil || s.AdditionalItems != nil || len(s.PrefixItems) > 0 || s.Items2020 != nil ||
		s.UnevaluatedItems != nil || s.Contains != nil || s.MinItems != nil || s.MaxItems != nil || s.UniqueItems ||
		s.Maximum != nil || s.Minimum != nil || s.ExclusiveMaximum != nil || s.ExclusiveMinimum != nil || s.MultipleOf != nil ||
		s.ContentEncoding != nil || s.ContentMediaType != nil || s.ContentSchema != nil {
		return false, false, false
	}
	retired, marked = retiredKeyword(s), markedDeprecated(s)
	members := append([]*jsonschema.Schema(nil), s.AllOf...)
	if s.Ref != nil {
		members = append(members, s.Ref)
	}
	for _, m := range members {
		mok, mret, mmark := plainLeaf(m, depth+1, seen)
		if !mok {
			return false, false, false
		}
		retired, marked = retired || mret, marked || mmark
	}
	return true, retired, marked
}

// retiredKeyword is the JSON Schema "deprecated": true keyword only. It is
// the one proof that an old key is retired: description text never proves
// retirement (PR #18 round 4), or "Deprecated? No, …" would.
func retiredKeyword(s *jsonschema.Schema) bool { return s.Deprecated }

// markedDeprecated is the conservative, blocking test: the keyword, or a
// description beginning "deprecated" (case-insensitive, trimmed). A new
// key or an intermediate object so marked never lets S pass.
func markedDeprecated(s *jsonschema.Schema) bool {
	return s.Deprecated || strings.HasPrefix(strings.ToLower(strings.TrimSpace(s.Description)), "deprecated")
}

const tooComplex = "schema too complex to verify"

// CheckS is the schema check. config-key rows: new is a bare key path that
// the allowlisted walk resolves to a declared, plain leaf not marked
// deprecated (keyword or "Deprecated…" description), and old is a bare key
// path a plain closed object refuses, or a declared plain leaf carrying the
// "deprecated": true keyword. A closed object refuses any unknown name, so
// a refused old passes only with independent evidence that it existed: in
// the previous snapshot of the page or in the docs git history (h, the
// same evidence D's first condition reads). flag rows: deferred (no binary
// is executed). Other kinds: not applicable.
func CheckS(r Row, cs *ConfigSchema, schemaErr string, h History) Check {
	switch r.Kind {
	case KindFlag:
		return notRun("S", "flag check deferred")
	case KindConfigKey:
	default:
		return notRun("S", "not applicable to kind "+r.Kind)
	}
	if cs == nil {
		if schemaErr == "" {
			schemaErr = "no upkeep-schema-url configured"
		}
		return notRun("S", schemaErr)
	}
	newPath, newAssign, newOK := keyPath(r.New)
	oldPath, oldAssign, oldOK := keyPath(r.Old)
	if newAssign || oldAssign {
		return notRun("S", "assignment values are not validated; only a bare key path is checked")
	}
	c := Check{Name: "S", Ran: true}
	if !newOK {
		c.Detail = fmt.Sprintf("new %q is not a config key path (schema %s)", r.New, cs.SHA)
		return c
	}
	if !oldOK {
		c.Detail = fmt.Sprintf("old %q is not a config key path (schema %s)", r.Old, cs.SHA)
		return c
	}
	nw := cs.walk(newPath)
	switch {
	case nw.state == keyComplex, nw.state == keyDeclared && !nw.leafPlain:
		return notRun("S", tooComplex+" (new key)")
	case nw.state != keyDeclared:
		c.Detail = fmt.Sprintf("new key is not declared in schema %s", cs.SHA)
		return c
	case nw.marked:
		return notRun("S", "new key is marked deprecated")
	}
	ow := cs.walk(oldPath)
	switch {
	case ow.state == keyComplex, ow.state == keyDeclared && !ow.leafPlain:
		return notRun("S", tooComplex+" (old key)")
	case ow.state == keyRejected:
		if !oldEvidenced(h, r.Old, strings.Join(oldPath, ".")) {
			c.Detail = fmt.Sprintf("old key has no history evidence (refused by schema %s)", cs.SHA)
			return c
		}
		c.Detail = fmt.Sprintf("new key declared, old key refused by schema %s and found in %s", cs.SHA, evidenceSource(h, r.Old, strings.Join(oldPath, ".")))
	case ow.state == keyDeclared && ow.retired:
		c.Detail = fmt.Sprintf("new key declared, old key deprecated in schema %s", cs.SHA)
	default:
		c.Detail = fmt.Sprintf("old key is still accepted by schema %s", cs.SHA)
		return c
	}
	c.Pass = true
	return c
}

// History is the D check's evidence about the past.
type History struct {
	// Previous is the previous (verified) snapshot text of the row's page.
	Previous    string
	HasPrevious bool
	// GitRan reports whether a git-history lookup ran; GitFound whether it
	// found old in the history; GitErr explains a lookup that could not run.
	GitRan   bool
	GitFound bool
	GitErr   string
}

// CheckD is the doc-history check: old was present before (in the
// previous snapshot or in upstream git history), old is absent from the
// newest snapshot of the page, and new is present now. Presence as
// evidence (old before, new now) is a whole-identifier match after Canon
// (NFC, Markdown unescape, whitespace collapse), so api.v1 is not evidenced
// by api.v10. Absence of old now is conservative: any Canon substring
// occurrence (api.v1.0, --legacy-mode) keeps old present. Deprecation
// wording is never evidence (owner decision, PR #18 round 3): a page that
// still mentions old, in any terms, keeps D unpassed.
func CheckD(r Row, now string, h History) Check {
	if !h.HasPrevious && !h.GitRan {
		why := "no previous snapshot or git history"
		if h.GitErr != "" {
			why += " (" + h.GitErr + ")"
		}
		return notRun("D", why)
	}
	c := Check{Name: "D", Ran: true}
	inPrev := inPrevious(h, r.Old)
	if Canon(r.Old) == "" || !oldEvidenced(h, r.Old) {
		c.Detail = "old not found in the previous snapshot or git history"
		return c
	}
	// Absence is the conservative test: any substring occurrence of old in
	// Canon form keeps it present ("--legacy-mode" still shows --legacy).
	if strings.Contains(Canon(now), Canon(r.Old)) {
		c.Detail = "old still present in the newest snapshot"
		return c
	}
	if !ContainsIdentifier(now, r.New) {
		c.Detail = "new not present in the newest snapshot"
		return c
	}
	c.Pass = true
	src := "previous snapshot"
	if !inPrev {
		src = "git history"
	}
	c.Detail = "old found in " + src + "; absent now; new present"
	return c
}

// inPrevious reports whether any of the spellings is mentioned, as a whole
// identifier, in the previous snapshot.
func inPrevious(h History, spellings ...string) bool {
	if !h.HasPrevious {
		return false
	}
	for _, sp := range spellings {
		if Canon(sp) != "" && ContainsIdentifier(h.Previous, sp) {
			return true
		}
	}
	return false
}

// oldEvidenced is the old-existence evidence S and D share: old in the
// previous snapshot of the page, or in the docs git history.
func oldEvidenced(h History, spellings ...string) bool {
	return inPrevious(h, spellings...) || (h.GitRan && h.GitFound)
}

func evidenceSource(h History, spellings ...string) string {
	if inPrevious(h, spellings...) {
		return "the previous snapshot"
	}
	return "git history"
}
