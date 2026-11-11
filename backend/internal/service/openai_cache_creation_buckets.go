package service

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Upstreams built on new-api report prompt-cache writes per TTL bucket in
// response headers, and for the Anthropic protocol also inside
// usage.cache_creation. The OpenAI Chat Completions usage has no such field, so
// a client reading a chat body cannot tell how much was written to the 5m and
// 1h buckets even when the upstream knows.
//
// These helpers add usage.cache_creation to a Chat Completions response from the
// header values. They only ever add: a body that already carries the object is
// left byte-for-byte as it was, and no header means no change.
const (
	mshCacheWrite5mHeader = "Msh-Usage-Cache-Write-Tokens-5m"
	mshCacheWrite1hHeader = "Msh-Usage-Cache-Write-Tokens-1h"
)

// ParseMSHCacheWriteBuckets reads the per-TTL prompt-cache write counts from
// upstream response headers. The second result is false when neither header is
// present, and also when a header that is present does not hold a non-negative
// integer: a split with one bucket silently read as zero would be worse than no
// split at all.
func ParseMSHCacheWriteBuckets(h http.Header) (fiveMinute, oneHour int, ok bool) {
	if h == nil {
		return 0, 0, false
	}
	// parse reports whether the header was there at all, and whether what it held
	// could be used.
	parse := func(name string) (value int, present, valid bool) {
		raw := strings.TrimSpace(h.Get(name))
		if raw == "" {
			return 0, false, false
		}
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			return 0, true, false
		}
		return parsed, true, true
	}
	fiveMinute, fivePresent, fiveValid := parse(mshCacheWrite5mHeader)
	oneHour, onePresent, oneValid := parse(mshCacheWrite1hHeader)
	if !fivePresent && !onePresent {
		return 0, 0, false
	}
	if (fivePresent && !fiveValid) || (onePresent && !oneValid) {
		return 0, 0, false
	}
	return fiveMinute, oneHour, true
}

// cacheCreationBucketsJSON renders the object exactly as the Anthropic protocol
// reports it, both keys present so a reader never has to treat a missing key as
// zero.
func cacheCreationBucketsJSON(fiveMinute, oneHour int) []byte {
	return []byte(fmt.Sprintf(
		`{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":%d}`,
		fiveMinute, oneHour,
	))
}

// InjectChatCompletionCacheCreation adds usage.cache_creation to a Chat
// Completions response body when the upstream reported the TTL buckets in
// headers but not in the body. It returns the input unchanged when there is
// nothing to add: no buckets in the headers, no usage object, or an existing
// cache_creation the upstream already wrote.
func InjectChatCompletionCacheCreation(body []byte, h http.Header) []byte {
	fiveMinute, oneHour, ok := ParseMSHCacheWriteBuckets(h)
	if !ok || len(body) == 0 {
		return body
	}
	usage := gjson.GetBytes(body, "usage")
	if !usage.IsObject() {
		return body
	}
	if usage.Get("cache_creation").IsObject() {
		return body
	}
	next, err := sjson.SetRawBytes(body, "usage.cache_creation", cacheCreationBucketsJSON(fiveMinute, oneHour))
	if err != nil {
		return body
	}
	return next
}

// InjectChatCompletionCacheCreationIntoSSELine is the streaming counterpart: one
// `data: {...}` line at a time. Lines that are not data lines, carry no usage,
// or already have cache_creation are returned untouched.
func InjectChatCompletionCacheCreationIntoSSELine(line string, fiveMinute, oneHour int) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "data:") {
		return line
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	if payload == "" || payload == "[DONE]" || !gjson.Valid(payload) {
		return line
	}
	if !gjson.Get(payload, "usage").IsObject() || gjson.Get(payload, "usage.cache_creation").IsObject() {
		return line
	}
	next, err := sjson.SetRaw(payload, "usage.cache_creation", string(cacheCreationBucketsJSON(fiveMinute, oneHour)))
	if err != nil {
		return line
	}
	// Keep whatever the upstream put between "data:" and the JSON (usually one space).
	prefix := line[:strings.Index(line, "data:")+len("data:")]
	separator := line[len(prefix) : len(line)-len(strings.TrimLeft(line[len(prefix):], " \t"))]
	return prefix + separator + next
}
