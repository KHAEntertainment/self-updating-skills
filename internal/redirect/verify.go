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
// "model_instructions_file", "`features.web_search`" or
// "model = \"o3\"". ok=false when the value is not a key path.
func keyPath(v string) ([]string, bool) {
	v = strings.TrimSpace(v)
	if k, _, found := strings.Cut(v, "="); found {
		v = strings.TrimSpace(k)
	}
	v = strings.Trim(v, "`\"' ")
	if !keyPathRe.MatchString(v) {
		return nil, false
	}
	return strings.Split(v, "."), true
}

// lookup resolves a key path in the compiled schema. found is true when
// every segment is a declared property (or matches a declared
// patternProperties / additionalProperties schema, i.e. a map entry);
// deprecated is true when the final property is marked deprecated, either
// with "deprecated": true or a description starting with "Deprecated".
func (cs *ConfigSchema) lookup(path []string) (found, deprecated bool) {
	cur := []*jsonschema.Schema{cs.root}
	var last []*jsonschema.Schema
	for _, seg := range path {
		var next []*jsonschema.Schema
		for _, s := range cur {
			next = append(next, child(s, seg, 0)...)
		}
		if len(next) == 0 {
			return false, false
		}
		cur, last = next, next
	}
	for _, s := range last {
		if isDeprecated(s, 0) {
			return true, true
		}
	}
	return true, false
}

const maxSchemaDepth = 32

// child returns the schemas a property named seg resolves to under s,
// following $ref and allOf/anyOf/oneOf branches.
func child(s *jsonschema.Schema, seg string, depth int) []*jsonschema.Schema {
	if s == nil || depth > maxSchemaDepth {
		return nil
	}
	var out []*jsonschema.Schema
	if p, ok := s.Properties[seg]; ok {
		out = append(out, p)
	}
	if len(out) == 0 {
		for re, p := range s.PatternProperties {
			if re.MatchString(seg) {
				out = append(out, p)
			}
		}
	}
	if len(out) == 0 && len(s.Properties) == 0 {
		// A map type (no fixed properties): additionalProperties with a
		// schema accepts any entry name.
		if ap, ok := s.AdditionalProperties.(*jsonschema.Schema); ok {
			out = append(out, ap)
		}
	}
	out = append(out, child(s.Ref, seg, depth+1)...)
	for _, groups := range [][]*jsonschema.Schema{s.AllOf, s.AnyOf, s.OneOf} {
		for _, g := range groups {
			out = append(out, child(g, seg, depth+1)...)
		}
	}
	return out
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

// CheckS is the schema check. config-key rows: new resolves to a declared,
// non-deprecated key in the harness schema, and old does not resolve or is
// deprecated. flag rows: deferred in this release (no binary is executed).
// Other kinds: not applicable.
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
	c := Check{Name: "S", Ran: true}
	newPath, ok := keyPath(r.New)
	if !ok {
		c.Detail = fmt.Sprintf("new %q is not a config key path (schema %s)", r.New, cs.SHA)
		return c
	}
	oldPath, ok := keyPath(r.Old)
	if !ok {
		c.Detail = fmt.Sprintf("old %q is not a config key path (schema %s)", r.Old, cs.SHA)
		return c
	}
	nf, nd := cs.lookup(newPath)
	if !nf || nd {
		c.Detail = fmt.Sprintf("new key is not a current key in schema %s", cs.SHA)
		return c
	}
	of, od := cs.lookup(oldPath)
	if of && !od {
		c.Detail = fmt.Sprintf("old key is still a current key in schema %s", cs.SHA)
		return c
	}
	c.Pass = true
	c.Detail = fmt.Sprintf("new key declared, old key %s in schema %s", map[bool]string{true: "deprecated", false: "absent"}[of], cs.SHA)
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
// newest snapshot of the page or every line mentioning it says deprecated,
// and new is present now.
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
	if strings.Contains(Normalize(now), oldN) && !allDeprecated(now, r.Old) {
		c.Detail = "old still present and not marked deprecated"
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

// allDeprecated reports whether every line of text that mentions old also
// says "deprecat" (case-insensitive). A mention split across lines counts
// as a non-deprecated mention.
func allDeprecated(text, old string) bool {
	found := false
	for _, ln := range strings.Split(text, "\n") {
		if strings.Contains(ln, old) {
			found = true
			if !strings.Contains(strings.ToLower(ln), "deprecat") {
				return false
			}
		}
	}
	return found
}
