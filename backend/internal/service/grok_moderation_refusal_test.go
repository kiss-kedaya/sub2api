package service

import "testing"

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
	result := PrepareGrokModerationRefusalResult(nil, "grok-4")
	if result.Model != "grok-4" || result.UpstreamModel != "grok-4" || result.NonBillableUpstreamError || result.ForcedBillingCost != 0.035 || !result.ForceBalanceBilling {
		t.Fatalf("unexpected result: %+v", result)
	}
}
