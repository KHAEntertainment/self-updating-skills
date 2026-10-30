package source

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// maxPageBytes caps any fetched doc page; larger responses are refused.
const maxPageBytes = 5 << 20

// pageClient fetches doc pages with the same 15s timeout as npm/git
// lookups.
var pageClient = &http.Client{Timeout: lookupTimeout}

// regionMarkerRe matches the open and close markers of frozen and generated
// regions, the two marker kinds a fetched page could forge.
var regionMarkerRe = regexp.MustCompile(`<!--\s*/?(?:frozen|generated):[A-Za-z0-9_.-]+\s*-->`)

// FetchPage fetches one tracked doc page and returns its sanitized bytes.
// The URL is {base}/{page}{ext}. A non-200 response, a fetch error, or a
// page over the size cap is an error — a failed fetch must never read as
// "unchanged" upstream.
func FetchPage(ctx context.Context, baseURL, page, ext string) ([]byte, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("no docs base URL configured for page %q", page)
	}
	u := strings.TrimRight(baseURL, "/") + "/" + page + ext
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := pageClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", u, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", u, err)
	}
	if len(body) > maxPageBytes {
		return nil, fmt.Errorf("fetch %s: page exceeds %d bytes", u, maxPageBytes)
	}
	return SanitizeDoc(body), nil
}

// SanitizeDoc strips frozen/generated region marker tags from fetched doc
// text (sanitization rule 3): a page cannot forge a protected region.
func SanitizeDoc(b []byte) []byte {
	return regionMarkerRe.ReplaceAll(b, nil)
}
