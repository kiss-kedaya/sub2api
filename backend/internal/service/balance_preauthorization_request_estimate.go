package service

import (
	"math"

	"github.com/tidwall/gjson"
)

const DefaultBalancePreauthorizationInputTokens = 500

// BalancePreauthorizationTokenEstimate is derived entirely from the current
// request. It deliberately carries no historical usage dependency: reserve
// sizing must not turn every gateway request into an aggregate database query.
type BalancePreauthorizationTokenEstimate struct {
	InputTokens  int
	OutputTokens int
}

// EstimateBalancePreauthorizationTokens follows the request-local pre-consume
// model used by mature API gateways: estimate prompt tokens before forwarding,
// include an explicit client output limit when present, and reconcile the
// reserve against authoritative provider usage after the request completes.
//
// Protocol-specific estimators already used by the count_tokens endpoints are
// preferred. If a request shape cannot be estimated, raw bytes remain the
// conservative upper bound. The minimum protects small requests without any
// database lookup; it is a reserve only and is never written as actual usage.
func EstimateBalancePreauthorizationTokens(body []byte) BalancePreauthorizationTokenEstimate {
	isStream := gjson.GetBytes(body, "stream").Bool()
	return EstimateBalancePreauthorizationTokensForStream(body, isStream)
}

// EstimateBalancePreauthorizationTokensForStream estimates prompt and initial
// output reserve tokens with an explicit streaming flag. Streaming requests can
// safely use the smaller 256-token initial window because ObserveStreamingOutput
// tops up the hold mid-stream and aborts on exhaustion; non-streaming requests
// without an explicit output limit use the conservative 4096-token window.
func EstimateBalancePreauthorizationTokensForStream(body []byte, isStream bool) BalancePreauthorizationTokenEstimate {
	inputTokens := estimateBalancePreauthorizationInputTokens(body)
	if inputTokens < DefaultBalancePreauthorizationInputTokens {
		inputTokens = DefaultBalancePreauthorizationInputTokens
	}

	outputTokens := requestedBalancePreauthorizationOutputTokens(body)
	if outputTokens <= 0 {
		if isStream {
			outputTokens = DefaultBalancePreauthorizationOutputWindow
		} else {
			outputTokens = DefaultBalancePreauthorizationNonStreamOutputWindow
		}
	}

	return BalancePreauthorizationTokenEstimate{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}
}

// HasExplicitBalancePreauthorizationOutputLimit reports whether body specifies
// any positive explicit output limit or thinking budget.
func HasExplicitBalancePreauthorizationOutputLimit(body []byte) bool {
	return requestedBalancePreauthorizationOutputTokens(body) > 0
}

func estimateBalancePreauthorizationInputTokens(body []byte) int {
	if len(body) == 0 {
		return 0
	}
	// The shape-based estimator is shared with the incomplete-request billing
	// (incomplete_request_usage.go); it returns 0 for a shape it cannot estimate.
	// The raw variant is kept on purpose: a reserve is a conservative upper bound and
	// the billing variant folds base64 payloads, which would shrink it.
	if estimated := estimateRequestInputTokensRaw(body); estimated > 0 {
		return estimated
	}

	// One token cannot encode fewer than one source byte, so this fallback is
	// conservative even for an unfamiliar or newly introduced request shape.
	return len(body)
}

func requestedBalancePreauthorizationOutputTokens(body []byte) int {
	root := gjson.ParseBytes(body)
	explicitMaxOutput := maxPositiveJSONInt64(root,
		"max_output_tokens",
		"max_completion_tokens",
		"max_tokens",
		"generationConfig.maxOutputTokens",
		"generation_config.max_output_tokens",
	)
	anthropicThinkingBudget := maxPositiveJSONInt64(root,
		"thinking.budget_tokens",
	)
	geminiThinkingBudget := maxPositiveJSONInt64(root,
		"generationConfig.thinkingConfig.thinkingBudget",
		"generation_config.thinking_config.thinking_budget",
	)

	maximum := explicitMaxOutput
	// Anthropic's max_tokens includes thinking tokens, so max(explicit, budget)
	// honors the thinking budget without double-counting when max_tokens is set.
	if anthropicThinkingBudget > maximum {
		maximum = anthropicThinkingBudget
	}
	// Gemini thinking tokens can be emitted alongside answer tokens. When
	// maxOutputTokens is omitted while thinkingBudget > 0, reserve at least
	// thinkingBudget + DefaultBalancePreauthorizationOutputWindow; when
	// maxOutputTokens is set, take max(explicit, thinkingBudget).
	if geminiThinkingBudget > 0 {
		candidate := geminiThinkingBudget
		if explicitMaxOutput == 0 {
			if candidate > math.MaxInt64-int64(DefaultBalancePreauthorizationOutputWindow) {
				candidate = math.MaxInt64
			} else {
				candidate += int64(DefaultBalancePreauthorizationOutputWindow)
			}
		}
		if candidate > maximum {
			maximum = candidate
		}
	}

	if maximum <= 0 {
		return 0
	}
	if uint64(maximum) > uint64(math.MaxInt) {
		return math.MaxInt
	}
	return int(maximum)
}

func maxPositiveJSONInt64(root gjson.Result, paths ...string) int64 {
	maximum := int64(0)
	for _, path := range paths {
		value := root.Get(path)
		if !value.Exists() || value.Type != gjson.Number {
			continue
		}
		candidate := value.Int()
		if candidate > maximum {
			maximum = candidate
		}
	}
	return maximum
}
