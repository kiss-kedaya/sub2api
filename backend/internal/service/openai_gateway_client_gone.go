package service

import (
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// A client that leaves after its request was dispatched upstream leaves no usage
// report behind, because the response was never read (the upstream is canceled the
// moment the client goes). Billing policy (owner decision 2026-11-06, see
// incomplete_request_usage.go): the input that was sent is billed from the
// request-body estimate, 95% of it priced as cache read; no output is invented. A
// request the upstream rejected with an error status, or one that never left the
// gateway (errOpenAIRequestNotSent), is not billed.
//
// It returns nil when the request body cannot be estimated, so nothing is recorded.
func newClientGoneOpenAIForwardResult(
	c *gin.Context,
	account *Account,
	requestBody []byte,
	originalModel, billingModel, upstreamModel string,
	reasoningEffort *string,
	stream bool,
	startTime time.Time,
) *OpenAIForwardResult {
	var usage OpenAIUsage
	_, inputTokens := fillIncompleteStreamUsage(&usage, 0, requestBody)
	if inputTokens <= 0 {
		return nil
	}
	accountID := int64(0)
	if account != nil {
		accountID = account.ID
	}
	logger.L().With(
		zap.String("component", "service.openai_gateway"),
		zap.Int64("account_id", accountID),
		zap.String("model", strings.TrimSpace(upstreamModel)),
		zap.Bool("stream", stream),
		zap.Int("estimated_input_tokens", usage.InputTokens),
		zap.Int("estimated_cache_read_tokens", usage.CacheReadInputTokens),
	).Warn("openai_usage.client_gone_estimated")
	return &OpenAIForwardResult{
		Usage:            usage,
		Model:            originalModel,
		BillingModel:     billingModel,
		UpstreamModel:    upstreamModel,
		ServiceTier:      resolvedOpenAIUpstreamServiceTier(c, extractOpenAIServiceTierFromBody(requestBody)),
		ReasoningEffort:  reasoningEffort,
		Stream:           stream,
		Duration:         time.Since(startTime),
		ClientDisconnect: true,
	}
}
