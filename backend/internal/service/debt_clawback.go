package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"

	"entgo.io/ent/dialect"
	dbent "github.com/Wei-Shaw/sub2api/ent"
)

// 历史负余额清零后的欠款回收（2026-11-21）。
//
// 2026-11-21 把生产上负余额用户的 users.balance 清零（快照表
// users_negative_balance_backup_20261121 + 落账表 negative_balance_writeoffs）。
// 清零后的欠款不再以负余额形式挂在 users.balance 上，而是搬进 user_debt：
// 用户页面/接口看到的余额始终是可用余额，欠款只在服务端内部回收。
//
// 任何一次充值到账（支付履约、兑换码、管理员加余额）都在同一事务里先抵扣
// user_debt，抵扣部分不进可用余额，流水写 user_debt_clawbacks 供对账。
//
// 开关：settings 表 key = "billing.debt_clawback_enabled"，值为 "false" 时关闭。
// 每次充值直接读库（不缓存），改这一行即时生效，便于回滚。

const (
	// SettingKeyDebtClawbackEnabled 欠款回收总开关。默认开启；"false" 关闭。
	SettingKeyDebtClawbackEnabled = "billing.debt_clawback_enabled"

	// DebtClawbackSourceRedeemCode 充值到账（支付履约 / 兑换码）触发的抵扣。
	DebtClawbackSourceRedeemCode = "redeem_code"
	// DebtClawbackSourceAdminAdjust 管理员给用户加余额触发的抵扣。
	DebtClawbackSourceAdminAdjust = "admin_adjust"

	// debtClawbackScale 金额精度（对齐 users.balance / user_debt 的 NUMERIC(20,8)）。
	debtClawbackScale = 1e8
	// debtClawbackSourceIDMaxLen 对齐 user_debt_clawbacks.source_id 的 VARCHAR(128)。
	debtClawbackSourceIDMaxLen = 128
)

// debtClawbackEnabled 读取开关；settings 不可读或没配都按开启处理，
// 宁可继续收欠款，也不因为配置表抖动放过。
func debtClawbackEnabled(ctx context.Context, client *dbent.Client) bool {
	rows, err := client.QueryContext(ctx,
		`SELECT value FROM settings WHERE key = $1`, SettingKeyDebtClawbackEnabled)
	if err != nil {
		return true
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return true
	}
	var raw string
	if err := rows.Scan(&raw); err != nil {
		return true
	}
	return parseDebtClawbackEnabled(raw)
}

// parseDebtClawbackEnabled 只认 "false" 为关闭；其它值（含空、脏数据）都视为开启。
func parseDebtClawbackEnabled(raw string) bool {
	return strings.TrimSpace(strings.ToLower(raw)) != "false"
}

// payDownUserDebt 在充值到账的同一事务里，用本次到账金额抵扣该用户的历史欠款。
//
// 返回本次实际抵扣金额；调用方只能把 (到账金额 - 返回值) 加进用户可用余额。
// 必须在调用方的事务 context 内执行（txCtx），保证「扣欠款」和「加余额」
// 同时提交或同时回滚，不会出现到账了但欠款没扣、或欠款扣了但没到账。
//
// 欠款台账表不存在时（迁移还没 apply 的老库、sqlite 单测库）静默跳过，
// 不影响充值主流程；表存在后自动开始回收。
func payDownUserDebt(ctx context.Context, client *dbent.Client, userID int64, creditAmount float64, source, sourceID string) (float64, error) {
	if client == nil || userID <= 0 || creditAmount <= 0 {
		return 0, nil
	}
	if clientDialect(client) != dialect.Postgres {
		// 欠款台账只随 PostgreSQL 迁移创建；sqlite 单测库没有这两张表。
		return 0, nil
	}
	if !debtClawbackEnabled(ctx, client) {
		return 0, nil
	}

	dbClient := client
	if tx := dbent.TxFromContext(ctx); tx != nil {
		dbClient = tx.Client()
	}

	outstanding, err := lockUserDebt(ctx, dbClient, userID)
	if err != nil {
		if isMissingDebtTableError(err) {
			slog.Debug("user_debt table unavailable, skip debt clawback",
				"user_id", userID, "err", err.Error())
			return 0, nil
		}
		return 0, fmt.Errorf("load user debt: %w", err)
	}

	applied := planDebtClawback(creditAmount, outstanding)
	if applied <= 0 {
		return 0, nil
	}

	// 先扣欠款再返回，调用方随后只入账剩余部分；FOR UPDATE 已锁住这一行，
	// 同一用户的并发充值会串行抵扣，不会重复收同一笔欠款。
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE user_debt
		SET amount = GREATEST(amount - $2::numeric, 0), updated_at = NOW()
		WHERE user_id = $1
	`, userID, applied); err != nil {
		return 0, fmt.Errorf("reduce user debt: %w", err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO user_debt_clawbacks (user_id, amount, credit_amount, source, source_id)
		VALUES ($1, $2::numeric, $3::numeric, $4, $5)
	`, userID, applied, creditAmount, source, clampDebtClawbackSourceID(sourceID)); err != nil {
		return 0, fmt.Errorf("record debt clawback: %w", err)
	}
	return applied, nil
}

// lockUserDebt 锁定并读取用户尚未收回的欠款；没有欠款行返回 0。
func lockUserDebt(ctx context.Context, client *dbent.Client, userID int64) (float64, error) {
	rows, err := client.QueryContext(ctx, `
		SELECT amount FROM user_debt WHERE user_id = $1 FOR UPDATE
	`, userID)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return 0, rows.Err()
	}
	var amount float64
	if err := rows.Scan(&amount); err != nil {
		return 0, err
	}
	return amount, rows.Err()
}

// planDebtClawback 计算本次到账里应优先抵扣的金额：min(到账, 欠款)，按 8 位小数取整。
func planDebtClawback(creditAmount, outstandingDebt float64) float64 {
	if creditAmount <= 0 || outstandingDebt <= 0 {
		return 0
	}
	if math.IsNaN(creditAmount) || math.IsNaN(outstandingDebt) ||
		math.IsInf(creditAmount, 0) || math.IsInf(outstandingDebt, 0) {
		return 0
	}
	applied := math.Min(creditAmount, outstandingDebt)
	applied = math.Round(applied*debtClawbackScale) / debtClawbackScale
	if applied > creditAmount {
		applied = creditAmount
	}
	if applied < 0 {
		return 0
	}
	return applied
}

// isMissingDebtTableError 识别「欠款台账表不存在」类错误，用于迁移未 apply 时放行充值。
func isMissingDebtTableError(err error) bool {
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "42p01") || strings.Contains(msg, "no such table") {
		return true
	}
	return strings.Contains(msg, "does not exist") && strings.Contains(msg, "user_debt")
}

// clampDebtClawbackSourceID 按字符截断，避免超出 source_id 字段长度导致整笔充值回滚。
func clampDebtClawbackSourceID(sourceID string) string {
	runes := []rune(sourceID)
	if len(runes) <= debtClawbackSourceIDMaxLen {
		return sourceID
	}
	return string(runes[:debtClawbackSourceIDMaxLen])
}
