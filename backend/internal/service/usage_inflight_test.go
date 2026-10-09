package service

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

func TestFilterUsageInflightKeepsLiveRowAndDropsFinished(t *testing.T) {
	now := time.Date(2026, 10, 9, 3, 0, 0, 0, time.UTC)
	rows := []UsageInflightSnapshot{
		{RequestID: "live", UserID: 1, APIKeyID: 7, Model: "deepseek-v4", StartedAt: now.Add(-2 * time.Second), ReservedAmount: 0.2},
		{RequestID: "done", UserID: 1, Model: "deepseek-v4", StartedAt: now},
		{RequestID: "other", UserID: 1, Model: "gpt", StartedAt: now},
	}
	got := filterUsageInflight(rows, usagestats.UsageLogFilters{Model: "deepseek-v4"}, map[string]struct{}{"done": {}})
	if len(got) != 1 || got[0].RequestID != "live" || got[0].ReservedAmount != 0.2 {
		t.Fatalf("got %+v", got)
	}
}

func TestQueryIncludesUsageInflightDefaultsOn(t *testing.T) {
	if !QueryIncludesUsageInflight("") || !QueryIncludesUsageInflight("yes") {
		t.Fatal("empty or invalid should include inflight")
	}
	if QueryIncludesUsageInflight("false") {
		t.Fatal("false should exclude inflight")
	}
}
