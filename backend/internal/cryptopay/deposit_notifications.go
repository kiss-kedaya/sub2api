package cryptopay

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	"github.com/shopspring/decimal"
)

// Tiny rejected token transfers are commonly address-poisoning dust. Keep the
// ledger for operators, without presenting each probe as a failed user deposit.
// This ceiling changes presentation only, never token acceptance or crediting.
const depositDustNoiseThreshold = "0.0001"

func depositDustNoiseCeiling(minAmount float64) decimal.Decimal {
	if minAmount <= 0 || math.IsNaN(minAmount) || math.IsInf(minAmount, 0) {
		return decimal.Zero
	}
	return decimal.Min(decimal.RequireFromString(depositDustNoiseThreshold), decimal.NewFromFloat(minAmount))
}

func isDepositDustNoise(t scanner.Transfer, reason string, minAmount float64) bool {
	if reason != "below_min" && !strings.HasPrefix(reason, "below the minimum credit amount (") {
		return false
	}
	if t.Amount == nil || t.Amount.Sign() <= 0 {
		return false
	}
	amount := decimal.NewFromBigInt(t.Amount, -int32(tokenDecimals(t.Chain, t.Token)))
	return amount.LessThan(depositDustNoiseCeiling(minAmount))
}

func (s *Service) notifyUnmatchedDeposit(ctx context.Context, t scanner.Transfer, reason string, cfg CryptoConfig) {
	if isDepositDustNoise(t, reason, cfg.CreditMinAmount) {
		slog.Debug("[cryptopay] rejected deposit dust retained for reconciliation", "chain", t.Chain, "tx", t.TxHash)
		return
	}
	slog.Warn("[cryptopay] unmatched deposit",
		"chain", t.Chain, "tx", t.TxHash, "to", t.To, "reason", reason)
	push := s.getPushNotifier()
	if push == nil {
		return
	}
	symbol := "未知代币"
	for _, token := range watchedTokens[t.Chain] {
		if strings.EqualFold(token.Contract, t.Token) {
			symbol = token.Symbol
			break
		}
	}
	amount := toDisplayAmount(t.Amount, tokenDecimals(t.Chain, t.Token))
	body := fmt.Sprintf("%s 收到 %s %s\n收款地址：%s\n交易：%s\n未入账原因：%s",
		chainDisplayName(t.Chain), amount, symbol, shortAddr(t.To), shortAddr(t.TxHash), unmatchedDepositReason(reason, cfg))
	if err := push.PushToAdmins(ctx, "充值未入账，需核对", body, "deposit"); err != nil {
		slog.Warn("[cryptopay] unmatched deposit push failed", "error", err)
	}
}

func unmatchedDepositReason(reason string, cfg CryptoConfig) string {
	switch {
	case strings.HasPrefix(reason, "below the minimum credit amount ("):
		return fmt.Sprintf("低于最低充值金额（%g USDT）", cfg.CreditMinAmount)
	case strings.HasPrefix(reason, "above the per-deposit maximum ("):
		return fmt.Sprintf("超过单笔充值上限（%g USDT）", cfg.CreditMaxAmount)
	case reason == "address is not assigned to a user":
		return "收款地址未分配或已停用，需人工核对"
	case reason == "reorg_duplicate_credit_manual_review":
		return "交易重组后检测到既有入账，需人工核对以防重复充值"
	default:
		return reason
	}
}
