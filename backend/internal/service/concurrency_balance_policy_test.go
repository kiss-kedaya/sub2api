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
		{name: "anthropic", platform: PlatformAnthropic, want: 1.0},
		{name: "openai", platform: PlatformOpenAI, want: 0.05},
		{name: "grok", platform: PlatformGrok, want: 0.05},
		{name: "future platform", platform: "future", want: 0.05},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, BalanceConcurrencyReserveUSD(tt.platform))
		})
	}
}
