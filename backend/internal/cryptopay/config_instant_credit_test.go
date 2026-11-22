package cryptopay

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// A config stored before the instant-credit switch existed has no key. It must
// load as ON (the shipped default), never as an accidental OFF.
func TestInstantCreditDefaultsOnForLegacyConfig(t *testing.T) {
	for _, raw := range []string{`{}`, `{"enabled":true}`, `{"enabled":true,"confirmations":{"bsc":3}}`} {
		var cfg CryptoConfig
		require.NoError(t, json.Unmarshal([]byte(raw), &cfg))
		normalized, err := NormalizeConfig(cfg)
		require.NoError(t, err)
		require.True(t, normalized.InstantCreditOn(), "legacy config %s must default to instant credit", raw)
		data, err := json.Marshal(normalized)
		require.NoError(t, err)
		var fields map[string]any
		require.NoError(t, json.Unmarshal(data, &fields))
		require.Equal(t, true, fields["instant_credit_enabled"])
	}
}

// The switch is an operator kill switch: one config write must restore the old
// confirmation-gated behaviour.
func TestInstantCreditPatchCanDisableAndSetFloor(t *testing.T) {
	base := DefaultCryptoConfig()
	off := false
	cfg := CryptoConfigPatch{InstantCreditEnabled: &off, InstantCreditMinAmount: floatPtr(5)}.Apply(base)
	normalized, err := NormalizeConfig(cfg)
	require.NoError(t, err)
	require.False(t, normalized.InstantCreditOn())
	require.Equal(t, 5.0, normalized.InstantCreditMinAmount)

	on := true
	patched := CryptoConfigPatch{InstantCreditEnabled: &on}.Apply(normalized)
	normalized, err = NormalizeConfig(patched)
	require.NoError(t, err)
	require.True(t, normalized.InstantCreditOn())
	require.Equal(t, 5.0, normalized.InstantCreditMinAmount, "omitted floor must be preserved")

	// An empty patch never changes the switch.
	normalized, err = NormalizeConfig(CryptoConfigPatch{}.Apply(base))
	require.NoError(t, err)
	require.True(t, normalized.InstantCreditOn())
}

func TestInstantCreditNegativeFloorIsSanitized(t *testing.T) {
	base := DefaultCryptoConfig()
	cfg := CryptoConfigPatch{InstantCreditMinAmount: floatPtr(-3)}.Apply(base)
	normalized, err := NormalizeConfig(cfg)
	require.NoError(t, err)
	require.Zero(t, normalized.InstantCreditMinAmount, "a negative floor must not exclude every deposit")
}

// The deposit page must declare the instant-credit state per network.
func TestDepositInfoDeclaresInstantCredit(t *testing.T) {
	cfg := depositInfoConfig()
	info := buildDepositInfo(cfg, depositInfoAssigned())
	require.NotEmpty(t, info.Chains)
	for _, chain := range info.Chains {
		require.True(t, chain.InstantCredit, "chain %s", chain.Chain)
		require.Nil(t, chain.EtaSeconds, "no measurement yet must serialize as null")
	}

	off := false
	cfg.InstantCreditEnabled = &off
	info = buildDepositInfo(cfg, depositInfoAssigned())
	for _, chain := range info.Chains {
		require.False(t, chain.InstantCredit, "chain %s", chain.Chain)
	}
}

func floatPtr(v float64) *float64 { return &v }
