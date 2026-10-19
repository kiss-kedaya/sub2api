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
	result := PrepareGrokModerationRefusalResult(nil, "grok-4", GrokModerationRefusalBilledCost(nil))
	if result.Model != "grok-4" || result.UpstreamModel != "grok-4" || result.NonBillableUpstreamError || result.ForcedBillingCost != 0.1 || !result.ForceBalanceBilling {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestGrokModerationRefusalBilledCostUsesGroupOverride(t *testing.T) {
	cases := []struct {
		name  string
		group *Group
		want  float64
	}{
		{"nil group falls back to 0.1", nil, 0.1},
		{"unset override falls back to 0.1", &Group{}, 0.1},
		{"explicit 0 disables", &Group{GrokModerationRefusalPrice: ptrFloat(0)}, 0},
		{"custom price", &Group{GrokModerationRefusalPrice: ptrFloat(0.5)}, 0.5},
		{"negative clamps to 0", &Group{GrokModerationRefusalPrice: ptrFloat(-1)}, 0},
	}
	for _, tc := range cases {
		got := GrokModerationRefusalBilledCost(tc.group)
		if math.Abs(got-tc.want) > 1e-12 {
			t.Fatalf("%s: want=%v got=%v", tc.name, tc.want, got)
		}
	}

	result := PrepareGrokModerationRefusalResult(nil, "grok-4", 0.5)
	if math.Abs(result.ForcedBillingCost-0.5) > 1e-12 || !result.ForceBalanceBilling {
		t.Fatalf("unexpected result: %+v", result)
	}

	disabled := PrepareGrokModerationRefusalResult(nil, "grok-4", 0)
	if disabled.ForcedBillingCost != 0 || disabled.ForceBalanceBilling {
		t.Fatalf("explicit zero must not force billing: %+v", disabled)
	}
}

func TestPrepareGrokModerationRefusalOpenAIResult(t *testing.T) {
	result := PrepareGrokModerationRefusalOpenAIResult(nil, "grok-4.7", 0.25)
	if result.Model != "grok-4.7" || result.UpstreamModel != "grok-4.7" ||
		result.NonBillableUpstreamError || math.Abs(result.ForcedBillingCost-0.25) > 1e-12 || !result.ForceBalanceBilling {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func ptrFloat(v float64) *float64 { return &v }

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
