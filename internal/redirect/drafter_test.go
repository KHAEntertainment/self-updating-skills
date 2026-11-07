package redirect

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// stubAPI is a local stand-in for the Messages API: no live call is made.
func stubAPI(t *testing.T, content string, seen *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if seen != nil {
			_ = json.Unmarshal(b, seen)
			(*seen)["_api_key_header_set"] = r.Header.Get("X-Api-Key") != ""
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"m","stop_reason":"tool_use","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1},"content":`+content+`}`)
	}))
}

func stubDrafter(srv *httptest.Server) *anthropicDrafter {
	return &anthropicDrafter{
		client: anthropic.NewClient(option.WithAPIKey("sk-test"), option.WithBaseURL(srv.URL), option.WithMaxRetries(0)),
		model:  DefaultModel,
	}
}

func TestAnthropicDrafterForcedToolCall(t *testing.T) {
	var seen map[string]any = map[string]any{}
	srv := stubAPI(t, `[{"type":"tool_use","id":"tu_1","name":"submit_rows","input":{"rows":[]}}]`, &seen)
	defer srv.Close()
	raw, err := stubDrafter(srv).Draft(context.Background(), Input{Skill: "s", Pages: []PageInput{{Name: "p", New: "text"}}})
	if err != nil {
		t.Fatal(err)
	}
	if d, err := ParseDraft(raw); err != nil || len(d.Rows) != 0 {
		t.Fatalf("tool input must parse as a draft: %s %v", raw, err)
	}
	tc, _ := seen["tool_choice"].(map[string]any)
	if tc["type"] != "tool" || tc["name"] != "submit_rows" {
		t.Fatalf("tool_choice not forced: %v", seen["tool_choice"])
	}
	if seen["model"] != DefaultModel {
		t.Fatalf("model %v", seen["model"])
	}
	if seen["_api_key_header_set"] != true {
		t.Fatal("API key header not sent")
	}
	body, _ := json.Marshal(seen)
	if strings.Contains(string(body), "sk-test") {
		t.Fatal("API key leaked into the request body")
	}
}

func TestAnthropicDrafterNoToolCall(t *testing.T) {
	srv := stubAPI(t, `[{"type":"text","text":"{\"rows\":[]}"}]`, nil)
	defer srv.Close()
	if _, err := stubDrafter(srv).Draft(context.Background(), Input{Skill: "s"}); err == nil {
		t.Fatal("a text-only reply must not be read as rows")
	}
}

func TestAnthropicDrafterAPIErrorHasNoKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	}))
	defer srv.Close()
	_, err := stubDrafter(srv).Draft(context.Background(), Input{Skill: "s"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "sk-test") {
		t.Fatalf("error text carries the key: %v", err)
	}
}
