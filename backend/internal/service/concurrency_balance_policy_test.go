package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBalanceConcurrencyReserveUSD(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		want     float64
	}{
		{name: "anthropic", platform: PlatformAnthropic, want: AnthropicBalanceConcurrencyReserveUSD},
		{name: "openai", platform: PlatformOpenAI, want: DefaultBalanceConcurrencyReserveUSD},
		{name: "grok", platform: PlatformGrok, want: DefaultBalanceConcurrencyReserveUSD},
		{name: "future platform", platform: "future", want: DefaultBalanceConcurrencyReserveUSD},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, BalanceConcurrencyReserveUSD(tt.platform))
		})
	}
}
