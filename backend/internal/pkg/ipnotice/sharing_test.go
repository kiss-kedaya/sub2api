package ipnotice

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIsGatewayBusinessPathCoversRootAliases is the regression for the root
// gateway aliases. Routes such as POST /responses, GET /models and
// POST /messages/count_tokens are the same API-key-authenticated gateway as
// their /v1 counterparts; before they were listed here a shared credential
// could call only the root aliases and never appear in the distinct-user
// counter, so the IP was never listed and the keys were never disabled.
func TestIsGatewayBusinessPathCoversRootAliases(t *testing.T) {
	for _, path := range []string{
		"/responses",
		"/responses/anything",
		"/alpha/search",
		"/models",
		"/models/gpt-5",
		"/messages/count_tokens",
		"/chat/completions",
		"/embeddings",
		"/images/generations",
		"/images/generations/async",
		"/images/edits/async",
		"/images/tasks/task_1",
		"/videos",
		"/videos/generations",
		"/videos/generations/req_1/content",
		"/tts",
		"/stt",
		"/custom-voices",
		"/custom-voices/voice_1/audio",
		"/realtime",
		"/web_search",
		"/x_search",
	} {
		require.True(t, IsGatewayBusinessPath(path), path)
	}

	// Prefixed /v1 routes keep working.
	for _, path := range []string{
		"/v1/messages",
		"/v1/chat/completions",
		"/v1/responses",
		"/v1beta/models",
		"/api/v3/contents/generations/tasks",
		"/antigravity/v1/usage",
		"/backend-api/codex/responses",
	} {
		require.True(t, IsGatewayBusinessPath(path), path)
	}

	// Infrastructure and admin traffic must never count. The root-alias list is
	// matched on a path-segment boundary, so a lookalike path does not match.
	for _, path := range []string{
		"",
		"/",
		"/health",
		"/healthz",
		"/readyz",
		"/metrics",
		"/static/app.js",
		"/admin/settings",
		"/api/v1/admin/settings",
		"/modelsx",
		"/responsesx",
		"/ttsx",
		"/videosx",
	} {
		require.False(t, IsGatewayBusinessPath(path), path)
	}
}
