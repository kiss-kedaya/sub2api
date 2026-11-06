package service

import "github.com/tidwall/gjson"

// Billing policy for a request that ends without any usage report (the client left,
// the upstream was cut or stalled). Owner decision, 2026-11-06:
//
//   - the upstream connection is closed the moment the client disconnects;
//   - output is billed for what was really produced, estimated from the content;
//   - input is billed for what was really sent, estimated from the request body,
//     with 95% of it priced as cache read and the rest as normal input;
//   - a model without a cache read price bills that share at the plain input price
//     (UsageTokens.CacheReadEstimated, handled in computeTokenBreakdown);
//   - usage the upstream really reported is never overwritten or re-split.
//
// The helpers here are protocol neutral. Each protocol family maps the split onto
// its own usage struct, because the families count "input" differently: OpenAI
// style reports InputTokens as the total including cache, Anthropic style reports
// InputTokens as the fresh input only.

// incompleteInputCacheReadPercent is the share of the estimated input priced as
// cache read. It applies only to estimated input, never to reported usage.
const incompleteInputCacheReadPercent = 95

// incompleteInputEstimate is an estimated request input split into its cache read
// part and the part billed as normal input.
type incompleteInputEstimate struct {
	// Total is the estimated number of input tokens of the request.
	Total int
	// CacheRead is the share of Total priced as cache read.
	CacheRead int
	// Fresh is the share of Total priced as normal input (Total - CacheRead).
	Fresh int
}

// splitIncompleteInput rounds the cache read share down, so the remainder (normal
// input) is the larger side of any rounding.
func splitIncompleteInput(total int) incompleteInputEstimate {
	if total <= 0 {
		return incompleteInputEstimate{}
	}
	cacheRead := total * incompleteInputCacheReadPercent / 100
	return incompleteInputEstimate{Total: total, CacheRead: cacheRead, Fresh: total - cacheRead}
}

// incompleteCacheReadTokens is the cache read share of an estimated input total.
func incompleteCacheReadTokens(total int) int {
	return splitIncompleteInput(total).CacheRead
}

// estimateRequestInputTokens returns the estimated input tokens of a request body,
// or 0 when the shape is not recognised or cannot be parsed (nothing is invented).
// It dispatches by the request shape: Gemini (contents / systemInstruction), OpenAI
// Responses (input), Anthropic and Chat Completions (messages). History that is not
// in the body, such as the context behind previous_response_id, is not counted, so
// the result is a lower bound. Feed it to splitIncompleteInput for the billing split.
func estimateRequestInputTokens(body []byte) int {
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
	if err != nil || estimated <= 0 {
		return 0
	}
	return estimated
}
