package cryptopay

import (
	"context"
	"math/big"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

type capturedDepositPush struct{ title, body, group string }

func (p *capturedDepositPush) PushToAdmins(_ context.Context, title, body, group string) error {
	p.title, p.body, p.group = title, body, group
	return nil
}

func TestRejectedDepositDustNoiseUsesExactAmountAndCurrentMinimum(t *testing.T) {
	for _, test := range []struct {
		name, amount, reason string
		minimum              float64
		quiet                bool
	}{
		{"tiny probe", "0.000001", "below_min", 0.001, true},
		{"below fixed boundary", "0.00009999", "below_min", 0.001, true},
		{"fixed boundary remains actionable", "0.0001", "below_min", 0.001, false},
		{"ordinary small deposit remains actionable", "0.1", "below_min", 1, false},
		{"at current minimum", "0.00000001", "below_min", 0.00000001, false},
		{"below current minimum", "0.000000009", "below_min", 0.00000001, true},
		{"current minimum disabled", "0.000001", "below_min", 0, false},
		{"another rejection must alert", "0.000001", "address is not assigned to a user", 1, false},
		{"normal deposit is not dust", "10", "", 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			amount := decimal.RequireFromString(test.amount).Shift(18).BigInt()
			transfer := scanner.Transfer{Chain: "bsc", Token: "0x55d398326f99059ff775485246999027b3197955", Amount: amount}
			require.Equal(t, test.quiet, isDepositDustNoise(transfer, test.reason, test.minimum))
		})
	}
}

func TestRejectedDepositDustRemainsRecordedWithoutOneBarkPerProbe(t *testing.T) {
	push := &capturedDepositPush{}
	svc := &Service{}
	svc.SetPushNotifier(push)
	svc.notifyUnmatchedDeposit(t.Context(), scanner.Transfer{Chain: "bsc",
		Token: "0x55d398326f99059ff775485246999027b3197955", Amount: big.NewInt(1_000_000_000_000)},
		"below the minimum credit amount (0.001)", CryptoConfig{CreditMinAmount: 0.001})
	require.Empty(t, push.title)
}

func TestUnmatchedDepositAlertIdentifiesAssetTransactionAndReason(t *testing.T) {
	for _, asset := range []struct {
		token, symbol string
		decimals      int
	}{
		{"0x55d398326f99059ff775485246999027b3197955", "USDT", 18},
		{"0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d", "USDC", 18},
	} {
		t.Run(asset.symbol, func(t *testing.T) {
			push := &capturedDepositPush{}
			svc := &Service{}
			svc.SetPushNotifier(push)
			hash := "0x" + strings.Repeat("ab", 32)
			amount := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(asset.decimals-1)), nil)
			svc.notifyUnmatchedDeposit(t.Context(), scanner.Transfer{Chain: "bsc", Token: asset.token,
				Amount: amount, To: "0x1111111111111111111111111111111111111111", TxHash: hash},
				"below the minimum credit amount (1)", CryptoConfig{CreditMinAmount: 1})
			require.Equal(t, "充值未入账，需核对", push.title)
			require.Equal(t, "deposit", push.group)
			require.Contains(t, push.body, "0.1 "+asset.symbol)
			require.Contains(t, push.body, shortAddr(hash))
			require.Contains(t, push.body, "低于最低充值金额（1 USDT）")
		})
	}
}
