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

// keyState is what the schema says about one key path.
type keyState int

const (
	// keyRejected: the schema refuses the name outright (no declared
	// property, pattern or additional-properties schema accepts it, and
	// additionalProperties is false), or it resolves to the false schema.
	keyRejected keyState = iota
	// keyOpen: the name is neither declared nor refused (an object with no
	// additionalProperties constraint, or a shape this walk cannot judge).
	keyOpen
	// keyDeclared: the name resolves to a declared, satisfiable schema.
	keyDeclared
)

const maxSchemaDepth = 32

// lookup resolves a key path in the compiled schema. At every level each
// applicable schema (following $ref and allOf/anyOf/oneOf) is asked about
// the next name: a fixed property, then a matching pattern property, then a
// schema-valued additionalProperties — which applies to unmatched names
// even when fixed properties exist (a map with named settings beside it).
// deprecated reports whether a declared leaf is marked deprecated.
func (cs *ConfigSchema) lookup(path []string) (st keyState, deprecated bool) {
	cur := []*jsonschema.Schema{cs.root}
	for _, seg := range path {
		var next []*jsonschema.Schema
		open, rejected := false, false
		for _, s := range cur {
			for _, e := range expand(s, 0) {
				if isFalse(e) {
					rejected = true
					continue
				}
				if p, ok := e.Properties[seg]; ok {
					next = append(next, p)
					continue
				}
				matched := false
				for re, p := range e.PatternProperties {
					if re.MatchString(seg) {
						next = append(next, p)
						matched = true
					}
				}
				if matched {
					continue
				}
				switch ap := e.AdditionalProperties.(type) {
				case *jsonschema.Schema:
					next = append(next, ap)
				case bool:
					if ap {
						open = true
					} else {
						rejected = true
					}
				default:
					// No additionalProperties keyword: only an object
					// description leaves the name open; a pure wrapper
					// ($ref/allOf holder, description) says nothing.
					if describesObject(e) {
						open = true
					}
				}
			}
		}
		var live []*jsonschema.Schema
		for _, n := range next {
			if isFalse(n) {
				// A name whose schema is false can hold no value.
				rejected = true
				continue
			}
			live = append(live, n)
		}
		switch {
		case len(live) > 0:
			cur = live
		case open || !rejected:
			return keyOpen, false
		default:
			return keyRejected, false
		}
	}
	for _, s := range cur {
		if isDeprecated(s, 0) {
			return keyDeclared, true
		}
	}
	return keyDeclared, false
}

// expand returns s plus every schema it applies through $ref and
// allOf/anyOf/oneOf, depth-bounded.
func expand(s *jsonschema.Schema, depth int) []*jsonschema.Schema {
	if s == nil || depth > maxSchemaDepth {
		return nil
	}
	out := []*jsonschema.Schema{s}
	out = append(out, expand(s.Ref, depth+1)...)
	for _, groups := range [][]*jsonschema.Schema{s.AllOf, s.AnyOf, s.OneOf} {
		for _, g := range groups {
			out = append(out, expand(g, depth+1)...)
		}
	}
	return out
}

func isFalse(s *jsonschema.Schema) bool { return s != nil && s.Bool != nil && !*s.Bool }

// describesObject reports whether s itself constrains object members.
func describesObject(s *jsonschema.Schema) bool {
	if len(s.Properties) > 0 || len(s.PatternProperties) > 0 {
		return true
	}
	if s.Types != nil {
		for _, t := range s.Types.ToStrings() {
			if t == "object" {
				return true
			}
		}
	}
	return false
}

func isDeprecated(s *jsonschema.Schema, depth int) bool {
	if s == nil || depth > maxSchemaDepth {
		return false
	}
	if s.Deprecated || strings.HasPrefix(strings.ToLower(strings.TrimSpace(s.Description)), "deprecated") {
		return true
	}
	if isDeprecated(s.Ref, depth+1) {
		return true
	}
	for _, g := range s.AllOf {
		if isDeprecated(g, depth+1) {
			return true
		}
	}
	return false
}

// CheckS is the schema check. config-key rows: new is a bare key path that
// resolves to a declared, satisfiable, non-deprecated key in the harness
// schema, and old is a bare key path the schema definitively refuses or
// marks deprecated. A key the schema leaves open (neither declared nor
// refused) is never evidence either way. flag rows: deferred (no binary is
// executed). Other kinds: not applicable.
func CheckS(r Row, cs *ConfigSchema, schemaErr string) Check {
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
	newPath, newAssign, ok := keyPath(r.New)
	if newAssign {
		return notRun("S", "assignment values are not validated; only a bare key path is checked")
	}
	c := Check{Name: "S", Ran: true}
	if !ok {
		c.Detail = fmt.Sprintf("new %q is not a config key path (schema %s)", r.New, cs.SHA)
		return c
	}
	oldPath, oldAssign, ok := keyPath(r.Old)
	if oldAssign {
		return notRun("S", "assignment values are not validated; only a bare key path is checked")
	}
	if !ok {
		c.Detail = fmt.Sprintf("old %q is not a config key path (schema %s)", r.Old, cs.SHA)
		return c
	}
	ns, nd := cs.lookup(newPath)
	if ns != keyDeclared || nd {
		c.Detail = fmt.Sprintf("new key is not a declared current key in schema %s", cs.SHA)
		return c
	}
	os, od := cs.lookup(oldPath)
	switch {
	case os == keyRejected:
		c.Detail = fmt.Sprintf("new key declared, old key refused by schema %s", cs.SHA)
	case os == keyDeclared && od:
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
// newest snapshot of the page or every mention of it is affirmatively
// marked deprecated, and new is present now.
func CheckD(r Row, now string, h History) Check {
	if !h.HasPrevious && !h.GitRan {
		why := "no previous snapshot or git history"
		if h.GitErr != "" {
			why += " (" + h.GitErr + ")"
		}
		return notRun("D", why)
	}
	c := Check{Name: "D", Ran: true}
	oldN := Normalize(r.Old)
	before := (h.HasPrevious && strings.Contains(Normalize(h.Previous), oldN)) || (h.GitRan && h.GitFound)
	if !before {
		c.Detail = "old not found in the previous snapshot or git history"
		return c
	}
	if !retiredNow(Normalize(now), oldN) {
		c.Detail = "old still present and not affirmatively marked deprecated"
		return c
	}
	if !strings.Contains(Normalize(now), Normalize(r.New)) {
		c.Detail = "new not present in the newest snapshot"
		return c
	}
	c.Pass = true
	src := "previous snapshot"
	if !(h.HasPrevious && strings.Contains(Normalize(h.Previous), oldN)) {
		src = "git history"
	}
	c.Detail = "old found in " + src + "; absent or deprecated now; new present"
	return c
}

var (
	// deprecatedAfter: old is the immediate subject of an affirmative
	// deprecation ("`--x` is deprecated", "the --x flag has been
	// deprecated", "--x (deprecated)").
	deprecatedAfter = regexp.MustCompile("(?i)^[`'\"]*(?:\\s+(?:flag|option|key|setting|field|command|path|directory|variable))?\\s*(?:\\(deprecated\\)|(?:is|are|was|were|has been|have been|is now|are now)\\s+deprecated\\b)")
	// deprecatedBefore: old directly follows a "Deprecated:" label.
	deprecatedBefore = regexp.MustCompile("(?i)(?:^|[\\s*_])deprecated[*_]*\\s*[:\\-—]\\s*[`'\"]*$")
	// hedgeRe: negation or hedging anywhere in the sentence before the
	// mention makes the evidence ambiguous.
	hedgeRe = regexp.MustCompile("(?i)\\b(?:not|no|never|without|none|nor|will|would|may|might|could|should|planned|plan|soon|future|until|unless|if|whether|maybe)\\b|n't\\b")
	// sentenceEnd ends a sentence: terminal punctuation followed by a
	// space, so dots inside identifiers do not split.
	sentenceEnd = regexp.MustCompile(`[.!?;] `)
)

// retiredNow reports whether old (normalized) is absent from text
// (normalized), or every occurrence is affirmatively marked deprecated in
// its own sentence with no negation or hedge before it. Any occurrence
// that is not clearly deprecated — including an occurrence that a
// line-wrap split in the raw page — keeps old current.
func retiredNow(text, old string) bool {
	if old == "" {
		return false
	}
	for from := 0; ; {
		k := strings.Index(text[from:], old)
		if k < 0 {
			return true
		}
		start := from + k
		end := start + len(old)
		sStart := 0
		for _, m := range sentenceEnd.FindAllStringIndex(text[:start], -1) {
			sStart = m[1]
		}
		prefix := text[sStart:start]
		if hedgeRe.MatchString(prefix) {
			return false
		}
		if !deprecatedAfter.MatchString(text[end:]) && !deprecatedBefore.MatchString(prefix) {
			return false
		}
		from = end
	}
}
