package service

import (
	"strconv"
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

func TestPageUsageInflightReturnsTen(t *testing.T) {
	rows := make([]UsageInflightSnapshot, 0, 12)
	for i := 0; i < 12; i++ {
		rows = append(rows, UsageInflightSnapshot{RequestID: strconv.Itoa(i)})
	}
	page, total := PageUsageInflight(rows, 1, 100)
	if total != 12 || len(page) != 10 || page[0].RequestID != "0" {
		t.Fatalf("first page %+v total %d", page, total)
	}
	page, total = PageUsageInflight(rows, 2, 10)
	if total != 12 || len(page) != 2 || page[0].RequestID != "10" {
		t.Fatalf("second page %+v", page)
	}
}

func TestFilterUsageInflightMatchesRequestIDFragment(t *testing.T) {
	rows := []UsageInflightSnapshot{{RequestID: "client:fcfb9013-144a-4994-a2e7-f40eb688adc5"}}
	got := filterUsageInflight(rows, usagestats.UsageLogFilters{RequestID: "fcfb9013"}, nil)
	if len(got) != 1 {
		t.Fatalf("fragment should match, got %+v", got)
	}
}

// 落库行带 client:/local: 前缀，调用方 List 用 NormalizeUsageRequestIDKey 归一成裸 id
// 后建 finished，才能和在途行的裸 id 对上；不归一就一条请求出两行。
func TestFilterUsageInflightDropsFinishedBareID(t *testing.T) {
	uuid := "fcfb9013-144a-4994-a2e7-f40eb688adc5"
	rows := []UsageInflightSnapshot{{RequestID: uuid}}
	// 归一后的键命中。
	if got := filterUsageInflight(rows, usagestats.UsageLogFilters{}, map[string]struct{}{NormalizeUsageRequestIDKey("client:" + uuid): {}}); len(got) != 0 {
		t.Fatalf("normalized key should drop inflight row, got %+v", got)
	}
	// 不带前缀的落库行也命中。
	if got := filterUsageInflight(rows, usagestats.UsageLogFilters{}, map[string]struct{}{uuid: {}}); len(got) != 0 {
		t.Fatalf("bare key should drop inflight row, got %+v", got)
	}
}

func TestNormalizeUsageRequestIDKey(t *testing.T) {
	uuid := "fcfb9013-144a-4994-a2e7-f40eb688adc5"
	for _, tc := range []struct{ in, want string }{
		{"client:" + uuid, uuid},
		{"local:" + uuid, uuid},
		{"  client:" + uuid + " ", uuid},
		{"web_search:" + uuid, "web_search:" + uuid},
		{uuid, uuid},
	} {
		if got := NormalizeUsageRequestIDKey(tc.in); got != tc.want {
			t.Fatalf("NormalizeUsageRequestIDKey(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}
