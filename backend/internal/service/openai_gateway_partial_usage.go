package service

import "errors"

// openAIForwardResultHasObservedBillingUnits reports whether a partial stream
// result contains upstream usage that must survive a terminal read error.
func openAIForwardResultHasObservedBillingUnits(result *OpenAIForwardResult) bool {
	if result == nil {
		return false
	}
	return openAIUsageHasTokens(&result.Usage) || result.ImageCount > 0 ||
		result.SearchCount > 0 || result.WebSearchCalls > 0 ||
		result.VideoCount > 0 || result.AudioUsage != nil
}

// preserveOpenAIStreamingResultOnError mirrors the Anthropic partial-usage
// invariant: an attempt eligible for replay never exposes a billable result,
// while a terminal stream error keeps usage already reported by the upstream.
func preserveOpenAIStreamingResultOnError(result *OpenAIForwardResult, err error) (*OpenAIForwardResult, error) {
	if err == nil {
		return result, nil
	}
	var failoverErr *UpstreamFailoverError
	if errors.As(err, &failoverErr) || !openAIForwardResultHasObservedBillingUnits(result) {
		return nil, err
	}
	return result, err
}
