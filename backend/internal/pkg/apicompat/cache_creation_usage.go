package apicompat

import (
	"math"

	"github.com/tidwall/gjson"
)

// CacheCreationTokensFromUsage returns the cache-creation aggregate and whether
// any field carrying it was present, so callers can tell an authoritative zero
// from a missing value. Nested OpenAI details win as soon as one exists; the
// flat compatibility aliases keep their original rule of "first positive
// value", because relays may send a zero alias next to the populated one.
func CacheCreationTokensFromUsage(value gjson.Result) (int, bool) {
	for _, field := range []string{
		"input_tokens_details.cache_write_tokens",
		"prompt_tokens_details.cache_write_tokens",
		"input_tokens_details.cache_creation_tokens",
		"prompt_tokens_details.cache_creation_tokens",
	} {
		if result := value.Get(field); result.Exists() {
			return max(int(result.Int()), 0), true
		}
	}
	present := false
	for _, field := range []string{
		"cache_write_tokens",
		"cache_creation_input_tokens",
		"cache_write_input_tokens",
		"cache_creation_tokens",
	} {
		result := value.Get(field)
		if !result.Exists() {
			continue
		}
		present = true
		if tokens := int(result.Int()); tokens > 0 {
			return tokens, true
		}
	}
	return 0, present
}

// NormalizeCacheCreationBreakdown caps contradictory TTL details at a positive
// aggregate and assigns any unclassified remainder to 5m. Absent details stay
// absent; legacy Anthropic callers without an aggregate retain their details.
// OpenAI callers must discard details when their authoritative total is <= 0.
func NormalizeCacheCreationBreakdown(aggregate, fiveMinute, oneHour int) (int, int) {
	fiveMinute, oneHour = max(fiveMinute, 0), max(oneHour, 0)
	if aggregate <= 0 || (fiveMinute == 0 && oneHour == 0) {
		return fiveMinute, oneHour
	}
	if fiveMinute <= aggregate && oneHour <= aggregate-fiveMinute {
		return aggregate - oneHour, oneHour
	}

	detailTotal := float64(fiveMinute) + float64(oneHour)
	normalized5m := math.Round(float64(aggregate) * float64(fiveMinute) / detailTotal)
	if normalized5m >= float64(aggregate) {
		fiveMinute = aggregate
	} else {
		fiveMinute = int(normalized5m)
	}
	return fiveMinute, aggregate - fiveMinute
}
