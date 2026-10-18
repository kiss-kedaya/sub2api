package service

import (
	"strings"
	"time"
)

const grokModerationRefusalBillingCost = 0.1
const grokModerationRefusalText = "i'm sorry, i can't help with that request."

// grokModerationRefusalSessionBlockTTL is how long a conversation stays blocked
// after the provider refused it on safety grounds. The block exists so a client
// hammering the same prompt stops burning upstream quota; it is not a permanent
// ban, so it expires.
const grokModerationRefusalSessionBlockTTL = 24 * time.Hour

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

// GrokModerationRefusalMessage is the text returned to a client whose
// conversation was blocked after the provider refused it on safety grounds.
func GrokModerationRefusalMessage() string {
	return "I'm sorry, I can't help with that request. Failed check: SAFETY_CHECK_TYPE"
}

// GrokModerationRefusalSessionBlockTTL exposes the block duration so handlers
// can apply it through the gateway cache.
func GrokModerationRefusalSessionBlockTTL() time.Duration {
	return grokModerationRefusalSessionBlockTTL
}

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
