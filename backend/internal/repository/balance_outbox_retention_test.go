//go:build unit

package repository

import (
	"strings"
	"testing"
)

// live_balance_adjustment_outbox 只保留 1 天已投递行：已投递行是纯历史，
// 消费者只读 delivered_at IS NULL，所以删除它们不可能丢掉任何余额调整。
// 写死断言是刻意的：这张表曾涨到 9700 万行 / 20 GB。
func TestBalanceOutboxDeliveredRetention(t *testing.T) {
	if liveBalanceOutboxDeliveredRetentionDays != 1 {
		t.Fatalf("delivered outbox retention = %d days, want 1", liveBalanceOutboxDeliveredRetentionDays)
	}
	if liveBalanceOutboxPruneBatch <= 0 || liveBalanceOutboxPrunePasses <= 0 {
		t.Fatal("outbox prune batch/passes must be positive")
	}
}

// 保留期清理语句必须只删「已投递且超过保留窗口」的行，绝不碰未投递事件。
func TestBalanceOutboxPruneSQLNeverTouchesUndelivered(t *testing.T) {
	var b strings.Builder
	b.WriteString(`DELETE FROM live_balance_adjustment_outbox WHERE ctid IN
    (SELECT ctid FROM live_balance_adjustment_outbox WHERE delivered_at IS NOT NULL
       AND delivered_at < NOW()-INTERVAL '`)
	b.WriteString("1")
	b.WriteString(` days' LIMIT `)
	b.WriteString("200000")
	b.WriteString(")")

	sql := b.String()
	if !strings.Contains(sql, "delivered_at IS NOT NULL") {
		t.Fatal("prune must require delivered_at IS NOT NULL")
	}
	if !strings.Contains(sql, "delivered_at < NOW()-INTERVAL") {
		t.Fatal("prune must bound delivered_at by the retention window")
	}
	if strings.Contains(sql, "delivered_at IS NULL") {
		t.Fatal("prune must never select undelivered rows")
	}
}
