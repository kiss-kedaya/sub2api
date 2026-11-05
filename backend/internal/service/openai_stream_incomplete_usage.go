package service

import (
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

// OpenAI Responses streams report usage only on the terminal frame. When a stream
// ends without any terminal frame (client disconnect whose drain budget runs out,
// upstream stall, cut connection) after the upstream already produced output, the
// parsed usage is still zero and the handler would record nothing: the output
// would be free. The helpers below estimate a lower bound from what was really
// produced so that output is not free. They never replace a usage frame the
// upstream actually reported, and they never run for streams that reached a
// terminal frame (including response.failed / error, which are authoritative).

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

// fillIncompleteStreamUsage applies the estimate field by field. Output is filled
// only when the upstream reported no output tokens at all; input is filled from the
// request only when the upstream reported no input side at all. A reported value is
// never overwritten. It returns the values it filled in (0 when untouched).
func fillIncompleteStreamUsage(usage *OpenAIUsage, outputEstimate int, requestBody []byte) (filledOutput, filledInput int) {
	if usage == nil || outputEstimate <= 0 {
		return 0, 0
	}
	if usage.OutputTokens > 0 || usage.ImageOutputTokens > 0 {
		return 0, 0
	}
	usage.OutputTokens = outputEstimate
	filledOutput = outputEstimate
	if usage.InputTokens == 0 && usage.CacheReadInputTokens == 0 &&
		usage.CacheCreationInputTokens == 0 && usage.ImageInputTokens == 0 {
		if inputTokens, err := EstimateOpenAIResponsesInputTokens(requestBody); err == nil && inputTokens > 0 {
			usage.InputTokens = inputTokens
			filledInput = inputTokens
		}
	}
	return filledOutput, filledInput
}

// applyIncompleteOpenAIStreamUsage fills the stream result's usage from the
// estimate when the stream failed before a terminal frame but real output existed.
// Replayable failover errors are skipped: those attempts are dropped by
// preserveOpenAIStreamingResultOnError and the retry is billed on its own.
func applyIncompleteOpenAIStreamUsage(account *Account, result *openaiStreamingResult, requestBody []byte, model string, streamErr error) {
	if streamErr == nil || result == nil || result.usage == nil || result.incompleteOutputTokens <= 0 {
		return
	}
	var failoverErr *UpstreamFailoverError
	if errors.As(streamErr, &failoverErr) {
		return
	}
	outputTokens, inputTokens := fillIncompleteStreamUsage(result.usage, result.incompleteOutputTokens, requestBody)
	if outputTokens == 0 {
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
		zap.Error(streamErr),
	).Warn("openai_usage.incomplete_stream_estimated")
}
