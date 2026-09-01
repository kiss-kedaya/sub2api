package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/tidwall/gjson"
)

func TestSanitizeAnthropicFallbackFieldsWithoutBeta(t *testing.T) {
	body := []byte(`{"fallbacks":["default"],"fallback_credit_token":"credit","messages":[]}`)
	out, changed := sanitizeAnthropicFallbackFields(body, "oauth-2025-04-20")
	if !changed {
		t.Fatal("fallback fields should be removed when capability betas are absent")
	}
	if gjson.GetBytes(out, "fallbacks").Exists() || gjson.GetBytes(out, "fallback_credit_token").Exists() {
		t.Fatalf("sanitized body still contains fallback fields: %s", out)
	}
}

func TestSanitizeAnthropicFallbackFieldsWithServerSideBeta(t *testing.T) {
	body := []byte(`{"fallbacks":["default"],"fallback_credit_token":"credit","messages":[]}`)
	out, changed := sanitizeAnthropicFallbackFields(body, claude.BetaServerSideFallback)
	if changed {
		t.Fatal("authorized fallback fields should be preserved")
	}
	if !gjson.GetBytes(out, "fallbacks").Exists() || !gjson.GetBytes(out, "fallback_credit_token").Exists() {
		t.Fatalf("authorized fallback fields were removed: %s", out)
	}
}

func TestSanitizeBedrockFallbackFieldsAlwaysRemovesUnsupportedFields(t *testing.T) {
	body := []byte(`{"fallbacks":["default"],"fallback_credit_token":"credit","messages":[]}`)
	out := sanitizeBedrockFieldsForBetaTokens(body, []string{})
	if gjson.GetBytes(out, "fallbacks").Exists() || gjson.GetBytes(out, "fallback_credit_token").Exists() {
		t.Fatalf("Bedrock body still contains unsupported fallback fields: %s", out)
	}
}
