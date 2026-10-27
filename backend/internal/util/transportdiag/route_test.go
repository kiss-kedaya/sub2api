package transportdiag

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHTTPRouteUsesActualEndpointAndDoesNotRetainSecrets(t *testing.T) {
	parent := Route{Downstream: "websocket", Upstream: "websocket", Mode: "http_bridge", Endpoint: "responses"}
	ctx := WithRoute(context.Background(), parent)
	for path, want := range map[string]string{
		"/backend-api/codex/responses": "responses",
		"/v1/responses/compact/":       "responses_compact",
		"/v1/chat/completions":         "chat_completions",
		"/v1/messages":                 "messages",
		"/private/SECRET_PATH":         "other",
	} {
		req, err := http.NewRequestWithContext(ctx, "POST", "https://user:SECRET_PASSWORD@example.test"+path+"?token=SECRET_QUERY", nil)
		require.NoError(t, err)
		route := HTTPRoute(req)
		require.Equal(t, want, route.Endpoint)
		require.Equal(t, "http", route.Upstream)
		require.Equal(t, "websocket", route.Downstream)
		require.Equal(t, parent, RouteFromContext(ctx))
		raw, err := json.Marshal(route)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "SECRET")
	}
}

func TestResponseFormatDoesNotGuessMissingOrUnknownContentType(t *testing.T) {
	for ct, want := range map[string]string{"": "", "application/octet-stream": "", "application/json; charset=utf-8": "json", "text/event-stream": "sse", "TEXT/EVENT-STREAM; charset=utf-8": "sse"} {
		require.Equal(t, want, ResponseFormat(http.Header{"Content-Type": {ct}}), strings.TrimSpace(ct))
	}
}
