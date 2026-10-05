package service

import "strings"

const grokModerationRefusalBillingCost = 0.035
const grokModerationRefusalText = "i'm sorry, i can't help with that request."

// IsGrokModerationRefusal reports the provider-specific refusal signature that
// must be surfaced unchanged and settled as a billable request. Keep this
// narrow: it must not affect other platforms or ordinary upstream errors.
func IsGrokModerationRefusal(platform string, body []byte) bool {
	if !strings.EqualFold(strings.TrimSpace(platform), PlatformGrok) {
		return false
	}
	text := strings.ToLower(string(body))
	return strings.Contains(text, grokModerationRefusalText)
}

func GrokModerationRefusalBillingCost() float64 { return grokModerationRefusalBillingCost }

// PrepareGrokModerationRefusalResult creates the minimal usage record needed
// when the upstream rejected before producing a normal forward result.
func PrepareGrokModerationRefusalResult(result *ForwardResult, requestedModel string) *ForwardResult {
	if result == nil {
		result = &ForwardResult{}
	}
	if strings.TrimSpace(result.Model) == "" {
		result.Model = strings.TrimSpace(requestedModel)
	}
	if strings.TrimSpace(result.UpstreamModel) == "" {
		result.UpstreamModel = result.Model
	}
	result.NonBillableUpstreamError = false
	result.ForcedBillingCost = GrokModerationRefusalBillingCost()
	result.ForceBalanceBilling = true
	return result
}
