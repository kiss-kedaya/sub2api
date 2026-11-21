package service

import (
	"sync/atomic"

	"github.com/tidwall/gjson"
)

// Billing policy for a request that ends without any usage report (the client left,
// the upstream was cut or stalled). Owner decision, 2026-11-06, revised by issue
// #456:
//
//   - the upstream connection is closed the moment the client disconnects;
//   - output is billed for what was really produced, estimated from the content;
//   - input is billed for what was really sent, estimated from the request body;
//   - an OpenAI-family estimate never invents a cache discount by default: its
//     share priced as cache read is configured by
//     BILLING_OPENAI_INCOMPLETE_INPUT_CACHE_READ_PERCENT (0 = plain input price,
//     which is what the upstream new-api abnormal-end fallback charges);
//   - the Anthropic/Gemini families keep the historical 95% cache read heuristic,
//     because their upstreams price a real cache read at about a tenth of input;
//   - a model without a cache read price bills that share at the plain input price
//     (UsageTokens.CacheReadEstimated, handled in computeTokenBreakdown);
//   - usage the upstream really reported is never overwritten or re-split.
//
// The helpers here are protocol neutral. Each protocol family maps the split onto
// its own usage struct, because the families count "input" differently: OpenAI
// style reports InputTokens as the total including cache, Anthropic style reports
// InputTokens as the fresh input only.

// legacyIncompleteInputCacheReadPercent is the share of the estimated input priced
// as cache read for the Anthropic and Gemini families. It applies only to estimated
// input, never to reported usage.
const legacyIncompleteInputCacheReadPercent = 95

// openAIIncompleteInputCacheReadPercentDefault is the shipped OpenAI-family share.
// 0 means the whole estimated input is billed at the plain input price, matching
// new-api's ResponseText2Usage fallback on an abnormal stream end (it bills the
// merged prompt including cache hits at the normal input price). Operators whose
// upstream really does discount cache reads on an abnormal end can raise it.
const openAIIncompleteInputCacheReadPercentDefault = 0

// openAIIncompleteInputCacheReadPercent holds the configured OpenAI-family share.
// It is written once at startup (SetOpenAIIncompleteInputCacheReadPercent) and read
// on the billing path, so it stays lock-free.
var openAIIncompleteInputCacheReadPercent atomic.Int64

func init() {
	openAIIncompleteInputCacheReadPercent.Store(openAIIncompleteInputCacheReadPercentDefault)
}

// SetOpenAIIncompleteInputCacheReadPercent publishes the configured OpenAI-family
// cache-read share of an estimated input. Values outside 0..100 are clamped; the
// config loader rejects them first, so this is only a runtime backstop.
func SetOpenAIIncompleteInputCacheReadPercent(percent int) {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	openAIIncompleteInputCacheReadPercent.Store(int64(percent))
}

// OpenAIIncompleteInputCacheReadPercent reports the effective OpenAI-family share.
func OpenAIIncompleteInputCacheReadPercent() int {
	return int(openAIIncompleteInputCacheReadPercent.Load())
}

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

// splitIncompleteInputWithPercent rounds the cache read share down, so the remainder
// (normal input) is the larger side of any rounding. percent is the family policy:
// 0 bills the whole estimate at the plain input price.
func splitIncompleteInputWithPercent(total, percent int) incompleteInputEstimate {
	if total <= 0 {
		return incompleteInputEstimate{}
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	cacheRead := total * percent / 100
	return incompleteInputEstimate{Total: total, CacheRead: cacheRead, Fresh: total - cacheRead}
}

// splitIncompleteInput applies the legacy Anthropic/Gemini policy (95% cache read).
func splitIncompleteInput(total int) incompleteInputEstimate {
	return splitIncompleteInputWithPercent(total, legacyIncompleteInputCacheReadPercent)
}

// splitOpenAIIncompleteInput applies the configured OpenAI-family policy.
func splitOpenAIIncompleteInput(total int) incompleteInputEstimate {
	return splitIncompleteInputWithPercent(total, OpenAIIncompleteInputCacheReadPercent())
}

// incompleteCacheReadTokens is the legacy Anthropic/Gemini cache read share of an
// estimated input total (for the estimate log lines).
func incompleteCacheReadTokens(total int) int {
	return splitIncompleteInput(total).CacheRead
}

// openAIIncompleteCacheReadTokens is the configured OpenAI-family cache read share
// of an estimated input total (for the estimate log lines).
func openAIIncompleteCacheReadTokens(total int) int {
	return splitOpenAIIncompleteInput(total).CacheRead
}

// maxIncompleteInputTokens bounds an estimated input. No supported model takes more
// than about 2M tokens of context and the largest completed request seen in
// production was 1.7M, so a larger estimate is an estimator fault, never a real
// request. It is a backstop behind stripOpaqueBlobs, not a substitute for it.
const maxIncompleteInputTokens = 2_000_000

func clampIncompleteInputTokens(tokens int) int {
	if tokens > maxIncompleteInputTokens {
		return maxIncompleteInputTokens
	}
	return tokens
}

// estimateRequestInputTokens is the input estimate for billing a request that ended
// without a usage report: opaque blobs are folded first because they are not text
// (see incomplete_request_sanitize.go), the request shape picks the estimator, and
// the result is clamped. 0 means "unknown", nothing is invented.
func estimateRequestInputTokens(body []byte) int {
	return clampIncompleteInputTokens(estimateRequestInputTokensRaw(stripOpaqueBlobs(body)))
}

// estimateResponsesInputTokensForBilling is the same for a body known to be an
// OpenAI Responses request, including one without an input field (it may only carry
// instructions and a previous_response_id).
func estimateResponsesInputTokensForBilling(body []byte) (int, error) {
	tokens, err := EstimateOpenAIResponsesInputTokens(stripOpaqueBlobs(body))
	return clampIncompleteInputTokens(tokens), err
}

// estimateIncompleteResponsesInput estimates the input of an OpenAI Responses
// request that ended without usage. The shape dispatcher goes first; a body it cannot
// place, for example one that only carries instructions and a previous_response_id,
// falls back to the Responses estimator.
func estimateIncompleteResponsesInput(body []byte) (int, error) {
	if tokens := estimateRequestInputTokens(body); tokens > 0 {
		return tokens, nil
	}
	return estimateResponsesInputTokensForBilling(body)
}

// estimateRequestInputTokensRaw returns the estimated input tokens of a request body
// as it is, or 0 when the shape is not recognised or cannot be parsed (nothing is
// invented). It dispatches by the request shape: Gemini (contents / systemInstruction),
// OpenAI Responses (input), Anthropic and Chat Completions (messages). History that is
// not in the body, such as the context behind previous_response_id, is not counted, so
// the result is a lower bound. Billing must use estimateRequestInputTokens instead:
// this one counts base64 payloads as text. The balance preauthorization reserve keeps
// it on purpose, it only sizes a hold and is a conservative upper bound.
func estimateRequestInputTokensRaw(body []byte) int {
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
