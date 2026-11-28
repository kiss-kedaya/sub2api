package service

import (
	"context"
	"math"
	"strings"

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

// EstimateBalancePreauthorizationTokensForContext uses an explicit transport-level
// stream hint on ctx when present (e.g. Gemini URL actions :streamGenerateContent
// vs :generateContent, where JSON "stream" is ignored or spoofable) and otherwise
// falls back to the JSON body's "stream" boolean.
func EstimateBalancePreauthorizationTokensForContext(ctx context.Context, body []byte) BalancePreauthorizationTokenEstimate {
	if isStream, hasHint := BalancePreauthorizationStreamHintFromContext(ctx); hasHint {
		return EstimateBalancePreauthorizationTokensForStream(body, isStream)
	}
	return EstimateBalancePreauthorizationTokens(body)
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

	defaultWindow := DefaultBalancePreauthorizationNonStreamOutputWindow
	if isStream {
		defaultWindow = DefaultBalancePreauthorizationOutputWindow
	}
	outputTokens := requestedBalancePreauthorizationOutputTokens(body, defaultWindow)
	if outputTokens <= 0 {
		outputTokens = defaultWindow
	}

	return BalancePreauthorizationTokenEstimate{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}
}

// HasExplicitBalancePreauthorizationOutputLimit reports whether body specifies
// a positive explicit output token limit for its inferred protocol shape.
// Thinking budgets alone do not count as an explicit output cap.
func HasExplicitBalancePreauthorizationOutputLimit(body []byte) bool {
	root := gjson.ParseBytes(body)
	hasContents, hasInput, hasMessages := detectPreauthProtocolShapes(root)
	shapes := boolToInt(hasContents) + boolToInt(hasInput) + boolToInt(hasMessages)
	if shapes > 1 {
		// Ambiguous multi-protocol body: never treat a foreign cap as authoritative
		// unless every present protocol specifies an explicit limit.
		if hasContents && maxPositiveJSONInt64(root, "generationConfig.maxOutputTokens", "generation_config.max_output_tokens") <= 0 {
			return false
		}
		if hasInput && maxPositiveJSONInt64(root, "max_output_tokens", "max_tokens") <= 0 {
			return false
		}
		if hasMessages && maxPositiveJSONInt64(root, "max_completion_tokens", "max_tokens") <= 0 {
			return false
		}
		return true
	}
	switch {
	case hasContents:
		return maxPositiveJSONInt64(root, "generationConfig.maxOutputTokens", "generation_config.max_output_tokens") > 0
	case hasInput:
		return maxPositiveJSONInt64(root, "max_output_tokens", "max_tokens") > 0
	case hasMessages:
		return maxPositiveJSONInt64(root, "max_completion_tokens", "max_tokens") > 0
	default:
		return maxPositiveJSONInt64(root,
			"max_output_tokens",
			"max_completion_tokens",
			"max_tokens",
			"generationConfig.maxOutputTokens",
			"generation_config.max_output_tokens",
		) > 0
	}
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

func requestedBalancePreauthorizationOutputTokens(body []byte, defaultOutputWindow int) int {
	root := gjson.ParseBytes(body)
	hasContents, hasInput, hasMessages := detectPreauthProtocolShapes(root)
	shapes := boolToInt(hasContents) + boolToInt(hasInput) + boolToInt(hasMessages)

	var maximum int64
	switch {
	case shapes == 1 && hasContents:
		maximum = geminiPreauthOutputTokens(root, defaultOutputWindow)
	case shapes == 1 && hasInput:
		maximum = responsesPreauthOutputTokens(root)
	case shapes == 1 && hasMessages:
		maximum = messagesPreauthOutputTokens(root, defaultOutputWindow)
	case shapes > 1:
		// Multi-protocol body (e.g. messages + smuggled contents/generationConfig):
		// compute each present protocol's reserve (using defaultOutputWindow when
		// that protocol omits its own explicit limit) and take the maximum so a
		// foreign field can never downgrade a non-streaming request from 4096 to 1.
		if hasContents {
			v := geminiPreauthOutputTokens(root, defaultOutputWindow)
			if v <= 0 {
				v = int64(defaultOutputWindow)
			}
			if v > maximum {
				maximum = v
			}
		}
		if hasInput {
			v := responsesPreauthOutputTokens(root)
			if v <= 0 {
				v = int64(defaultOutputWindow)
			}
			if v > maximum {
				maximum = v
			}
		}
		if hasMessages {
			v := messagesPreauthOutputTokens(root, defaultOutputWindow)
			if v <= 0 {
				v = int64(defaultOutputWindow)
			}
			if v > maximum {
				maximum = v
			}
		}
	default:
		// Unrecognized shape (e.g. Responses with previous_response_id + instructions
		// or legacy prompt): check OpenAI/Anthropic limits without letting a
		// foreign Gemini cap downgrade an unrelated request.
		explicitMaxOutput := maxPositiveJSONInt64(root,
			"max_output_tokens",
			"max_completion_tokens",
			"max_tokens",
		)
		anthropicThinkingBudget := maxPositiveJSONInt64(root, "thinking.budget_tokens")
		if explicitMaxOutput > 0 {
			maximum = explicitMaxOutput
			if anthropicThinkingBudget > maximum {
				maximum = anthropicThinkingBudget
			}
		} else if anthropicThinkingBudget > 0 {
			maximum = int64(defaultOutputWindow)
			if anthropicThinkingBudget > maximum {
				maximum = anthropicThinkingBudget
			}
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

func detectPreauthProtocolShapes(root gjson.Result) (hasContents, hasInput, hasMessages bool) {
	hasContents = hasNonEmptyPreauthJSONField(root, "contents") ||
		hasNonEmptyPreauthJSONField(root, "systemInstruction") ||
		hasNonEmptyPreauthJSONField(root, "system_instruction")
	hasInput = hasNonEmptyPreauthJSONField(root, "input")
	hasMessages = hasNonEmptyPreauthJSONField(root, "messages")
	return hasContents, hasInput, hasMessages
}

func hasNonEmptyPreauthJSONField(root gjson.Result, path string) bool {
	value := root.Get(path)
	if !value.Exists() || value.Type == gjson.Null {
		return false
	}
	switch {
	case value.IsArray():
		return len(value.Array()) > 0
	case value.IsObject():
		return len(value.Map()) > 0
	case value.Type == gjson.String:
		return strings.TrimSpace(value.String()) != ""
	default:
		return true
	}
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func geminiPreauthOutputTokens(root gjson.Result, defaultOutputWindow int) int64 {
	explicitMaxOutput := maxPositiveJSONInt64(root,
		"generationConfig.maxOutputTokens",
		"generation_config.max_output_tokens",
	)
	geminiThinkingBudget := maxPositiveJSONInt64(root,
		"generationConfig.thinkingConfig.thinkingBudget",
		"generation_config.thinking_config.thinking_budget",
	)
	maximum := explicitMaxOutput
	// Gemini thinking tokens can be emitted alongside answer tokens. When
	// maxOutputTokens is omitted while thinkingBudget > 0, reserve at least
	// thinkingBudget + defaultOutputWindow (4096 for non-streaming, 256 for
	// streaming); when maxOutputTokens is set, take max(explicit, thinkingBudget).
	if geminiThinkingBudget > 0 {
		candidate := geminiThinkingBudget
		if explicitMaxOutput == 0 {
			window := int64(defaultOutputWindow)
			if window < 0 {
				window = 0
			}
			if candidate > math.MaxInt64-window {
				candidate = math.MaxInt64
			} else {
				candidate += window
			}
		}
		if candidate > maximum {
			maximum = candidate
		}
	}
	return maximum
}

func responsesPreauthOutputTokens(root gjson.Result) int64 {
	return maxPositiveJSONInt64(root,
		"max_output_tokens",
		"max_tokens",
	)
}

func messagesPreauthOutputTokens(root gjson.Result, defaultOutputWindow int) int64 {
	explicitMaxOutput := maxPositiveJSONInt64(root,
		"max_completion_tokens",
		"max_tokens",
	)
	anthropicThinkingBudget := maxPositiveJSONInt64(root,
		"thinking.budget_tokens",
	)
	if explicitMaxOutput > 0 {
		// Anthropic's max_tokens includes thinking tokens, so max(explicit, budget)
		// honors the thinking budget without double-counting when max_tokens is set.
		if anthropicThinkingBudget > explicitMaxOutput {
			return anthropicThinkingBudget
		}
		return explicitMaxOutput
	}
	if anthropicThinkingBudget > 0 {
		// Without an explicit output cap, thinking.budget_tokens alone must never
		// downgrade the default output reserve (e.g. non-stream 4096 -> 1).
		window := int64(defaultOutputWindow)
		if anthropicThinkingBudget > window {
			return anthropicThinkingBudget
		}
		return window
	}
	return 0
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
