package source

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// maxPageBytes caps any fetched doc page; larger responses are refused.
const maxPageBytes = 5 << 20

// AllowInsecureHTTP is a test-only seam: when non-empty, plain http URLs
// are permitted and TLS verification is relaxed (for httptest.NewTLSServer
// calls). It is intentionally NOT bound to an environment variable, and
// release builds leave it empty (default deny). Host allowlisting is
// deferred (https alone is not an SSRF/host boundary).
var AllowInsecureHTTP string

var pageClient = &http.Client{
	Timeout: lookupTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		if req.URL.Scheme != "https" && AllowInsecureHTTP == "" {
			return fmt.Errorf("redirect to non-https scheme %q refused", req.URL.Scheme)
		}
		return nil
	},
}

// client returns the page client, lazily relaxing TLS when the test-only
// seam is enabled.
func client() *http.Client {
	if AllowInsecureHTTP != "" && pageClient.Transport == nil {
		pageClient.Transport = &http.Transport{
			// Test-only seam: httptest.NewTLSServer certificates are
			// self-signed. Never enabled in release builds.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		}
	}
	return pageClient
}

// regionMarkerRe matches the open and close markers of frozen and generated
// regions, the two marker kinds a fetched page could forge.
var regionMarkerRe = regexp.MustCompile(`<!--\s*/?(?:frozen|generated):[A-Za-z0-9_.-]+\s*-->`)

// FetchPage fetches one tracked doc page and returns its sanitized bytes.
// The URL is {base}/{page}{ext}. Only https is permitted on the initial URL
// and on every redirect (the AllowInsecureHTTP seam may relax this for
// tests). A non-200 response, a fetch error, or a page over the size cap
// is an error — a failed fetch must never read as "unchanged" upstream.
func FetchPage(ctx context.Context, baseURL, page, ext string) ([]byte, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("no docs base URL configured for page %q", page)
	}
	full := strings.TrimRight(baseURL, "/") + "/" + page + ext
	u, err := url.Parse(full)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", full, err)
	}
	if u.Scheme != "https" && AllowInsecureHTTP == "" {
		return nil, fmt.Errorf("fetch %s: non-https scheme %q refused", full, u.Scheme)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", full, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", full, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", full, err)
	}
	if len(body) > maxPageBytes {
		return nil, fmt.Errorf("fetch %s: page exceeds %d bytes", full, maxPageBytes)
	}
	return SanitizeDoc(body), nil
}

// SanitizeDoc strips frozen/generated region marker tags from fetched doc
// text (sanitization rule 3): a page cannot forge a protected region.
func SanitizeDoc(b []byte) []byte {
	return regionMarkerRe.ReplaceAll(b, nil)
}
