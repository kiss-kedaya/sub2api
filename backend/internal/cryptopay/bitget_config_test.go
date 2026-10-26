package cryptopay

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestBitgetModeRejectsUnsupportedChainsAndUnsafeFees(t *testing.T) {
	for _, chain := range []string{"bsc", "ethereum", "polygon", "arbitrum", "base"} {
		cfg, err := NormalizeConfig(CryptoConfig{Chains: []string{chain}, SweepProvider: "bitget"})
		require.NoError(t, err)
		require.Equal(t, "0.5", cfg.BitgetMaxFee)
	}
	for _, chain := range []string{"plasma", "xlayer"} {
		_, err := NormalizeConfig(CryptoConfig{Chains: []string{chain}, SweepProvider: "bitget"})
		require.Error(t, err)
	}
	for _, fee := range []string{"-1", "0", "1e2147483647", "0.0000001", "11"} {
		_, err := NormalizeConfig(CryptoConfig{Chains: []string{"bsc"}, SweepProvider: "bitget", BitgetMaxFee: fee})
		require.Error(t, err)
	}
}
