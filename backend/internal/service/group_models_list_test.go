//go:build unit

package service

import (
	"strings"
	"testing"
)

func TestModelsListAllows(t *testing.T) {
	list := GroupModelsListConfig{
		Enabled: true,
		Models:  []string{"claude-sonnet-4.5", "gemini-2.5-pro", "gpt-5.5", "grok-*"},
	}

	tests := []struct {
		name  string
		model string
		want  bool
	}{
		{name: "exact match", model: "claude-sonnet-4.5", want: true},
		{name: "case-insensitive entry match", model: "Claude-Sonnet-4.5", want: true},
		{name: "not listed", model: "claude-opus-4.6", want: false},
		{name: "thinking suffix tolerated via claude normalization", model: "claude-sonnet-4.5-thinking", want: true},
		{name: "gemini models/ prefix stripped", model: "models/gemini-2.5-pro", want: true},
		{name: "gemini models/ prefix not in list", model: "models/gemini-2.5-flash", want: false},
		{name: "openai reasoning suffix normalizes to base model", model: "gpt-5.5-codex-high", want: true},
		{name: "openai reasoning suffix on unlisted model", model: "gpt-4.1-low", want: false},
		{name: "trailing wildcard prefix match", model: "grok-4.6", want: true},
		{name: "trailing wildcard requires prefix", model: "grok", want: false},
		{name: "empty model passes (handler decides required-ness)", model: "", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ModelsListAllows(list, tt.model); got != tt.want {
				t.Fatalf("ModelsListAllows(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}

	t.Run("disabled list allows everything", func(t *testing.T) {
		disabled := GroupModelsListConfig{Enabled: false, Models: []string{"only-model"}}
		if !ModelsListAllows(disabled, "anything") {
			t.Fatal("disabled list must allow all models")
		}
	})

	t.Run("bare wildcard allows everything", func(t *testing.T) {
		all := GroupModelsListConfig{Enabled: true, Models: []string{"*"}}
		if !ModelsListAllows(all, "claude-opus-4.6") || !ModelsListAllows(all, "models/gemini-2.5-pro") {
			t.Fatal("bare wildcard must allow all models")
		}
	})
}

func TestFilterModelsListForListing(t *testing.T) {
	source := []string{"claude-opus-4.6", "claude-sonnet-4.5", "gpt-5.4", "gpt-5.5-codex", "gpt-5.5-mini", "grok-4.6"}

	t.Run("passthrough when disabled", func(t *testing.T) {
		disabled := GroupModelsListConfig{Enabled: false, Models: []string{"gpt-5.4"}}
		got := FilterModelsListForListing(disabled, source)
		if len(got) != len(source) {
			t.Fatalf("disabled list must not filter, got %#v", got)
		}
	})

	t.Run("exact entries keep entry order and require source membership", func(t *testing.T) {
		cfg := GroupModelsListConfig{Enabled: true, Models: []string{"gpt-5.5-codex", "claude-sonnet-4.5", "claude-haiku-4.5"}}
		got := FilterModelsListForListing(cfg, source)
		want := []string{"gpt-5.5-codex", "claude-sonnet-4.5"}
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("got %#v want %#v", got, want)
		}
	})

	t.Run("wildcard expands against source", func(t *testing.T) {
		cfg := GroupModelsListConfig{Enabled: true, Models: []string{"grok-*"}}
		got := FilterModelsListForListing(cfg, source)
		if strings.Join(got, ",") != "grok-4.6" {
			t.Fatalf("got %#v", got)
		}
	})

	t.Run("bare wildcard expands to the full source list", func(t *testing.T) {
		cfg := GroupModelsListConfig{Enabled: true, Models: []string{"*"}}
		got := FilterModelsListForListing(cfg, []string{"gpt-5.4"})
		if strings.Join(got, ",") != "gpt-5.4" {
			t.Fatalf("got %#v", got)
		}
	})
}

func TestNormalizeGroupModelsListConfigDedupeCaseInsensitive(t *testing.T) {
	got := normalizeGroupModelsListConfig(GroupModelsListConfig{
		Enabled: true,
		Models:  []string{" gpt-5.4 ", "GPT-5.4", "claude-sonnet-4.5", ""},
	})
	if !got.Enabled || len(got.Models) != 2 || got.Models[0] != "gpt-5.4" || got.Models[1] != "claude-sonnet-4.5" {
		t.Fatalf("got %#v", got)
	}
}

func TestSupplementUnmappedOpenAIModels(t *testing.T) {
	mapped := []Account{{
		Platform:    PlatformOpenAI,
		Credentials: map[string]any{"model_mapping": map[string]any{"gemini-3.8-flash": "gemini-3.8-flash"}},
	}}
	got := supplementUnmappedOpenAIModels(mapped, []string{"gemini-3.8-flash"})
	if len(got) != 1 || got[0] != "gemini-3.8-flash" {
		t.Fatalf("mapped-only catalog must stay unchanged, got %#v", got)
	}

	mixed := append(mapped, Account{Platform: PlatformOpenAI, Credentials: map[string]any{}})
	got = supplementUnmappedOpenAIModels(mixed, []string{"gemini-3.8-flash"})
	if len(got) < 2 {
		t.Fatalf("unmapped OpenAI account should keep default catalog, got %#v", got)
	}
	foundGemini, foundDefault := false, false
	for _, model := range got {
		if model == "gemini-3.8-flash" {
			foundGemini = true
		}
		if strings.HasPrefix(model, "gpt-") {
			foundDefault = true
		}
	}
	if !foundGemini || !foundDefault {
		t.Fatalf("mixed catalog missing gemini or default gpt models: %#v", got)
	}
}
