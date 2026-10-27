package transportdiag

import (
	"context"
	"net/http"
	"strings"
)

// Route contains diagnostic codes, never URLs, headers, or client identity.
// Upstream describes the observed transport; Mode/Reason describe gateway policy.
// An HTTP response is not assumed to be SSE until its Content-Type says so.
type Route struct {
	Downstream     string `json:"downstream,omitempty"`
	Upstream       string `json:"upstream,omitempty"`
	Mode           string `json:"mode,omitempty"`
	Reason         string `json:"reason,omitempty"`
	FallbackReason string `json:"fallback_reason,omitempty"`
	Endpoint       string `json:"endpoint,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
}

type routeKey struct{}

func WithRoute(ctx context.Context, route Route) context.Context {
	return context.WithValue(ctx, routeKey{}, route)
}

func RouteFromContext(ctx context.Context) Route {
	if ctx == nil {
		return Route{}
	}
	route, _ := ctx.Value(routeKey{}).(Route)
	return route
}

// EndpointCategory intentionally discards hosts, arbitrary paths and queries.
func EndpointCategory(path string) string {
	path = strings.TrimRight(path, "/")
	for _, endpoint := range []string{"responses/compact", "responses", "chat/completions", "messages"} {
		if path == "/"+endpoint || strings.HasSuffix(path, "/"+endpoint) {
			return strings.ReplaceAll(endpoint, "/", "_")
		}
	}
	return "other"
}

func HTTPRoute(req *http.Request) Route {
	route := RouteFromContext(req.Context())
	route.Upstream = "http"
	route.Endpoint = EndpointCategory(req.URL.Path)
	route.ResponseFormat = ""
	return route
}

func ResponseFormat(h http.Header) string {
	ct, _, _ := strings.Cut(strings.ToLower(h.Get("Content-Type")), ";")
	switch strings.TrimSpace(ct) {
	case "text/event-stream":
		return "sse"
	case "application/json":
		return "json"
	default:
		return "" // Absent/unknown must not be reported as SSE or JSON.
	}
}
