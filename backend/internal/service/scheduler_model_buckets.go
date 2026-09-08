package service

import "strings"

// accountsSupportingRequestedModel keeps mapped accounts that actually serve
// the model. Empty-mapping accounts are used only when no mapped sibling in
// this pool advertises the model. That stops an empty OpenAI mapping from
// stealing traffic meant for a mapped Gemini/OpenAI-compat account.
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
			unmapped = append(unmapped, account)
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
