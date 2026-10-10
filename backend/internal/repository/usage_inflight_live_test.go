package repository

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestUsageInflightLiveDropsSilentRow(t *testing.T) {
	now := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	old := service.UsageInflightSnapshot{StartedAt: now.Add(-2 * time.Minute), ExpiresAtMs: now.Add(time.Hour).UnixMilli()}
	if usageInflightLive(old, now) {
		t.Fatal("row without a heartbeat should drop")
	}
	fresh := old
	fresh.UpdatedAt = now.Add(-10 * time.Second)
	if !usageInflightLive(fresh, now) {
		t.Fatal("recent heartbeat should stay")
	}
}
