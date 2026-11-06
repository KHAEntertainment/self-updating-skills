package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestFetchSchema(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"type":"object"}`))
	}))
	defer srv.Close()
	AllowInsecureHTTP = "source-test"
	defer func() { AllowInsecureHTTP = "" }()
	host := mustHost(t, srv.URL)
	ctx := context.Background()

	b, err := FetchSchema(ctx, srv.URL+"/schema.json", []string{host})
	if err != nil || string(b) != `{"type":"object"}` {
		t.Fatalf("allowed host: %q %v", b, err)
	}
	before := hits.Load()
	if _, err := FetchSchema(ctx, srv.URL+"/schema.json", []string{"docs.example"}); err == nil {
		t.Fatal("foreign host accepted")
	}
	if _, err := FetchSchema(ctx, "", []string{host}); err == nil {
		t.Fatal("empty URL accepted")
	}
	if hits.Load() != before {
		t.Fatal("a refused host must be refused before any request")
	}
}

func TestFetchSchemaHTTPSOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	AllowInsecureHTTP = ""
	if _, err := FetchSchema(context.Background(), srv.URL+"/s.json", []string{mustHost(t, srv.URL)}); err == nil {
		t.Fatal("plain http accepted without the test seam")
	}
}

func TestHostAllowed(t *testing.T) {
	if !HostAllowed("Docs.Example", []string{"docs.example"}) {
		t.Fatal("case-insensitive match")
	}
	for _, h := range []string{"", "docs.example.evil", "evil.docs.example"} {
		if HostAllowed(h, []string{"docs.example"}) {
			t.Errorf("%q allowed", h)
		}
	}
	if HostAllowed("docs.example", nil) {
		t.Fatal("empty allowlist allowed a host")
	}
}

func mustHost(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Hostname()
}
