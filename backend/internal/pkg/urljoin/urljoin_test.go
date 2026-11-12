package urljoin

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAPIPathModelsBaseURLTable is the regression table for the reported bug:
// every base_url spelling an operator can paste into an account or audit node
// must end up on the single /v1/models endpoint.
func TestAPIPathModelsBaseURLTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		base string
		want string
	}{
		{base: "https://x", want: "https://x/v1/models"},
		{base: "https://x/", want: "https://x/v1/models"},
		{base: "https://x/v1", want: "https://x/v1/models"},
		{base: "https://x/v1/", want: "https://x/v1/models"},
		{base: "https://x/api/openai/v1", want: "https://x/api/openai/v1/models"},
		{base: "https://x/prefix", want: "https://x/prefix/v1/models"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.base, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, APIPath(tt.base, "/v1/models"))
		})
	}
}

// TestAPIPathMessagesBaseURLTable covers the same base_url table for the
// Anthropic messages endpoint: no spelling may produce /v1/v1/messages.
func TestAPIPathMessagesBaseURLTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		base string
		want string
	}{
		{base: "https://x", want: "https://x/v1/messages"},
		{base: "https://x/", want: "https://x/v1/messages"},
		{base: "https://x/v1", want: "https://x/v1/messages"},
		{base: "https://x/v1/", want: "https://x/v1/messages"},
		{base: "https://x/api/openai/v1", want: "https://x/api/openai/v1/messages"},
		{base: "https://x/prefix", want: "https://x/prefix/v1/messages"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.base, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, APIPath(tt.base, "/v1/messages"))
		})
	}
}

func TestAPIPathPreservesQueryAndDropsFragment(t *testing.T) {
	t.Parallel()

	require.Equal(t, "https://x/v1/models?tenant=a", APIPath("https://x/v1?tenant=a", "/v1/models"))
	require.Equal(t, "https://x/v1/models?tenant=a", APIPath("https://x/v1/?tenant=a", "/v1/models"))
	require.Equal(t, "https://x/v1/models?tenant=a", APIPath("https://x/v1/models?tenant=a", "/v1/models"))
	require.Equal(t, "https://x/v1/messages?beta=true", APIPath("https://x/v1?beta=true", "/v1/messages"))
	require.Equal(t, "https://x/v1/models", APIPath("https://x/v1#stale", "/v1/models"))
	require.Equal(t, "https://x/v1/models?tenant=a", APIPath("https://x/v1?tenant=a#stale", "/v1/models"))
}

func TestAPIPathDoesNotTreatLookalikeSegmentsAsVersion(t *testing.T) {
	t.Parallel()

	// /v10 and an encoded slash are plain path segments, not the v1 prefix.
	require.Equal(t, "https://x/v10/v1/messages", APIPath("https://x/v10", "/v1/messages"))
	require.Equal(t, "https://relay.example/tenant%2Fv1/v1/messages",
		APIPath("https://relay.example/tenant%2Fv1", "/v1/messages"))
	// A hostname that happens to be "v1" is not a path segment either.
	require.Equal(t, "https://v1/v1/messages", APIPath("https://v1", "/v1/messages"))
}

func TestAPIPathLeavesCompleteEndpointsAlone(t *testing.T) {
	t.Parallel()

	require.Equal(t, "https://x/v1/models", APIPath("https://x/v1/models", "/v1/models"))
	require.Equal(t, "https://x/anthropic/v1/models", APIPath("https://x/anthropic/v1/models", "/v1/models"))
	require.Equal(t, "https://x/v1/messages", APIPath("https://x/v1/messages", "/v1/messages"))
	require.Equal(t, "https://x/anthropic/v1/messages", APIPath("https://x/anthropic/v1/messages", "/v1/messages"))
}

func TestAPIPathKeepsVersionedBases(t *testing.T) {
	t.Parallel()

	require.Equal(t, "https://generativelanguage.googleapis.com/v1beta/models",
		APIPath("https://generativelanguage.googleapis.com/v1beta", "/v1beta/models"))
	require.Equal(t, "https://generativelanguage.googleapis.com/v1beta/models",
		APIPath("https://generativelanguage.googleapis.com", "/v1beta/models"))
}

func TestAPIPathDegenerateInputs(t *testing.T) {
	t.Parallel()

	require.Empty(t, APIPath("", "/v1/models"))
	require.Empty(t, APIPath("   ", "/v1/models"))
	require.Equal(t, "https://x/v1/models", APIPath("https://x", "v1/models"))
	require.Equal(t, "https://x/v1/models", APIPath("https://x", "/v1/models/"))
	require.Equal(t, "https://x/v1/models", APIPath("  https://x  ", "  /v1/models  "))
}

func TestWithQueryParam(t *testing.T) {
	t.Parallel()

	require.Equal(t, "https://x/v1/messages?beta=true", WithQueryParam("https://x/v1/messages", "beta", "true"))
	require.Equal(t, "https://x/v1/messages?beta=true&proxy=p", WithQueryParam("https://x/v1/messages?beta=true", "proxy", "p"))
	require.Equal(t, "https://x/v1/messages?beta=true", WithQueryParam("https://x/v1/messages?", "beta", "true"))
	require.Equal(t, "https://x/v1/messages?a=b", WithQueryParam("https://x/v1/messages?a=b", "", "true"))
	require.Equal(t, "https://x/v1/messages?a=b", WithQueryParam("https://x/v1/messages?a=b", "beta", ""))
}
