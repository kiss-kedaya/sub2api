package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func TestResolveInstanceRechargeTermsFallsBackToGlobal(t *testing.T) {
	cfg := &PaymentConfig{RechargeFeeRate: 3, BalanceRechargeMultiplier: 1.1}
	fee, mult := resolveInstanceRechargeTerms(cfg, nil)
	require.Equal(t, 3.0, fee)
	require.Equal(t, 1.1, mult)
}

func TestResolveInstanceRechargeTermsUsesInstanceOverrides(t *testing.T) {
	cfg := &PaymentConfig{RechargeFeeRate: 3, BalanceRechargeMultiplier: 1.1}
	feeRate := 2.0
	multiplier := 1.02
	sel := &payment.InstanceSelection{
		RechargeFeeRate:           &feeRate,
		BalanceRechargeMultiplier: &multiplier,
	}
	fee, mult := resolveInstanceRechargeTerms(cfg, sel)
	require.Equal(t, 2.0, fee)
	require.Equal(t, 1.02, mult)
}

func TestOptionalFloatUnmarshal(t *testing.T) {
	var omitted optionalFloat
	require.False(t, omitted.Present)

	var inherit optionalFloat
	require.NoError(t, inherit.UnmarshalJSON([]byte("null")))
	require.True(t, inherit.Present)
	require.Nil(t, inherit.Value)

	var set optionalFloat
	require.NoError(t, set.UnmarshalJSON([]byte("2")))
	require.True(t, set.Present)
	require.NotNil(t, set.Value)
	require.Equal(t, 2.0, *set.Value)
}
