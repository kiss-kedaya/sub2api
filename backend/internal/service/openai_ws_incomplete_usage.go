package service

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

// A Responses WebSocket turn reports usage only on its terminal event. When the
// client leaves while the turn is still running, the upstream is closed on the spot
// and that event never arrives, so the turn would be recorded with zero usage. The
// billing policy for that case (output estimated from what the upstream produced,
// input from the request with 95% priced as cache read, nothing the upstream really
// reported is overwritten) is shared with the SSE path: this file applies it to a WS
// turn.

// incompleteOpenAIWSTurn names a turn that ended without a terminal event, for the
// estimate log line.
type incompleteOpenAIWSTurn struct {
	// mode is the ingress path: ctx_pool, http_bridge, passthrough or forward_v2.
	mode       string
	model      string
	responseID string
}

// applyIncompleteOpenAIWSTurnUsage fills usage from the estimates. Output comes from
// the content the upstream produced; input comes from the request body that was sent.
// Each side is filled only when the upstream reported nothing for it. It is meant for
// a turn whose request reached the upstream: a request that never left must not call
// it. Nothing is logged when nothing could be estimated.
func applyIncompleteOpenAIWSTurnUsage(
	account *Account,
	usage *OpenAIUsage,
	output *apicompat.BufferedResponseAccumulator,
	requestBody []byte,
	turn incompleteOpenAIWSTurn,
) {
	if usage == nil {
		return
	}
	outputTokens, inputTokens := fillIncompleteStreamUsage(usage, estimateIncompleteStreamOutputTokens(output), requestBody)
	if outputTokens == 0 && inputTokens == 0 {
		return
	}
	accountID := int64(0)
	if account != nil {
		accountID = account.ID
	}
	logger.L().With(
		zap.String("component", "service.openai_gateway"),
		zap.Int64("account_id", accountID),
		zap.String("ws_mode", turn.mode),
		zap.String("model", strings.TrimSpace(turn.model)),
		zap.String("response_id", turn.responseID),
		zap.Bool("client_disconnect", true),
		zap.Int("estimated_output_tokens", outputTokens),
		zap.Int("estimated_input_tokens", inputTokens),
		zap.Int("estimated_cache_read_tokens", incompleteCacheReadTokens(inputTokens)),
	).Warn("openai_usage.incomplete_ws_turn_estimated")
}
