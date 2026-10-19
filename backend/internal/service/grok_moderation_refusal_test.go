package service

import (
	"errors"
	"math"
	"testing"
)

func TestIsGrokModerationRefusal(t *testing.T) {
	if !IsGrokModerationRefusal(PlatformGrok, []byte(`status_code=403, I'm sorry, I can't help with that request.`)) {
		t.Fatal("expected Grok refusal to match")
	}
	if !IsGrokModerationRefusal(PlatformGrok, []byte(`I'M SORRY, I CAN'T HELP WITH THAT REQUEST.`)) {
		t.Fatal("expected case-insensitive refusal to match")
	}
	if IsGrokModerationRefusal(PlatformGrok, []byte(`I'm sorry`)) {
		t.Fatal("incomplete refusal must not match")
	}
	if IsGrokModerationRefusal(PlatformGrok, []byte(`I'm sorry, I can't help`)) {
		t.Fatal("truncated refusal must not match")
	}
	if IsGrokModerationRefusal(PlatformOpenAI, []byte(`I'm sorry`)) {
		t.Fatal("non-Grok refusal must not match")
	}
	if IsGrokModerationRefusal(PlatformGrok, []byte(`upstream error`)) {
		t.Fatal("ordinary upstream error must not match")
	}
}

func TestPrepareGrokModerationRefusalResult(t *testing.T) {
	result := PrepareGrokModerationRefusalResult(nil, "grok-4", 0)
	if result.Model != "grok-4" || result.UpstreamModel != "grok-4" || result.NonBillableUpstreamError || result.ForcedBillingCost != 0.1 || !result.ForceBalanceBilling {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestGrokModerationRefusalBilledCostAppliesMultiplier(t *testing.T) {
	cases := []struct {
		multiplier float64
		want       float64
	}{
		{1, 0.1},
		{0.07, 0.007},
		{2, 0.2},
		{0, 0},
		{-1, 0},
	}
	for _, tc := range cases {
		got := GrokModerationRefusalBilledCost(tc.multiplier)
		if math.Abs(got-tc.want) > 1e-12 {
			t.Fatalf("multiplier=%v want=%v got=%v", tc.multiplier, tc.want, got)
		}
	}

	result := PrepareGrokModerationRefusalResult(nil, "grok-4", GrokModerationRefusalBilledCost(0.07))
	if math.Abs(result.ForcedBillingCost-0.007) > 1e-12 || !result.ForceBalanceBilling {
		t.Fatalf("unexpected multiplied result: %+v", result)
	}
}

func TestPrepareGrokModerationRefusalOpenAIResult(t *testing.T) {
	result := PrepareGrokModerationRefusalOpenAIResult(nil, "grok-4.7", 0.007)
	if result.Model != "grok-4.7" || result.UpstreamModel != "grok-4.7" ||
		result.NonBillableUpstreamError || math.Abs(result.ForcedBillingCost-0.007) > 1e-12 || !result.ForceBalanceBilling {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestIsGrokPlatformContentPolicyRejection(t *testing.T) {
	if !IsGrokPlatformContentPolicyRejection(PlatformGrok,
		errors.New("grok content policy rejection: text is sensitive")) {
		t.Fatal("expected content policy rejection to match")
	}
	if IsGrokPlatformContentPolicyRejection(PlatformOpenAI,
		errors.New("grok content policy rejection: x")) {
		t.Fatal("non-Grok platform must not match")
	}
	if IsGrokPlatformContentPolicyRejection(PlatformGrok, errors.New("some other error")) {
		t.Fatal("unrelated error must not match")
	}
	if IsGrokPlatformContentPolicyRejection(PlatformGrok, nil) {
		t.Fatal("nil error must not match")
	}
}
