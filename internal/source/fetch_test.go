package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// setSeam enables the test-only plain-http seam for the duration of a test.
func setSeam(t *testing.T) {
	t.Helper()
	AllowInsecureHTTP = "test"
	t.Cleanup(func() { AllowInsecureHTTP = "" })
}

func TestFetchPage(t *testing.T) {
	setSeam(t)
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
	setSeam(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("x", maxPageBytes+1)))
	}))
	defer srv.Close()
	if _, err := FetchPage(context.Background(), srv.URL+"/docs", "big", ".md"); err == nil {
		t.Error("oversized page must be an error")
	}
}

// C1: with the seam off (default), plain http is refused on the initial
// URL, and a redirect downgrade to http is refused even from an https
// source. With the seam on, plain http works and https (TLSServer) works.
func TestFetchPageHttpsOnly(t *testing.T) {
	if AllowInsecureHTTP != "" {
		t.Fatal("seam must default off for this test")
	}

	// Initial plain http refused with seam off.
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("x"))
	}))
	defer plain.Close()
	if _, err := FetchPage(context.Background(), plain.URL+"/docs", "p", ".md"); err == nil {
		t.Fatal("plain http must be refused when the seam is off")
	} else if !strings.Contains(err.Error(), "non-https") {
		t.Errorf("error = %v, want a non-https refusal", err)
	}

	// Redirect downgrade https → http refused with seam off.
	downgrade := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/docs/p.md", http.StatusFound)
	}))
	defer downgrade.Close()
	if _, err := FetchPage(context.Background(), downgrade.URL+"/docs", "p", ".md"); err == nil {
		t.Fatal("https→http redirect must be refused when the seam is off")
	} else if !strings.Contains(err.Error(), "redirect") {
		t.Errorf("error = %v, want a redirect refusal", err)
	}
}

func TestFetchPageHttpsSeamOn(t *testing.T) {
	setSeam(t)
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("# TLS\n"))
	}))
	defer tlsSrv.Close()
	body, err := FetchPage(context.Background(), tlsSrv.URL+"/docs", "tls", ".md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "# TLS") {
		t.Errorf("body = %q", body)
	}
}
