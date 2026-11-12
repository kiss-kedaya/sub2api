package service

import "github.com/Wei-Shaw/sub2api/internal/pkg/urljoin"

// anthropicMessagesPath is the canonical Anthropic messages endpoint.
const anthropicMessagesPath = "/v1/messages"

// anthropicCountTokensPath is the Anthropic token-counting endpoint.
const anthropicCountTokensPath = "/v1/messages/count_tokens"

// buildAnthropicMessagesURL joins the Anthropic messages endpoint onto a
// validated upstream base URL.
//
// Operators paste relay bases that already carry the API version
// (`https://relay.example/claude/v1`); appending `/v1/messages` blindly turns
// those into `/v1/v1/messages`, which the upstream answers with 404 while the
// plain `/v1/messages` base works. The shared join keeps the version segment
// exactly once for every base spelling.
func buildAnthropicMessagesURL(base string) string {
	return urljoin.APIPath(base, anthropicMessagesPath)
}

// buildAnthropicMessagesURLWithBeta is buildAnthropicMessagesURL plus the
// `beta=true` flag the messages endpoint expects. A query already present on
// the base is preserved instead of being replaced.
func buildAnthropicMessagesURLWithBeta(base string) string {
	return urljoin.WithQueryParam(buildAnthropicMessagesURL(base), "beta", "true")
}

// buildAnthropicCountTokensURL returns the count_tokens endpoint for the same
// base spellings buildAnthropicMessagesURL accepts.
func buildAnthropicCountTokensURL(base string) string {
	return urljoin.WithQueryParam(urljoin.APIPath(base, anthropicCountTokensPath), "beta", "true")
}
