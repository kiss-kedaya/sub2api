package service

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"
)

const (
	liveBalanceOutboxBatchSize        = 1024
	liveBalanceOutboxPerUserBatchSize = 256
	liveBalanceOutboxPollInterval     = 250 * time.Millisecond
	liveBalanceOutboxPollMaxInterval  = 5 * time.Second
	liveBalanceOutboxLease            = 30 * time.Second
	liveBalanceOutboxClaimTimeout     = 5 * time.Second
	liveBalanceOutboxRedisTimeout     = 2 * time.Second
	liveBalanceOutboxConcurrency      = 16
	liveBalanceOutboxRetention        = 24 * time.Hour
	liveBalanceOutboxCleanupInterval  = 5 * time.Minute
	liveBalanceOutboxCleanupBatch     = 20000
	// liveBalanceOutboxCleanupMaxBatches bounds one cleanup tick so deleting a
	// multi-million-row backlog never holds one transaction for minutes. Three
	// batches every five minutes outpaces the ~350k rows/hour production creates.
	liveBalanceOutboxCleanupMaxBatches = 3
	// liveBalanceOutboxMaxAttempts is the hard stop for a poison row. Recoverable
	// conflicts are healed before this is reached; once reached the worker records
	// the error and abandons the row instead of retrying it forever.
	liveBalanceOutboxMaxAttempts = 64
)

const LiveBalanceOutboxMaxBatchSize = liveBalanceOutboxBatchSize
const LiveBalanceOutboxPerUserMaxBatchSize = liveBalanceOutboxPerUserBatchSize

type LiveBalanceAdjustmentEvent struct {
	ID            int64
	UserID        int64
	PredecessorID int64
	DeltaUnits    int64
	Attempts      int
	CreatedAt     time.Time
}

type LiveBalanceInitializationSnapshot struct {
	Balance      float64
	Watermark    int64
	HasUnsettled bool
}

func (e LiveBalanceAdjustmentEvent) RedisEventID() string {
	return fmt.Sprintf("live-balance-outbox:%d", e.ID)
}

type LiveBalanceAdjustmentOutboxStats struct {
	Pending         int64
	Delivered       int64
	OldestCreatedAt *time.Time
	MaxAttempts     int
	LastError       string
}

type LiveBalanceAdjustmentOutboxRepository interface {
	Claim(ctx context.Context, workerID string, limit int, lease time.Duration) ([]LiveBalanceAdjustmentEvent, error)
	MarkDelivered(ctx context.Context, id int64, workerID string) error
	MarkDeliveredBatch(ctx context.Context, ids []int64, workerID string) error
	RetryClaimed(ctx context.Context, id int64, workerID string, availableAt time.Time, lastError string) error
	RetryClaimedBatch(ctx context.Context, ids []int64, workerID string, availableAt time.Time, lastError string) error
	AbandonClaimed(ctx context.Context, id int64, workerID string, reason string) error
	AbandonClaimedBatch(ctx context.Context, ids []int64, workerID string, reason string) error
	DeleteDelivered(ctx context.Context, before time.Time, limit int) (int64, error)
	Stats(ctx context.Context) (LiveBalanceAdjustmentOutboxStats, error)
}

type LiveBalanceAdjustmentOutboxHealth struct {
	Running      bool          `json:"running"`
	Processed    uint64        `json:"processed"`
	Retries      uint64        `json:"retries"`
	Failures     uint64        `json:"failures"`
	Conflicts    uint64        `json:"conflicts"`
	Healed       uint64        `json:"healed"`
	HealFailures uint64        `json:"heal_failures"`
	Abandoned    uint64        `json:"abandoned"`
	Cleaned      uint64        `json:"cleaned"`
	Pending      int64         `json:"pending"`
	Delivered    int64         `json:"delivered"`
	OldestLag    time.Duration `json:"oldest_lag"`
	MaxAttempts  int           `json:"max_attempts"`
	LastError    string        `json:"last_error,omitempty"`
	StatsError   string        `json:"stats_error,omitempty"`
}

type liveBalanceRecoveryStatsProvider interface {
	RecoveryStats() LiveBalanceAdjustmentRecoveryStats
}

type liveBalanceAdjustmentApplier interface {
	ApplyExternalBalanceOutboxAdjustment(ctx context.Context, event LiveBalanceAdjustmentEvent) error
}

func (s *OpsService) GetLiveBalanceAdjustmentOutboxHealth(ctx context.Context) LiveBalanceAdjustmentOutboxHealth {
	if s == nil || s.liveBalanceOutboxWorker == nil {
		return LiveBalanceAdjustmentOutboxHealth{}
	}
	return s.liveBalanceOutboxWorker.Health(ctx)
}

func liveBalanceOutboxRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 9 {
		attempt = 9
	}
	base := time.Second * time.Duration(1<<(attempt-1))
	return time.Duration(float64(base) * (0.8 + rand.Float64()*0.4))
}

func boundedLiveBalanceOutboxError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > 1024 {
		return message[:1024]
	}
	return message
}
