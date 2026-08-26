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
	require.Equal(t, DefaultBalancePreauthorizationOutputWindow, got.OutputTokens)
}

func TestEstimateBalancePreauthorizationTokensIgnoresInvalidOutputLimit(t *testing.T) {
	body := []byte(`{"model":"gpt-5","input":"hello","max_output_tokens":-1}`)
	got := EstimateBalancePreauthorizationTokens(body)
	require.Equal(t, DefaultBalancePreauthorizationOutputWindow, got.OutputTokens)
}
