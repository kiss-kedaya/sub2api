//go:build unit

package repository

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// live_balance_adjustment_outbox 的已投递清理归 LiveBalanceAdjustmentOutboxWorker
// 独占（每 5 分钟通过 DeleteDelivered 清理 1 天前已投递行），usage_connections.go
// 不得重复扫描或删除同一张表，避免多路清理撞锁。
func TestUsageConnectionsDoesNotDuplicateBalanceOutboxPruning(t *testing.T) {
	raw, err := os.ReadFile("usage_connections.go")
	require.NoError(t, err)
	require.NotContains(t, string(raw), "DELETE FROM live_balance_adjustment_outbox")
}

// 保留期清理语句必须只删「已投递且超过保留窗口」的行，绝不碰未投递事件。
func TestBalanceOutboxPruneSQLNeverTouchesUndelivered(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(expected, actual string) error {
		if !strings.Contains(actual, "delivered_at IS NOT NULL") {
			t.Fatalf("DeleteDelivered must require delivered_at IS NOT NULL: %s", actual)
		}
		if !strings.Contains(actual, "delivered_at < $1") {
			t.Fatalf("DeleteDelivered must bound delivered_at by retention cutoff: %s", actual)
		}
		if strings.Contains(actual, "delivered_at IS NULL") {
			t.Fatalf("DeleteDelivered must never touch undelivered rows: %s", actual)
		}
		return nil
	})))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	before := time.Now().UTC().Add(-24 * time.Hour)
	mock.ExpectExec("DELETE FROM live_balance_adjustment_outbox").
		WithArgs(before, 10000).
		WillReturnResult(sqlmock.NewResult(0, 5))

	repo := &liveBalanceAdjustmentOutboxRepository{db: db}
	deleted, err := repo.DeleteDelivered(context.Background(), before, 0)
	require.NoError(t, err)
	require.EqualValues(t, 5, deleted)
	require.NoError(t, mock.ExpectationsWereMet())
}
