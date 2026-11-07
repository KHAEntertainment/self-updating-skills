// Package redirect drafts redirect rows with a model and gates each row
// with deterministic checks before anything reaches a skill (Phase 4,
// decisions D8 and D10).
//
// The pipeline is: build a drafting Input from sanitized snapshots → a
// Drafter returns raw JSON → ParseDraft validates it against the row schema
// (and nothing else is ever read from model output) → Sanitize applies the
// D10 field bounds and source-host rule per row → the Q/S/D verifiers run →
// Route decides each row's outcome and the skill's outcome.
package redirect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/KHAEntertainment/self-updating-skills/internal/source"
)

// Row kinds (sidecar redirect.kind). The kind decides which verifiers run.
const (
	KindAPI       = "api"
	KindFlag      = "flag"
	KindConfigKey = "config-key"
	KindPath      = "path"
	KindConcept   = "concept"
)

// Row is one drafted redirect row as the model returns it. Quote and Page
// are evidence only: they go to SQLite and the run report, never to a
// skill file.
type Row struct {
	ID     string `json:"id"`
	Old    string `json:"old"`
	New    string `json:"new"`
	Kind   string `json:"kind"`
	Since  string `json:"since"`
	Source string `json:"source"`
	Quote  string `json:"quote"`
	Page   string `json:"page"`
}

// Draft is the full model output.
type Draft struct {
	Rows []Row `json:"rows"`
}

// MaxFieldLen is the D10 bound on old/new (and every other rendered field),
// counted in characters.
const MaxFieldLen = 120

// maxQuoteLen bounds the evidence quote stored in SQLite.
const maxQuoteLen = 2000

// RowSchema is the JSON Schema the model's output must match. It is the
// structural contract (types, required fields, the kind enum, generous
// size caps); the tighter per-row D10 bounds are applied by Sanitize so one
// bad row is reported on its own instead of sinking the whole draft.
const RowSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["rows"],
  "properties": {
    "rows": {
      "type": "array",
      "maxItems": 50,
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["id", "old", "new", "kind", "since", "source", "quote", "page"],
        "properties": {
          "id":     {"type": "string", "maxLength": 200},
          "old":    {"type": "string", "maxLength": 1000},
          "new":    {"type": "string", "maxLength": 1000},
          "kind":   {"enum": ["api", "flag", "config-key", "path", "concept"]},
          "since":  {"type": "string", "maxLength": 200},
          "source": {"type": "string", "maxLength": 1000},
          "quote":  {"type": "string", "maxLength": 4000},
          "page":   {"type": "string", "maxLength": 500}
        }
      }
    }
  }
}`

var (
	rowSchemaOnce sync.Once
	rowSchema     *jsonschema.Schema
	rowSchemaErr  error
)

// compiledRowSchema compiles RowSchema once, with no URL loader at all so
// compilation can never reach the network or the filesystem.
func compiledRowSchema() (*jsonschema.Schema, error) {
	rowSchemaOnce.Do(func() {
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(RowSchema))
		if err != nil {
			rowSchemaErr = err
			return
		}
		c := jsonschema.NewCompiler()
		c.UseLoader(jsonschema.SchemeURLLoader{})
		if err := c.AddResource("upkeep:redirect-rows.json", doc); err != nil {
			rowSchemaErr = err
			return
		}
		rowSchema, rowSchemaErr = c.Compile("upkeep:redirect-rows.json")
	})
	return rowSchema, rowSchemaErr
}

// ParseDraft validates raw model output against RowSchema and decodes it.
// Anything that is not one JSON object matching the schema is an error;
// prose around the JSON, unknown fields and trailing data are all refused.
func ParseDraft(raw []byte) (Draft, error) {
	sch, err := compiledRowSchema()
	if err != nil {
		return Draft{}, fmt.Errorf("row schema: %w", err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return Draft{}, fmt.Errorf("model output is not JSON: %w", err)
	}
	if err := sch.Validate(inst); err != nil {
		return Draft{}, fmt.Errorf("model output does not match the row schema: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var d Draft
	if err := dec.Decode(&d); err != nil {
		return Draft{}, fmt.Errorf("model output: %w", err)
	}
	if dec.More() {
		return Draft{}, fmt.Errorf("model output: trailing data after the JSON object")
	}
	return d, nil
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,63}$`)

// Sanitize applies the D10 rules to one row: old/new (and the other
// rendered fields) are at most MaxFieldLen characters with no line break,
// no region marker and no run of three backticks; id is a short slug;
// source is an https URL on an allowed host; quote is non-empty and
// bounded; page is one of the pages the draft was given.
func Sanitize(r Row, allowedHosts []string, pages map[string]bool) error {
	if !idRe.MatchString(r.ID) {
		return fmt.Errorf("id %q is not a lowercase slug of at most 64 characters", r.ID)
	}
	for _, f := range []struct{ what, val string }{
		{"old", r.Old}, {"new", r.New}, {"since", r.Since}, {"source", r.Source},
	} {
		if err := fieldOK(f.what, f.val); err != nil {
			return err
		}
	}
	if strings.TrimSpace(r.Old) == "" || strings.TrimSpace(r.New) == "" {
		return fmt.Errorf("old and new must both be non-empty")
	}
	if r.Old == r.New {
		return fmt.Errorf("old and new are identical")
	}
	u, err := url.Parse(r.Source)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return fmt.Errorf("source %q is not an https URL", r.Source)
	}
	if !source.HostAllowed(u.Hostname(), allowedHosts) {
		return fmt.Errorf("source host %q is not the skill's docs or repository host", u.Hostname())
	}
	if strings.TrimSpace(r.Quote) == "" {
		return fmt.Errorf("quote is empty")
	}
	if utf8.RuneCountInString(r.Quote) > maxQuoteLen {
		return fmt.Errorf("quote exceeds %d characters", maxQuoteLen)
	}
	if !pages[r.Page] {
		return fmt.Errorf("page %q was not part of the drafting input", r.Page)
	}
	return nil
}

func fieldOK(what, s string) error {
	if n := utf8.RuneCountInString(s); n > MaxFieldLen {
		return fmt.Errorf("%s: %d characters exceeds the %d-character limit", what, n, MaxFieldLen)
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
