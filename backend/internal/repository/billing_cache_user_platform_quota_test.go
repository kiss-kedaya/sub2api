//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func newMiniRedisCache(t *testing.T) (*billingCache, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return &billingCache{rdb: rdb}, mr
}

func TestUserPlatformQuotaCache_GetMissReturnsNotFound(t *testing.T) {
	c, _ := newMiniRedisCache(t)
	entry, ok, err := c.GetUserPlatformQuotaCache(context.Background(), 1, "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if ok || entry != nil {
		t.Errorf("expected miss, got ok=%v entry=%v", ok, entry)
	}
}

func TestUserPlatformQuotaCache_SetThenGet(t *testing.T) {
	c, _ := newMiniRedisCache(t)
	ctx := context.Background()
	dailyLimit := 20.0
	ts := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	in := &service.UserPlatformQuotaCacheEntry{
		DailyUsageUSD:    1.5,
		WeeklyUsageUSD:   3.0,
		MonthlyUsageUSD:  10.0,
		Version:          7,
		SchemaVersion:    service.UserPlatformQuotaCacheSchemaV1,
		DailyLimitUSD:    &dailyLimit,
		DailyWindowStart: &ts,
	}
	if err := c.SetUserPlatformQuotaCache(ctx, 1, "openai", in, time.Minute); err != nil {
		t.Fatal(err)
	}
	got, ok, err := c.GetUserPlatformQuotaCache(ctx, 1, "openai")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.DailyUsageUSD != 1.5 || got.WeeklyUsageUSD != 3.0 || got.MonthlyUsageUSD != 10.0 || got.Version != 7 {
		t.Errorf("got = %+v, want %+v", got, in)
	}
	if got.SchemaVersion != service.UserPlatformQuotaCacheSchemaV1 {
		t.Errorf("SchemaVersion = %d, want %d", got.SchemaVersion, service.UserPlatformQuotaCacheSchemaV1)
	}
	if got.DailyLimitUSD == nil || *got.DailyLimitUSD != dailyLimit {
		t.Errorf("DailyLimitUSD = %v, want %v", got.DailyLimitUSD, dailyLimit)
	}
	if got.DailyWindowStart == nil || !got.DailyWindowStart.Equal(ts) {
		t.Errorf("DailyWindowStart = %v, want %v", got.DailyWindowStart, ts)
	}
}

func TestUserPlatformQuotaCache_NilLimitSetThenGet(t *testing.T) {
	c, _ := newMiniRedisCache(t)
	ctx := context.Background()
	in := &service.UserPlatformQuotaCacheEntry{
		DailyUsageUSD: 1.0,
		SchemaVersion: service.UserPlatformQuotaCacheSchemaV1,
		// DailyLimitUSD nil → 无限额
	}
	if err := c.SetUserPlatformQuotaCache(ctx, 1, "openai", in, time.Minute); err != nil {
		t.Fatal(err)
	}
	got, ok, err := c.GetUserPlatformQuotaCache(ctx, 1, "openai")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.DailyLimitUSD != nil {
		t.Errorf("DailyLimitUSD should be nil for unlimited, got %v", got.DailyLimitUSD)
	}
}

func TestUserPlatformQuotaCache_IncrMissIsNoop(t *testing.T) {
	c, _ := newMiniRedisCache(t)
	if err := c.IncrUserPlatformQuotaUsageCache(context.Background(), 1, "openai", 0.5, time.Minute, false); err != nil {
		t.Fatal(err)
	}
	_, ok, _ := c.GetUserPlatformQuotaCache(context.Background(), 1, "openai")
	if ok {
		t.Error("expected key absent after no-op incr")
	}
}

func TestUserPlatformQuotaCache_IncrHitAccumulates(t *testing.T) {
	c, _ := newMiniRedisCache(t)
	ctx := context.Background()
	// SchemaVersion 必须显式设为 V1,否则 Lua 脚本会因 schema 不匹配而 return 0,跳过累加。
	_ = c.SetUserPlatformQuotaCache(ctx, 1, "openai", &service.UserPlatformQuotaCacheEntry{
		Version:       1,
		SchemaVersion: service.UserPlatformQuotaCacheSchemaV1,
	}, time.Minute)
	if err := c.IncrUserPlatformQuotaUsageCache(ctx, 1, "openai", 0.5, time.Minute, false); err != nil {
		t.Fatal(err)
	}
	if err := c.IncrUserPlatformQuotaUsageCache(ctx, 1, "openai", 0.25, time.Minute, false); err != nil {
		t.Fatal(err)
	}
	got, _, _ := c.GetUserPlatformQuotaCache(ctx, 1, "openai")
	if got.DailyUsageUSD != 0.75 || got.WeeklyUsageUSD != 0.75 || got.MonthlyUsageUSD != 0.75 {
		t.Errorf("got %+v, want daily/weekly/monthly=0.75", got)
	}
	if got.Version != 3 {
		t.Errorf("version = %d, want 3 (initial 1 + 2 incr)", got.Version)
	}
}

func TestUserPlatformQuotaCache_Delete(t *testing.T) {
	c, _ := newMiniRedisCache(t)
	ctx := context.Background()
	_ = c.SetUserPlatformQuotaCache(ctx, 1, "openai", &service.UserPlatformQuotaCacheEntry{Version: 1}, time.Minute)
	if err := c.DeleteUserPlatformQuotaCache(ctx, 1, "openai"); err != nil {
		t.Fatal(err)
	}
	_, ok, _ := c.GetUserPlatformQuotaCache(ctx, 1, "openai")
	if ok {
		t.Error("expected miss after delete")
	}
}

func TestBillingCache_PackageLevelScriptsUseEvalSHA(t *testing.T) {
	c, mr := newMiniRedisCache(t)
	t.Cleanup(func() { _ = c.rdb.Close() })
	hook := &redisCommandRecorderHook{}
	c.rdb.AddHook(hook)
	ctx := context.Background()

	// 1. SetUserBalanceIfLower: 空键写入、更低覆盖、更高/相等保留，且预热后复走 EVALSHA。
	if err := c.SetUserBalanceIfLower(ctx, 101, 50.0); err != nil {
		t.Fatalf("first SetUserBalanceIfLower: %v", err)
	}
	bal, err := c.GetUserBalance(ctx, 101)
	if err != nil || bal != 50.0 {
		t.Fatalf("got bal=%v err=%v, want 50.0", bal, err)
	}

	hook.reset()
	if err := c.SetUserBalanceIfLower(ctx, 101, 35.5); err != nil {
		t.Fatalf("lower SetUserBalanceIfLower: %v", err)
	}
	if err := c.SetUserBalanceIfLower(ctx, 101, 40.0); err != nil {
		t.Fatalf("higher SetUserBalanceIfLower: %v", err)
	}
	if err := c.SetUserBalanceIfLower(ctx, 101, 35.5); err != nil {
		t.Fatalf("equal SetUserBalanceIfLower: %v", err)
	}
	if gotEval := hook.countCommand("eval"); gotEval != 0 {
		t.Fatalf("SetUserBalanceIfLower must reuse EVALSHA without raw EVAL, got %d eval calls", gotEval)
	}
	if gotEvalSHA := hook.countCommand("evalsha"); gotEvalSHA != 3 {
		t.Fatalf("expected 3 evalsha calls, got %d", gotEvalSHA)
	}
	bal, err = c.GetUserBalance(ctx, 101)
	if err != nil || bal != 35.5 {
		t.Fatalf("got bal=%v err=%v, want 35.5", bal, err)
	}

	// 2. IncrUserPlatformQuotaUsageCache: 包级 redis.NewScript 预热后复走 EVALSHA，不发裸 EVAL。
	if err := c.SetUserPlatformQuotaCache(ctx, 101, "openai", &service.UserPlatformQuotaCacheEntry{
		Version:       1,
		SchemaVersion: service.UserPlatformQuotaCacheSchemaV1,
	}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := c.IncrUserPlatformQuotaUsageCache(ctx, 101, "openai", 0.5, time.Minute, true); err != nil {
		t.Fatalf("warmup IncrUserPlatformQuotaUsageCache: %v", err)
	}
	hook.reset()
	if err := c.IncrUserPlatformQuotaUsageCache(ctx, 101, "openai", 0.75, time.Minute, true); err != nil {
		t.Fatalf("second IncrUserPlatformQuotaUsageCache: %v", err)
	}
	if gotEval := hook.countCommand("eval"); gotEval != 0 {
		t.Fatalf("IncrUserPlatformQuotaUsageCache must reuse EVALSHA without raw EVAL, got %d eval calls", gotEval)
	}
	if gotEvalSHA := hook.countCommand("evalsha"); gotEvalSHA != 1 {
		t.Fatalf("expected 1 evalsha call, got %d", gotEvalSHA)
	}
	got, ok, err := c.GetUserPlatformQuotaCache(ctx, 101, "openai")
	if err != nil || !ok {
		t.Fatalf("get quota cache: ok=%v err=%v", ok, err)
	}
	if got.DailyUsageUSD != 1.25 || got.Version != 3 {
		t.Fatalf("got %+v, want usage=1.25 version=3", got)
	}
	isDirty, err := mr.IsMember(userPlatformQuotaDirtySetKey(), "101:openai")
	if err != nil || !isDirty {
		t.Fatalf("expected dirty set membership, got isDirty=%v err=%v", isDirty, err)
	}
}
