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
	inputTokens := estimateBalancePreauthorizationInputTokens(body)
	if inputTokens < DefaultBalancePreauthorizationInputTokens {
		inputTokens = DefaultBalancePreauthorizationInputTokens
	}

	outputTokens := requestedBalancePreauthorizationOutputTokens(body)
	if outputTokens <= 0 {
		outputTokens = DefaultBalancePreauthorizationOutputWindow
	}

	return BalancePreauthorizationTokenEstimate{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}
}

func estimateBalancePreauthorizationInputTokens(body []byte) int {
	if len(body) == 0 {
		return 0
	}

	root := gjson.ParseBytes(body)
	var estimated int
	var err error
	switch {
	case root.Get("contents").Exists() || root.Get("systemInstruction").Exists():
		estimated = estimateGeminiCountTokens(body)
	case root.Get("input").Exists():
		estimated, err = EstimateOpenAIResponsesInputTokens(body)
	case root.Get("messages").Exists():
		estimated, err = EstimateAnthropicCountTokens(body)
	}
	if err == nil && estimated > 0 {
		return estimated
	}

	// One token cannot encode fewer than one source byte, so this fallback is
	// conservative even for an unfamiliar or newly introduced request shape.
	return len(body)
}

func requestedBalancePreauthorizationOutputTokens(body []byte) int {
	root := gjson.ParseBytes(body)
	paths := [...]string{
		"max_output_tokens",
		"max_completion_tokens",
		"max_tokens",
		"generationConfig.maxOutputTokens",
	}
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
	if maximum <= 0 {
		return 0
	}
	if uint64(maximum) > uint64(math.MaxInt) {
		return math.MaxInt
	}
	return int(maximum)
}
