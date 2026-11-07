package source

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// FetchSchema fetches a harness's published config JSON schema for the S
// check. The URL must be https (same rules as FetchPage: https on every
// redirect, HTTP 200, size cap) and its host must be one of allowedHosts —
// the skill's upkeep-source-base/upkeep-source-index hosts or the package's
// repository host — so skill metadata cannot point the engine at an
// arbitrary server. The bytes are returned unparsed; the caller compiles
// them as JSON Schema.
func FetchSchema(ctx context.Context, rawURL string, allowedHosts []string) ([]byte, error) {
	if strings.TrimSpace(rawURL) == "" {
		return nil, fmt.Errorf("no schema URL configured")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("schema %s: %w", rawURL, err)
	}
	if !HostAllowed(u.Hostname(), allowedHosts) {
		return nil, fmt.Errorf("schema %s: host %q is not the skill's docs or repository host", rawURL, u.Hostname())
	}
	return get(ctx, rawURL, u)
}

// HostAllowed reports whether host (case-insensitive) is one of allowed.
// An empty host never matches.
func HostAllowed(host string, allowed []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, a := range allowed {
		if strings.ToLower(strings.TrimSpace(a)) == host {
			return true
		}
	}
	return false
}
