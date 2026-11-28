package service

import (
	"bytes"
	"fmt"
	"strings"
	"sync/atomic"
)

// Billing policy for a request that ends without any usage report (the client left,
// the upstream was cut or stalled). Owner decision, 2026-11-06, revised by issue
// #456:
//
//   - the upstream connection is closed the moment the client disconnects;
//   - output is billed for what was really produced, estimated from the content;
//   - input is billed for what was really sent, estimated from the request body;
//   - an estimate never invents a cache discount by default: each family's share
//     priced as cache read is independently configured by
//     BILLING_OPENAI_INCOMPLETE_INPUT_CACHE_READ_PERCENT,
//     BILLING_ANTHROPIC_INCOMPLETE_INPUT_CACHE_READ_PERCENT, and
//     BILLING_GEMINI_INCOMPLETE_INPUT_CACHE_READ_PERCENT (0 = plain input price,
//     matching the upstream new-api abnormal-end fallback);
//   - a model without a cache read price bills any configured share at the plain
//     input price (UsageTokens.CacheReadEstimated, handled in computeTokenBreakdown);
//   - usage the upstream really reported is never overwritten or re-split.
//
// The helpers here are protocol neutral. Each protocol family maps the split onto
// its own usage struct, because the families count "input" differently: OpenAI
// style reports InputTokens as the total including cache, Anthropic style reports
// InputTokens as the fresh input only.

// Default cache-read shares for estimated inputs across all protocol families.
// 0 means the whole estimated input is billed at the plain input price, matching
// new-api's ResponseText2Usage fallback on an abnormal stream end (it bills the
// merged prompt including cache hits at the normal input price). Operators whose
// upstream really does discount cache reads on an abnormal end can raise each
// family independently via config.
const (
	openAIIncompleteInputCacheReadPercentDefault    = 0
	anthropicIncompleteInputCacheReadPercentDefault = 0
	geminiIncompleteInputCacheReadPercentDefault    = 0
)

// Configured cache-read shares for estimated inputs when no upstream usage arrived.
// Written once at startup and read lock-free on the billing path.
var (
	openAIIncompleteInputCacheReadPercent    atomic.Int64
	anthropicIncompleteInputCacheReadPercent atomic.Int64
	geminiIncompleteInputCacheReadPercent    atomic.Int64
)

func init() {
	openAIIncompleteInputCacheReadPercent.Store(openAIIncompleteInputCacheReadPercentDefault)
	anthropicIncompleteInputCacheReadPercent.Store(anthropicIncompleteInputCacheReadPercentDefault)
	geminiIncompleteInputCacheReadPercent.Store(geminiIncompleteInputCacheReadPercentDefault)
}

func clampIncompleteInputCacheReadPercent(percent int) int {
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

// SetOpenAIIncompleteInputCacheReadPercent publishes the configured OpenAI-family
// cache-read share of an estimated input. Values outside 0..100 are clamped; the
// config loader rejects them first, so this is only a runtime backstop.
func SetOpenAIIncompleteInputCacheReadPercent(percent int) {
	openAIIncompleteInputCacheReadPercent.Store(int64(clampIncompleteInputCacheReadPercent(percent)))
}

// OpenAIIncompleteInputCacheReadPercent reports the effective OpenAI-family share.
func OpenAIIncompleteInputCacheReadPercent() int {
	return int(openAIIncompleteInputCacheReadPercent.Load())
}

// SetAnthropicIncompleteInputCacheReadPercent publishes the configured Anthropic-family
// cache-read share of an estimated input (default 0 = plain input price).
func SetAnthropicIncompleteInputCacheReadPercent(percent int) {
	anthropicIncompleteInputCacheReadPercent.Store(int64(clampIncompleteInputCacheReadPercent(percent)))
}

// AnthropicIncompleteInputCacheReadPercent reports the effective Anthropic-family share.
func AnthropicIncompleteInputCacheReadPercent() int {
	return int(anthropicIncompleteInputCacheReadPercent.Load())
}

// SetGeminiIncompleteInputCacheReadPercent publishes the configured Gemini/Antigravity-family
// cache-read share of an estimated input (default 0 = plain input price).
func SetGeminiIncompleteInputCacheReadPercent(percent int) {
	geminiIncompleteInputCacheReadPercent.Store(int64(clampIncompleteInputCacheReadPercent(percent)))
}

// GeminiIncompleteInputCacheReadPercent reports the effective Gemini/Antigravity-family share.
func GeminiIncompleteInputCacheReadPercent() int {
	return int(geminiIncompleteInputCacheReadPercent.Load())
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

// splitAnthropicIncompleteInput applies the configured Anthropic-family policy (default 0%).
func splitAnthropicIncompleteInput(total int) incompleteInputEstimate {
	return splitIncompleteInputWithPercent(total, AnthropicIncompleteInputCacheReadPercent())
}

// splitGeminiIncompleteInput applies the configured Gemini/Antigravity-family policy (default 0%).
func splitGeminiIncompleteInput(total int) incompleteInputEstimate {
	return splitIncompleteInputWithPercent(total, GeminiIncompleteInputCacheReadPercent())
}

// splitOpenAIIncompleteInput applies the configured OpenAI-family policy.
func splitOpenAIIncompleteInput(total int) incompleteInputEstimate {
	return splitIncompleteInputWithPercent(total, OpenAIIncompleteInputCacheReadPercent())
}

// anthropicIncompleteCacheReadTokens is the configured Anthropic-family cache read share.
func anthropicIncompleteCacheReadTokens(total int) int {
	return splitAnthropicIncompleteInput(total).CacheRead
}

// geminiIncompleteCacheReadTokens is the configured Gemini/Antigravity-family cache read share.
func geminiIncompleteCacheReadTokens(total int) int {
	return splitGeminiIncompleteInput(total).CacheRead
}

// openAIIncompleteCacheReadTokens is the configured OpenAI-family cache read share
// of an estimated input total (for the estimate log lines).
func openAIIncompleteCacheReadTokens(total int) int {
	return splitOpenAIIncompleteInput(total).CacheRead
}

// maxIncompleteInputTokens bounds an estimated input. No supported model takes more
// than about 2M tokens of context and the largest completed request seen in
// production was 1.7M, so a larger estimate is an estimator fault, never a real
// request. It is a backstop behind inline opaque-blob folding, not a substitute for it.
const maxIncompleteInputTokens = 2_000_000

func clampIncompleteInputTokens(tokens int) int {
	if tokens > maxIncompleteInputTokens {
		return maxIncompleteInputTokens
	}
	return tokens
}

// estimateRequestInputTokens is the input estimate for billing a request that ended
// without a usage report. Because mid-stream body release precomputes this fallback
// on every streaming request (99.5%+ of which finish normally with upstream usage
// and discard the estimate), it uses a single-pass, zero-allocation JSON structural
// estimator with inline base64 blob folding instead of tiktoken-go regex BPE or
// full json.Unmarshal. Explicit /count_tokens and /input_tokens endpoints keep exact
// tiktoken BPE in EstimateAnthropicCountTokens / EstimateOpenAIResponsesInputTokens.
// 0 means "unknown", nothing is invented.
func estimateRequestInputTokens(body []byte) int {
	spans, ok := scanIncompleteRequestSpans(body)
	if !ok {
		return 0
	}
	// Evaluate every present protocol shape and keep the largest estimate so a
	// smuggled empty/tiny foreign field (e.g. "contents":[] on an Anthropic or
	// OpenAI messages payload) can never hijack the dispatcher and return 0.
	best := 0
	if spans.hasGemini {
		if estimated := estimateGeminiInputTokensFast(body, spans); estimated > best {
			best = estimated
		}
	}
	if spans.hasInput && spans.hasModel {
		if estimated := estimateResponsesInputTokensFast(body, spans); estimated > best {
			best = estimated
		}
	}
	if spans.hasMessages && spans.hasModel {
		if estimated := estimateAnthropicInputTokensFast(body, spans); estimated > best {
			best = estimated
		}
	}
	if best <= 0 {
		return 0
	}
	return clampIncompleteInputTokens(best)
}

// estimateResponsesInputTokensForBilling is the same for a body known to be an
// OpenAI Responses request, including one without an input field (it may only carry
// instructions and a previous_response_id).
func estimateResponsesInputTokensForBilling(body []byte) (int, error) {
	spans, ok := scanIncompleteRequestSpans(body)
	if !ok {
		return 0, fmt.Errorf("parse responses input_tokens request: invalid JSON")
	}
	if !spans.hasModel {
		return 0, fmt.Errorf("parse responses input_tokens request: model is required")
	}
	return clampIncompleteInputTokens(estimateResponsesInputTokensFast(body, spans)), nil
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
	hasGemini, hasInput, hasMessages := detectRequestInputShape(body)
	// Evaluate every present protocol shape and keep the largest estimate so a
	// smuggled empty/tiny foreign field (e.g. "contents":[] on an Anthropic or
	// OpenAI messages payload) can never hijack the dispatcher and return 0.
	best := 0
	if hasGemini {
		if estimated := estimateGeminiCountTokens(body); estimated > best {
			best = estimated
		}
	}
	if hasInput {
		if estimated, err := EstimateOpenAIResponsesInputTokens(body); err == nil && estimated > best {
			best = estimated
		}
	}
	if hasMessages {
		if estimated, err := EstimateAnthropicCountTokens(body); err == nil && estimated > best {
			best = estimated
		}
	}
	return best
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

func trimJSONObjectBounds(body []byte) (int, int, bool) {
	start := 0
	if bytes.HasPrefix(body, utf8BOM) {
		start = len(utf8BOM)
	}
	start = skipJSONWhitespace(body, start)
	end := len(body)
	for end > start && isJSONSpace(body[end-1]) {
		end--
	}
	if end-start < 2 || body[start] != '{' || body[end-1] != '}' {
		return 0, 0, false
	}
	return start, end, true
}

func detectRequestInputShape(body []byte) (hasGemini, hasInput, hasMessages bool) {
	if len(body) == 0 {
		return false, false, false
	}
	if start, end, ok := trimJSONObjectBounds(body); ok {
		walkOK := jsonForEachMemberSpan(body, start, end, func(key string, _, _, _, _ int) bool {
			switch key {
			case "contents", "systemInstruction", "system_instruction":
				hasGemini = true
			case "input":
				hasInput = true
			case "messages":
				hasMessages = true
			}
			return !hasGemini || !hasInput || !hasMessages
		})
		if walkOK {
			return hasGemini, hasInput, hasMessages
		}
	}
	root := parseRawJSONView(bytes.TrimPrefix(body, utf8BOM))
	return root.Get("contents").Exists() || root.Get("systemInstruction").Exists() || root.Get("system_instruction").Exists(),
		root.Get("input").Exists(),
		root.Get("messages").Exists()
}

type jsonSpan struct {
	start int
	end   int
}

func (s jsonSpan) valid() bool {
	return s.end > s.start
}

func (s jsonSpan) isNull(b []byte) bool {
	return !s.valid() || (s.end-s.start == 4 && string(b[s.start:s.end]) == "null")
}

type incompleteRequestSpans struct {
	hasModel          bool
	hasGemini         bool
	hasInput          bool
	hasMessages       bool
	contents          jsonSpan
	systemInstruction jsonSpan
	input             jsonSpan
	instructions      jsonSpan
	messages          jsonSpan
	system            jsonSpan
	tools             jsonSpan
	toolChoice        jsonSpan
}

func scanIncompleteRequestSpans(body []byte) (incompleteRequestSpans, bool) {
	if len(body) == 0 {
		return incompleteRequestSpans{}, false
	}
	objStart, objEnd, ok := trimJSONObjectBounds(body)
	if !ok {
		return incompleteRequestSpans{}, false
	}
	var spans incompleteRequestSpans
	walkOK := jsonForEachMemberSpan(body, objStart, objEnd, func(key string, valStart, valEnd, _, _ int) bool {
		val := jsonSpan{start: valStart, end: valEnd}
		switch {
		case strings.EqualFold(key, "model"):
			spans.hasModel = jsonStringSpanHasNonSpace(body, valStart, valEnd)
		case key == "contents":
			spans.hasGemini = true
			spans.contents = val
		case key == "systemInstruction" || key == "system_instruction":
			spans.hasGemini = true
			spans.systemInstruction = val
		case strings.EqualFold(key, "input"):
			spans.hasInput = true
			spans.input = val
		case strings.EqualFold(key, "instructions"):
			if !val.isNull(body) {
				spans.instructions = val
			}
		case strings.EqualFold(key, "messages"):
			spans.hasMessages = true
			spans.messages = val
		case strings.EqualFold(key, "system"):
			if !val.isNull(body) {
				spans.system = val
			}
		case strings.EqualFold(key, "tools"):
			if !val.isNull(body) {
				spans.tools = val
			}
		case strings.EqualFold(key, "tool_choice"):
			if !val.isNull(body) {
				spans.toolChoice = val
			}
		}
		return true
	})
	if !walkOK {
		return incompleteRequestSpans{}, false
	}
	return spans, true
}

func jsonStringSpanHasNonSpace(body []byte, start, end int) bool {
	if end-start < 2 || body[start] != '"' || body[end-1] != '"' {
		return false
	}
	for i := start + 1; i < end-1; i++ {
		c := body[i]
		if c == '\\' {
			if i+1 < end-1 {
				switch body[i+1] {
				case 't', 'n', 'r':
					i++
					continue
				default:
					return true
				}
			}
			return false
		}
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return true
		}
	}
	return false
}

// Fixed-point token weights (1024 = 1.0 token), calibrated against o200k_base and
// cl100k_base across English prose, Go/TS source code, JSON schemas, and CJK text.
const (
	fastTokenScale         int64 = 1024
	fastWeightASCIIAlnum   int64 = 265  // ~3.86 alnum chars/token
	fastWeightASCIISpace   int64 = 128  // most spaces merge into the following word
	fastWeightASCIINewline int64 = 560  // ~0.55 token per newline
	fastWeightASCIIPunct   int64 = 640  // ~0.625 token per punctuation/operator
	fastWeightUTF8_2       int64 = 680  // ~0.66 token per 2-byte UTF-8 rune
	fastWeightUTF8_3       int64 = 900  // ~0.88 token per 3-byte UTF-8 rune (CJK)
	fastWeightUTF8_4       int64 = 1600 // ~1.56 tokens per 4-byte UTF-8 rune
	fastWeightOpaqueBlob   int64 = 2 * fastTokenScale
	fastMaxRecursionDepth        = 64
	fastMaxWeightCap             = int64(maxIncompleteInputTokens+1024) * fastTokenScale
)

func fastASCIIByteWeight(c byte) int64 {
	switch {
	case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_':
		return fastWeightASCIIAlnum
	case c == ' ' || c == '\t':
		return fastWeightASCIISpace
	case c == '\n' || c == '\r':
		return fastWeightASCIINewline
	default:
		return fastWeightASCIIPunct
	}
}

type fastTokenCounter struct {
	weight         int64
	overheadTokens int
}

func (c *fastTokenCounter) saturated() bool {
	return c.weight >= fastMaxWeightCap
}

func (c *fastTokenCounter) tokens() int {
	if c.weight <= 0 && c.overheadTokens <= 0 {
		return 0
	}
	total := int((c.weight+fastTokenScale/2)/fastTokenScale) + c.overheadTokens
	if total <= 0 && c.weight > 0 {
		total = 1
	}
	return clampIncompleteInputTokens(total)
}

func parseFastHex4(b []byte) (uint32, bool) {
	if len(b) < 4 {
		return 0, false
	}
	var v uint32
	for i := 0; i < 4; i++ {
		c := b[i]
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v |= uint32(c - '0')
		case c >= 'a' && c <= 'f':
			v |= uint32(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v |= uint32(c-'A') + 10
		default:
			return 0, false
		}
	}
	return v, true
}

// observeJSONString scans a JSON string span [start, end) in a single pass with
// zero allocations, folding any >= opaqueBlobMinRun base64 run inline into
// fastWeightOpaqueBlob so screenshots/signatures are never billed as text.
func (c *fastTokenCounter) observeJSONString(b []byte, start, end int) {
	if c.saturated() || end-start <= 2 || b[start] != '"' || b[end-1] != '"' {
		return
	}
	limit := end - 1
	runLen := 0
	var runWeight int64
	flushBlob := func() {
		if runLen >= opaqueBlobMinRun {
			c.weight -= runWeight
			c.weight += fastWeightOpaqueBlob
		}
		runLen = 0
		runWeight = 0
	}

	i := start + 1
	for i < limit {
		ch := b[i]
		if ch == '\\' {
			if i+1 < limit {
				next := b[i+1]
				if next == '/' {
					c.weight += fastWeightASCIIPunct
					runLen += 2
					runWeight += fastWeightASCIIPunct
					i += 2
					continue
				}
				if next == 'u' && i+6 <= limit {
					if u, ok := parseFastHex4(b[i+2 : i+6]); ok {
						if u < 0x80 && isOpaqueBlobByte(byte(u)) {
							w := fastASCIIByteWeight(byte(u))
							c.weight += w
							runLen++
							runWeight += w
							i += 6
							continue
						}
						if runLen > 0 {
							flushBlob()
						}
						if u >= 0xD800 && u <= 0xDBFF && i+12 <= limit && b[i+6] == '\\' && b[i+7] == 'u' {
							if u2, ok2 := parseFastHex4(b[i+8 : i+12]); ok2 && u2 >= 0xDC00 && u2 <= 0xDFFF {
								c.weight += fastWeightUTF8_4
								i += 12
								continue
							}
						}
						switch {
						case u < 0x80:
							c.weight += fastASCIIByteWeight(byte(u))
						case u < 0x800:
							c.weight += fastWeightUTF8_2
						default:
							c.weight += fastWeightUTF8_3
						}
						i += 6
						continue
					}
				}
				if runLen > 0 {
					flushBlob()
				}
				switch next {
				case 'n', 'r':
					c.weight += fastWeightASCIINewline
					i += 2
				case 't':
					c.weight += fastWeightASCIISpace
					i += 2
				default:
					c.weight += fastWeightASCIIPunct
					i += 2
				}
				continue
			}
			if runLen > 0 {
				flushBlob()
			}
			i++
			continue
		}
		if isOpaqueBlobByte(ch) {
			w := fastASCIIByteWeight(ch)
			c.weight += w
			runLen++
			runWeight += w
			i++
			continue
		}
		if runLen > 0 {
			flushBlob()
		}
		switch {
		case ch < 0x80:
			c.weight += fastASCIIByteWeight(ch)
			i++
		case ch < 0xC0:
			i++
		case ch < 0xE0:
			c.weight += fastWeightUTF8_2
			i += 2
		case ch < 0xF0:
			c.weight += fastWeightUTF8_3
			i += 3
		default:
			c.weight += fastWeightUTF8_4
			i += 4
		}
		if c.weight >= fastMaxWeightCap && runLen == 0 {
			return
		}
	}
	if runLen > 0 {
		flushBlob()
	}
}

func (c *fastTokenCounter) observePlainBytes(p []byte) {
	if c.saturated() || len(p) == 0 {
		return
	}
	for i := 0; i < len(p); {
		ch := p[i]
		switch {
		case ch < 0x80:
			c.weight += fastASCIIByteWeight(ch)
			i++
		case ch < 0xC0:
			i++
		case ch < 0xE0:
			c.weight += fastWeightUTF8_2
			i += 2
		case ch < 0xF0:
			c.weight += fastWeightUTF8_3
			i += 3
		default:
			c.weight += fastWeightUTF8_4
			i += 4
		}
		if c.weight >= fastMaxWeightCap {
			return
		}
	}
}

type fastWalkMode uint8

const (
	fastWalkResponses fastWalkMode = iota
	fastWalkAnthropic
	fastWalkSchema
)

func shouldSkipMetadataKey(mode fastWalkMode, key string) bool {
	if mode == fastWalkSchema {
		return false
	}
	switch key {
	case "role", "type", "id", "call_id", "tool_use_id", "tool_call_id",
		"status", "index", "media_type", "mime_type", "mimeType",
		"cache_control", "encrypted_content", "signature", "detail":
		return true
	}
	return false
}

func (c *fastTokenCounter) observeJSONValue(b []byte, start, end, depth int, mode fastWalkMode) {
	if c.saturated() || end <= start || depth > fastMaxRecursionDepth {
		return
	}
	switch b[start] {
	case '"':
		c.observeJSONString(b, start, end)
	case '[':
		jsonForEachElementSpan(b, start, end, func(es, ee int) bool {
			c.observeJSONValue(b, es, ee, depth+1, mode)
			return !c.saturated()
		})
	case '{':
		// Skip inline base64 image/binary data objects (e.g. Anthropic image source
		// {"type":"base64","media_type":"image/png","data":"..."}) without counting
		// binary payloads as text. For Anthropic tool_use blocks, walk "input" in
		// schema mode so argument keys and numeric/boolean values are counted.
		skipDataField := false
		isToolUse := false
		if mode != fastWalkSchema {
			jsonForEachMemberSpan(b, start, end, func(key string, vs, ve, _, _ int) bool {
				switch key {
				case "media_type", "mime_type", "mimeType":
					skipDataField = true
					return false
				case "type":
					if ve-vs == 8 && string(b[vs:ve]) == `"base64"` {
						skipDataField = true
						return false
					}
					if mode == fastWalkAnthropic && ve-vs == 10 && string(b[vs:ve]) == `"tool_use"` {
						isToolUse = true
						return false
					}
				}
				return true
			})
		}
		jsonForEachMemberSpan(b, start, end, func(key string, vs, ve, ks, ke int) bool {
			if skipDataField && key == "data" {
				return true
			}
			if shouldSkipMetadataKey(mode, key) {
				return true
			}
			if mode == fastWalkSchema {
				c.observeJSONString(b, ks, ke)
				c.weight += fastWeightASCIIPunct
			}
			childMode := mode
			if isToolUse && key == "input" {
				childMode = fastWalkSchema
			}
			c.observeJSONValue(b, vs, ve, depth+1, childMode)
			return !c.saturated()
		})
	default:
		if mode == fastWalkSchema {
			c.observePlainBytes(b[start:end])
		}
	}
}

func estimateGeminiInputTokensFast(body []byte, spans incompleteRequestSpans) int {
	total := 0
	if spans.systemInstruction.valid() && !spans.systemInstruction.isNull(body) {
		total += estimateGeminiPartsObjectFast(body, spans.systemInstruction.start, spans.systemInstruction.end)
	}
	if spans.contents.valid() && !spans.contents.isNull(body) && body[spans.contents.start] == '[' {
		jsonForEachElementSpan(body, spans.contents.start, spans.contents.end, func(es, ee int) bool {
			total += estimateGeminiPartsObjectFast(body, es, ee)
			return total < maxIncompleteInputTokens
		})
	}
	if total <= 0 {
		return 0
	}
	return clampIncompleteInputTokens(total)
}

func estimateGeminiPartsObjectFast(body []byte, objStart, objEnd int) int {
	if objEnd <= objStart || body[objStart] != '{' {
		return 0
	}
	total := 0
	jsonForEachMemberSpan(body, objStart, objEnd, func(key string, vs, ve, _, _ int) bool {
		if key != "parts" || ve <= vs || body[vs] != '[' {
			return true
		}
		jsonForEachElementSpan(body, vs, ve, func(ps, pe int) bool {
			if pe <= ps || body[ps] != '{' {
				return true
			}
			jsonForEachMemberSpan(body, ps, pe, func(pk string, tvs, tve, _, _ int) bool {
				if pk == "text" {
					total += countGeminiTextJSONStringFast(body, tvs, tve)
				}
				return total < maxIncompleteInputTokens
			})
			return total < maxIncompleteInputTokens
		})
		return total < maxIncompleteInputTokens
	})
	return total
}

func isFastASCIISpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

// countGeminiTextJSONStringFast matches estimateTokensForText on a JSON string
// span in a single zero-allocation pass while folding >= opaqueBlobMinRun
// base64 runs inline to "[blob]" (6 ASCII runes).
func countGeminiTextJSONStringFast(b []byte, start, end int) int {
	if end-start <= 2 || b[start] != '"' || b[end-1] != '"' {
		return 0
	}
	limit := end - 1
	totalRunes := 0
	asciiRunes := 0
	seenNonSpace := false
	trailingSpace := 0
	runLen := 0
	runRunes := 0
	flushBlob := func() {
		if runLen >= opaqueBlobMinRun {
			const blobRunes = 6 // len("[blob]")
			totalRunes = totalRunes - runRunes + blobRunes
			asciiRunes = asciiRunes - runRunes + blobRunes
		}
		runLen = 0
		runRunes = 0
	}
	addRune := func(isASCII, isSpace, isBlob bool, blobStep int) {
		if isBlob {
			if !seenNonSpace {
				seenNonSpace = true
			}
			trailingSpace = 0
			totalRunes++
			asciiRunes++
			runLen += blobStep
			runRunes++
			return
		}
		if runLen > 0 {
			flushBlob()
		}
		if !seenNonSpace {
			if isSpace {
				return
			}
			seenNonSpace = true
		}
		if isSpace {
			totalRunes++
			if isASCII {
				asciiRunes++
			}
			trailingSpace++
			return
		}
		trailingSpace = 0
		totalRunes++
		if isASCII {
			asciiRunes++
		}
	}

	i := start + 1
	for i < limit {
		ch := b[i]
		if ch == '\\' {
			if i+1 < limit {
				next := b[i+1]
				if next == '/' {
					addRune(true, false, true, 2)
					i += 2
					continue
				}
				if next == 'u' && i+6 <= limit {
					if u, ok := parseFastHex4(b[i+2 : i+6]); ok {
						if u < 0x80 {
							c := byte(u)
							addRune(true, isFastASCIISpace(c), isOpaqueBlobByte(c), 1)
							i += 6
							continue
						}
						if u >= 0xD800 && u <= 0xDBFF && i+12 <= limit && b[i+6] == '\\' && b[i+7] == 'u' {
							if u2, ok2 := parseFastHex4(b[i+8 : i+12]); ok2 && u2 >= 0xDC00 && u2 <= 0xDFFF {
								addRune(false, false, false, 0)
								i += 12
								continue
							}
						}
						addRune(false, u == 0x00A0 || u == 0x3000, false, 0)
						i += 6
						continue
					}
				}
				switch next {
				case 'n', 'r', 't', 'f':
					addRune(true, true, false, 0)
				default:
					addRune(true, false, false, 0)
				}
				i += 2
				continue
			}
			i++
			continue
		}
		if ch < 0x80 {
			addRune(true, isFastASCIISpace(ch), isOpaqueBlobByte(ch), 1)
			i++
			continue
		}
		if ch < 0xC0 {
			i++
			continue
		}
		addRune(false, false, false, 0)
		switch {
		case ch < 0xE0:
			i += 2
		case ch < 0xF0:
			i += 3
		default:
			i += 4
		}
	}
	if runLen > 0 {
		flushBlob()
	}
	if trailingSpace > 0 {
		totalRunes -= trailingSpace
		if asciiRunes >= trailingSpace {
			asciiRunes -= trailingSpace
		} else {
			asciiRunes = 0
		}
	}
	if totalRunes <= 0 {
		return 0
	}
	if asciiRunes*5 >= totalRunes*4 {
		return (totalRunes + 3) / 4
	}
	return totalRunes
}

func estimateResponsesInputTokensFast(body []byte, spans incompleteRequestSpans) int {
	var c fastTokenCounter
	if spans.instructions.valid() && !spans.instructions.isNull(body) {
		c.observeJSONValue(body, spans.instructions.start, spans.instructions.end, 0, fastWalkResponses)
	}
	if spans.input.valid() && !spans.input.isNull(body) {
		if body[spans.input.start] == '[' {
			jsonForEachElementSpan(body, spans.input.start, spans.input.end, func(es, ee int) bool {
				before := c.weight
				c.observeJSONValue(body, es, ee, 0, fastWalkResponses)
				if c.weight > before {
					c.overheadTokens += 3
				}
				return !c.saturated()
			})
		} else {
			c.observeJSONValue(body, spans.input.start, spans.input.end, 0, fastWalkResponses)
		}
	}
	if spans.tools.valid() && !spans.tools.isNull(body) {
		c.observeJSONValue(body, spans.tools.start, spans.tools.end, 0, fastWalkSchema)
	}
	if spans.toolChoice.valid() && !spans.toolChoice.isNull(body) {
		c.observeJSONValue(body, spans.toolChoice.start, spans.toolChoice.end, 0, fastWalkSchema)
	}
	return c.tokens()
}

func estimateAnthropicInputTokensFast(body []byte, spans incompleteRequestSpans) int {
	var c fastTokenCounter
	if spans.system.valid() && !spans.system.isNull(body) {
		c.observeJSONValue(body, spans.system.start, spans.system.end, 0, fastWalkAnthropic)
	}
	if spans.messages.valid() && !spans.messages.isNull(body) {
		if body[spans.messages.start] == '[' {
			jsonForEachElementSpan(body, spans.messages.start, spans.messages.end, func(es, ee int) bool {
				before := c.weight
				c.observeJSONValue(body, es, ee, 0, fastWalkAnthropic)
				if c.weight > before {
					c.overheadTokens += 3
				}
				return !c.saturated()
			})
		} else {
			c.observeJSONValue(body, spans.messages.start, spans.messages.end, 0, fastWalkAnthropic)
		}
	}
	if spans.tools.valid() && !spans.tools.isNull(body) {
		c.observeJSONValue(body, spans.tools.start, spans.tools.end, 0, fastWalkSchema)
	}
	if spans.toolChoice.valid() && !spans.toolChoice.isNull(body) {
		c.observeJSONValue(body, spans.toolChoice.start, spans.toolChoice.end, 0, fastWalkSchema)
	}
	return c.tokens()
}
