package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBuildAnthropicMessagesURL is the messages half of the reported bug: the
// Anthropic endpoint must not gain a second version segment when the account
// or relay base URL already carries `/v1`.
func TestBuildAnthropicMessagesURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		base string
		want string
	}{
		{name: "origin", base: "https://x", want: "https://x/v1/messages"},
		{name: "origin trailing slash", base: "https://x/", want: "https://x/v1/messages"},
		{name: "version suffix", base: "https://x/v1", want: "https://x/v1/messages"},
		{name: "version suffix trailing slash", base: "https://x/v1/", want: "https://x/v1/messages"},
		{name: "versioned prefix", base: "https://x/api/openai/v1", want: "https://x/api/openai/v1/messages"},
		{name: "plain prefix", base: "https://x/prefix", want: "https://x/prefix/v1/messages"},
		{name: "already complete", base: "https://x/v1/messages", want: "https://x/v1/messages"},
		{name: "query preserved", base: "https://x/v1?tenant=a", want: "https://x/v1/messages?tenant=a"},
		{name: "ollama default", base: "https://ollama.com", want: "https://ollama.com/v1/messages"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, buildAnthropicMessagesURL(tt.base))
		})
	}
}

func TestBuildAnthropicMessagesURLWithBeta(t *testing.T) {
	t.Parallel()

	require.Equal(t, "https://x/v1/messages?beta=true", buildAnthropicMessagesURLWithBeta("https://x"))
	require.Equal(t, "https://x/v1/messages?beta=true", buildAnthropicMessagesURLWithBeta("https://x/v1"))
	require.Equal(t, "https://x/claude/v1/messages?beta=true", buildAnthropicMessagesURLWithBeta("https://x/claude/v1/"))
	// An existing query stays intact and beta is appended, not substituted.
	require.Equal(t, "https://x/v1/messages?tenant=a&beta=true", buildAnthropicMessagesURLWithBeta("https://x/v1?tenant=a"))
}
