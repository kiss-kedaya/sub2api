package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEstimateBalancePreauthorizationTokensUsesProtocolEstimators(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantOutput int
	}{
		{
			name:       "openai responses",
			body:       `{"model":"gpt-5","input":"hello","max_output_tokens":1024}`,
			wantOutput: 1024,
		},
		{
			name:       "anthropic messages",
			body:       `{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hello"}],"max_tokens":4096}`,
			wantOutput: 4096,
		},
		{
			name:       "gemini contents",
			body:       `{"model":"gemini-2.5-pro","contents":[{"parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":2048}}`,
			wantOutput: 2048,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateBalancePreauthorizationTokens([]byte(tt.body))
			require.GreaterOrEqual(t, got.InputTokens, DefaultBalancePreauthorizationInputTokens)
			require.Equal(t, tt.wantOutput, got.OutputTokens)
		})
	}
}

func TestEstimateBalancePreauthorizationTokensUsesLargestOutputLimit(t *testing.T) {
	body := []byte(`{"model":"gpt-5","input":"hello","max_tokens":32,"max_completion_tokens":64,"max_output_tokens":48}`)
	got := EstimateBalancePreauthorizationTokens(body)
	require.Equal(t, 64, got.OutputTokens)
}

func TestEstimateBalancePreauthorizationTokensFallsBackWithoutIO(t *testing.T) {
	body := []byte(`{"model":"custom","prompt":"` + strings.Repeat("x", 700) + `"}`)
	got := EstimateBalancePreauthorizationTokens(body)
	require.Equal(t, len(body), got.InputTokens)
	require.Equal(t, DefaultBalancePreauthorizationNonStreamOutputWindow, got.OutputTokens)
}

func TestEstimateBalancePreauthorizationTokensIgnoresInvalidOutputLimit(t *testing.T) {
	body := []byte(`{"model":"gpt-5","input":"hello","max_output_tokens":-1}`)
	got := EstimateBalancePreauthorizationTokens(body)
	require.Equal(t, DefaultBalancePreauthorizationNonStreamOutputWindow, got.OutputTokens)
}

func TestEstimateBalancePreauthorizationTokensStreamVsNonStreamDefaultWindow(t *testing.T) {
	nonStreamBody := []byte(`{"model":"gpt-5","input":"hello"}`)
	require.Equal(t, DefaultBalancePreauthorizationNonStreamOutputWindow, EstimateBalancePreauthorizationTokens(nonStreamBody).OutputTokens)

	streamBody := []byte(`{"model":"gpt-5","input":"hello","stream":true}`)
	require.Equal(t, DefaultBalancePreauthorizationOutputWindow, EstimateBalancePreauthorizationTokens(streamBody).OutputTokens)

	// Explicit transport stream flag overrides absent/conflicting JSON stream field.
	require.Equal(t, DefaultBalancePreauthorizationOutputWindow, EstimateBalancePreauthorizationTokensForStream(nonStreamBody, true).OutputTokens)
	require.Equal(t, DefaultBalancePreauthorizationNonStreamOutputWindow, EstimateBalancePreauthorizationTokensForStream(streamBody, false).OutputTokens)
}

func TestEstimateBalancePreauthorizationTokensGeminiSnakeCaseAndThinkingBudgets(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantOutput int
	}{
		{
			name:       "gemini snake_case max_output_tokens",
			body:       `{"contents":[{"parts":[{"text":"hi"}]}],"generation_config":{"max_output_tokens":1536}}`,
			wantOutput: 1536,
		},
		{
			name:       "anthropic thinking budget larger than max_tokens",
			body:       `{"model":"claude-sonnet-4","max_tokens":1024,"thinking":{"type":"enabled","budget_tokens":4096},"messages":[{"role":"user","content":"hi"}]}`,
			wantOutput: 4096,
		},
		{
			name:       "anthropic thinking budget smaller than max_tokens does not double count",
			body:       `{"model":"claude-sonnet-4","max_tokens":8192,"thinking":{"type":"enabled","budget_tokens":4096},"messages":[{"role":"user","content":"hi"}]}`,
			wantOutput: 8192,
		},
		{
			name:       "anthropic thinking budget with omitted max_tokens",
			body:       `{"model":"claude-sonnet-4","thinking":{"type":"enabled","budget_tokens":3000},"messages":[{"role":"user","content":"hi"}]}`,
			wantOutput: 3000,
		},
		{
			name:       "gemini camelCase thinkingBudget with omitted maxOutputTokens reserves budget plus output window",
			body:       `{"contents":[{"parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":{"thinkingBudget":2048}}}`,
			wantOutput: 2048 + DefaultBalancePreauthorizationOutputWindow,
		},
		{
			name:       "gemini snake_case thinking_budget with omitted max_output_tokens reserves budget plus output window",
			body:       `{"contents":[{"parts":[{"text":"hi"}]}],"generation_config":{"thinking_config":{"thinking_budget":1024}}}`,
			wantOutput: 1024 + DefaultBalancePreauthorizationOutputWindow,
		},
		{
			name:       "gemini thinkingBudget with explicit maxOutputTokens takes max",
			body:       `{"contents":[{"parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":1024,"thinkingConfig":{"thinkingBudget":6144}}}`,
			wantOutput: 6144,
		},
		{
			name:       "gemini dynamic thinkingBudget -1 ignored",
			body:       `{"contents":[{"parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":{"thinkingBudget":-1}}}`,
			wantOutput: DefaultBalancePreauthorizationNonStreamOutputWindow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateBalancePreauthorizationTokens([]byte(tt.body))
			require.Equal(t, tt.wantOutput, got.OutputTokens)
		})
	}
}
