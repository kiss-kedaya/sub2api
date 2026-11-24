package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A user types the base URL by hand. These are the shapes that come out of it, for each of the three text
// protocols a client can pick (Anthropic Messages, OpenAI Chat Completions, OpenAI Responses): with and without
// /v1, with /v1 repeated, and the small slips around it. Every one of them has to reach the same handler as the
// canonical path, with the method, query string and body intact, because it runs through the same middleware
// chain afterwards (authentication, limits, billing are decided there, not here).
func TestGatewayPathAliasBaseURLForms(t *testing.T) {
	protocols := []struct {
		name      string
		canonical string
		method    string
	}{
		{"anthropic messages", "/v1/messages", http.MethodPost},
		{"openai chat completions", "/v1/chat/completions", http.MethodPost},
		{"openai responses", "/v1/responses", http.MethodPost},
	}
	// Prefix a client puts in front of the endpoint, as the base URL it was told to use plus the path the
	// SDK appends. "" is a base URL without /v1 plus an SDK that appends /v1/... itself, and so on.
	prefixes := []struct {
		name   string
		prefix string
	}{
		{"site root only", ""},
		{"/v1", "/v1"},
		{"/v1 twice", "/v1/v1"},
		{"/v1 three times", "/v1/v1/v1"},
		{"/v1 five times", "/v1/v1/v1/v1/v1"},
	}
	for _, protocol := range protocols {
		endpoint := strings.TrimPrefix(protocol.canonical, "/v1")
		for _, prefix := range prefixes {
			path := prefix.prefix + endpoint
			t.Run(protocol.name+" via "+prefix.name+" "+path, func(t *testing.T) {
				router, seen := newGatewayPathAliasTestRouter()
				handler := GatewayPathAlias(router)
				body := `{"model":"m"}`
				req := httptest.NewRequest(protocol.method, path+"?beta=1", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()

				handler.ServeHTTP(w, req)

				require.Equal(t, http.StatusOK, w.Code, "%s must reach the gateway", path)
				require.Len(t, *seen, 1, path)
				require.Equal(t, protocol.canonical, (*seen)[0].Path, path)
				require.Equal(t, "beta=1", (*seen)[0].RawQuery, path)
				require.Equal(t, body, (*seen)[0].Body, path)
			})
		}
	}
}

// The slips that are not about /v1 itself: an extra slash (a base URL that ends in "/" followed by an SDK that
// appends "/v1/..."), or a trailing slash. gin 404s the first and answers the second with a 307 that many HTTP
// clients do not follow for a POST, so both fail on the client side. Letter case is deliberately NOT folded:
// model ids in the path are case sensitive.
func TestGatewayPathAliasToleratesExtraSlashes(t *testing.T) {
	for _, path := range []string{
		"/v1//chat/completions",
		"//v1/chat/completions",
		"/v1/chat/completions/",
		"/v1//v1/chat/completions",
		"/v1/v1//chat/completions",
		"/v1/chat//completions",
		"/v1//messages",
		"/v1/messages/",
		"/v1//responses",
		"/v1/responses/",
	} {
		t.Run(path, func(t *testing.T) {
			router, seen := newGatewayPathAliasTestRouter()
			handler := GatewayPathAlias(router)
			body := `{"model":"m"}`
			req := httptest.NewRequest(http.MethodPost, path+"?x=1", strings.NewReader(body))
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code, "%s must be served, not redirected or 404", path)
			require.Len(t, *seen, 1, path)
			want := "/v1/chat/completions"
			switch {
			case strings.Contains(path, "messages"):
				want = "/v1/messages"
			case strings.Contains(path, "responses"):
				want = "/v1/responses"
			}
			require.Equal(t, want, (*seen)[0].Path, path)
			require.Equal(t, "x=1", (*seen)[0].RawQuery, path)
			require.Equal(t, body, (*seen)[0].Body, "the body is not consumed or dropped by the rewrite: "+path)
		})
	}
}

// Whatever the cleaning does for gateway-looking paths must not leak into anything else: panel pages and APIs,
// static assets and the SPA keep their exact path, and a path that is not a gateway route stays unmatched.
func TestGatewayPathCleaningLeavesNonGatewayPathsAlone(t *testing.T) {
	for _, tc := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/api/v1/admin/users", http.StatusTeapot},
		{http.MethodGet, "/api/v1/user/usage", http.StatusTeapot},
		{http.MethodGet, "/health", http.StatusTeapot},
		{http.MethodGet, "/usage", http.StatusTeapot},
		{http.MethodGet, "/admin/dashboard", http.StatusTeapot},
		{http.MethodGet, "/assets/index-abc.js", http.StatusTeapot},
		// not gateway routes at all: still not found, not rewritten into one
		{http.MethodPost, "/v1//nothing-here", http.StatusNotFound},
		{http.MethodPost, "/api/v1//chat/completions", http.StatusNotFound},
		{http.MethodPost, "/openai/v1/chat/completions", http.StatusNotFound},
		// letter case is part of the path
		{http.MethodPost, "/V1/chat/completions", http.StatusNotFound},
		{http.MethodPost, "/v1/Chat/Completions", http.StatusNotFound},
		// A panel path with a trailing slash keeps gin's own answer (a 301 to the clean path for GET). If the alias layer
		// rewrote it, the request would be served directly (418 here) and the route would have been "cleaned"
		// by something that is not allowed to touch it.
		{http.MethodGet, "/api/v1/admin/users/", http.StatusMovedPermanently},
		{http.MethodGet, "/api/v1/user/usage/", http.StatusMovedPermanently},
		{http.MethodGet, "/health/", http.StatusMovedPermanently},
		{http.MethodGet, "/usage/", http.StatusMovedPermanently},
		{http.MethodGet, "/admin/dashboard/", http.StatusMovedPermanently},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			router, seen := newGatewayPathAliasTestRouter()
			handler := GatewayPathAlias(router)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))

			require.Equal(t, tc.status, w.Code, tc.path)
			if tc.status == http.StatusTeapot {
				require.Len(t, *seen, 1, tc.path)
				require.Equal(t, strings.SplitN(tc.path, "?", 2)[0], (*seen)[0].Path, "path must be untouched: "+tc.path)
			}
		})
	}
}

func TestCollapseGatewaySlashes(t *testing.T) {
	for in, want := range map[string]string{
		"/":                         "/",
		"//":                        "/",
		"///":                       "/",
		"/v1/chat/completions":      "/v1/chat/completions",
		"/v1/chat/completions/":     "/v1/chat/completions",
		"/v1//chat/completions":     "/v1/chat/completions",
		"//v1/chat/completions":     "/v1/chat/completions",
		"/v1///chat//completions//": "/v1/chat/completions",
		"/v1/Chat/Completions/":     "/v1/Chat/Completions", // only slashes are touched, never letter case
		"/v1/models/a..b/":          "/v1/models/a..b",      // nor dots
		"":                          "",
		"x":                         "x",
	} {
		require.Equal(t, want, collapseGatewaySlashes(in), "in=%q", in)
	}
}

// Cleaning runs before authentication, so a hostile path must cost nothing extra: beyond the length bound the
// path is handed to gin untouched, and the rewrite never happens for it.
func TestGatewayPathCleaningIgnoresOversizedPaths(t *testing.T) {
	long := "/v1/" + strings.Repeat("/", maxGatewayCleanPathBytes) + "chat/completions"
	require.Equal(t, long, collapseGatewaySlashes(long))

	router, seen := newGatewayPathAliasTestRouter()
	handler := GatewayPathAlias(router)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, long, nil))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Empty(t, *seen)

	// Just under the bound it is still cleaned.
	ok := "/v1/" + strings.Repeat("/", 200) + "chat/completions"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, ok, nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, *seen, 1)
	require.Equal(t, "/v1/chat/completions", (*seen)[0].Path)
}
