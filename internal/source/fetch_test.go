package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/docs/skills.md":
			w.Write([]byte("# Skills\n\n<!-- frozen:pol -->\nx\n<!-- /frozen:pol -->\n<!-- generated:pins -->\ny\n<!-- /generated:pins -->\n"))
		case "/docs/missing.md":
			http.NotFound(w, r)
		default:
			http.Error(w, "nope", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	ctx := context.Background()

	body, err := FetchPage(ctx, srv.URL+"/docs", "skills", ".md")
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if strings.Contains(got, "<!--") || strings.Contains(got, "frozen") {
		t.Errorf("region markers not stripped: %q", got)
	}
	if !strings.Contains(got, "# Skills") {
		t.Errorf("content missing: %q", got)
	}

	if _, err := FetchPage(ctx, srv.URL+"/docs", "missing", ".md"); err == nil {
		t.Error("404 must be an error")
	}
	if _, err := FetchPage(ctx, srv.URL+"/docs", "err", ".md"); err == nil {
		t.Error("500 must be an error")
	}
	if _, err := FetchPage(ctx, "", "skills", ".md"); err == nil {
		t.Error("missing base URL must be an error")
	}
}

func TestFetchPageSizeCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("x", maxPageBytes+1)))
	}))
	defer srv.Close()
	if _, err := FetchPage(context.Background(), srv.URL+"/docs", "big", ".md"); err == nil {
		t.Error("oversized page must be an error")
	}
}
