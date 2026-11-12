// Package urljoin builds upstream API URLs from an operator-supplied base URL
// and a versioned API path.
//
// Base URLs are written by hand, and relay providers commonly keep the API
// version inside the base path (`https://relay.example/claude/v1`), while the
// endpoint constants used by the gateway are already versioned (`/v1/models`,
// `/v1/messages`). Plain string concatenation therefore produces
// `/v1/v1/models`, which the upstream answers with 404 even though
// `/v1/messages` on the same base works.
//
// APIPath joins the two idempotently: an existing version or endpoint suffix in
// the base path is never duplicated, and a query string stays attached to the
// URL instead of being buried in front of the appended path.
package urljoin

import (
	"net/url"
	"strings"
)

// APIPath joins endpoint onto base.
//
// It is idempotent for every base spelling that appears in operator
// configuration:
//
//	APIPath("https://x", "v1/models")            = "https://x/v1/models"
//	APIPath("https://x/", "v1/models")           = "https://x/v1/models"
//	APIPath("https://x/v1", "v1/models")         = "https://x/v1/models"
//	APIPath("https://x/v1/", "v1/models")        = "https://x/v1/models"
//	APIPath("https://x/api/openai/v1", "v1/models") = "https://x/api/openai/v1/models"
//	APIPath("https://x/prefix", "v1/models")     = "https://x/prefix/v1/models"
//
// An endpoint that is already present in full is returned unchanged, and any
// query string on base is preserved:
//
//	APIPath("https://x/v1/models?tenant=a", "v1/models") = "https://x/v1/models?tenant=a"
//	APIPath("https://x/v1?tenant=a", "v1/models")        = "https://x/v1/models?tenant=a"
func APIPath(base, endpoint string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return ""
	}
	endpoint = "/" + strings.Trim(strings.TrimSpace(endpoint), "/")
	if endpoint == "/" {
		return base
	}

	// A fragment is never sent upstream; drop it instead of carrying it into
	// the appended path.
	if i := strings.IndexByte(base, '#'); i >= 0 {
		base = base[:i]
	}
	if base == "" {
		return ""
	}

	query := ""
	if i := strings.IndexByte(base, '?'); i >= 0 {
		query = base[i:]
		base = base[:i]
	}
	base = strings.TrimRight(base, "/")

	path := ""
	if parsed, err := url.Parse(base); err == nil {
		path = parsed.EscapedPath()
	}
	path = strings.TrimRight(path, "/")
	if path == "" {
		return base + endpoint + query
	}

	// The endpoint is already complete (`.../v1/models`).
	if path == endpoint || strings.HasSuffix(path, endpoint) {
		return base + query
	}
	// Walk the endpoint's own segments from the longest to the shortest: a base
	// that already ends in the version segment (`.../v1`) only needs the rest of
	// the endpoint (`/models`). Segment matching keeps a base whose last segment
	// merely contains the version (`/v10`, `/tenant%2Fv1`) from being treated as
	// versioned.
	for end := strings.LastIndexByte(endpoint, '/'); end > 0; end = strings.LastIndexByte(endpoint[:end], '/') {
		if strings.HasSuffix(path, endpoint[:end]) {
			return base + endpoint[end:] + query
		}
	}
	return base + endpoint + query
}

// WithQueryParam appends key=value to raw's query string without rewriting the
// parameters it already carries. Callers that add protocol flags (for example
// `beta=true` on the Anthropic messages endpoint) must not turn an existing
// query into a second `?`.
func WithQueryParam(raw, key, value string) string {
	if key == "" || value == "" {
		return raw
	}
	pair := url.QueryEscape(key) + "=" + url.QueryEscape(value)
	if i := strings.IndexByte(raw, '?'); i >= 0 {
		if strings.HasSuffix(raw, "?") || strings.HasSuffix(raw, "&") {
			return raw + pair
		}
		return raw + "&" + pair
	}
	return raw + "?" + pair
}
