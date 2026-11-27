package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/tiktoken-go/tokenizer"
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

const (
	// incompleteInputExactEstimateLimit is the stripped request size threshold
	// above which incomplete-stream input estimation uses zero-copy span walking
	// with bounded tiktoken sampling instead of full json.Unmarshal + full-body
	// tiktoken regex scanning.
	incompleteInputExactEstimateLimit = 512 << 10 // 512 KiB
	// incompleteInputSampleByteBudget caps total decoded text bytes passed to
	// tiktoken per incomplete-stream estimate when stripped payload exceeds
	// incompleteInputExactEstimateLimit.
	incompleteInputSampleByteBudget = 64 << 10 // 64 KiB
	// incompleteInputChunkByteLimit caps each individual codec.Count call.
	incompleteInputChunkByteLimit = 32 << 10 // 32 KiB
)

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
	stripped := stripOpaqueBlobs(body)
	if len(stripped) == 0 {
		return 0
	}
	hasGemini, hasInput, hasMessages := detectRequestInputShape(stripped)
	var estimated int
	var err error
	switch {
	case hasGemini:
		estimated = estimateGeminiCountTokens(stripped)
	case hasInput:
		estimated, err = estimateOpenAIResponsesInputTokensBounded(stripped)
	case hasMessages:
		estimated, err = estimateAnthropicInputTokensBounded(stripped)
	}
	if err != nil || estimated <= 0 {
		return 0
	}
	return clampIncompleteInputTokens(estimated)
}

// estimateResponsesInputTokensForBilling is the same for a body known to be an
// OpenAI Responses request, including one without an input field (it may only carry
// instructions and a previous_response_id).
func estimateResponsesInputTokensForBilling(body []byte) (int, error) {
	tokens, err := estimateOpenAIResponsesInputTokensBounded(stripOpaqueBlobs(body))
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
	hasGemini, hasInput, hasMessages := detectRequestInputShape(body)
	var estimated int
	var err error
	switch {
	case hasGemini:
		estimated = estimateGeminiCountTokens(body)
	case hasInput:
		estimated, err = EstimateOpenAIResponsesInputTokens(body)
	case hasMessages:
		estimated, err = EstimateAnthropicCountTokens(body)
	}
	if err != nil || estimated <= 0 {
		return 0
	}
	return estimated
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
			case "contents", "systemInstruction":
				hasGemini = true
				return false
			case "input":
				hasInput = true
			case "messages":
				hasMessages = true
			}
			return true
		})
		if walkOK {
			return hasGemini, hasInput, hasMessages
		}
	}
	root := parseRawJSONView(bytes.TrimPrefix(body, utf8BOM))
	return root.Get("contents").Exists() || root.Get("systemInstruction").Exists(),
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

type boundedTokenEstimator struct {
	codec            tokenizer.Codec
	sampleBudget     int64
	sampledTextBytes int64
	totalTextBytes   int64
	sampledTokens    int
	overheadTokens   int
	scratch          []byte
	metaScratch      []byte
}

func newBoundedTokenEstimator(codec tokenizer.Codec) *boundedTokenEstimator {
	return &boundedTokenEstimator{
		codec:        codec,
		sampleBudget: incompleteInputSampleByteBudget,
	}
}

func (e *boundedTokenEstimator) result() int {
	total := int64(e.overheadTokens)
	if e.totalTextBytes > 0 && e.sampledTextBytes > 0 {
		if e.totalTextBytes <= e.sampledTextBytes {
			total += int64(e.sampledTokens)
		} else {
			scaled := int64(math.Round(float64(e.sampledTokens) * (float64(e.totalTextBytes) / float64(e.sampledTextBytes))))
			if scaled < int64(e.sampledTokens) {
				scaled = int64(e.sampledTokens)
			}
			total += scaled
		}
	}
	if total < 0 {
		return 0
	}
	if total > maxIncompleteInputTokens {
		return maxIncompleteInputTokens
	}
	return int(total)
}

func (e *boundedTokenEstimator) observePlainBytes(p []byte) error {
	return e.observeSampleAndTotal(p, int64(len(p)))
}

func (e *boundedTokenEstimator) observePlainString(s string) error {
	if len(s) == 0 {
		return nil
	}
	if e.sampledTextBytes >= e.sampleBudget {
		e.totalTextBytes += int64(len(s))
		return nil
	}
	remain := int(e.sampleBudget - e.sampledTextBytes)
	take := len(s)
	if take > remain {
		take = len(trimTrailingIncompleteUTF8String(s[:remain]))
	}
	sample := append(e.scratch[:0], s[:take]...)
	err := e.observeSampleAndTotal(sample, int64(len(s)))
	e.scratch = sample[:0]
	return err
}

func (e *boundedTokenEstimator) observeSampleAndTotal(sample []byte, totalBytes int64) error {
	if totalBytes <= 0 {
		return nil
	}
	e.totalTextBytes += totalBytes
	if e.sampledTextBytes >= e.sampleBudget {
		return nil
	}
	if len(sample) == 0 {
		// Sampling cannot advance when the first rune of a non-empty field
		// exceeded the remaining sample budget.
		e.sampledTextBytes = e.sampleBudget
		return nil
	}
	remain := int(e.sampleBudget - e.sampledTextBytes)
	take := len(sample)
	if take > remain {
		take = len(trimTrailingIncompleteUTF8(sample[:remain]))
	}
	if take <= 0 {
		e.sampledTextBytes = e.sampleBudget
		return nil
	}
	for off := 0; off < take; {
		end := off + incompleteInputChunkByteLimit
		if end > take {
			end = take
		} else {
			trimmed := trimTrailingIncompleteUTF8(sample[off:end])
			if len(trimmed) > 0 {
				end = off + len(trimmed)
			}
		}
		n, err := e.codec.Count(jsonView(sample[off:end]))
		if err != nil {
			return err
		}
		e.sampledTokens += n
		e.sampledTextBytes += int64(end - off)
		off = end
	}
	return nil
}

func (e *boundedTokenEstimator) observeJSONString(b []byte, spanStart, spanEnd int, trimSpace bool) (bool, error) {
	if spanEnd-spanStart < 2 || b[spanStart] != '"' || b[spanEnd-1] != '"' {
		return false, nil
	}
	inner := b[spanStart+1 : spanEnd-1]
	if len(inner) == 0 {
		return true, nil
	}
	if bytes.IndexByte(inner, '\\') < 0 {
		if trimSpace {
			inner = bytes.TrimSpace(inner)
		}
		if len(inner) == 0 {
			return true, nil
		}
		return true, e.observePlainBytes(inner)
	}
	if e.sampledTextBytes >= e.sampleBudget {
		_, totalDecoded, ok := appendUnescapedJSONStringBounded(nil, inner, 0, trimSpace)
		if !ok {
			return false, nil
		}
		e.totalTextBytes += int64(totalDecoded)
		return true, nil
	}
	remain := int(e.sampleBudget - e.sampledTextBytes)
	decoded, totalDecoded, ok := appendUnescapedJSONStringBounded(e.scratch[:0], inner, remain, trimSpace)
	if !ok {
		return false, nil
	}
	e.scratch = decoded[:0]
	if totalDecoded == 0 {
		return true, nil
	}
	return true, e.observeSampleAndTotal(decoded, int64(totalDecoded))
}

func hasNonASCIIOrHTMLEscape(inner []byte, escapeHTML bool) bool {
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		if c < 0x20 || c >= utf8.RuneSelf {
			return true
		}
		if escapeHTML && (c == '<' || c == '>' || c == '&') {
			return true
		}
	}
	return false
}

// observeJSONEscapedStringContent observes the JSON-encoded content of a JSON
// string span (without outer quotes) matching encoding/json output (and
// marshalOpenAIUpstreamJSON when escapeHTML is false), keeping canonical escapes
// (\n, \r, \t, \b, \f, \", \\, \u00xx, \u2028, \u2029) while normalizing
// printable \uXXXX runes to UTF-8 and \/ to /.
func (e *boundedTokenEstimator) observeJSONEscapedStringContent(b []byte, spanStart, spanEnd int, escapeHTML bool) (bool, error) {
	if spanEnd-spanStart < 2 || b[spanStart] != '"' || b[spanEnd-1] != '"' {
		return false, nil
	}
	inner := b[spanStart+1 : spanEnd-1]
	if len(inner) == 0 {
		return true, nil
	}
	if bytes.IndexByte(inner, '\\') < 0 && !hasNonASCIIOrHTMLEscape(inner, escapeHTML) {
		return true, e.observePlainBytes(inner)
	}
	if e.sampledTextBytes >= e.sampleBudget {
		_, totalEscaped, ok := appendNormalizedJSONEscapedStringBounded(nil, inner, 0, escapeHTML)
		if !ok {
			return false, nil
		}
		e.totalTextBytes += int64(totalEscaped)
		return true, nil
	}
	remain := int(e.sampleBudget - e.sampledTextBytes)
	encoded, totalEscaped, ok := appendNormalizedJSONEscapedStringBounded(e.scratch[:0], inner, remain, escapeHTML)
	if !ok {
		return false, nil
	}
	e.scratch = encoded[:0]
	if totalEscaped == 0 {
		return true, nil
	}
	return true, e.observeSampleAndTotal(encoded, int64(totalEscaped))
}

func (e *boundedTokenEstimator) observeRawJSONSpan(b []byte, spanStart, spanEnd int) error {
	if spanEnd <= spanStart {
		return nil
	}
	if e.sampledTextBytes >= e.sampleBudget {
		e.totalTextBytes += int64(countCompactedJSONBytes(b[spanStart:spanEnd]))
		return nil
	}
	remain := int(e.sampleBudget - e.sampledTextBytes)
	sample, totalCompacted := appendCompactedJSONBounded(e.scratch[:0], b[spanStart:spanEnd], remain)
	e.scratch = sample[:0]
	return e.observeSampleAndTotal(sample, int64(totalCompacted))
}

func (e *boundedTokenEstimator) observeRawBytesSpan(b []byte, spanStart, spanEnd int) error {
	if spanEnd <= spanStart {
		return nil
	}
	raw := b[spanStart:spanEnd]
	if e.sampledTextBytes >= e.sampleBudget {
		e.totalTextBytes += int64(len(raw))
		return nil
	}
	remain := int(e.sampleBudget - e.sampledTextBytes)
	sample := raw
	if len(sample) > remain {
		sample = trimTrailingIncompleteUTF8(sample[:remain])
	}
	return e.observeSampleAndTotal(sample, int64(len(raw)))
}

func (e *boundedTokenEstimator) observeStringArrayFieldNormalized(b []byte, field string, arrStart, arrEnd int, escapeHTML bool) error {
	if e.sampledTextBytes >= e.sampleBudget {
		total := int64(len(field) + 6) // ,"field":[...]
		first := true
		var elemErr error
		ok := jsonForEachElementSpan(b, arrStart, arrEnd, func(es, ee int) bool {
			if !first {
				total++
			}
			first = false
			if (jsonSpan{es, ee}).isNull(b) {
				total += 2
				return true
			}
			if b[es] == '"' {
				_, elemLen, okStr := appendNormalizedJSONEscapedStringBounded(nil, b[es+1:ee-1], 0, escapeHTML)
				if !okStr {
					elemErr = fmt.Errorf("parse responses input_tokens request: invalid %s element", field)
					return false
				}
				total += int64(elemLen) + 2
				return true
			}
			total += int64(countCompactedJSONBytes(b[es:ee]))
			return true
		})
		if elemErr != nil {
			return elemErr
		}
		if !ok {
			return fmt.Errorf("parse responses input_tokens request: invalid %s array", field)
		}
		e.totalTextBytes += total
		return nil
	}

	remain := int(e.sampleBudget - e.sampledTextBytes)
	buf := e.scratch[:0]
	capturing := len(buf) < remain
	var total int64
	emitBytes := func(p []byte) {
		total += int64(len(p))
		if !capturing {
			return
		}
		need := remain - len(buf)
		if len(p) <= need {
			buf = append(buf, p...)
		} else {
			take := len(trimTrailingIncompleteUTF8(p[:need]))
			if take > 0 {
				buf = append(buf, p[:take]...)
			}
			capturing = false
		}
		if len(buf) >= remain {
			capturing = false
		}
	}
	emitASCIIByte := func(c byte) {
		total++
		if !capturing {
			return
		}
		if len(buf) < remain {
			buf = append(buf, c)
		}
		if len(buf) >= remain {
			capturing = false
		}
	}

	emitBytes([]byte(`,"`))
	emitBytes([]byte(field))
	emitBytes([]byte(`":[`))
	first := true
	var elemErr error
	ok := jsonForEachElementSpan(b, arrStart, arrEnd, func(es, ee int) bool {
		if !first {
			emitASCIIByte(',')
		}
		first = false
		if (jsonSpan{es, ee}).isNull(b) {
			emitBytes([]byte(`""`))
			return true
		}
		if b[es] == '"' {
			emitASCIIByte('"')
			prevLen := len(buf)
			var elemLen int
			var okStr bool
			if capturing {
				buf, elemLen, okStr = appendNormalizedJSONEscapedStringBounded(buf, b[es+1:ee-1], remain, escapeHTML)
				if len(buf)-prevLen < elemLen || len(buf) >= remain {
					capturing = false
				}
			} else {
				_, elemLen, okStr = appendNormalizedJSONEscapedStringBounded(nil, b[es+1:ee-1], 0, escapeHTML)
			}
			if !okStr {
				elemErr = fmt.Errorf("parse responses input_tokens request: invalid %s element", field)
				return false
			}
			total += int64(elemLen)
			emitASCIIByte('"')
			return true
		}
		prevLen := len(buf)
		var elemCompacted int
		if capturing {
			buf, elemCompacted = appendCompactedJSONBounded(buf, b[es:ee], remain)
			if len(buf)-prevLen < elemCompacted || len(buf) >= remain {
				capturing = false
			}
		} else {
			elemCompacted = countCompactedJSONBytes(b[es:ee])
		}
		total += int64(elemCompacted)
		return true
	})
	if elemErr != nil {
		e.scratch = buf[:0]
		return elemErr
	}
	if !ok {
		e.scratch = buf[:0]
		return fmt.Errorf("parse responses input_tokens request: invalid %s array", field)
	}
	emitASCIIByte(']')
	err := e.observeSampleAndTotal(buf, total)
	e.scratch = buf[:0]
	return err
}

func trimTrailingIncompleteUTF8(b []byte) []byte {
	for k := 0; k < utf8.UTFMax && len(b)-1-k >= 0; k++ {
		start := len(b) - 1 - k
		if utf8.RuneStart(b[start]) {
			if !utf8.FullRune(b[start:]) {
				return b[:start]
			}
			return b
		}
	}
	return b
}

func trimTrailingIncompleteUTF8String(s string) string {
	for k := 0; k < utf8.UTFMax && len(s)-1-k >= 0; k++ {
		start := len(s) - 1 - k
		if utf8.RuneStart(s[start]) {
			if !utf8.FullRuneInString(s[start:]) {
				return s[:start]
			}
			return s
		}
	}
	return s
}

const jsonLowerHex = "0123456789abcdef"

func appendNormalizedJSONEscapedStringBounded(dst, inner []byte, maxBytes int, escapeHTML bool) ([]byte, int, bool) {
	capturing := len(dst) < maxBytes
	total := 0
	emitSeq := func(seq []byte) {
		if capturing {
			if len(dst)+len(seq) <= maxBytes {
				dst = append(dst, seq...)
			} else {
				capturing = false
			}
			if len(dst) >= maxBytes {
				capturing = false
			}
		}
		total += len(seq)
	}
	emitRune := func(r rune) {
		switch r {
		case '"':
			emitSeq([]byte(`\"`))
		case '\\':
			emitSeq([]byte(`\\`))
		case '\b':
			emitSeq([]byte(`\b`))
		case '\f':
			emitSeq([]byte(`\f`))
		case '\n':
			emitSeq([]byte(`\n`))
		case '\r':
			emitSeq([]byte(`\r`))
		case '\t':
			emitSeq([]byte(`\t`))
		case '\u2028':
			emitSeq([]byte(`\u2028`))
		case '\u2029':
			emitSeq([]byte(`\u2029`))
		default:
			if r >= 0 && r < 0x20 || (escapeHTML && (r == '<' || r == '>' || r == '&')) {
				var escBuf [6]byte
				escBuf[0] = '\\'
				escBuf[1] = 'u'
				escBuf[2] = '0'
				escBuf[3] = '0'
				escBuf[4] = jsonLowerHex[(r>>4)&0xF]
				escBuf[5] = jsonLowerHex[r&0xF]
				emitSeq(escBuf[:])
				return
			}
			if !utf8.ValidRune(r) {
				r = utf8.RuneError
			}
			var rbuf [utf8.UTFMax]byte
			rn := utf8.EncodeRune(rbuf[:], r)
			emitSeq(rbuf[:rn])
		}
	}

	i := 0
	n := len(inner)
	for i < n {
		c := inner[i]
		if c != '\\' {
			if c < 0x20 {
				return dst, total, false
			}
			if c >= utf8.RuneSelf {
				r, size := utf8.DecodeRune(inner[i:])
				if r == utf8.RuneError && size == 1 {
					// json.Unmarshal converts invalid raw UTF-8 bytes in JSON to U+FFFD.
					r = utf8.RuneError
				}
				emitRune(r)
				i += size
				continue
			}
			if escapeHTML && (c == '<' || c == '>' || c == '&') {
				emitRune(rune(c))
				i++
				continue
			}
			start := i
			i++
			for i < n {
				b := inner[i]
				if b == '\\' || b < 0x20 || b >= utf8.RuneSelf || (escapeHTML && (b == '<' || b == '>' || b == '&')) {
					break
				}
				i++
			}
			chunk := inner[start:i]
			if capturing {
				need := maxBytes - len(dst)
				if len(chunk) <= need {
					dst = append(dst, chunk...)
				} else {
					dst = append(dst, chunk[:need]...)
					capturing = false
				}
				if len(dst) >= maxBytes {
					capturing = false
				}
			}
			total += len(chunk)
			continue
		}
		if i+1 >= n {
			return dst, total, false
		}
		esc := inner[i+1]
		switch esc {
		case '"', '\\', 'b', 'f', 'n', 'r', 't':
			var seq [2]byte
			seq[0] = '\\'
			seq[1] = esc
			emitSeq(seq[:])
			i += 2
		case '/':
			emitSeq([]byte{'/'})
			i += 2
		case 'u':
			if i+6 > n {
				return dst, total, false
			}
			r1, ok := parseHex4(inner[i+2 : i+6])
			if !ok {
				return dst, total, false
			}
			i += 6
			r := rune(r1)
			if r1 >= 0xD800 && r1 <= 0xDBFF && i+6 <= n && inner[i] == '\\' && inner[i+1] == 'u' {
				if r2, ok2 := parseHex4(inner[i+2 : i+6]); ok2 && r2 >= 0xDC00 && r2 <= 0xDFFF {
					r = utf16.DecodeRune(rune(r1), rune(r2))
					i += 6
				}
			}
			emitRune(r)
		default:
			return dst, total, false
		}
	}
	return dst, total, true
}

func trimLeftJSONOrUnicodeSpace(b []byte) []byte {
	i := 0
	for i < len(b) {
		c := b[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f' {
			i++
			continue
		}
		if c < utf8.RuneSelf {
			return b[i:]
		}
		r, size := utf8.DecodeRune(b[i:])
		if !unicode.IsSpace(r) {
			return b[i:]
		}
		i += size
	}
	return nil
}

func countTrailingSpaceBytes(b []byte) int {
	i := len(b)
	for i > 0 {
		c := b[i-1]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f' {
			i--
			continue
		}
		if c < utf8.RuneSelf {
			return len(b) - i
		}
		start := i - 1
		for start > 0 && i-start < utf8.UTFMax && !utf8.RuneStart(b[start]) {
			start--
		}
		r, size := utf8.DecodeRune(b[start:i])
		if start+size != i || !unicode.IsSpace(r) {
			return len(b) - i
		}
		i = start
	}
	return len(b)
}

func parseHex4(b []byte) (uint16, bool) {
	if len(b) < 4 {
		return 0, false
	}
	var v uint16
	for i := 0; i < 4; i++ {
		c := b[i]
		var d uint16
		switch {
		case c >= '0' && c <= '9':
			d = uint16(c - '0')
		case c >= 'a' && c <= 'f':
			d = uint16(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = uint16(c-'A') + 10
		default:
			return 0, false
		}
		v = (v << 4) | d
	}
	return v, true
}

func appendUnescapedJSONStringBounded(dst, inner []byte, maxDecoded int, trimSpace bool) ([]byte, int, bool) {
	capturing := len(dst) < maxDecoded
	leading := trimSpace
	total := 0
	trailingSpace := 0
	i := 0
	n := len(inner)
	for i < n {
		k := bytes.IndexByte(inner[i:], '\\')
		var chunk []byte
		if k < 0 {
			chunk = inner[i:]
			i = n
		} else {
			chunk = inner[i : i+k]
			i += k
		}
		if len(chunk) > 0 {
			if leading {
				chunk = trimLeftJSONOrUnicodeSpace(chunk)
				if len(chunk) > 0 {
					leading = false
				}
			}
			if len(chunk) > 0 {
				if capturing {
					need := maxDecoded - len(dst)
					if len(chunk) <= need {
						dst = append(dst, chunk...)
					} else {
						take := len(trimTrailingIncompleteUTF8(chunk[:need]))
						if take > 0 {
							dst = append(dst, chunk[:take]...)
						}
						capturing = false
					}
					if len(dst) >= maxDecoded {
						capturing = false
					}
				}
				total += len(chunk)
				if trimSpace {
					trail := countTrailingSpaceBytes(chunk)
					if trail == len(chunk) {
						trailingSpace += trail
					} else {
						trailingSpace = trail
					}
				}
			}
		}
		if i >= n {
			break
		}
		if i+1 >= n {
			return dst, total, false
		}
		esc := inner[i+1]
		switch esc {
		case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			var ch byte
			switch esc {
			case '"', '\\', '/':
				ch = esc
			case 'b':
				ch = '\b'
			case 'f':
				ch = '\f'
			case 'n':
				ch = '\n'
			case 'r':
				ch = '\r'
			case 't':
				ch = '\t'
			}
			i += 2
			isSpace := ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '\f'
			if leading {
				if isSpace {
					continue
				}
				leading = false
			}
			if capturing {
				if len(dst)+1 <= maxDecoded {
					dst = append(dst, ch)
				} else {
					capturing = false
				}
				if len(dst) >= maxDecoded {
					capturing = false
				}
			}
			total++
			if trimSpace {
				if isSpace {
					trailingSpace++
				} else {
					trailingSpace = 0
				}
			}
		case 'u':
			if i+6 > n {
				return dst, total, false
			}
			r1, ok := parseHex4(inner[i+2 : i+6])
			if !ok {
				return dst, total, false
			}
			i += 6
			r := rune(r1)
			if r1 >= 0xD800 && r1 <= 0xDBFF && i+6 <= n && inner[i] == '\\' && inner[i+1] == 'u' {
				if r2, ok2 := parseHex4(inner[i+2 : i+6]); ok2 && r2 >= 0xDC00 && r2 <= 0xDFFF {
					r = utf16.DecodeRune(rune(r1), rune(r2))
					i += 6
				}
			}
			if !utf8.ValidRune(r) {
				r = utf8.RuneError
			}
			isSpace := trimSpace && unicode.IsSpace(r)
			if leading {
				if isSpace {
					continue
				}
				leading = false
			}
			rLen := utf8.RuneLen(r)
			if capturing {
				if len(dst)+rLen <= maxDecoded {
					dst = utf8.AppendRune(dst, r)
				} else {
					capturing = false
				}
				if len(dst) >= maxDecoded {
					capturing = false
				}
			}
			total += rLen
			if trimSpace {
				if isSpace {
					trailingSpace += rLen
				} else {
					trailingSpace = 0
				}
			}
		default:
			return dst, total, false
		}
	}
	if trimSpace {
		total -= trailingSpace
		if total < 0 {
			total = 0
		}
		if len(dst) > total {
			dst = dst[:total]
		}
	}
	return dst, total, true
}

func appendCompactedJSONBounded(dst, raw []byte, maxBytes int) ([]byte, int) {
	capturing := len(dst) < maxBytes
	total := 0
	inString := false
	escaped := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
		} else {
			if isJSONSpace(c) {
				continue
			}
			if c == '"' {
				inString = true
			}
		}
		if capturing {
			if len(dst) < maxBytes {
				dst = append(dst, c)
			} else {
				capturing = false
			}
		}
		total++
	}
	if len(dst) > 0 {
		dst = trimTrailingIncompleteUTF8(dst)
	}
	return dst, total
}

func countCompactedJSONBytes(raw []byte) int {
	total := 0
	inString := false
	escaped := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
		} else {
			if isJSONSpace(c) {
				continue
			}
			if c == '"' {
				inString = true
			}
		}
		total++
	}
	return total
}

const (
	boundedMetaInlineLimit  = 4 << 10  // 4 KiB: inline small metadata verbatim for encoding/json validation
	boundedToolsInlineLimit = 64 << 10 // 64 KiB: inline normal tools arrays verbatim
)

const (
	jsonEmptyStringPlaceholder    = `""`
	jsonNonEmptyStringPlaceholder = `"x"`
)

var errResponsesInputRawFallback = errors.New("responses input requires raw JSON fallback")

// forEachJSONMemberDecoded walks a JSON object at b[objStart:objEnd] without
// allocation on unescaped keys, and decodes escaped keys via encoding/json so
// case-insensitive and escaped field matching never misses a key.
func forEachJSONMemberDecoded(b []byte, objStart, objEnd int, fn func(key string, rawKeyStart, rawKeyEnd, valStart, valEnd int) bool) bool {
	if objStart < 0 || objEnd > len(b) || objEnd-objStart < 2 || b[objStart] != '{' || b[objEnd-1] != '}' {
		return false
	}
	i := skipJSONWhitespace(b[:objEnd], objStart+1)
	if i == objEnd-1 {
		return true
	}
	s := jsonView(b)
	for {
		if i >= objEnd-1 || b[i] != '"' {
			return false
		}
		rawKeyStart := i
		keyEnd, esc, ok := jsonSkipString(s, i)
		if !ok || keyEnd >= objEnd {
			return false
		}
		var key string
		if !esc {
			key = s[i+1 : keyEnd-1]
		} else if keyEnd-i <= boundedMetaInlineLimit {
			if err := json.Unmarshal(b[i:keyEnd], &key); err != nil {
				return false
			}
		}
		i = skipJSONWhitespace(b[:objEnd], keyEnd)
		if i >= objEnd-1 || b[i] != ':' {
			return false
		}
		i = skipJSONWhitespace(b[:objEnd], i+1)
		if i >= objEnd-1 {
			return false
		}
		valStart := i
		valEnd, ok := jsonSkipValueStrict(b, valStart)
		if !ok || valEnd >= objEnd {
			return false
		}
		if !fn(key, rawKeyStart, keyEnd, valStart, valEnd) {
			return true
		}
		i = skipJSONWhitespace(b[:objEnd], valEnd)
		if i == objEnd-1 {
			return true
		}
		if i >= objEnd-1 || b[i] != ',' {
			return false
		}
		i = skipJSONWhitespace(b[:objEnd], i+1)
	}
}

func appendMetaKeyPrefix(dst, body []byte, rawKeyStart, rawKeyEnd int, needComma bool) []byte {
	if needComma {
		dst = append(dst, ',')
	}
	dst = append(dst, body[rawKeyStart:rawKeyEnd]...)
	return append(dst, ':')
}

func appendJSONMetaPlaceholder(dst, body []byte, valStart, valEnd int, stringPlaceholder string) []byte {
	if valStart >= valEnd {
		return append(dst, "null"...)
	}
	switch body[valStart] {
	case '"':
		return append(dst, stringPlaceholder...)
	case '[':
		return append(dst, "[]"...)
	case '{':
		return append(dst, "{}"...)
	case 'n':
		return append(dst, "null"...)
	case 't':
		return append(dst, "true"...)
	case 'f':
		return append(dst, "false"...)
	default:
		return append(dst, '0')
	}
}

func appendBoundedModelValue(dst, body []byte, valStart, valEnd int) ([]byte, error) {
	if valEnd-valStart <= boundedToolsInlineLimit {
		return append(dst, body[valStart:valEnd]...), nil
	}
	if body[valStart] != '"' {
		return appendJSONMetaPlaceholder(dst, body, valStart, valEnd, jsonEmptyStringPlaceholder), nil
	}
	prefix, totalDecoded, ok := appendUnescapedJSONStringBounded(nil, body[valStart+1:valEnd-1], 256, true)
	if !ok {
		return nil, fmt.Errorf("invalid model string")
	}
	if totalDecoded == 0 {
		return append(dst, jsonEmptyStringPlaceholder...), nil
	}
	encoded, err := json.Marshal(string(prefix))
	if err != nil {
		return nil, err
	}
	return append(dst, encoded...), nil
}

func validateStringArraySpan(body []byte, arrStart, arrEnd int) bool {
	valid := true
	walkOK := jsonForEachElementSpan(body, arrStart, arrEnd, func(es, ee int) bool {
		if !(jsonSpan{es, ee}).isNull(body) && body[es] != '"' {
			valid = false
			return false
		}
		return true
	})
	return walkOK && valid
}

func unmarshalBoundedObject[T any](
	body []byte,
	objStart, objEnd int,
	scratch []byte,
	dst *T,
	knownField func(key string) (isRawJSON bool, ok bool),
	largeField func(key string, valStart, valEnd int) (placeholder string, handled bool, err error),
) ([]byte, error) {
	if objEnd-objStart <= boundedMetaInlineLimit {
		return scratch, json.Unmarshal(body[objStart:objEnd], dst)
	}
	meta := append(scratch[:0], '{')
	hasMember := false
	var walkErr error
	walkOK := forEachJSONMemberDecoded(body, objStart, objEnd, func(key string, rawKeyStart, rawKeyEnd, valStart, valEnd int) bool {
		isRaw, ok := knownField(key)
		if !ok {
			return true
		}
		meta = appendMetaKeyPrefix(meta, body, rawKeyStart, rawKeyEnd, hasMember)
		hasMember = true
		if valEnd-valStart <= boundedMetaInlineLimit {
			meta = append(meta, body[valStart:valEnd]...)
			return true
		}
		if largeField != nil {
			placeholder, handled, err := largeField(key, valStart, valEnd)
			if err != nil {
				walkErr = err
				return false
			}
			if handled {
				meta = append(meta, placeholder...)
				return true
			}
		}
		if isRaw {
			meta = append(meta, "null"...)
			return true
		}
		meta = appendJSONMetaPlaceholder(meta, body, valStart, valEnd, jsonNonEmptyStringPlaceholder)
		return true
	})
	if walkErr != nil {
		return meta[:0], walkErr
	}
	if !walkOK {
		return meta[:0], fmt.Errorf("invalid JSON object")
	}
	meta = append(meta, '}')
	err := json.Unmarshal(meta, dst)
	return meta[:0], err
}

func knownAnthropicCacheControlField(key string) (bool, bool) {
	return false, strings.EqualFold(key, "type") || strings.EqualFold(key, "ttl")
}

func validateAnthropicCacheControlSpan(body []byte, objStart, objEnd int, scratch []byte) ([]byte, error) {
	var cc apicompat.AnthropicCacheControl
	return unmarshalBoundedObject(body, objStart, objEnd, scratch, &cc, knownAnthropicCacheControlField, nil)
}

func knownAnthropicImageSourceField(key string) (bool, bool) {
	return false, strings.EqualFold(key, "type") || strings.EqualFold(key, "media_type") || strings.EqualFold(key, "data")
}

func validateAnthropicImageSourceSpan(body []byte, objStart, objEnd int, scratch []byte) ([]byte, error) {
	var src apicompat.AnthropicImageSource
	return unmarshalBoundedObject(body, objStart, objEnd, scratch, &src, knownAnthropicImageSourceField, nil)
}

func hasJSONStringPrefix(b []byte, spanStart, spanEnd int, prefix string) bool {
	if len(prefix) == 0 {
		return spanEnd-spanStart >= 2 && b[spanStart] == '"' && b[spanEnd-1] == '"'
	}
	if spanEnd-spanStart < 2 || b[spanStart] != '"' || b[spanEnd-1] != '"' {
		return false
	}
	inner := b[spanStart+1 : spanEnd-1]
	if len(inner) < len(prefix) {
		return false
	}
	if bytes.IndexByte(inner, '\\') < 0 {
		return bytes.HasPrefix(inner, []byte(prefix))
	}
	var buf [64]byte
	decoded := buf[:0]
	if len(prefix) > len(buf) {
		decoded = make([]byte, 0, len(prefix))
	}
	n := len(inner)
	for i := 0; i < n && len(decoded) < len(prefix); {
		if inner[i] != '\\' {
			k := bytes.IndexByte(inner[i:], '\\')
			var chunk []byte
			if k < 0 {
				chunk = inner[i:]
				i = n
			} else {
				chunk = inner[i : i+k]
				i += k
			}
			need := len(prefix) - len(decoded)
			if len(chunk) > need {
				chunk = chunk[:need]
			}
			decoded = append(decoded, chunk...)
			continue
		}
		if i+1 >= n {
			return false
		}
		esc := inner[i+1]
		switch esc {
		case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			var ch byte
			switch esc {
			case '"', '\\', '/':
				ch = esc
			case 'b':
				ch = '\b'
			case 'f':
				ch = '\f'
			case 'n':
				ch = '\n'
			case 'r':
				ch = '\r'
			case 't':
				ch = '\t'
			}
			decoded = append(decoded, ch)
			i += 2
		case 'u':
			if i+6 > n {
				return false
			}
			r1, ok := parseHex4(inner[i+2 : i+6])
			if !ok {
				return false
			}
			i += 6
			r := rune(r1)
			if r1 >= 0xD800 && r1 <= 0xDBFF && i+6 <= n && inner[i] == '\\' && inner[i+1] == 'u' {
				if r2, ok2 := parseHex4(inner[i+2 : i+6]); ok2 && r2 >= 0xDC00 && r2 <= 0xDFFF {
					r = utf16.DecodeRune(rune(r1), rune(r2))
					i += 6
				}
			}
			if !utf8.ValidRune(r) {
				r = utf8.RuneError
			}
			var rbuf [utf8.UTFMax]byte
			rn := utf8.EncodeRune(rbuf[:], r)
			need := len(prefix) - len(decoded)
			if rn > need {
				rn = need
			}
			decoded = append(decoded, rbuf[:rn]...)
		default:
			return false
		}
	}
	return len(decoded) == len(prefix) && string(decoded) == prefix
}

func boundedSmallStringJSON(body []byte, vs, ve, maxDecoded int, trimSpace bool) (string, bool, error) {
	if ve-vs < 2 || body[vs] != '"' || body[ve-1] != '"' {
		return "", false, nil
	}
	decoded, totalDecoded, ok := appendUnescapedJSONStringBounded(nil, body[vs+1:ve-1], maxDecoded, trimSpace)
	if !ok {
		return "", true, fmt.Errorf("invalid JSON string")
	}
	if totalDecoded > maxDecoded {
		return jsonNonEmptyStringPlaceholder, true, nil
	}
	encoded, err := json.Marshal(string(decoded))
	if err != nil {
		return "", true, err
	}
	return string(encoded), true, nil
}

func knownAnthropicThinkingField(key string) (bool, bool) {
	return false, strings.EqualFold(key, "type") || strings.EqualFold(key, "budget_tokens")
}

func validateAnthropicThinkingSpan(body []byte, objStart, objEnd int, scratch []byte) (apicompat.AnthropicThinking, []byte, error) {
	var th apicompat.AnthropicThinking
	var err error
	scratch, err = unmarshalBoundedObject(body, objStart, objEnd, scratch, &th, knownAnthropicThinkingField, func(key string, vs, ve int) (string, bool, error) {
		if strings.EqualFold(key, "type") && body[vs] == '"' {
			return boundedSmallStringJSON(body, vs, ve, 64, false)
		}
		return "", false, nil
	})
	return th, scratch, err
}

func knownAnthropicOutputConfigField(key string) (bool, bool) {
	return false, strings.EqualFold(key, "effort")
}

func validateAnthropicOutputConfigSpan(body []byte, objStart, objEnd int, scratch []byte) (apicompat.AnthropicOutputConfig, []byte, error) {
	var oc apicompat.AnthropicOutputConfig
	var err error
	scratch, err = unmarshalBoundedObject(body, objStart, objEnd, scratch, &oc, knownAnthropicOutputConfigField, func(key string, vs, ve int) (string, bool, error) {
		if strings.EqualFold(key, "effort") && body[vs] == '"' {
			return boundedSmallStringJSON(body, vs, ve, 64, true)
		}
		return "", false, nil
	})
	return oc, scratch, err
}

func knownAnthropicToolChoiceField(key string) (bool, bool) {
	return false, strings.EqualFold(key, "type") || strings.EqualFold(key, "name")
}

func validateAnthropicToolChoiceSpan(body []byte, objStart, objEnd int, scratch []byte) (string, string, jsonSpan, []byte, error) {
	var tc struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	var err error
	scratch, err = unmarshalBoundedObject(body, objStart, objEnd, scratch, &tc, knownAnthropicToolChoiceField, nil)
	if err != nil {
		return "", "", jsonSpan{}, scratch, err
	}
	var largeNameSpan jsonSpan
	if tc.Type == "tool" && objEnd-objStart > boundedMetaInlineLimit {
		var nameSpan jsonSpan
		if !forEachJSONMemberDecoded(body, objStart, objEnd, func(key string, _, _, vs, ve int) bool {
			sp := jsonSpan{vs, ve}
			if strings.EqualFold(key, "name") && !sp.isNull(body) && body[sp.start] == '"' {
				nameSpan = sp
			}
			return true
		}) {
			return "", "", jsonSpan{}, scratch, fmt.Errorf("invalid JSON object")
		}
		if nameSpan.valid() && nameSpan.end-nameSpan.start > boundedMetaInlineLimit {
			largeNameSpan = nameSpan
		}
	}
	return tc.Type, tc.Name, largeNameSpan, scratch, nil
}

func knownResponsesSummaryField(key string) (bool, bool) {
	return false, strings.EqualFold(key, "type") || strings.EqualFold(key, "text")
}

func validateResponsesSummarySpan(body []byte, objStart, objEnd int, scratch []byte) ([]byte, error) {
	var sum apicompat.ResponsesSummary
	return unmarshalBoundedObject(body, objStart, objEnd, scratch, &sum, knownResponsesSummaryField, nil)
}

func knownResponsesToolField(key string) (bool, bool) {
	switch {
	case strings.EqualFold(key, "parameters"):
		return true, true
	case strings.EqualFold(key, "cache_control"),
		strings.EqualFold(key, "type"),
		strings.EqualFold(key, "name"),
		strings.EqualFold(key, "description"),
		strings.EqualFold(key, "strict"),
		strings.EqualFold(key, "tools"),
		strings.EqualFold(key, "children"),
		strings.EqualFold(key, "allowed_x_handles"),
		strings.EqualFold(key, "excluded_x_handles"),
		strings.EqualFold(key, "from_date"),
		strings.EqualFold(key, "to_date"),
		strings.EqualFold(key, "enable_image_understanding"),
		strings.EqualFold(key, "enable_video_understanding"):
		return false, true
	default:
		return false, false
	}
}

const maxBoundedResponsesToolDepth = 256

func validateResponsesToolSpan(body []byte, elemStart, elemEnd int, scratch []byte, depth int) ([]byte, error) {
	if depth > maxBoundedResponsesToolDepth {
		return scratch, nil
	}
	if elemEnd-elemStart <= boundedMetaInlineLimit {
		var tool apicompat.ResponsesTool
		return scratch, json.Unmarshal(body[elemStart:elemEnd], &tool)
	}
	if body[elemStart] == '"' {
		return scratch, nil
	}
	if body[elemStart] != '{' {
		return scratch, fmt.Errorf("invalid responses tool")
	}
	var tool apicompat.ResponsesTool
	return unmarshalBoundedObject(body, elemStart, elemEnd, scratch, &tool, knownResponsesToolField, func(key string, vs, ve int) (string, bool, error) {
		switch {
		case strings.EqualFold(key, "cache_control") && body[vs] == '{':
			_, err := validateAnthropicCacheControlSpan(body, vs, ve, nil)
			return "{}", true, err
		case (strings.EqualFold(key, "tools") || strings.EqualFold(key, "children")) && body[vs] == '[':
			if depth >= maxBoundedResponsesToolDepth {
				return "[]", true, nil
			}
			var nestedScratch []byte
			var nestedErr error
			ok := jsonForEachElementSpan(body, vs, ve, func(ns, ne int) bool {
				if (jsonSpan{ns, ne}).isNull(body) {
					return true
				}
				nestedScratch, nestedErr = validateResponsesToolSpan(body, ns, ne, nestedScratch, depth+1)
				return nestedErr == nil
			})
			if nestedErr != nil {
				return "", true, nestedErr
			}
			if !ok {
				return "", true, fmt.Errorf("invalid nested responses tools")
			}
			return "[]", true, nil
		case (strings.EqualFold(key, "allowed_x_handles") || strings.EqualFold(key, "excluded_x_handles")) && body[vs] == '[':
			if !validateStringArraySpan(body, vs, ve) {
				return "[0]", true, nil
			}
			return "[]", true, nil
		}
		return "", false, nil
	})
}

func validateResponsesToolsArraySpan(body []byte, arrStart, arrEnd int, scratch []byte) ([]byte, error) {
	var toolErr error
	ok := jsonForEachElementSpan(body, arrStart, arrEnd, func(es, ee int) bool {
		if (jsonSpan{es, ee}).isNull(body) {
			return true
		}
		scratch, toolErr = validateResponsesToolSpan(body, es, ee, scratch, 0)
		return toolErr == nil
	})
	if toolErr != nil {
		return scratch, toolErr
	}
	if !ok {
		return scratch, fmt.Errorf("invalid tools array")
	}
	return scratch, nil
}

func knownResponsesInputItemField(key string) (bool, bool) {
	switch {
	case strings.EqualFold(key, "content"),
		strings.EqualFold(key, "output"),
		strings.EqualFold(key, "action"):
		return true, true
	case strings.EqualFold(key, "cache_control"),
		strings.EqualFold(key, "type"),
		strings.EqualFold(key, "role"),
		strings.EqualFold(key, "encrypted_content"),
		strings.EqualFold(key, "summary"),
		strings.EqualFold(key, "call_id"),
		strings.EqualFold(key, "name"),
		strings.EqualFold(key, "arguments"),
		strings.EqualFold(key, "input"),
		strings.EqualFold(key, "id"):
		return false, true
	default:
		return false, false
	}
}

func validateResponsesInputItemSpan(body []byte, objStart, objEnd int, scratch []byte) ([]byte, error) {
	if objEnd-objStart <= boundedMetaInlineLimit {
		var item apicompat.ResponsesInputItem
		return scratch, json.Unmarshal(body[objStart:objEnd], &item)
	}
	var item apicompat.ResponsesInputItem
	return unmarshalBoundedObject(body, objStart, objEnd, scratch, &item, knownResponsesInputItemField, func(key string, vs, ve int) (string, bool, error) {
		switch {
		case strings.EqualFold(key, "cache_control") && body[vs] == '{':
			_, err := validateAnthropicCacheControlSpan(body, vs, ve, nil)
			return "{}", true, err
		case strings.EqualFold(key, "summary") && body[vs] == '[':
			var sumScratch []byte
			var sumErr error
			ok := jsonForEachElementSpan(body, vs, ve, func(ss, se int) bool {
				if (jsonSpan{ss, se}).isNull(body) {
					return true
				}
				if body[ss] != '{' {
					sumErr = fmt.Errorf("invalid summary element")
					return false
				}
				sumScratch, sumErr = validateResponsesSummarySpan(body, ss, se, sumScratch)
				return sumErr == nil
			})
			if sumErr != nil {
				return "", true, sumErr
			}
			if !ok {
				return "", true, fmt.Errorf("invalid summary array")
			}
			return "[]", true, nil
		}
		return "", false, nil
	})
}

func knownResponsesContentPartField(key string) (bool, bool) {
	switch {
	case strings.EqualFold(key, "prompt_cache_breakpoint"):
		return true, true
	case strings.EqualFold(key, "cache_control"),
		strings.EqualFold(key, "type"),
		strings.EqualFold(key, "text"),
		strings.EqualFold(key, "refusal"),
		strings.EqualFold(key, "image_url"),
		strings.EqualFold(key, "filename"),
		strings.EqualFold(key, "file_data"),
		strings.EqualFold(key, "file_id"):
		return false, true
	default:
		return false, false
	}
}

func validateResponsesContentPartSpan(body []byte, objStart, objEnd int, scratch []byte) ([]byte, error) {
	if objEnd-objStart <= boundedMetaInlineLimit {
		var part apicompat.ResponsesContentPart
		return scratch, json.Unmarshal(body[objStart:objEnd], &part)
	}
	var part apicompat.ResponsesContentPart
	return unmarshalBoundedObject(body, objStart, objEnd, scratch, &part, knownResponsesContentPartField, func(key string, vs, ve int) (string, bool, error) {
		if strings.EqualFold(key, "cache_control") && body[vs] == '{' {
			_, err := validateAnthropicCacheControlSpan(body, vs, ve, nil)
			return "{}", true, err
		}
		return "", false, nil
	})
}

func knownAnthropicToolField(key string) (bool, bool) {
	switch {
	case strings.EqualFold(key, "input_schema"):
		return true, true
	case strings.EqualFold(key, "type"),
		strings.EqualFold(key, "name"),
		strings.EqualFold(key, "description"),
		strings.EqualFold(key, "cache_control"):
		return false, true
	default:
		return false, false
	}
}

func validateAnthropicToolSpan(body []byte, elemStart, elemEnd int, scratch []byte) ([]byte, error) {
	if elemEnd-elemStart <= boundedMetaInlineLimit {
		var tool apicompat.AnthropicTool
		return scratch, json.Unmarshal(body[elemStart:elemEnd], &tool)
	}
	if body[elemStart] != '{' {
		return scratch, fmt.Errorf("invalid anthropic tool")
	}
	var tool apicompat.AnthropicTool
	return unmarshalBoundedObject(body, elemStart, elemEnd, scratch, &tool, knownAnthropicToolField, func(key string, vs, ve int) (string, bool, error) {
		if strings.EqualFold(key, "cache_control") && body[vs] == '{' {
			_, err := validateAnthropicCacheControlSpan(body, vs, ve, nil)
			return "{}", true, err
		}
		return "", false, nil
	})
}

func validateAnthropicToolsArraySpan(body []byte, arrStart, arrEnd int, scratch []byte) ([]byte, error) {
	var toolErr error
	ok := jsonForEachElementSpan(body, arrStart, arrEnd, func(es, ee int) bool {
		if (jsonSpan{es, ee}).isNull(body) {
			return true
		}
		scratch, toolErr = validateAnthropicToolSpan(body, es, ee, scratch)
		return toolErr == nil
	})
	if toolErr != nil {
		return scratch, toolErr
	}
	if !ok {
		return scratch, fmt.Errorf("invalid tools array")
	}
	return scratch, nil
}

func knownAnthropicMessageField(key string) (bool, bool) {
	if strings.EqualFold(key, "content") {
		return true, true
	}
	return false, strings.EqualFold(key, "role")
}

func validateAnthropicMessageSpan(body []byte, objStart, objEnd int, scratch []byte, requireContent bool) ([]byte, error) {
	var msg apicompat.AnthropicMessage
	var err error
	scratch, err = unmarshalBoundedObject(body, objStart, objEnd, scratch, &msg, knownAnthropicMessageField, nil)
	if err != nil {
		return scratch, err
	}
	if requireContent && len(msg.Content) == 0 {
		return scratch, fmt.Errorf("parse anthropic count_tokens request: message content is required")
	}
	return scratch, nil
}

func knownAnthropicContentBlockField(key string) (bool, bool) {
	switch {
	case strings.EqualFold(key, "input"),
		strings.EqualFold(key, "content"):
		return true, true
	case strings.EqualFold(key, "type"),
		strings.EqualFold(key, "cache_control"),
		strings.EqualFold(key, "text"),
		strings.EqualFold(key, "thinking"),
		strings.EqualFold(key, "signature"),
		strings.EqualFold(key, "source"),
		strings.EqualFold(key, "id"),
		strings.EqualFold(key, "name"),
		strings.EqualFold(key, "tool_use_id"),
		strings.EqualFold(key, "is_error"):
		return false, true
	default:
		return false, false
	}
}

func scanAnthropicContentBlockMembers(b []byte, objStart, objEnd int) (typeSpan, textSpan jsonSpan, needsUnmarshal, walkOK bool) {
	if objStart < 0 || objEnd > len(b) || objEnd-objStart < 2 || b[objStart] != '{' || b[objEnd-1] != '}' {
		return jsonSpan{}, jsonSpan{}, false, false
	}
	i := skipJSONWhitespace(b[:objEnd], objStart+1)
	if i == objEnd-1 {
		return jsonSpan{}, jsonSpan{}, false, true
	}
	s := jsonView(b)
	for {
		if i >= objEnd-1 || b[i] != '"' {
			return jsonSpan{}, jsonSpan{}, false, false
		}
		keyEnd, esc, ok := jsonSkipString(s, i)
		if !ok || keyEnd >= objEnd {
			return jsonSpan{}, jsonSpan{}, false, false
		}
		var key string
		if !esc {
			key = s[i+1 : keyEnd-1]
		} else if keyEnd-i <= boundedMetaInlineLimit {
			var decodedKey string
			if err := json.Unmarshal(b[i:keyEnd], &decodedKey); err != nil {
				return jsonSpan{}, jsonSpan{}, false, false
			}
			key = decodedKey
		}
		i = skipJSONWhitespace(b[:objEnd], keyEnd)
		if i >= objEnd-1 || b[i] != ':' {
			return jsonSpan{}, jsonSpan{}, false, false
		}
		i = skipJSONWhitespace(b[:objEnd], i+1)
		if i >= objEnd-1 {
			return jsonSpan{}, jsonSpan{}, false, false
		}
		vs := i
		ve, ok := jsonSkipValueStrict(b, vs)
		if !ok || ve >= objEnd {
			return jsonSpan{}, jsonSpan{}, false, false
		}
		sp := jsonSpan{vs, ve}
		switch {
		case strings.EqualFold(key, "type"):
			if !sp.isNull(b) {
				if b[vs] != '"' {
					return jsonSpan{}, jsonSpan{}, true, true
				}
				typeSpan = sp
			}
		case strings.EqualFold(key, "text"):
			if !sp.isNull(b) {
				if b[vs] != '"' {
					return jsonSpan{}, jsonSpan{}, true, true
				}
				textSpan = sp
			}
		case strings.EqualFold(key, "thinking"),
			strings.EqualFold(key, "signature"),
			strings.EqualFold(key, "id"),
			strings.EqualFold(key, "name"),
			strings.EqualFold(key, "tool_use_id"):
			if !sp.isNull(b) && b[vs] != '"' {
				return jsonSpan{}, jsonSpan{}, true, true
			}
		case strings.EqualFold(key, "is_error"):
			if !sp.isNull(b) {
				valLen := ve - vs
				if (valLen != 4 || string(b[vs:ve]) != "true") && (valLen != 5 || string(b[vs:ve]) != "false") {
					return jsonSpan{}, jsonSpan{}, true, true
				}
			}
		case strings.EqualFold(key, "cache_control"),
			strings.EqualFold(key, "source"):
			if !sp.isNull(b) {
				return jsonSpan{}, jsonSpan{}, true, true
			}
		}
		i = skipJSONWhitespace(b[:objEnd], ve)
		if i == objEnd-1 {
			return typeSpan, textSpan, false, true
		}
		if i >= objEnd-1 || b[i] != ',' {
			return jsonSpan{}, jsonSpan{}, false, false
		}
		i = skipJSONWhitespace(b[:objEnd], i+1)
	}
}

func parseAnthropicLargeSystemBlock(body []byte, elemStart, elemEnd int, scratch []byte) (typeSpan, textSpan jsonSpan, nextScratch []byte, err error) {
	nextScratch, err = validateAnthropicContentBlockSpan(body, elemStart, elemEnd, scratch)
	if err != nil {
		return jsonSpan{}, jsonSpan{}, nextScratch, err
	}
	memberOK := forEachJSONMemberDecoded(body, elemStart, elemEnd, func(k string, _, _, vs, ve int) bool {
		sp := jsonSpan{vs, ve}
		switch {
		case strings.EqualFold(k, "type"):
			if !sp.isNull(body) {
				typeSpan = sp
			}
		case strings.EqualFold(k, "text"):
			if !sp.isNull(body) {
				textSpan = sp
			}
		}
		return true
	})
	if !memberOK {
		return jsonSpan{}, jsonSpan{}, nextScratch, fmt.Errorf("parse anthropic count_tokens request: invalid system block")
	}
	return typeSpan, textSpan, nextScratch, nil
}

func validateAnthropicContentBlockSpan(body []byte, objStart, objEnd int, scratch []byte) ([]byte, error) {
	if objEnd-objStart <= boundedMetaInlineLimit {
		_, _, needsUnmarshal, walkOK := scanAnthropicContentBlockMembers(body, objStart, objEnd)
		if !walkOK || needsUnmarshal {
			var blk apicompat.AnthropicContentBlock
			return scratch, json.Unmarshal(body[objStart:objEnd], &blk)
		}
		return scratch, nil
	}
	var blk apicompat.AnthropicContentBlock
	return unmarshalBoundedObject(body, objStart, objEnd, scratch, &blk, knownAnthropicContentBlockField, func(key string, vs, ve int) (string, bool, error) {
		switch {
		case strings.EqualFold(key, "cache_control") && body[vs] == '{':
			_, err := validateAnthropicCacheControlSpan(body, vs, ve, nil)
			return "{}", true, err
		case strings.EqualFold(key, "source") && body[vs] == '{':
			_, err := validateAnthropicImageSourceSpan(body, vs, ve, nil)
			return "{}", true, err
		}
		return "", false, nil
	})
}

func estimateOpenAIResponsesInputTokensBounded(body []byte) (int, error) {
	if len(body) <= incompleteInputExactEstimateLimit {
		return EstimateOpenAIResponsesInputTokens(body)
	}
	objStart, objEnd, ok := trimJSONObjectBounds(body)
	if !ok || bytes.HasPrefix(body, utf8BOM) || !validJSON(body[objStart:objEnd]) {
		return 0, fmt.Errorf("parse responses input_tokens request: invalid JSON")
	}

	var instructionsSpan, inputSpan, toolsSpan, toolChoiceSpan jsonSpan
	metaBuf := make([]byte, 0, 512)
	metaBuf = append(metaBuf, '{')
	hasMetaMember := false
	var metaScratch []byte
	var walkErr error

	walkOK := forEachJSONMemberDecoded(body, objStart, objEnd, func(key string, rawKeyStart, rawKeyEnd, valStart, valEnd int) bool {
		val := jsonSpan{valStart, valEnd}
		valLen := valEnd - valStart
		switch {
		case strings.EqualFold(key, "model"):
			metaBuf = appendMetaKeyPrefix(metaBuf, body, rawKeyStart, rawKeyEnd, hasMetaMember)
			hasMetaMember = true
			var err error
			metaBuf, err = appendBoundedModelValue(metaBuf, body, valStart, valEnd)
			if err != nil {
				walkErr = fmt.Errorf("parse responses input_tokens request: %w", err)
				return false
			}
		case strings.EqualFold(key, "instructions"):
			if !val.isNull(body) {
				instructionsSpan = val
			}
			metaBuf = appendMetaKeyPrefix(metaBuf, body, rawKeyStart, rawKeyEnd, hasMetaMember)
			hasMetaMember = true
			if valLen <= boundedMetaInlineLimit {
				metaBuf = append(metaBuf, body[valStart:valEnd]...)
			} else {
				metaBuf = appendJSONMetaPlaceholder(metaBuf, body, valStart, valEnd, jsonNonEmptyStringPlaceholder)
			}
		case strings.EqualFold(key, "input"):
			inputSpan = val
			metaBuf = appendMetaKeyPrefix(metaBuf, body, rawKeyStart, rawKeyEnd, hasMetaMember)
			hasMetaMember = true
			metaBuf = append(metaBuf, "null"...)
		case strings.EqualFold(key, "tools"):
			toolsSpan = val
			metaBuf = appendMetaKeyPrefix(metaBuf, body, rawKeyStart, rawKeyEnd, hasMetaMember)
			hasMetaMember = true
			if valLen <= boundedToolsInlineLimit {
				metaBuf = append(metaBuf, body[valStart:valEnd]...)
			} else if body[valStart] == '[' {
				var err error
				metaScratch, err = validateResponsesToolsArraySpan(body, valStart, valEnd, metaScratch)
				if err != nil {
					walkErr = fmt.Errorf("parse responses input_tokens request: %w", err)
					return false
				}
				metaBuf = append(metaBuf, "[]"...)
			} else {
				metaBuf = appendJSONMetaPlaceholder(metaBuf, body, valStart, valEnd, jsonNonEmptyStringPlaceholder)
			}
		case strings.EqualFold(key, "tool_choice"):
			toolChoiceSpan = val
			metaBuf = appendMetaKeyPrefix(metaBuf, body, rawKeyStart, rawKeyEnd, hasMetaMember)
			hasMetaMember = true
			metaBuf = append(metaBuf, "null"...)
		}
		return true
	})
	if walkErr != nil {
		return 0, walkErr
	}
	if !walkOK {
		return EstimateOpenAIResponsesInputTokens(body)
	}
	metaBuf = append(metaBuf, '}')

	var metaReq openAIInputTokensCountRequest
	if err := json.Unmarshal(metaBuf, &metaReq); err != nil {
		return 0, fmt.Errorf("parse responses input_tokens request: %w", err)
	}
	if strings.TrimSpace(metaReq.Model) == "" {
		return 0, fmt.Errorf("parse responses input_tokens request: model is required")
	}
	codec, err := openAIInputTokensCodecForModel(metaReq.Model)
	if err != nil {
		return 0, err
	}
	est := newBoundedTokenEstimator(codec)
	est.metaScratch = metaScratch

	if !instructionsSpan.isNull(body) {
		okStr, strErr := est.observeJSONString(body, instructionsSpan.start, instructionsSpan.end, true)
		if strErr != nil {
			return 0, strErr
		}
		if !okStr {
			return 0, fmt.Errorf("parse responses input_tokens request: invalid instructions")
		}
	}

	if !inputSpan.isNull(body) {
		switch body[inputSpan.start] {
		case '"':
			okStr, strErr := est.observeJSONString(body, inputSpan.start, inputSpan.end, false)
			if strErr != nil {
				return 0, strErr
			}
			if !okStr {
				return 0, fmt.Errorf("parse responses input_tokens request: invalid input string")
			}
		case '[':
			savedSampledBytes := est.sampledTextBytes
			savedTotalBytes := est.totalTextBytes
			savedSampledTokens := est.sampledTokens
			savedOverhead := est.overheadTokens

			fallbackRaw := false
			var itemErr error
			arrOK := jsonForEachElementSpan(body, inputSpan.start, inputSpan.end, func(elemStart, elemEnd int) bool {
				est.overheadTokens += openAIResponsesInputItemTokenOverhead
				if (jsonSpan{elemStart, elemEnd}).isNull(body) {
					return true
				}
				if body[elemStart] != '{' {
					fallbackRaw = true
					return false
				}
				if err := observeResponsesInputItemSpan(est, body, elemStart, elemEnd); err != nil {
					if errors.Is(err, errResponsesInputRawFallback) {
						fallbackRaw = true
						return false
					}
					itemErr = err
					return false
				}
				return true
			})
			if itemErr != nil {
				return 0, itemErr
			}
			if fallbackRaw || !arrOK {
				est.sampledTextBytes = savedSampledBytes
				est.totalTextBytes = savedTotalBytes
				est.sampledTokens = savedSampledTokens
				est.overheadTokens = savedOverhead
				if err := est.observeRawJSONSpan(body, inputSpan.start, inputSpan.end); err != nil {
					return 0, err
				}
			}
		default:
			if err := est.observeRawJSONSpan(body, inputSpan.start, inputSpan.end); err != nil {
				return 0, err
			}
		}
	}

	if !toolsSpan.isNull(body) {
		var toolErr error
		toolsOK := jsonForEachElementSpan(body, toolsSpan.start, toolsSpan.end, func(elemStart, elemEnd int) bool {
			if err := observeResponsesToolSpan(est, body, elemStart, elemEnd, 0); err != nil {
				toolErr = err
				return false
			}
			return true
		})
		if toolErr != nil {
			return 0, toolErr
		}
		if !toolsOK {
			return 0, fmt.Errorf("parse responses input_tokens request: invalid tools array")
		}
	}

	if toolChoiceSpan.valid() {
		if err := est.observeRawJSONSpan(body, toolChoiceSpan.start, toolChoiceSpan.end); err != nil {
			return 0, err
		}
	}

	return est.result(), nil
}

func appendFilteredAnthropicCacheControlJSON(dst, body []byte, objStart, objEnd, limit int) ([]byte, bool) {
	if objEnd-objStart <= boundedMetaInlineLimit {
		if len(dst)+(objEnd-objStart) > limit {
			return dst, false
		}
		return append(dst, body[objStart:objEnd]...), true
	}
	dst = append(dst, '{')
	hasMember := false
	fit := true
	ok := forEachJSONMemberDecoded(body, objStart, objEnd, func(key string, rawKeyStart, rawKeyEnd, valStart, valEnd int) bool {
		if _, known := knownAnthropicCacheControlField(key); !known {
			return true
		}
		valLen := valEnd - valStart
		if valLen > boundedMetaInlineLimit || len(dst)+(rawKeyEnd-rawKeyStart)+valLen+3 > limit {
			fit = false
			return false
		}
		dst = appendMetaKeyPrefix(dst, body, rawKeyStart, rawKeyEnd, hasMember)
		hasMember = true
		dst = append(dst, body[valStart:valEnd]...)
		return true
	})
	if !ok || !fit || len(dst)+1 > limit {
		return dst, false
	}
	return append(dst, '}'), true
}

func hasNonEmptyJSONArray(body []byte, arrStart, arrEnd int) bool {
	if arrEnd-arrStart <= 2 || body[arrStart] != '[' || body[arrEnd-1] != ']' {
		return false
	}
	return skipJSONWhitespace(body[:arrEnd], arrStart+1) < arrEnd-1
}

func appendFilteredResponsesToolJSON(dst, body []byte, elemStart, elemEnd, limit, depth int) ([]byte, bool) {
	if depth > maxBoundedResponsesToolDepth {
		return dst, false
	}
	if (jsonSpan{elemStart, elemEnd}).isNull(body) || body[elemStart] == '"' {
		if elemEnd-elemStart > boundedMetaInlineLimit || len(dst)+(elemEnd-elemStart) > limit {
			return dst, false
		}
		return append(dst, body[elemStart:elemEnd]...), true
	}
	if body[elemStart] != '{' {
		return dst, false
	}
	if elemEnd-elemStart <= boundedMetaInlineLimit {
		if len(dst)+(elemEnd-elemStart) > limit {
			return dst, false
		}
		return append(dst, body[elemStart:elemEnd]...), true
	}
	dst = append(dst, '{')
	hasMember := false
	fit := true
	ok := forEachJSONMemberDecoded(body, elemStart, elemEnd, func(key string, rawKeyStart, rawKeyEnd, valStart, valEnd int) bool {
		isRaw, known := knownResponsesToolField(key)
		if !known {
			return true
		}
		keyLen := rawKeyEnd - rawKeyStart
		valLen := valEnd - valStart
		if len(dst)+keyLen+3 > limit {
			fit = false
			return false
		}
		dst = appendMetaKeyPrefix(dst, body, rawKeyStart, rawKeyEnd, hasMember)
		hasMember = true
		switch {
		case strings.EqualFold(key, "cache_control") && body[valStart] == '{':
			var okCC bool
			dst, okCC = appendFilteredAnthropicCacheControlJSON(dst, body, valStart, valEnd, limit)
			if !okCC {
				fit = false
				return false
			}
		case (strings.EqualFold(key, "tools") || strings.EqualFold(key, "children")) && body[valStart] == '[':
			dst = append(dst, '[')
			first := true
			arrOK := jsonForEachElementSpan(body, valStart, valEnd, func(ns, ne int) bool {
				if !first {
					if len(dst)+1 > limit {
						fit = false
						return false
					}
					dst = append(dst, ',')
				}
				first = false
				var okChild bool
				dst, okChild = appendFilteredResponsesToolJSON(dst, body, ns, ne, limit, depth+1)
				if !okChild {
					fit = false
					return false
				}
				return true
			})
			if !arrOK || !fit || len(dst)+1 > limit {
				fit = false
				return false
			}
			dst = append(dst, ']')
		case strings.EqualFold(key, "allowed_x_handles") || strings.EqualFold(key, "excluded_x_handles"):
			if body[valStart] == '[' && !hasNonEmptyJSONArray(body, valStart, valEnd) {
				dst = append(dst, "[]"...)
			} else {
				if valLen > boundedToolsInlineLimit || len(dst)+valLen+1 > limit {
					fit = false
					return false
				}
				dst = append(dst, body[valStart:valEnd]...)
			}
		case isRaw:
			if valLen > boundedToolsInlineLimit || len(dst)+valLen+1 > limit {
				fit = false
				return false
			}
			dst = append(dst, body[valStart:valEnd]...)
		default:
			if valLen > boundedMetaInlineLimit || len(dst)+valLen+1 > limit {
				fit = false
				return false
			}
			dst = append(dst, body[valStart:valEnd]...)
		}
		return true
	})
	if !ok || !fit || len(dst)+1 > limit {
		return dst, false
	}
	return append(dst, '}'), true
}

func observeAnthropicCacheControlSpan(est *boundedTokenEstimator, body []byte, objStart, objEnd int) error {
	if objEnd-objStart <= boundedMetaInlineLimit {
		var cc apicompat.AnthropicCacheControl
		if err := json.Unmarshal(body[objStart:objEnd], &cc); err != nil {
			return err
		}
		raw, err := marshalOpenAIUpstreamJSON(cc)
		if err != nil {
			return err
		}
		return est.observePlainBytes(raw)
	}
	var typeSpan, ttlSpan jsonSpan
	if !forEachJSONMemberDecoded(body, objStart, objEnd, func(key string, _, _, vs, ve int) bool {
		sp := jsonSpan{vs, ve}
		if !sp.isNull(body) && body[sp.start] == '"' {
			switch {
			case strings.EqualFold(key, "type"):
				typeSpan = sp
			case strings.EqualFold(key, "ttl"):
				ttlSpan = sp
			}
		}
		return true
	}) {
		return fmt.Errorf("invalid cache_control object")
	}
	if err := est.observePlainBytes([]byte(`{"type":`)); err != nil {
		return err
	}
	if typeSpan.isNull(body) {
		if err := est.observePlainBytes([]byte(`""`)); err != nil {
			return err
		}
	} else {
		if err := est.observePlainBytes([]byte(`"`)); err != nil {
			return err
		}
		if okStr, err := est.observeJSONEscapedStringContent(body, typeSpan.start, typeSpan.end, false); err != nil {
			return err
		} else if !okStr {
			return fmt.Errorf("invalid cache_control type")
		}
		if err := est.observePlainBytes([]byte(`"`)); err != nil {
			return err
		}
	}
	if !ttlSpan.isNull(body) && ttlSpan.end-ttlSpan.start > 2 {
		if err := est.observePlainBytes([]byte(`,"ttl":"`)); err != nil {
			return err
		}
		if okStr, err := est.observeJSONEscapedStringContent(body, ttlSpan.start, ttlSpan.end, false); err != nil {
			return err
		} else if !okStr {
			return fmt.Errorf("invalid cache_control ttl")
		}
		if err := est.observePlainBytes([]byte(`"`)); err != nil {
			return err
		}
	}
	return est.observePlainBytes([]byte(`}`))
}

func observeResponsesToolSpan(est *boundedTokenEstimator, body []byte, elemStart, elemEnd, depth int) error {
	if depth > maxBoundedResponsesToolDepth {
		return nil
	}
	if (jsonSpan{elemStart, elemEnd}).isNull(body) {
		raw, err := marshalOpenAIUpstreamJSON(apicompat.ResponsesTool{Type: "custom"})
		if err != nil {
			return err
		}
		return est.observePlainBytes(raw)
	}
	if body[elemStart] == '"' {
		if elemEnd-elemStart <= boundedMetaInlineLimit {
			var tool apicompat.ResponsesTool
			if err := json.Unmarshal(body[elemStart:elemEnd], &tool); err != nil {
				return err
			}
			raw, err := marshalOpenAIUpstreamJSON(tool)
			if err != nil {
				return err
			}
			return est.observePlainBytes(raw)
		}
		if err := est.observePlainBytes([]byte(`{"type":"custom","name":"`)); err != nil {
			return err
		}
		if okStr, err := est.observeJSONEscapedStringContent(body, elemStart, elemEnd, false); err != nil {
			return err
		} else if !okStr {
			return fmt.Errorf("parse responses input_tokens request: invalid tool string")
		}
		return est.observePlainBytes([]byte(`"}`))
	}
	if body[elemStart] != '{' {
		return fmt.Errorf("parse responses input_tokens request: invalid responses tool")
	}
	if elemEnd-elemStart <= boundedMetaInlineLimit {
		var tool apicompat.ResponsesTool
		if err := json.Unmarshal(body[elemStart:elemEnd], &tool); err != nil {
			return err
		}
		raw, err := marshalOpenAIUpstreamJSON(tool)
		if err != nil {
			return err
		}
		return est.observePlainBytes(raw)
	}
	filtered, ok := appendFilteredResponsesToolJSON(est.metaScratch[:0], body, elemStart, elemEnd, boundedToolsInlineLimit, depth)
	est.metaScratch = filtered[:0]
	if ok {
		var tool apicompat.ResponsesTool
		if err := json.Unmarshal(filtered, &tool); err != nil {
			return err
		}
		raw, err := marshalOpenAIUpstreamJSON(tool)
		if err != nil {
			return err
		}
		return est.observePlainBytes(raw)
	}
	return observeResponsesToolSpanStreaming(est, body, elemStart, elemEnd, depth)
}

func observeResponsesToolSpanStreaming(est *boundedTokenEstimator, body []byte, elemStart, elemEnd, depth int) error {
	var ccSpan, typeSpan, nameSpan, descSpan, paramsSpan, strictSpan jsonSpan
	var toolsSpan, childrenSpan, allowedXSpan, excludedXSpan jsonSpan
	var fromDateSpan, toDateSpan, imgUnderstandSpan, vidUnderstandSpan jsonSpan

	if !forEachJSONMemberDecoded(body, elemStart, elemEnd, func(key string, _, _, vs, ve int) bool {
		sp := jsonSpan{vs, ve}
		switch {
		case strings.EqualFold(key, "cache_control"):
			ccSpan = sp
		case strings.EqualFold(key, "type"):
			if !sp.isNull(body) && body[sp.start] == '"' {
				typeSpan = sp
			}
		case strings.EqualFold(key, "name"):
			if !sp.isNull(body) && body[sp.start] == '"' {
				nameSpan = sp
			}
		case strings.EqualFold(key, "description"):
			if !sp.isNull(body) && body[sp.start] == '"' {
				descSpan = sp
			}
		case strings.EqualFold(key, "parameters"):
			paramsSpan = sp
		case strings.EqualFold(key, "strict"):
			strictSpan = sp
		case strings.EqualFold(key, "tools"):
			toolsSpan = sp
		case strings.EqualFold(key, "children"):
			childrenSpan = sp
		case strings.EqualFold(key, "allowed_x_handles"):
			allowedXSpan = sp
		case strings.EqualFold(key, "excluded_x_handles"):
			excludedXSpan = sp
		case strings.EqualFold(key, "from_date"):
			if !sp.isNull(body) && body[sp.start] == '"' {
				fromDateSpan = sp
			}
		case strings.EqualFold(key, "to_date"):
			if !sp.isNull(body) && body[sp.start] == '"' {
				toDateSpan = sp
			}
		case strings.EqualFold(key, "enable_image_understanding"):
			imgUnderstandSpan = sp
		case strings.EqualFold(key, "enable_video_understanding"):
			vidUnderstandSpan = sp
		}
		return true
	}) {
		return fmt.Errorf("parse responses input_tokens request: invalid responses tool")
	}

	if err := est.observePlainBytes([]byte(`{`)); err != nil {
		return err
	}
	needComma := false
	if !ccSpan.isNull(body) && body[ccSpan.start] == '{' {
		if err := est.observePlainBytes([]byte(`"cache_control":`)); err != nil {
			return err
		}
		if err := observeAnthropicCacheControlSpan(est, body, ccSpan.start, ccSpan.end); err != nil {
			return err
		}
		needComma = true
	}
	if needComma {
		if err := est.observePlainBytes([]byte(`,"type":`)); err != nil {
			return err
		}
	} else {
		if err := est.observePlainBytes([]byte(`"type":`)); err != nil {
			return err
		}
	}
	if typeSpan.isNull(body) {
		if err := est.observePlainBytes([]byte(`""`)); err != nil {
			return err
		}
	} else {
		if err := est.observePlainBytes([]byte(`"`)); err != nil {
			return err
		}
		if okStr, err := est.observeJSONEscapedStringContent(body, typeSpan.start, typeSpan.end, false); err != nil {
			return err
		} else if !okStr {
			return fmt.Errorf("parse responses input_tokens request: invalid tool type")
		}
		if err := est.observePlainBytes([]byte(`"`)); err != nil {
			return err
		}
	}

	observeOptString := func(field string, sp jsonSpan) error {
		if sp.isNull(body) || sp.end-sp.start <= 2 {
			return nil
		}
		if err := est.observePlainBytes([]byte(`,"` + field + `":"`)); err != nil {
			return err
		}
		if okStr, err := est.observeJSONEscapedStringContent(body, sp.start, sp.end, false); err != nil {
			return err
		} else if !okStr {
			return fmt.Errorf("parse responses input_tokens request: invalid tool %s", field)
		}
		return est.observePlainBytes([]byte(`"`))
	}
	observeOptBool := func(field string, sp jsonSpan) error {
		if sp.isNull(body) {
			return nil
		}
		if err := est.observePlainBytes([]byte(`,"` + field + `":`)); err != nil {
			return err
		}
		return est.observePlainBytes(body[sp.start:sp.end])
	}
	observeOptToolArray := func(field string, sp jsonSpan) error {
		if depth >= maxBoundedResponsesToolDepth || sp.isNull(body) || body[sp.start] != '[' {
			return nil
		}
		hasElem := false
		var childErr error
		ok := jsonForEachElementSpan(body, sp.start, sp.end, func(ns, ne int) bool {
			if !hasElem {
				if err := est.observePlainBytes([]byte(`,"` + field + `":[`)); err != nil {
					childErr = err
					return false
				}
				hasElem = true
			} else {
				if err := est.observePlainBytes([]byte(`,`)); err != nil {
					childErr = err
					return false
				}
			}
			if err := observeResponsesToolSpan(est, body, ns, ne, depth+1); err != nil {
				childErr = err
				return false
			}
			return true
		})
		if childErr != nil {
			return childErr
		}
		if !ok {
			return fmt.Errorf("parse responses input_tokens request: invalid nested tools array")
		}
		if hasElem {
			return est.observePlainBytes([]byte(`]`))
		}
		return nil
	}

	if err := observeOptString("name", nameSpan); err != nil {
		return err
	}
	if err := observeOptString("description", descSpan); err != nil {
		return err
	}
	if !paramsSpan.isNull(body) {
		if err := est.observePlainBytes([]byte(`,"parameters":`)); err != nil {
			return err
		}
		if err := est.observeRawJSONSpan(body, paramsSpan.start, paramsSpan.end); err != nil {
			return err
		}
	}
	if err := observeOptBool("strict", strictSpan); err != nil {
		return err
	}
	if err := observeOptToolArray("tools", toolsSpan); err != nil {
		return err
	}
	if err := observeOptToolArray("children", childrenSpan); err != nil {
		return err
	}
	if !allowedXSpan.isNull(body) && hasNonEmptyJSONArray(body, allowedXSpan.start, allowedXSpan.end) {
		if err := est.observeStringArrayFieldNormalized(body, "allowed_x_handles", allowedXSpan.start, allowedXSpan.end, false); err != nil {
			return err
		}
	}
	if !excludedXSpan.isNull(body) && hasNonEmptyJSONArray(body, excludedXSpan.start, excludedXSpan.end) {
		if err := est.observeStringArrayFieldNormalized(body, "excluded_x_handles", excludedXSpan.start, excludedXSpan.end, false); err != nil {
			return err
		}
	}
	if err := observeOptString("from_date", fromDateSpan); err != nil {
		return err
	}
	if err := observeOptString("to_date", toDateSpan); err != nil {
		return err
	}
	if err := observeOptBool("enable_image_understanding", imgUnderstandSpan); err != nil {
		return err
	}
	if err := observeOptBool("enable_video_understanding", vidUnderstandSpan); err != nil {
		return err
	}
	return est.observePlainBytes([]byte(`}`))
}

func observeResponsesInputItemSpan(est *boundedTokenEstimator, body []byte, objStart, objEnd int) error {
	var err error
	est.metaScratch, err = validateResponsesInputItemSpan(body, objStart, objEnd, est.metaScratch)
	if err != nil {
		return errResponsesInputRawFallback
	}

	var roleSpan, typeSpan, nameSpan, argsSpan, inputSpan, outputSpan, callIDSpan, idSpan, contentSpan jsonSpan
	walkOK := forEachJSONMemberDecoded(body, objStart, objEnd, func(key string, _, _, valStart, valEnd int) bool {
		sp := jsonSpan{valStart, valEnd}
		switch {
		case strings.EqualFold(key, "role"):
			if !sp.isNull(body) {
				roleSpan = sp
			}
		case strings.EqualFold(key, "type"):
			if !sp.isNull(body) {
				typeSpan = sp
			}
		case strings.EqualFold(key, "name"):
			if !sp.isNull(body) {
				nameSpan = sp
			}
		case strings.EqualFold(key, "arguments"):
			if !sp.isNull(body) {
				argsSpan = sp
			}
		case strings.EqualFold(key, "input"):
			if !sp.isNull(body) {
				inputSpan = sp
			}
		case strings.EqualFold(key, "output"):
			outputSpan = sp
		case strings.EqualFold(key, "call_id"):
			if !sp.isNull(body) {
				callIDSpan = sp
			}
		case strings.EqualFold(key, "id"):
			if !sp.isNull(body) {
				idSpan = sp
			}
		case strings.EqualFold(key, "content"):
			contentSpan = sp
		}
		return true
	})
	if !walkOK {
		return errResponsesInputRawFallback
	}

	for _, sp := range []jsonSpan{roleSpan, nameSpan, argsSpan, inputSpan, callIDSpan, idSpan} {
		if !sp.isNull(body) && body[sp.start] == '"' {
			if okStr, err := est.observeJSONString(body, sp.start, sp.end, true); err != nil {
				return err
			} else if !okStr {
				return errResponsesInputRawFallback
			}
		}
	}

	if !typeSpan.isNull(body) && body[typeSpan.start] == '"' {
		if typeSpan.end-typeSpan.start <= boundedMetaInlineLimit {
			itemType := extractJSONMemberString(jsonView(body[typeSpan.start:typeSpan.end]), false)
			if itemType != "" && itemType != "message" {
				if trimmed := strings.TrimSpace(itemType); trimmed != "" {
					if err := est.observePlainBytes([]byte(trimmed)); err != nil {
						return err
					}
				}
			}
		} else {
			if okStr, err := est.observeJSONString(body, typeSpan.start, typeSpan.end, true); err != nil {
				return err
			} else if !okStr {
				return errResponsesInputRawFallback
			}
		}
	}

	if !outputSpan.isNull(body) {
		if body[outputSpan.start] == '"' {
			if okStr, err := est.observeJSONString(body, outputSpan.start, outputSpan.end, true); err != nil {
				return err
			} else if !okStr {
				return errResponsesInputRawFallback
			}
		} else {
			if err := est.observeRawBytesSpan(body, outputSpan.start, outputSpan.end); err != nil {
				return err
			}
		}
	}

	if contentSpan.isNull(body) {
		return nil
	}
	switch body[contentSpan.start] {
	case '"':
		if okStr, err := est.observeJSONString(body, contentSpan.start, contentSpan.end, true); err != nil {
			return err
		} else if !okStr {
			return errResponsesInputRawFallback
		}
	case '[':
		savedSampledBytes := est.sampledTextBytes
		savedTotalBytes := est.totalTextBytes
		savedSampledTokens := est.sampledTokens
		savedOverhead := est.overheadTokens

		fallbackRaw := false
		var partErr error
		partsOK := jsonForEachElementSpan(body, contentSpan.start, contentSpan.end, func(partStart, partEnd int) bool {
			est.overheadTokens += openAIResponsesContentPartOverhead
			if (jsonSpan{partStart, partEnd}).isNull(body) {
				return true
			}
			if body[partStart] != '{' {
				fallbackRaw = true
				return false
			}
			est.metaScratch, partErr = validateResponsesContentPartSpan(body, partStart, partEnd, est.metaScratch)
			if partErr != nil {
				fallbackRaw = true
				partErr = nil
				return false
			}
			if err := observeResponsesContentPartSpan(est, body, partStart, partEnd); err != nil {
				partErr = err
				return false
			}
			return true
		})
		if partErr != nil {
			return partErr
		}
		if fallbackRaw || !partsOK {
			est.sampledTextBytes = savedSampledBytes
			est.totalTextBytes = savedTotalBytes
			est.sampledTokens = savedSampledTokens
			est.overheadTokens = savedOverhead
			return est.observeRawJSONSpan(body, contentSpan.start, contentSpan.end)
		}
	default:
		return est.observeRawJSONSpan(body, contentSpan.start, contentSpan.end)
	}
	return nil
}

func observeResponsesContentPartSpan(est *boundedTokenEstimator, body []byte, objStart, objEnd int) error {
	var typeSpan, textSpan, imageURLSpan jsonSpan
	walkOK := forEachJSONMemberDecoded(body, objStart, objEnd, func(key string, _, _, valStart, valEnd int) bool {
		sp := jsonSpan{valStart, valEnd}
		switch {
		case strings.EqualFold(key, "type"):
			if !sp.isNull(body) {
				typeSpan = sp
			}
		case strings.EqualFold(key, "text"):
			if !sp.isNull(body) {
				textSpan = sp
			}
		case strings.EqualFold(key, "image_url"):
			if !sp.isNull(body) {
				imageURLSpan = sp
			}
		}
		return true
	})
	if !walkOK {
		return est.observeRawJSONSpan(body, objStart, objEnd)
	}
	if typeSpan.isNull(body) || body[typeSpan.start] != '"' {
		return nil
	}
	if typeSpan.end-typeSpan.start > boundedMetaInlineLimit {
		if okStr, err := est.observeJSONString(body, typeSpan.start, typeSpan.end, true); err != nil {
			return err
		} else if !okStr {
			return est.observeRawJSONSpan(body, objStart, objEnd)
		}
		return nil
	}
	partType := extractJSONMemberString(jsonView(body[typeSpan.start:typeSpan.end]), false)
	switch partType {
	case "input_text", "output_text", "text":
		if !textSpan.isNull(body) && body[textSpan.start] == '"' {
			if okStr, err := est.observeJSONString(body, textSpan.start, textSpan.end, true); err != nil {
				return err
			} else if !okStr {
				return fmt.Errorf("parse responses input_tokens request: invalid content part text")
			}
		}
	case "input_image":
		if !imageURLSpan.isNull(body) {
			if body[imageURLSpan.start] == '"' {
				raw := jsonView(body[imageURLSpan.start:imageURLSpan.end])
				// Short-circuit large data URIs to avoid decoding past the header.
				if strings.IndexByte(raw, '\\') < 0 && len(raw) >= 2 {
					imgText := strings.TrimSpace(estimateOpenAIInputImageText(raw[1 : len(raw)-1]))
					if imgText != "" {
						return est.observePlainString(imgText)
					}
					return nil
				}
				if len(raw) <= boundedMetaInlineLimit {
					urlStr := extractJSONMemberString(raw, false)
					if imgText := strings.TrimSpace(estimateOpenAIInputImageText(urlStr)); imgText != "" {
						return est.observePlainString(imgText)
					}
					return nil
				}
				remain := int(est.sampleBudget - est.sampledTextBytes)
				if remain < 0 {
					remain = 0
				}
				header, totalHeaderBytes, okHeader := extractBoundedDataURIHeader(est.scratch[:0], body[imageURLSpan.start+1:imageURLSpan.end-1], remain)
				if okHeader {
					err := est.observeSampleAndTotal(header, totalHeaderBytes)
					est.scratch = header[:0]
					return err
				}
				est.scratch = header[:0]
				if okStr, err := est.observeJSONString(body, imageURLSpan.start, imageURLSpan.end, true); err != nil {
					return err
				} else if !okStr {
					return est.observeRawJSONSpan(body, imageURLSpan.start, imageURLSpan.end)
				}
				return nil
			}
			return est.observeRawJSONSpan(body, imageURLSpan.start, imageURLSpan.end)
		}
	default:
		trimmedType := strings.TrimSpace(partType)
		if trimmedType != "" {
			return est.observePlainBytes([]byte(trimmedType))
		}
	}
	return nil
}

func isDataSchemePrefix5(b [5]byte) bool {
	return (b[0] == 'd' || b[0] == 'D') &&
		(b[1] == 'a' || b[1] == 'A') &&
		(b[2] == 't' || b[2] == 'T') &&
		(b[3] == 'a' || b[3] == 'A') &&
		b[4] == ':'
}

func extractBoundedDataURIHeader(dst, inner []byte, maxSampleBytes int) ([]byte, int64, bool) {
	var prefix5 [5]byte
	prefixLen := 0
	var total int64
	var trailingSpace int64
	lastNonSpaceDstLen := len(dst)
	leading := true
	capturing := len(dst) < maxSampleBytes
	finish := func() ([]byte, int64, bool) {
		total -= trailingSpace
		if total < 0 {
			total = 0
		}
		if lastNonSpaceDstLen < len(dst) {
			dst = dst[:lastNonSpaceDstLen]
		}
		return dst, total, true
	}
	i := 0
	n := len(inner)
	for i < n {
		if inner[i] != '\\' {
			k := bytes.IndexByte(inner[i:], '\\')
			var chunk []byte
			if k < 0 {
				chunk = inner[i:]
				i = n
			} else {
				chunk = inner[i : i+k]
				i += k
			}
			if leading {
				chunk = trimLeftJSONOrUnicodeSpace(chunk)
				if len(chunk) > 0 {
					leading = false
				}
			}
			if len(chunk) == 0 {
				continue
			}
			if prefixLen < 5 {
				j := 0
				for j < len(chunk) && prefixLen < 5 {
					c := chunk[j]
					if c == ',' {
						return dst, total, false
					}
					prefix5[prefixLen] = c
					prefixLen++
					if capturing {
						if len(dst) < maxSampleBytes {
							dst = append(dst, c)
						}
						if len(dst) >= maxSampleBytes {
							capturing = false
						}
					}
					total++
					isSpace := c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
					if isSpace {
						trailingSpace++
					} else {
						trailingSpace = 0
						lastNonSpaceDstLen = len(dst)
					}
					j++
				}
				if prefixLen == 5 && !isDataSchemePrefix5(prefix5) {
					return dst, total, false
				}
				chunk = chunk[j:]
				if len(chunk) == 0 {
					continue
				}
			}
			part := chunk
			foundComma := false
			if commaIdx := bytes.IndexByte(chunk, ','); commaIdx >= 0 {
				part = chunk[:commaIdx]
				foundComma = true
			}
			if len(part) > 0 {
				trail := countTrailingSpaceBytes(part)
				if trail == len(part) {
					if capturing && !foundComma {
						need := maxSampleBytes - len(dst)
						if len(part) <= need {
							dst = append(dst, part...)
						} else {
							take := len(trimTrailingIncompleteUTF8(part[:need]))
							if take > 0 {
								dst = append(dst, part[:take]...)
							}
							capturing = false
						}
						if len(dst) >= maxSampleBytes {
							capturing = false
						}
					}
					total += int64(len(part))
					trailingSpace += int64(trail)
				} else {
					nonSpaceLen := len(part) - trail
					if capturing {
						prevDstLen := len(dst)
						appended := 0
						need := maxSampleBytes - prevDstLen
						if len(part) <= need {
							dst = append(dst, part...)
							appended = len(part)
						} else {
							take := len(trimTrailingIncompleteUTF8(part[:need]))
							if take > 0 {
								dst = append(dst, part[:take]...)
								appended = take
							}
							capturing = false
						}
						if len(dst) >= maxSampleBytes {
							capturing = false
						}
						if appended <= nonSpaceLen {
							lastNonSpaceDstLen = prevDstLen + appended
						} else {
							lastNonSpaceDstLen = prevDstLen + nonSpaceLen
						}
					} else {
						lastNonSpaceDstLen = len(dst)
					}
					total += int64(len(part))
					trailingSpace = int64(trail)
				}
			}
			if foundComma {
				return finish()
			}
			continue
		}
		if i+1 >= n {
			return dst, total, false
		}
		esc := inner[i+1]
		switch esc {
		case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			var ch byte
			switch esc {
			case '"', '\\', '/':
				ch = esc
			case 'b':
				ch = '\b'
			case 'f':
				ch = '\f'
			case 'n':
				ch = '\n'
			case 'r':
				ch = '\r'
			case 't':
				ch = '\t'
			}
			i += 2
			isSpace := ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '\f'
			if leading {
				if isSpace {
					continue
				}
				leading = false
			}
			if ch == ',' {
				if prefixLen == 5 && isDataSchemePrefix5(prefix5) {
					return finish()
				}
				return dst, total, false
			}
			if prefixLen < 5 {
				prefix5[prefixLen] = ch
				prefixLen++
				if prefixLen == 5 && !isDataSchemePrefix5(prefix5) {
					return dst, total, false
				}
			}
			if capturing {
				if len(dst) < maxSampleBytes {
					dst = append(dst, ch)
				}
				if len(dst) >= maxSampleBytes {
					capturing = false
				}
			}
			total++
			if isSpace {
				trailingSpace++
			} else {
				trailingSpace = 0
				lastNonSpaceDstLen = len(dst)
			}
		case 'u':
			if i+6 > n {
				return dst, total, false
			}
			r1, ok := parseHex4(inner[i+2 : i+6])
			if !ok {
				return dst, total, false
			}
			i += 6
			r := rune(r1)
			if r1 >= 0xD800 && r1 <= 0xDBFF && i+6 <= n && inner[i] == '\\' && inner[i+1] == 'u' {
				if r2, ok2 := parseHex4(inner[i+2 : i+6]); ok2 && r2 >= 0xDC00 && r2 <= 0xDFFF {
					r = utf16.DecodeRune(rune(r1), rune(r2))
					i += 6
				}
			}
			if !utf8.ValidRune(r) {
				r = utf8.RuneError
			}
			isSpace := unicode.IsSpace(r)
			if leading {
				if isSpace {
					continue
				}
				leading = false
			}
			if r == ',' {
				if prefixLen == 5 && isDataSchemePrefix5(prefix5) {
					return finish()
				}
				return dst, total, false
			}
			var rbuf [utf8.UTFMax]byte
			rn := utf8.EncodeRune(rbuf[:], r)
			if prefixLen < 5 {
				for k := 0; k < rn && prefixLen < 5; k++ {
					prefix5[prefixLen] = rbuf[k]
					prefixLen++
				}
				if prefixLen == 5 && !isDataSchemePrefix5(prefix5) {
					return dst, total, false
				}
			}
			if capturing {
				if len(dst)+rn <= maxSampleBytes {
					dst = append(dst, rbuf[:rn]...)
				} else {
					capturing = false
				}
				if len(dst) >= maxSampleBytes {
					capturing = false
				}
			}
			total += int64(rn)
			if isSpace {
				trailingSpace += int64(rn)
			} else {
				trailingSpace = 0
				lastNonSpaceDstLen = len(dst)
			}
		default:
			return dst, total, false
		}
	}
	return dst, total, false
}

func validateAnthropicSupersededMessagesArray(body []byte, arrStart, arrEnd int, scratch []byte) ([]byte, error) {
	var msgErr error
	ok := jsonForEachElementSpan(body, arrStart, arrEnd, func(es, ee int) bool {
		if (jsonSpan{es, ee}).isNull(body) {
			return true
		}
		if body[es] != '{' {
			msgErr = fmt.Errorf("parse anthropic count_tokens request: invalid message")
			return false
		}
		scratch, msgErr = validateAnthropicMessageSpan(body, es, ee, scratch, false)
		return msgErr == nil
	})
	if msgErr != nil {
		return scratch, msgErr
	}
	if !ok {
		return scratch, fmt.Errorf("parse anthropic count_tokens request: invalid messages array")
	}
	return scratch, nil
}

var (
	anthropicRoleDeveloperBytes              = []byte("developer")
	anthropicRoleUserBytes                   = []byte("user")
	anthropicRoleAssistantBytes              = []byte("assistant")
	anthropicItemTypeReasoningBytes          = []byte("reasoning")
	anthropicItemTypeFunctionCallBytes       = []byte("function_call")
	anthropicItemTypeFunctionCallOutputBytes = []byte("function_call_output")
	anthropicDefaultImageURIPrefixBytes      = []byte("data:image/png;base64")
	anthropicEmptyJSONObjectBytes            = []byte("{}")
	anthropicEmptyToolResultBytes            = []byte("(empty)")
	anthropicTextJoinSepBytes                = []byte("\n\n")
)

type anthropicMessageEmitState struct {
	hasUserMessage       bool
	hasAssistantText     bool
	hasAssistantNonSpace bool
}

func estimateAnthropicInputTokensBounded(body []byte) (int, error) {
	if len(body) <= incompleteInputExactEstimateLimit {
		return EstimateAnthropicCountTokens(body)
	}
	objStart, objEnd, ok := trimJSONObjectBounds(body)
	if !ok || bytes.HasPrefix(body, utf8BOM) || !validJSON(body[objStart:objEnd]) {
		return 0, fmt.Errorf("parse anthropic count_tokens request: invalid JSON")
	}

	var systemSpan, messagesSpan, toolsSpan, toolChoiceSpan jsonSpan
	metaBuf := make([]byte, 0, 512)
	metaBuf = append(metaBuf, '{')
	hasMetaMember := false
	var metaScratch []byte
	var walkErr error

	walkOK := forEachJSONMemberDecoded(body, objStart, objEnd, func(key string, rawKeyStart, rawKeyEnd, valStart, valEnd int) bool {
		val := jsonSpan{valStart, valEnd}
		valLen := valEnd - valStart
		switch {
		case strings.EqualFold(key, "model"):
			metaBuf = appendMetaKeyPrefix(metaBuf, body, rawKeyStart, rawKeyEnd, hasMetaMember)
			hasMetaMember = true
			var err error
			metaBuf, err = appendBoundedModelValue(metaBuf, body, valStart, valEnd)
			if err != nil {
				walkErr = fmt.Errorf("parse anthropic count_tokens request: %w", err)
				return false
			}
		case strings.EqualFold(key, "system"):
			systemSpan = val
			metaBuf = appendMetaKeyPrefix(metaBuf, body, rawKeyStart, rawKeyEnd, hasMetaMember)
			hasMetaMember = true
			metaBuf = append(metaBuf, "null"...)
		case strings.EqualFold(key, "messages"):
			if messagesSpan.valid() && body[messagesSpan.start] == '[' && messagesSpan.end-messagesSpan.start > boundedMetaInlineLimit {
				var err error
				metaScratch, err = validateAnthropicSupersededMessagesArray(body, messagesSpan.start, messagesSpan.end, metaScratch)
				if err != nil {
					walkErr = err
					return false
				}
			}
			messagesSpan = val
			metaBuf = appendMetaKeyPrefix(metaBuf, body, rawKeyStart, rawKeyEnd, hasMetaMember)
			hasMetaMember = true
			if valLen <= boundedMetaInlineLimit {
				metaBuf = append(metaBuf, body[valStart:valEnd]...)
			} else {
				metaBuf = appendJSONMetaPlaceholder(metaBuf, body, valStart, valEnd, jsonNonEmptyStringPlaceholder)
			}
		case strings.EqualFold(key, "tools"):
			toolsSpan = val
			metaBuf = appendMetaKeyPrefix(metaBuf, body, rawKeyStart, rawKeyEnd, hasMetaMember)
			hasMetaMember = true
			if valLen <= boundedToolsInlineLimit {
				metaBuf = append(metaBuf, body[valStart:valEnd]...)
			} else if body[valStart] == '[' {
				var err error
				metaScratch, err = validateAnthropicToolsArraySpan(body, valStart, valEnd, metaScratch)
				if err != nil {
					walkErr = fmt.Errorf("parse anthropic count_tokens request: %w", err)
					return false
				}
				metaBuf = append(metaBuf, "[]"...)
			} else {
				metaBuf = appendJSONMetaPlaceholder(metaBuf, body, valStart, valEnd, jsonNonEmptyStringPlaceholder)
			}
		case strings.EqualFold(key, "tool_choice"):
			toolChoiceSpan = val
			metaBuf = appendMetaKeyPrefix(metaBuf, body, rawKeyStart, rawKeyEnd, hasMetaMember)
			hasMetaMember = true
			metaBuf = append(metaBuf, "null"...)
		case strings.EqualFold(key, "max_tokens"),
			strings.EqualFold(key, "stream"),
			strings.EqualFold(key, "temperature"),
			strings.EqualFold(key, "top_p"):
			metaBuf = appendMetaKeyPrefix(metaBuf, body, rawKeyStart, rawKeyEnd, hasMetaMember)
			hasMetaMember = true
			if valLen <= boundedMetaInlineLimit {
				metaBuf = append(metaBuf, body[valStart:valEnd]...)
			} else {
				metaBuf = appendJSONMetaPlaceholder(metaBuf, body, valStart, valEnd, jsonNonEmptyStringPlaceholder)
			}
		case strings.EqualFold(key, "stop_sequences"):
			metaBuf = appendMetaKeyPrefix(metaBuf, body, rawKeyStart, rawKeyEnd, hasMetaMember)
			hasMetaMember = true
			if valLen <= boundedMetaInlineLimit {
				metaBuf = append(metaBuf, body[valStart:valEnd]...)
			} else if body[valStart] == '[' {
				if !validateStringArraySpan(body, valStart, valEnd) {
					metaBuf = append(metaBuf, "[0]"...)
				} else {
					metaBuf = append(metaBuf, "[]"...)
				}
			} else {
				metaBuf = appendJSONMetaPlaceholder(metaBuf, body, valStart, valEnd, jsonNonEmptyStringPlaceholder)
			}
		case strings.EqualFold(key, "cache_control"):
			metaBuf = appendMetaKeyPrefix(metaBuf, body, rawKeyStart, rawKeyEnd, hasMetaMember)
			hasMetaMember = true
			if valLen <= boundedMetaInlineLimit {
				metaBuf = append(metaBuf, body[valStart:valEnd]...)
			} else if body[valStart] == '{' {
				var err error
				metaScratch, err = validateAnthropicCacheControlSpan(body, valStart, valEnd, metaScratch)
				if err != nil {
					walkErr = fmt.Errorf("parse anthropic count_tokens request: %w", err)
					return false
				}
				metaBuf = append(metaBuf, "{}"...)
			} else {
				metaBuf = appendJSONMetaPlaceholder(metaBuf, body, valStart, valEnd, jsonNonEmptyStringPlaceholder)
			}
		case strings.EqualFold(key, "thinking"):
			metaBuf = appendMetaKeyPrefix(metaBuf, body, rawKeyStart, rawKeyEnd, hasMetaMember)
			hasMetaMember = true
			if valLen <= boundedMetaInlineLimit {
				metaBuf = append(metaBuf, body[valStart:valEnd]...)
			} else if body[valStart] == '{' {
				th, nextScratch, err := validateAnthropicThinkingSpan(body, valStart, valEnd, metaScratch)
				metaScratch = nextScratch
				if err != nil {
					walkErr = fmt.Errorf("parse anthropic count_tokens request: %w", err)
					return false
				}
				encoded, err := json.Marshal(th)
				if err != nil {
					walkErr = fmt.Errorf("parse anthropic count_tokens request: %w", err)
					return false
				}
				metaBuf = append(metaBuf, encoded...)
			} else {
				metaBuf = appendJSONMetaPlaceholder(metaBuf, body, valStart, valEnd, jsonNonEmptyStringPlaceholder)
			}
		case strings.EqualFold(key, "output_config"):
			metaBuf = appendMetaKeyPrefix(metaBuf, body, rawKeyStart, rawKeyEnd, hasMetaMember)
			hasMetaMember = true
			if valLen <= boundedMetaInlineLimit {
				metaBuf = append(metaBuf, body[valStart:valEnd]...)
			} else if body[valStart] == '{' {
				oc, nextScratch, err := validateAnthropicOutputConfigSpan(body, valStart, valEnd, metaScratch)
				metaScratch = nextScratch
				if err != nil {
					walkErr = fmt.Errorf("parse anthropic count_tokens request: %w", err)
					return false
				}
				encoded, err := json.Marshal(oc)
				if err != nil {
					walkErr = fmt.Errorf("parse anthropic count_tokens request: %w", err)
					return false
				}
				metaBuf = append(metaBuf, encoded...)
			} else {
				metaBuf = appendJSONMetaPlaceholder(metaBuf, body, valStart, valEnd, jsonNonEmptyStringPlaceholder)
			}
		case strings.EqualFold(key, "metadata"):
			metaBuf = appendMetaKeyPrefix(metaBuf, body, rawKeyStart, rawKeyEnd, hasMetaMember)
			hasMetaMember = true
			metaBuf = append(metaBuf, "null"...)
		}
		return true
	})
	if walkErr != nil {
		return 0, walkErr
	}
	if !walkOK {
		return EstimateAnthropicCountTokens(body)
	}
	metaBuf = append(metaBuf, '}')

	var metaReq apicompat.AnthropicRequest
	if err := json.Unmarshal(metaBuf, &metaReq); err != nil {
		return 0, fmt.Errorf("parse anthropic count_tokens request: %w", err)
	}
	if strings.TrimSpace(metaReq.Model) == "" {
		return 0, fmt.Errorf("parse anthropic count_tokens request: model is required")
	}
	if _, err := apicompat.AnthropicToResponses(&metaReq); err != nil {
		return 0, fmt.Errorf("convert anthropic request to responses: %w", err)
	}
	var tcType, tcName string
	var tcLargeNameSpan jsonSpan
	if !toolChoiceSpan.isNull(body) {
		var tcErr error
		tcType, tcName, tcLargeNameSpan, metaScratch, tcErr = validateAnthropicToolChoiceSpan(body, toolChoiceSpan.start, toolChoiceSpan.end, metaScratch)
		if tcErr != nil {
			return 0, fmt.Errorf("convert anthropic request to responses: convert tool_choice: %w", tcErr)
		}
	}

	codec, err := openAIInputTokensCodecForModel(metaReq.Model)
	if err != nil {
		return 0, err
	}
	est := newBoundedTokenEstimator(codec)
	est.metaScratch = metaScratch

	if !systemSpan.isNull(body) {
		switch body[systemSpan.start] {
		case '"':
			if systemSpan.end-systemSpan.start > 2 && !hasJSONStringPrefix(body, systemSpan.start, systemSpan.end, "x-anthropic-billing-header: ") {
				est.overheadTokens += openAIResponsesInputItemTokenOverhead + openAIResponsesContentPartOverhead
				if err := est.observePlainBytes(anthropicRoleDeveloperBytes); err != nil {
					return 0, err
				}
				okStr, strErr := est.observeJSONString(body, systemSpan.start, systemSpan.end, true)
				if strErr != nil {
					return 0, strErr
				}
				if !okStr {
					return 0, fmt.Errorf("parse anthropic count_tokens request: invalid system string")
				}
			}
		case '[':
			hasSystemMessage := false
			var sysErr error
			sysOK := jsonForEachElementSpan(body, systemSpan.start, systemSpan.end, func(elemStart, elemEnd int) bool {
				if (jsonSpan{elemStart, elemEnd}).isNull(body) {
					return true
				}
				if body[elemStart] != '{' {
					sysErr = fmt.Errorf("parse anthropic count_tokens request: invalid system block")
					return false
				}
				var typeSpan, textSpan jsonSpan
				if elemEnd-elemStart <= boundedMetaInlineLimit {
					var needsUnmarshal, walkOK bool
					typeSpan, textSpan, needsUnmarshal, walkOK = scanAnthropicContentBlockMembers(body, elemStart, elemEnd)
					if !walkOK || needsUnmarshal {
						var blk apicompat.AnthropicContentBlock
						if sysErr = json.Unmarshal(body[elemStart:elemEnd], &blk); sysErr != nil {
							return false
						}
						if blk.Type == "text" && blk.Text != "" && !strings.HasPrefix(blk.Text, "x-anthropic-billing-header: ") {
							if !hasSystemMessage {
								hasSystemMessage = true
								est.overheadTokens += openAIResponsesInputItemTokenOverhead
								if err := est.observePlainBytes(anthropicRoleDeveloperBytes); err != nil {
									sysErr = err
									return false
								}
							}
							est.overheadTokens += openAIResponsesContentPartOverhead
							if err := est.observePlainString(strings.TrimSpace(blk.Text)); err != nil {
								sysErr = err
								return false
							}
						}
						return true
					}
				} else {
					var parseErr error
					typeSpan, textSpan, est.metaScratch, parseErr = parseAnthropicLargeSystemBlock(body, elemStart, elemEnd, est.metaScratch)
					if parseErr != nil {
						sysErr = parseErr
						return false
					}
				}
				blockType := ""
				if !typeSpan.isNull(body) && body[typeSpan.start] == '"' && typeSpan.end-typeSpan.start <= boundedMetaInlineLimit {
					blockType = extractJSONMemberString(jsonView(body[typeSpan.start:typeSpan.end]), false)
				}
				if blockType == "text" && !textSpan.isNull(body) && body[textSpan.start] == '"' && textSpan.end-textSpan.start > 2 &&
					!hasJSONStringPrefix(body, textSpan.start, textSpan.end, "x-anthropic-billing-header: ") {
					if !hasSystemMessage {
						hasSystemMessage = true
						est.overheadTokens += openAIResponsesInputItemTokenOverhead
						if err := est.observePlainBytes(anthropicRoleDeveloperBytes); err != nil {
							sysErr = err
							return false
						}
					}
					est.overheadTokens += openAIResponsesContentPartOverhead
					okStr, strErr := est.observeJSONString(body, textSpan.start, textSpan.end, true)
					if strErr != nil {
						sysErr = strErr
						return false
					}
					if !okStr {
						sysErr = fmt.Errorf("parse anthropic count_tokens request: invalid system text string")
						return false
					}
				}
				return true
			})
			if sysErr != nil {
				return 0, sysErr
			}
			if !sysOK {
				return 0, fmt.Errorf("parse anthropic count_tokens request: invalid system array")
			}
		default:
			return 0, fmt.Errorf("parse anthropic count_tokens request: invalid system")
		}
	}

	if !messagesSpan.isNull(body) {
		if body[messagesSpan.start] != '[' {
			return 0, fmt.Errorf("parse anthropic count_tokens request: invalid messages")
		}
		var msgErr error
		msgsOK := jsonForEachElementSpan(body, messagesSpan.start, messagesSpan.end, func(elemStart, elemEnd int) bool {
			if (jsonSpan{elemStart, elemEnd}).isNull(body) || body[elemStart] != '{' {
				msgErr = fmt.Errorf("parse anthropic count_tokens request: invalid message")
				return false
			}
			est.metaScratch, msgErr = validateAnthropicMessageSpan(body, elemStart, elemEnd, est.metaScratch, true)
			if msgErr != nil {
				return false
			}
			var roleSpan, contentSpan jsonSpan
			if !forEachJSONMemberDecoded(body, elemStart, elemEnd, func(k string, _, _, vs, ve int) bool {
				sp := jsonSpan{vs, ve}
				switch {
				case strings.EqualFold(k, "role"):
					if !sp.isNull(body) {
						roleSpan = sp
					}
				case strings.EqualFold(k, "content"):
					contentSpan = sp
				}
				return true
			}) {
				msgErr = fmt.Errorf("parse anthropic count_tokens request: invalid message")
				return false
			}
			isAssistant := false
			if !roleSpan.isNull(body) && body[roleSpan.start] == '"' && roleSpan.end-roleSpan.start <= boundedMetaInlineLimit {
				isAssistant = extractJSONMemberString(jsonView(body[roleSpan.start:roleSpan.end]), false) == "assistant"
			}
			normalizedRole := anthropicRoleUserBytes
			if isAssistant {
				normalizedRole = anthropicRoleAssistantBytes
			}
			if !contentSpan.valid() {
				msgErr = fmt.Errorf("parse anthropic count_tokens request: message content is required")
				return false
			}
			if contentSpan.isNull(body) {
				est.overheadTokens += openAIResponsesInputItemTokenOverhead + openAIResponsesContentPartOverhead
				if err := est.observePlainBytes(normalizedRole); err != nil {
					msgErr = err
					return false
				}
			} else {
				switch body[contentSpan.start] {
				case '"':
					est.overheadTokens += openAIResponsesInputItemTokenOverhead + openAIResponsesContentPartOverhead
					if err := est.observePlainBytes(normalizedRole); err != nil {
						msgErr = err
						return false
					}
					okStr, strErr := est.observeJSONString(body, contentSpan.start, contentSpan.end, true)
					if strErr != nil {
						msgErr = strErr
						return false
					}
					if !okStr {
						msgErr = fmt.Errorf("parse anthropic count_tokens request: invalid message content string")
						return false
					}
				case '[':
					var msgState anthropicMessageEmitState
					contentOK := jsonForEachElementSpan(body, contentSpan.start, contentSpan.end, func(bs, be int) bool {
						if (jsonSpan{bs, be}).isNull(body) {
							return true
						}
						if body[bs] != '{' {
							msgErr = fmt.Errorf("parse anthropic count_tokens request: invalid message content block")
							return false
						}
						if err := observeAnthropicBlockSpan(est, body, bs, be, 0, isAssistant, &msgState, nil, nil); err != nil {
							msgErr = err
							return false
						}
						return true
					})
					if msgErr != nil {
						return false
					}
					if !contentOK {
						msgErr = fmt.Errorf("parse anthropic count_tokens request: invalid message content array")
						return false
					}
				default:
					msgErr = fmt.Errorf("parse anthropic count_tokens request: invalid message content")
					return false
				}
			}
			return msgErr == nil
		})
		if msgErr != nil {
			return 0, msgErr
		}
		if !msgsOK {
			return 0, fmt.Errorf("parse anthropic count_tokens request: invalid messages array")
		}
	}

	if !toolsSpan.isNull(body) {
		var toolErr error
		toolsOK := jsonForEachElementSpan(body, toolsSpan.start, toolsSpan.end, func(elemStart, elemEnd int) bool {
			if err := observeAnthropicToolSpan(est, body, elemStart, elemEnd); err != nil {
				toolErr = err
				return false
			}
			return true
		})
		if toolErr != nil {
			return 0, toolErr
		}
		if !toolsOK {
			return 0, fmt.Errorf("parse anthropic count_tokens request: invalid tools array")
		}
	}

	if toolChoiceSpan.valid() {
		if err := observeAnthropicToolChoiceSpan(est, body, toolChoiceSpan, tcType, tcName, tcLargeNameSpan); err != nil {
			return 0, err
		}
	}

	res := est.result()
	if res < openAIInputTokensFallbackMinimum {
		res = openAIInputTokensFallbackMinimum
	}
	return res, nil
}

func observeAnthropicToolChoiceSpan(est *boundedTokenEstimator, body []byte, toolChoiceSpan jsonSpan, tcType, tcName string, tcLargeNameSpan jsonSpan) error {
	if toolChoiceSpan.isNull(body) {
		return est.observeRawJSONSpan(body, toolChoiceSpan.start, toolChoiceSpan.end)
	}
	switch tcType {
	case "auto":
		return est.observePlainBytes([]byte(`"auto"`))
	case "any":
		return est.observePlainBytes([]byte(`"required"`))
	case "none":
		return est.observePlainBytes([]byte(`"none"`))
	case "tool":
		if !tcLargeNameSpan.valid() {
			encoded, err := json.Marshal(map[string]any{
				"type": "function",
				"name": tcName,
			})
			if err != nil {
				return err
			}
			return est.observePlainBytes(encoded)
		}
		if err := est.observePlainBytes([]byte(`{"name":"`)); err != nil {
			return err
		}
		if okStr, err := est.observeJSONEscapedStringContent(body, tcLargeNameSpan.start, tcLargeNameSpan.end, true); err != nil {
			return err
		} else if !okStr {
			return fmt.Errorf("parse anthropic count_tokens request: invalid tool_choice name")
		}
		return est.observePlainBytes([]byte(`","type":"function"}`))
	default:
		return est.observeRawJSONSpan(body, toolChoiceSpan.start, toolChoiceSpan.end)
	}
}

func normalizeToolParametersForEstimate(schema json.RawMessage) json.RawMessage {
	if len(schema) == 0 || string(schema) == "null" {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(schema, &m); err != nil {
		return schema
	}
	if string(m["type"]) != `"object"` {
		return schema
	}
	if _, ok := m["properties"]; ok {
		return schema
	}
	m["properties"] = json.RawMessage(`{}`)
	out, err := json.Marshal(m)
	if err != nil {
		return schema
	}
	return out
}

func observeAnthropicToolSpan(est *boundedTokenEstimator, body []byte, elemStart, elemEnd int) error {
	if (jsonSpan{elemStart, elemEnd}).isNull(body) {
		strictFalse := false
		raw, err := marshalOpenAIUpstreamJSON(apicompat.ResponsesTool{
			Type:       "function",
			Parameters: json.RawMessage(`{"type":"object","properties":{}}`),
			Strict:     &strictFalse,
		})
		if err != nil {
			return err
		}
		return est.observePlainBytes(raw)
	}
	if elemEnd-elemStart <= boundedMetaInlineLimit {
		var t apicompat.AnthropicTool
		if err := json.Unmarshal(body[elemStart:elemEnd], &t); err != nil {
			return err
		}
		var rt apicompat.ResponsesTool
		if strings.HasPrefix(t.Type, "web_search") {
			rt = apicompat.ResponsesTool{Type: "web_search"}
		} else {
			strictFalse := false
			rt = apicompat.ResponsesTool{
				Type:        "function",
				Name:        t.Name,
				Description: t.Description,
				Parameters:  normalizeToolParametersForEstimate(t.InputSchema),
				Strict:      &strictFalse,
			}
		}
		raw, err := marshalOpenAIUpstreamJSON(rt)
		if err != nil {
			return err
		}
		return est.observePlainBytes(raw)
	}

	var typeSpan, nameSpan, descSpan, inputSchemaSpan jsonSpan
	if !forEachJSONMemberDecoded(body, elemStart, elemEnd, func(key string, _, _, vs, ve int) bool {
		sp := jsonSpan{vs, ve}
		switch {
		case strings.EqualFold(key, "type"):
			if !sp.isNull(body) && body[sp.start] == '"' {
				typeSpan = sp
			}
		case strings.EqualFold(key, "name"):
			if !sp.isNull(body) && body[sp.start] == '"' {
				nameSpan = sp
			}
		case strings.EqualFold(key, "description"):
			if !sp.isNull(body) && body[sp.start] == '"' {
				descSpan = sp
			}
		case strings.EqualFold(key, "input_schema"):
			inputSchemaSpan = sp
		}
		return true
	}) {
		return fmt.Errorf("parse anthropic count_tokens request: invalid tool object")
	}

	if !typeSpan.isNull(body) && hasJSONStringPrefix(body, typeSpan.start, typeSpan.end, "web_search") {
		return est.observePlainBytes([]byte(`{"type":"web_search"}`))
	}

	nameSmall := !nameSpan.valid() || nameSpan.end-nameSpan.start <= boundedMetaInlineLimit
	descSmall := !descSpan.valid() || descSpan.end-descSpan.start <= boundedMetaInlineLimit
	schemaSmall := !inputSchemaSpan.valid() || inputSchemaSpan.end-inputSchemaSpan.start <= boundedToolsInlineLimit
	if nameSmall && descSmall && schemaSmall {
		meta := append(est.metaScratch[:0], '{')
		hasMember := false
		if !nameSpan.isNull(body) {
			meta = append(meta, `"name":`...)
			meta = append(meta, body[nameSpan.start:nameSpan.end]...)
			hasMember = true
		}
		if !descSpan.isNull(body) {
			if hasMember {
				meta = append(meta, ',')
			}
			meta = append(meta, `"description":`...)
			meta = append(meta, body[descSpan.start:descSpan.end]...)
			hasMember = true
		}
		if !inputSchemaSpan.isNull(body) {
			if hasMember {
				meta = append(meta, ',')
			}
			meta = append(meta, `"input_schema":`...)
			meta = append(meta, body[inputSchemaSpan.start:inputSchemaSpan.end]...)
		}
		meta = append(meta, '}')
		var t apicompat.AnthropicTool
		err := json.Unmarshal(meta, &t)
		est.metaScratch = meta[:0]
		if err != nil {
			return err
		}
		strictFalse := false
		raw, err := marshalOpenAIUpstreamJSON(apicompat.ResponsesTool{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
			Parameters:  normalizeToolParametersForEstimate(t.InputSchema),
			Strict:      &strictFalse,
		})
		if err != nil {
			return err
		}
		return est.observePlainBytes(raw)
	}

	if err := est.observePlainBytes([]byte(`{"type":"function"`)); err != nil {
		return err
	}
	if !nameSpan.isNull(body) && nameSpan.end-nameSpan.start > 2 {
		if err := est.observePlainBytes([]byte(`,"name":"`)); err != nil {
			return err
		}
		if okStr, err := est.observeJSONEscapedStringContent(body, nameSpan.start, nameSpan.end, false); err != nil {
			return err
		} else if !okStr {
			return fmt.Errorf("parse anthropic count_tokens request: invalid tool name")
		}
		if err := est.observePlainBytes([]byte(`"`)); err != nil {
			return err
		}
	}
	if !descSpan.isNull(body) && descSpan.end-descSpan.start > 2 {
		if err := est.observePlainBytes([]byte(`,"description":"`)); err != nil {
			return err
		}
		if okStr, err := est.observeJSONEscapedStringContent(body, descSpan.start, descSpan.end, false); err != nil {
			return err
		} else if !okStr {
			return fmt.Errorf("parse anthropic count_tokens request: invalid tool description")
		}
		if err := est.observePlainBytes([]byte(`"`)); err != nil {
			return err
		}
	}
	if inputSchemaSpan.isNull(body) {
		if err := est.observePlainBytes([]byte(`,"parameters":{"type":"object","properties":{}}`)); err != nil {
			return err
		}
	} else if inputSchemaSpan.end-inputSchemaSpan.start <= boundedToolsInlineLimit {
		normalized := normalizeToolParametersForEstimate(json.RawMessage(bytes.TrimSpace(body[inputSchemaSpan.start:inputSchemaSpan.end])))
		compacted, err := compactOpenAIInputTokensJSON(normalized)
		if err != nil {
			if err := est.observePlainBytes([]byte(`,"parameters":`)); err != nil {
				return err
			}
			if err := est.observeRawJSONSpan(body, inputSchemaSpan.start, inputSchemaSpan.end); err != nil {
				return err
			}
		} else {
			if err := est.observePlainBytes([]byte(`,"parameters":`)); err != nil {
				return err
			}
			if err := est.observePlainBytes([]byte(compacted)); err != nil {
				return err
			}
		}
	} else {
		if err := est.observePlainBytes([]byte(`,"parameters":`)); err != nil {
			return err
		}
		if err := est.observeRawJSONSpan(body, inputSchemaSpan.start, inputSchemaSpan.end); err != nil {
			return err
		}
	}
	return est.observePlainBytes([]byte(`,"strict":false}`))
}

func ensureAnthropicUserMessage(est *boundedTokenEstimator, msgState *anthropicMessageEmitState) error {
	if msgState.hasUserMessage {
		return nil
	}
	msgState.hasUserMessage = true
	est.overheadTokens += openAIResponsesInputItemTokenOverhead
	return est.observePlainBytes(anthropicRoleUserBytes)
}

func observeAnthropicUserImageSource(est *boundedTokenEstimator, body []byte, sourceSpan jsonSpan, msgState *anthropicMessageEmitState) error {
	if sourceSpan.isNull(body) || body[sourceSpan.start] != '{' {
		return nil
	}
	var mediaTypeSpan, dataSpan jsonSpan
	if !forEachJSONMemberDecoded(body, sourceSpan.start, sourceSpan.end, func(k string, _, _, vs, ve int) bool {
		sp := jsonSpan{vs, ve}
		switch {
		case strings.EqualFold(k, "media_type"):
			if !sp.isNull(body) {
				mediaTypeSpan = sp
			}
		case strings.EqualFold(k, "data"):
			if !sp.isNull(body) {
				dataSpan = sp
			}
		}
		return true
	}) {
		return nil
	}
	if dataSpan.isNull(body) || body[dataSpan.start] != '"' || dataSpan.end-dataSpan.start <= 2 {
		return nil
	}
	if err := ensureAnthropicUserMessage(est, msgState); err != nil {
		return err
	}
	est.overheadTokens += openAIResponsesContentPartOverhead
	if !mediaTypeSpan.isNull(body) && body[mediaTypeSpan.start] == '"' && mediaTypeSpan.end-mediaTypeSpan.start > 2 {
		if mediaTypeSpan.end-mediaTypeSpan.start <= boundedMetaInlineLimit {
			mediaType := extractJSONMemberString(jsonView(body[mediaTypeSpan.start:mediaTypeSpan.end]), false)
			if mediaType == "" {
				mediaType = "image/png"
			}
			imgText := strings.TrimSpace(estimateOpenAIInputImageText("data:" + mediaType + ";base64,"))
			if imgText != "" {
				return est.observePlainString(imgText)
			}
			return nil
		}
		if err := est.observePlainBytes([]byte("data:")); err != nil {
			return err
		}
		if okStr, strErr := est.observeJSONString(body, mediaTypeSpan.start, mediaTypeSpan.end, false); strErr != nil {
			return strErr
		} else if !okStr {
			return fmt.Errorf("parse anthropic count_tokens request: invalid image media_type")
		}
		return est.observePlainBytes([]byte(";base64"))
	}
	return est.observePlainBytes(anthropicDefaultImageURIPrefixBytes)
}

func observeAnthropicBlockSpan(est *boundedTokenEstimator, body []byte, objStart, objEnd, depth int, isAssistant bool, msgState *anthropicMessageEmitState, toolResultHasText, toolResultHasNonSpace *bool) error {
	if depth > 1 {
		return nil
	}
	var err error
	est.metaScratch, err = validateAnthropicContentBlockSpan(body, objStart, objEnd, est.metaScratch)
	if err != nil {
		return err
	}

	var typeSpan, textSpan, nameSpan, inputSpan, contentSpan, toolUseIDSpan, idSpan, signatureSpan, sourceSpan jsonSpan
	if !forEachJSONMemberDecoded(body, objStart, objEnd, func(k string, _, _, vs, ve int) bool {
		sp := jsonSpan{vs, ve}
		switch {
		case strings.EqualFold(k, "type"):
			if !sp.isNull(body) {
				typeSpan = sp
			}
		case strings.EqualFold(k, "text"):
			if !sp.isNull(body) {
				textSpan = sp
			}
		case strings.EqualFold(k, "name"):
			if !sp.isNull(body) {
				nameSpan = sp
			}
		case strings.EqualFold(k, "input"):
			inputSpan = sp
		case strings.EqualFold(k, "content"):
			contentSpan = sp
		case strings.EqualFold(k, "tool_use_id"):
			if !sp.isNull(body) {
				toolUseIDSpan = sp
			}
		case strings.EqualFold(k, "id"):
			if !sp.isNull(body) {
				idSpan = sp
			}
		case strings.EqualFold(k, "signature"):
			if !sp.isNull(body) {
				signatureSpan = sp
			}
		case strings.EqualFold(k, "source"):
			if !sp.isNull(body) {
				sourceSpan = sp
			}
		}
		return true
	}) {
		return fmt.Errorf("parse anthropic count_tokens request: invalid content block")
	}

	blockType := ""
	if !typeSpan.isNull(body) && body[typeSpan.start] == '"' && typeSpan.end-typeSpan.start <= boundedMetaInlineLimit {
		blockType = extractJSONMemberString(jsonView(body[typeSpan.start:typeSpan.end]), false)
	}

	// Inside tool_result.content[] (depth > 0), convertToolResultOutput only extracts
	// "text" (b.Text) and image headers; nested tool_result/tool_use/thinking are ignored.
	if depth > 0 {
		switch blockType {
		case "text":
			if !textSpan.isNull(body) && body[textSpan.start] == '"' && textSpan.end-textSpan.start > 2 {
				if toolResultHasText != nil {
					*toolResultHasText = true
				}
				beforeBytes := est.totalTextBytes
				okStr, strErr := est.observeJSONString(body, textSpan.start, textSpan.end, true)
				if strErr != nil {
					return strErr
				}
				if !okStr {
					return fmt.Errorf("parse anthropic count_tokens request: invalid block string")
				}
				if est.totalTextBytes > beforeBytes && toolResultHasNonSpace != nil {
					if *toolResultHasNonSpace {
						if err := est.observePlainBytes(anthropicTextJoinSepBytes); err != nil {
							return err
						}
					}
					*toolResultHasNonSpace = true
				}
			}
		case "image":
			return observeAnthropicUserImageSource(est, body, sourceSpan, msgState)
		}
		return nil
	}

	switch blockType {
	case "text":
		if !textSpan.isNull(body) && body[textSpan.start] == '"' && textSpan.end-textSpan.start > 2 {
			if isAssistant {
				if !msgState.hasAssistantText {
					msgState.hasAssistantText = true
					est.overheadTokens += openAIResponsesInputItemTokenOverhead + openAIResponsesContentPartOverhead
					if err := est.observePlainBytes(anthropicRoleAssistantBytes); err != nil {
						return err
					}
				}
			} else {
				if err := ensureAnthropicUserMessage(est, msgState); err != nil {
					return err
				}
				est.overheadTokens += openAIResponsesContentPartOverhead
			}
			beforeBytes := est.totalTextBytes
			okStr, strErr := est.observeJSONString(body, textSpan.start, textSpan.end, true)
			if strErr != nil {
				return strErr
			}
			if !okStr {
				return fmt.Errorf("parse anthropic count_tokens request: invalid block string")
			}
			if isAssistant && est.totalTextBytes > beforeBytes {
				if msgState.hasAssistantNonSpace {
					if err := est.observePlainBytes(anthropicTextJoinSepBytes); err != nil {
						return err
					}
				}
				msgState.hasAssistantNonSpace = true
			}
		}
	case "image":
		if !isAssistant {
			return observeAnthropicUserImageSource(est, body, sourceSpan, msgState)
		}
	case "tool_use":
		if !isAssistant {
			return nil
		}
		est.overheadTokens += openAIResponsesInputItemTokenOverhead
		if err := est.observePlainBytes(anthropicItemTypeFunctionCallBytes); err != nil {
			return err
		}
		for _, sp := range []jsonSpan{idSpan, nameSpan} {
			if !sp.isNull(body) && body[sp.start] == '"' {
				okStr, strErr := est.observeJSONString(body, sp.start, sp.end, true)
				if strErr != nil {
					return strErr
				}
				if !okStr {
					return fmt.Errorf("parse anthropic count_tokens request: invalid block string")
				}
			}
		}
		if !inputSpan.valid() {
			return est.observePlainBytes(anthropicEmptyJSONObjectBytes)
		}
		if err := est.observeRawBytesSpan(body, inputSpan.start, inputSpan.end); err != nil {
			return err
		}
	case "tool_result":
		if isAssistant {
			return nil
		}
		est.overheadTokens += openAIResponsesInputItemTokenOverhead
		if err := est.observePlainBytes(anthropicItemTypeFunctionCallOutputBytes); err != nil {
			return err
		}
		if !toolUseIDSpan.isNull(body) && body[toolUseIDSpan.start] == '"' {
			okStr, strErr := est.observeJSONString(body, toolUseIDSpan.start, toolUseIDSpan.end, true)
			if strErr != nil {
				return strErr
			}
			if !okStr {
				return fmt.Errorf("parse anthropic count_tokens request: invalid block string")
			}
		}
		if contentSpan.isNull(body) {
			return est.observePlainBytes(anthropicEmptyToolResultBytes)
		}
		switch body[contentSpan.start] {
		case '"':
			if contentSpan.end-contentSpan.start <= 2 {
				return est.observePlainBytes(anthropicEmptyToolResultBytes)
			}
			okStr, strErr := est.observeJSONString(body, contentSpan.start, contentSpan.end, true)
			if strErr != nil {
				return strErr
			}
			if !okStr {
				return fmt.Errorf("parse anthropic count_tokens request: invalid block content string")
			}
		case '[':
			savedSampledBytes := est.sampledTextBytes
			savedTotalBytes := est.totalTextBytes
			savedSampledTokens := est.sampledTokens
			savedOverhead := est.overheadTokens
			savedHasUserMessage := msgState.hasUserMessage

			hasInnerText := false
			hasInnerNonSpace := false
			innerInvalid := false
			subOK := jsonForEachElementSpan(body, contentSpan.start, contentSpan.end, func(ss, se int) bool {
				if (jsonSpan{ss, se}).isNull(body) {
					return true
				}
				if body[ss] != '{' {
					innerInvalid = true
					return false
				}
				if err := observeAnthropicBlockSpan(est, body, ss, se, depth+1, isAssistant, msgState, &hasInnerText, &hasInnerNonSpace); err != nil {
					innerInvalid = true
					return false
				}
				return true
			})
			if innerInvalid || !subOK {
				est.sampledTextBytes = savedSampledBytes
				est.totalTextBytes = savedTotalBytes
				est.sampledTokens = savedSampledTokens
				est.overheadTokens = savedOverhead
				msgState.hasUserMessage = savedHasUserMessage
				return est.observePlainBytes(anthropicEmptyToolResultBytes)
			}
			if !hasInnerText {
				return est.observePlainBytes(anthropicEmptyToolResultBytes)
			}
		default:
			// convertToolResultOutput returns "(empty)" without error on non-string,
			// non-array valid JSON content (validJSON was already verified at root).
			return est.observePlainBytes(anthropicEmptyToolResultBytes)
		}
	case "thinking":
		// apicompat.AnthropicToResponses (preserveThinking=false) drops thinking blocks
		// without a valid non-gAAAA signature and otherwise emits only EncryptedContent
		// (never b.Thinking), which estimateOpenAIInputTokensForInputItems does not count.
		if isAssistant && !signatureSpan.isNull(body) && body[signatureSpan.start] == '"' && signatureSpan.end-signatureSpan.start > 2 {
			validSig := false
			if signatureSpan.end-signatureSpan.start <= boundedMetaInlineLimit {
				sig := strings.TrimSpace(extractJSONMemberString(jsonView(body[signatureSpan.start:signatureSpan.end]), false))
				validSig = sig != "" && !strings.HasPrefix(sig, "gAAAA")
			} else {
				prefix, totalDecoded, okSig := appendUnescapedJSONStringBounded(est.metaScratch[:0], body[signatureSpan.start+1:signatureSpan.end-1], 16, true)
				est.metaScratch = prefix[:0]
				validSig = okSig && totalDecoded > 0 && !bytes.HasPrefix(prefix, []byte("gAAAA"))
			}
			if validSig {
				est.overheadTokens += openAIResponsesInputItemTokenOverhead
				return est.observePlainBytes(anthropicItemTypeReasoningBytes)
			}
		}
		return nil
	}
	return nil
}
