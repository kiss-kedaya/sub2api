package service

import "testing"

// A channel-level Ultrafast multiplier must win over the model-owned default,
// because upstreams price the tier differently. Everything else stays as-is.
func TestConfiguredServiceTierMultiplierUltrafastChannelOverride(t *testing.T) {
	override := 4.5

	withOverride := &ModelPricing{UltrafastMultiplier: 6, FastMultiplier: nil}
	channelOverride := &ModelPricing{UltrafastMultiplier: override}

	if got := configuredServiceTierMultiplier(OpenAIFastTierUltrafast, withOverride); got != 6 {
		t.Fatalf("model default = %v, want 6", got)
	}
	if got := configuredServiceTierMultiplier(OpenAIFastTierUltrafast, channelOverride); got != override {
		t.Fatalf("channel override = %v, want %v", got, override)
	}

	// A non-positive value must never zero out the tier: it means "unset".
	zero := &ModelPricing{UltrafastMultiplier: 0}
	if got := configuredServiceTierMultiplier(OpenAIFastTierUltrafast, zero); got <= 0 {
		t.Fatalf("zero override leaked through: %v", got)
	}
}
