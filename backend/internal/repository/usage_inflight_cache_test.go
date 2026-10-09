package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestUsageInflightCacheRoundTrip(t *testing.T) {
	server := miniredis.RunT(t)
	cache := NewUsageInflightCache(redis.NewClient(&redis.Options{Addr: server.Addr()}))
	ctx := context.Background()
	first := 40
	snap := service.UsageInflightSnapshot{
		RequestID: "req-1", UserID: 9, APIKeyID: 3, Model: "deepseek-v4",
		StartedAt: time.Now().Add(-time.Second), FirstTokenMs: &first,
		InputTokens: 12, OutputTokens: 4, ReservedAmount: 1.25, Email: "a@b.c",
	}
	if err := cache.Save(ctx, snap); err != nil {
		t.Fatal(err)
	}
	rows, err := cache.ListUser(ctx, 9)
	if err != nil || len(rows) != 1 || rows[0].ReservedAmount != 1.25 || rows[0].FirstTokenMs == nil || *rows[0].FirstTokenMs != 40 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	all, err := cache.ListAll(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("all=%+v err=%v", all, err)
	}
	if err := cache.Delete(ctx, 9, "req-1"); err != nil {
		t.Fatal(err)
	}
	rows, err = cache.ListUser(ctx, 9)
	if err != nil || len(rows) != 0 {
		t.Fatalf("after delete rows=%+v err=%v", rows, err)
	}
	if err := cache.Save(ctx, snap); err != nil {
		t.Fatal(err)
	}
	rows, _ = cache.ListUser(ctx, 9)
	if len(rows) != 0 {
		t.Fatalf("tombstone should block rewrite, got %+v", rows)
	}
}
