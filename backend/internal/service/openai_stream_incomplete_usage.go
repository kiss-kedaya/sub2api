package service

import (
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

// OpenAI Responses streams report usage only on the terminal frame. A stream that
// ends without one (the client left, the upstream was cut or stalled) leaves the
// parsed usage at zero and the handler would record nothing.
//
// Billing policy for that case (owner decision, 2026-11-06):
//   - the upstream connection is closed the moment the client disconnects;
//   - output is billed for what was really produced, estimated from the streamed
//     content;
//   - input is billed for what was really sent, estimated from the request body,
//     with 95% of it priced as cache read and the rest as normal input.
//
// A usage figure the upstream really reported is never overwritten, and a stream
// that reached a terminal frame (response.completed / failed / error) is never
// estimated.

// incompleteStreamInputCacheReadPercent is the share of the estimated input priced
// as cache read. It applies only to estimated input, never to reported usage.
const incompleteStreamInputCacheReadPercent = 95

// incompleteStreamCacheReadTokens rounds down, so the remainder (the part priced as
// normal input) is the larger side of any rounding.
func incompleteStreamCacheReadTokens(inputTokens int) int {
	if inputTokens <= 0 {
		return 0
	}
	return inputTokens * incompleteStreamInputCacheReadPercent / 100
}

// estimateIncompleteStreamOutputTokens estimates the output tokens of the content
// accumulated from stream deltas (visible text, refusal, reasoning summary,
// function-call name and arguments). Hidden reasoning tokens are not streamed and
// custom tool input deltas are not accumulated, so this is a lower bound.
// It returns 0 when nothing was produced.
func estimateIncompleteStreamOutputTokens(acc *apicompat.BufferedResponseAccumulator) int {
	if acc == nil || !acc.HasContent() {
		return 0
	}
	total := 0
	for _, item := range acc.BuildOutput() {
		for _, summary := range item.Summary {
			total += estimateTokensForText(summary.Text)
		}
		for _, part := range item.Content {
			total += estimateTokensForText(part.Text)
			total += estimateTokensForText(part.Refusal)
		}
		total += estimateTokensForText(item.Name)
		total += estimateTokensForText(item.Arguments)
	}
	return total
}

// fillIncompleteStreamUsage applies the estimates field by field and returns what
// it filled in (0 when untouched). Output is filled only when the upstream reported
// no output tokens at all. Input is filled from the request only when the upstream
// reported no input side at all; InputTokens stays the total (as OpenAI reports it)
// and CacheReadInputTokens carries the cache-priced share of it.
func fillIncompleteStreamUsage(usage *OpenAIUsage, outputEstimate int, requestBody []byte) (filledOutput, filledInput int) {
	if usage == nil {
		return 0, 0
	}
	if outputEstimate > 0 && usage.OutputTokens == 0 && usage.ImageOutputTokens == 0 {
		usage.OutputTokens = outputEstimate
		filledOutput = outputEstimate
	}
	if usage.InputTokens == 0 && usage.CacheReadInputTokens == 0 &&
		usage.CacheCreationInputTokens == 0 && usage.ImageInputTokens == 0 {
		if inputTokens, err := EstimateOpenAIResponsesInputTokens(requestBody); err == nil && inputTokens > 0 {
			usage.InputTokens = inputTokens
			usage.CacheReadInputTokens = incompleteStreamCacheReadTokens(inputTokens)
			filledInput = inputTokens
		}
	}
	return filledOutput, filledInput
}

// applyIncompleteOpenAIStreamUsage fills the stream result's usage from the
// estimates when the stream ended without a terminal frame.
//
// Input is billed once the upstream had accepted the request and the client then
// left, or whenever output was produced. An upstream break that produced nothing
// stays free. Replayable failover errors are skipped: those attempts are dropped by
// preserveOpenAIStreamingResultOnError and the retry is billed on its own.
func applyIncompleteOpenAIStreamUsage(account *Account, result *openaiStreamingResult, requestBody []byte, model string, streamErr error) {
	if streamErr == nil || result == nil || result.usage == nil || !result.incompleteStream {
		return
	}
	var failoverErr *UpstreamFailoverError
	if errors.As(streamErr, &failoverErr) {
		return
	}
	if !result.clientDisconnect && result.incompleteOutputTokens <= 0 {
		return
	}
	outputTokens, inputTokens := fillIncompleteStreamUsage(result.usage, result.incompleteOutputTokens, requestBody)
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
		zap.String("model", strings.TrimSpace(model)),
		zap.String("response_id", result.responseID),
		zap.Bool("client_disconnect", result.clientDisconnect),
		zap.Int("estimated_output_tokens", outputTokens),
		zap.Int("estimated_input_tokens", inputTokens),
		zap.Int("estimated_cache_read_tokens", incompleteStreamCacheReadTokens(inputTokens)),
		zap.Error(streamErr),
	).Warn("openai_usage.incomplete_stream_estimated")
}
