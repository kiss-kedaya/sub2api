package claude

import "testing"

func TestIsFable52AndMythos52(t *testing.T) {
	for _, model := range []string{
		"claude-fable-5-2",
		"anthropic/claude-fable-5-2",
		"us.anthropic.claude-fable-5-2",
		"claude-fable-5-2-thinking",
	} {
		if !IsFable52(model) {
			t.Fatalf("IsFable52(%q) = false, want true", model)
		}
		if IsMythos52(model) {
			t.Fatalf("IsMythos52(%q) = true, want false", model)
		}
	}
	for _, model := range []string{
		"claude-mythos-5-2",
		"anthropic/claude-mythos-5-2",
	} {
		if !IsMythos52(model) {
			t.Fatalf("IsMythos52(%q) = false, want true", model)
		}
	}
	for _, model := range []string{"claude-fable-5", "claude-fable-5-1", "claude-mythos-5-preview", "claude-opus-5-5"} {
		if IsFable52(model) || IsMythos52(model) {
			t.Fatalf("%q must not be classified as a 5.2 model", model)
		}
	}
}
