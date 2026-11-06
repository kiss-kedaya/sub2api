package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBuildDepositAddressResponseHidesUnscannedSolana(t *testing.T) {
	assigned := map[string]string{
		addresses.FamilyEVM:    "0xabc",
		addresses.FamilySolana: "So11111111111111111111111111111111111111112",
	}

	// The provider only scans EVM chains today, so a Solana address must never
	// be handed out: funds sent there would never be credited.
	data := buildDepositAddressResponse([]string{"ethereum", "bsc"}, assigned)
	require.Equal(t, "0xabc", data["evm"])
	require.NotContains(t, data, "solana")
	chains, ok := data["chains"].([]gin.H)
	require.True(t, ok)
	require.Len(t, chains, 2)
}

func TestBuildDepositAddressResponseIncludesSolanaWhenEnabled(t *testing.T) {
	assigned := map[string]string{
		addresses.FamilyEVM:    "0xabc",
		addresses.FamilySolana: "So11111111111111111111111111111111111111112",
	}

	data := buildDepositAddressResponse([]string{"ethereum", "solana"}, assigned)
	require.Equal(t, "0xabc", data["evm"])
	require.Equal(t, "So11111111111111111111111111111111111111112", data["solana"])
}
