package service

import "strings"

// accountsSupportingRequestedModel keeps mapped accounts that actually serve
// the model. Empty-mapping accounts are used only when no mapped sibling in
// this pool advertises the model. That stops an empty OpenAI mapping from
// stealing traffic meant for a mapped Gemini/OpenAI-compat account.
//
// Empty-mapping accounts still go through IsModelSupported: CN/OAuth defaults
// may reject a foreign family even without an explicit mapping. Those accounts
// must not keep the request on a 503 capacity path.
func accountsSupportingRequestedModel(accounts []Account, requestedModel string) []Account {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" || len(accounts) == 0 {
		return accounts
	}
	matched := make([]Account, 0, len(accounts))
	unmapped := make([]Account, 0)
	for _, account := range accounts {
		mapping := account.GetModelMapping()
		if len(mapping) == 0 {
			if account.IsModelSupported(requestedModel) {
				unmapped = append(unmapped, account)
			}
			continue
		}
		if account.IsModelSupported(requestedModel) {
			matched = append(matched, account)
		}
	}
	if len(matched) > 0 {
		return matched
	}
	return unmapped
}

// filterAccountsSupportingRequestedModel reports how many accounts were dropped
// solely because they cannot serve the requested model. Callers use that count
// to emit model_not_supported=N instead of pool=0, so handlers can return 404
// instead of a retryable 503.
func filterAccountsSupportingRequestedModel(accounts []Account, requestedModel string) (filtered []Account, unsupported int) {
	filtered = accountsSupportingRequestedModel(accounts, requestedModel)
	if len(accounts) == 0 || len(filtered) > 0 || strings.TrimSpace(requestedModel) == "" {
		return filtered, 0
	}
	return filtered, len(accounts)
}

func noAvailableAccountsDueToModelSupport(requestedModel string, unsupportedCount int) error {
	stats := openAISelectionFilterStats{pool: unsupportedCount}
	if unsupportedCount > 0 {
		stats.reasons = map[string]int{"model_not_supported": unsupportedCount}
	}
	return noAvailableOpenAISelectionError(requestedModel, false, stats.summary(""))
}
