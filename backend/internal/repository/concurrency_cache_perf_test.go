package repository

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type redisCommandRecorderHook struct {
	mu          sync.Mutex
	roundTrips  int
	commands    []string
	zaddTargets []string
}

func (h *redisCommandRecorderHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *redisCommandRecorderHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		name := strings.ToLower(cmd.Name())
		h.mu.Lock()
		h.roundTrips++
		h.commands = append(h.commands, name)
		if name == "zadd" && len(cmd.Args()) >= 2 {
			if key, ok := cmd.Args()[1].(string); ok {
				h.zaddTargets = append(h.zaddTargets, key)
			}
		}
		h.mu.Unlock()
		return next(ctx, cmd)
	}
}

func (h *redisCommandRecorderHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.mu.Lock()
		h.roundTrips++
		for _, cmd := range cmds {
			h.commands = append(h.commands, strings.ToLower(cmd.Name()))
		}
		h.mu.Unlock()
		return next(ctx, cmds)
	}
}

func (h *redisCommandRecorderHook) reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.roundTrips = 0
	h.commands = nil
	h.zaddTargets = nil
}

func (h *redisCommandRecorderHook) snapshot() (int, []string, []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cmds := append([]string(nil), h.commands...)
	zadds := append([]string(nil), h.zaddTargets...)
	return h.roundTrips, cmds, zadds
}

func (h *redisCommandRecorderHook) countCommand(name string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, cmd := range h.commands {
		if cmd == name {
			n++
		}
	}
	return n
}

func newRecordedConcurrencyCache(t *testing.T, slotTTLMinutes, waitQueueTTLSeconds int) (*miniredis.Miniredis, *redis.Client, *concurrencyCache, *redisCommandRecorderHook) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	hook := &redisCommandRecorderHook{}
	rdb.AddHook(hook)
	cache, ok := NewConcurrencyCache(rdb, slotTTLMinutes, waitQueueTTLSeconds).(*concurrencyCache)
	require.True(t, ok)
	return mr, rdb, cache, hook
}

func TestConcurrencyCache_TouchActiveIndexThrottleAndEviction(t *testing.T) {
	// 使用真实生产参数（slotTTL=15m=900s，waitQueueTTL=120s），确认较短的 waitQueueTTL
	// 不会把槽位活跃索引的 7 分钟节流窗口误压缩为 60s。
	mr, rdb, cache, hook := newRecordedConcurrencyCache(t, 15, 120)
	ctx := context.Background()

	base := time.Unix(1750000000, 0).UTC()
	mr.SetTime(base)

	// 预热脚本（避免首次 NOSCRIPT->EVAL 回退计入稳态 RTT）。
	_, err := cache.AcquireAccountSlot(ctx, 9998, 2, "warmup-a")
	require.NoError(t, err)
	require.NoError(t, cache.ReleaseAccountSlot(ctx, 9998, "warmup-a"))

	// 首次占槽：严格 1 RTT，单脚本原子写入活跃索引。
	hook.reset()
	ok, err := cache.AcquireAccountSlot(ctx, 100, 10, "req-1")
	require.NoError(t, err)
	require.True(t, ok)
	rtts, cmds, _ := hook.snapshot()
	require.Equal(t, 1, rtts, "cold AcquireAccountSlot must take 1 RTT, cmds=%v", cmds)
	require.Equal(t, []string{"evalsha"}, cmds)

	score1, err := rdb.ZScore(ctx, accountActiveIndexKey, "100").Result()
	require.NoError(t, err)
	require.Equal(t, float64(base.Unix()+900), score1)

	// 6m59s 内重复占槽（即便已远超 waitQueueTTL/2 = 60s）：活跃索引 score 保持不变（跳过冗余 ZADD），且严格 1 RTT。
	mr.SetTime(base.Add(6*time.Minute + 59*time.Second))
	for _, reqID := range []string{"req-2", "req-3", "req-4"} {
		hook.reset()
		ok, err = cache.AcquireAccountSlot(ctx, 100, 10, reqID)
		require.NoError(t, err)
		require.True(t, ok)
		rtts, cmds, _ = hook.snapshot()
		require.Equal(t, 1, rtts, "throttled AcquireAccountSlot must take 1 RTT, cmds=%v", cmds)
		require.Equal(t, []string{"evalsha"}, cmds)
	}
	scoreThrottled, err := rdb.ZScore(ctx, accountActiveIndexKey, "100").Result()
	require.NoError(t, err)
	require.Equal(t, score1, scoreThrottled, "slot active index score must remain unchanged within 7m even when waitQueueTTL=120s")

	// 期间若等待队列递增（120s TTL），不能把已有更晚的槽位 score（base+900）往回降级。
	ok, err = cache.IncrementAccountWaitCount(ctx, 100, 5)
	require.NoError(t, err)
	require.True(t, ok)
	scoreAfterWait, err := rdb.ZScore(ctx, accountActiveIndexKey, "100").Result()
	require.NoError(t, err)
	require.Equal(t, score1, scoreAfterWait, "shorter wait TTL must never downgrade existing slot active index score")
	require.NoError(t, cache.DecrementAccountWaitCount(ctx, 100))

	// 满 7 分钟节流窗口后，下一次占槽在同一次 1 RTT Lua 中刷新活跃索引 score。
	t7 := base.Add(7 * time.Minute)
	mr.SetTime(t7)
	hook.reset()
	ok, err = cache.AcquireAccountSlot(ctx, 100, 10, "req-5")
	require.NoError(t, err)
	require.True(t, ok)
	rtts, cmds, _ = hook.snapshot()
	require.Equal(t, 1, rtts, "7m refresh AcquireAccountSlot must still take 1 RTT, cmds=%v", cmds)
	require.Equal(t, []string{"evalsha"}, cmds)

	score2, err := rdb.ZScore(ctx, accountActiveIndexKey, "100").Result()
	require.NoError(t, err)
	require.Equal(t, float64(t7.Unix()+900), score2)

	// 释放部分槽位（仍有剩余槽位）：单 RTT，活跃索引保留且 score 不被冗余改写。
	for _, reqID := range []string{"req-1", "req-2", "req-3", "req-4"} {
		hook.reset()
		require.NoError(t, cache.ReleaseAccountSlot(ctx, 100, reqID))
		rtts, cmds, _ = hook.snapshot()
		require.Equal(t, 1, rtts, "partial release must take 1 RTT, cmds=%v", cmds)
		require.Equal(t, []string{"evalsha"}, cmds)
	}
	scorePartial, err := rdb.ZScore(ctx, accountActiveIndexKey, "100").Result()
	require.NoError(t, err)
	require.Equal(t, score2, scorePartial)

	// 释放最后一个槽位：Lua 原子从 active_index 移除。
	require.NoError(t, cache.ReleaseAccountSlot(ctx, 100, "req-5"))
	_, err = rdb.ZScore(ctx, accountActiveIndexKey, "100").Result()
	require.ErrorIs(t, err, redis.Nil, "active index member must be removed when last slot releases")

	// 串行单并发场景立刻再次占槽：单 RTT 原子重新写回 active_index（无额外 ZADD RTT）。
	hook.reset()
	ok, err = cache.AcquireAccountSlot(ctx, 100, 10, "req-6")
	require.NoError(t, err)
	require.True(t, ok)
	rtts, cmds, _ = hook.snapshot()
	require.Equal(t, 1, rtts, "re-acquiring after empty release must take 1 RTT, cmds=%v", cmds)
	require.Equal(t, []string{"evalsha"}, cmds)
	score3, err := rdb.ZScore(ctx, accountActiveIndexKey, "100").Result()
	require.NoError(t, err)
	require.Equal(t, float64(t7.Unix()+900), score3)
}

func TestConcurrencyCache_MultiInstanceReleaseReRegistersActiveIndex(t *testing.T) {
	mr := miniredis.RunT(t)
	rdbA := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	rdbB := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		_ = rdbA.Close()
		_ = rdbB.Close()
	})

	cacheA := NewConcurrencyCache(rdbA, 15, 120)
	cacheB := NewConcurrencyCache(rdbB, 15, 120)
	ctx := context.Background()

	base := time.Unix(1750000500, 0).UTC()
	mr.SetTime(base)

	// 实例 A 与 B 同时占槽 -> A 先释放（B 仍持有，索引保留）-> B 释放归零（删除共享索引成员）。
	ok, err := cacheA.AcquireAccountSlot(ctx, 77, 5, "inst-a-1")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = cacheB.AcquireAccountSlot(ctx, 77, 5, "inst-b-1")
	require.NoError(t, err)
	require.True(t, ok)

	require.NoError(t, cacheA.ReleaseAccountSlot(ctx, 77, "inst-a-1"))
	_, err = rdbA.ZScore(ctx, accountActiveIndexKey, "77").Result()
	require.NoError(t, err)

	require.NoError(t, cacheB.ReleaseAccountSlot(ctx, 77, "inst-b-1"))
	_, err = rdbA.ZScore(ctx, accountActiveIndexKey, "77").Result()
	require.ErrorIs(t, err, redis.Nil)

	// 10 秒后实例 A 再次占槽：必须立即重新登记到共享活跃索引，不存在跨实例节流盲窗。
	mr.SetTime(base.Add(10 * time.Second))
	ok, err = cacheA.AcquireAccountSlot(ctx, 77, 5, "inst-a-2")
	require.NoError(t, err)
	require.True(t, ok)
	score, err := rdbA.ZScore(ctx, accountActiveIndexKey, "77").Result()
	require.NoError(t, err, "instance A must re-register active index after instance B removed it")
	require.Equal(t, float64(base.Add(10*time.Second).Unix()+900), score)
}

func TestConcurrencyCache_TouchActiveIndexShortTTLScalesThrottleWindow(t *testing.T) {
	mr, rdb, cache, hook := newRecordedConcurrencyCache(t, 2, 120) // 120s slot & wait TTL -> 60s throttle
	ctx := context.Background()

	base := time.Unix(1750001000, 0).UTC()
	mr.SetTime(base)

	ok, err := cache.AcquireUserSlot(ctx, 55, 5, "u-1")
	require.NoError(t, err)
	require.True(t, ok)
	score1, err := rdb.ZScore(ctx, userActiveIndexKey, "55").Result()
	require.NoError(t, err)
	require.Equal(t, float64(base.Unix()+120), score1)

	// 59s 内仍节流，score 不变
	mr.SetTime(base.Add(59 * time.Second))
	hook.reset()
	ok, err = cache.AcquireUserSlot(ctx, 55, 5, "u-2")
	require.NoError(t, err)
	require.True(t, ok)
	rtts, cmds, _ := hook.snapshot()
	require.Equal(t, 1, rtts, "cmds=%v", cmds)
	scoreMid, err := rdb.ZScore(ctx, userActiveIndexKey, "55").Result()
	require.NoError(t, err)
	require.Equal(t, score1, scoreMid)

	// 60s (= ttl/2) 触发刷新
	t60 := base.Add(60 * time.Second)
	mr.SetTime(t60)
	ok, err = cache.AcquireUserSlot(ctx, 55, 5, "u-3")
	require.NoError(t, err)
	require.True(t, ok)
	score2, err := rdb.ZScore(ctx, userActiveIndexKey, "55").Result()
	require.NoError(t, err)
	require.Equal(t, float64(t60.Unix()+120), score2)
}

func TestConcurrencyCache_WaitQueueIncrementAndDecrementSingleRTT(t *testing.T) {
	mr, rdb, cache, hook := newRecordedConcurrencyCache(t, 15, 120)
	ctx := context.Background()

	base := time.Unix(1750001500, 0).UTC()
	mr.SetTime(base)

	// 预热等待脚本
	_, err := cache.IncrementWaitCount(ctx, 9997, 5)
	require.NoError(t, err)
	require.NoError(t, cache.DecrementWaitCount(ctx, 9997))

	// IncrementWaitCount 严格 1 RTT 并按 120s TTL 登记活跃索引。
	hook.reset()
	ok, err := cache.IncrementWaitCount(ctx, 30, 5)
	require.NoError(t, err)
	require.True(t, ok)
	rtts, cmds, _ := hook.snapshot()
	require.Equal(t, 1, rtts, "IncrementWaitCount must take 1 RTT, cmds=%v", cmds)
	require.Equal(t, []string{"evalsha"}, cmds)
	score, err := rdb.ZScore(ctx, userActiveIndexKey, "30").Result()
	require.NoError(t, err)
	require.Equal(t, float64(base.Unix()+120), score)

	// 59s 内再次递增不刷新 score，60s 触发刷新。
	mr.SetTime(base.Add(59 * time.Second))
	ok, err = cache.IncrementWaitCount(ctx, 30, 5)
	require.NoError(t, err)
	require.True(t, ok)
	scoreMid, err := rdb.ZScore(ctx, userActiveIndexKey, "30").Result()
	require.NoError(t, err)
	require.Equal(t, score, scoreMid)

	t60 := base.Add(60 * time.Second)
	mr.SetTime(t60)
	ok, err = cache.IncrementWaitCount(ctx, 30, 5)
	require.NoError(t, err)
	require.True(t, ok)
	scoreRefreshed, err := rdb.ZScore(ctx, userActiveIndexKey, "30").Result()
	require.NoError(t, err)
	require.Equal(t, float64(t60.Unix()+120), scoreRefreshed)

	// DecrementWaitCount 严格 1 RTT；仍有剩余等待时保留索引，归零后移除索引。
	hook.reset()
	require.NoError(t, cache.DecrementWaitCount(ctx, 30))
	rtts, cmds, _ = hook.snapshot()
	require.Equal(t, 1, rtts, "DecrementWaitCount must take 1 RTT, cmds=%v", cmds)
	require.Equal(t, []string{"evalsha"}, cmds)
	_, err = rdb.ZScore(ctx, userActiveIndexKey, "30").Result()
	require.NoError(t, err)

	require.NoError(t, cache.DecrementWaitCount(ctx, 30))
	hook.reset()
	require.NoError(t, cache.DecrementWaitCount(ctx, 30))
	rtts, cmds, _ = hook.snapshot()
	require.Equal(t, 1, rtts, "final DecrementWaitCount must take 1 RTT, cmds=%v", cmds)
	_, err = rdb.ZScore(ctx, userActiveIndexKey, "30").Result()
	require.ErrorIs(t, err, redis.Nil, "active index must clear once wait count reaches 0 with no slots")
}

func TestConcurrencyCache_ReleaseSlotSingleRTTAndWaitQueueRetention(t *testing.T) {
	mr, rdb, cache, hook := newRecordedConcurrencyCache(t, 15, 900)
	ctx := context.Background()

	base := time.Unix(1750002000, 0).UTC()
	mr.SetTime(base)

	// 预热 releaseSlotScript（首次 EVALSHA NOSCRIPT 会 fallback 一次 EVAL 加载脚本）。
	require.NoError(t, cache.ReleaseAccountSlot(ctx, 9999, "warmup"))

	// 占 2 个账号槽位 + 1 个用户槽位
	ok, err := cache.AcquireAccountSlot(ctx, 10, 5, "a-1")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = cache.AcquireAccountSlot(ctx, 10, 5, "a-2")
	require.NoError(t, err)
	require.True(t, ok)

	// 释放仍有剩余负载的槽位：严格 1 RTT，无额外 TIME/ZADD。
	hook.reset()
	require.NoError(t, cache.ReleaseAccountSlot(ctx, 10, "a-1"))
	rtts, cmds, _ := hook.snapshot()
	require.Equal(t, 1, rtts, "release with remaining slot must take exactly 1 RTT, cmds=%v", cmds)
	require.Equal(t, []string{"evalsha"}, cmds)
	_, err = rdb.ZScore(ctx, accountActiveIndexKey, "10").Result()
	require.NoError(t, err)

	// 释放归零且无等待计数：严格 1 RTT，且原子移除 active_index member。
	hook.reset()
	require.NoError(t, cache.ReleaseAccountSlot(ctx, 10, "a-2"))
	rtts, cmds, _ = hook.snapshot()
	require.Equal(t, 1, rtts, "release to zero must take exactly 1 RTT, cmds=%v", cmds)
	require.Equal(t, []string{"evalsha"}, cmds)
	_, err = rdb.ZScore(ctx, accountActiveIndexKey, "10").Result()
	require.ErrorIs(t, err, redis.Nil)

	// 若仍有等待计数，槽位归零时不能移除 active_index。
	ok, err = cache.AcquireUserSlot(ctx, 20, 2, "u-1")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = cache.IncrementWaitCount(ctx, 20, 2)
	require.NoError(t, err)
	require.True(t, ok)

	hook.reset()
	require.NoError(t, cache.ReleaseUserSlot(ctx, 20, "u-1"))
	rtts, cmds, _ = hook.snapshot()
	require.Equal(t, 1, rtts, "user release with waiter must take 1 RTT when throttled, cmds=%v", cmds)
	_, err = rdb.ZScore(ctx, userActiveIndexKey, "20").Result()
	require.NoError(t, err, "active index member must remain while wait queue is non-empty")

	// 槽位中仅有已过期成员 + 当前释放成员时，release 应清理过期成员并移除 active_index。
	ok, err = cache.AcquireAccountSlot(ctx, 11, 5, "old-slot")
	require.NoError(t, err)
	require.True(t, ok)
	mr.SetTime(base.Add(16 * time.Minute))
	ok, err = cache.AcquireAccountSlot(ctx, 11, 5, "fresh-slot")
	require.NoError(t, err)
	require.True(t, ok)
	// 手动塞回一条已过期成员，验证 releaseSlotScript 会顺手 ZREMRANGEBYSCORE。
	require.NoError(t, rdb.ZAdd(ctx, accountSlotKey(11), redis.Z{
		Score:  float64(base.Unix()),
		Member: "stale-slot",
	}).Err())
	require.NoError(t, cache.ReleaseAccountSlot(ctx, 11, "fresh-slot"))
	_, err = rdb.ZScore(ctx, accountActiveIndexKey, "11").Result()
	require.ErrorIs(t, err, redis.Nil, "expired siblings must be reaped during release so active_index clears")
}

func TestConcurrencyCache_BatchConcurrencyAndLoadSingleRTT(t *testing.T) {
	mr, rdb, cache, hook := newRecordedConcurrencyCache(t, 15, 900)
	ctx := context.Background()

	base := time.Unix(1750003000, 0).UTC()
	mr.SetTime(base)

	// 预热批量脚本
	_, err := cache.GetAccountConcurrencyBatch(ctx, []int64{1})
	require.NoError(t, err)
	_, err = cache.GetAccountsLoadBatch(ctx, []service.AccountWithConcurrency{{ID: 1, MaxConcurrency: 2}})
	require.NoError(t, err)

	// 构造账号 10：1 条普通槽位 + 1 条 Live 租约 + 1 条已过期槽位 + 2 个等待计数
	ok, err := cache.AcquireAccountSlot(ctx, 10, 5, "acc-live")
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, rdb.ZAdd(ctx, accountSlotKey(10), redis.Z{
		Score:  float64(base.Unix() - 901),
		Member: "acc-expired",
	}).Err())
	require.NoError(t, rdb.ZAdd(ctx, liveAccountSlotKey(10),
		redis.Z{Score: float64(base.Unix()), Member: "live-valid"},
		redis.Z{Score: float64(base.Unix() - 61), Member: "live-expired"},
	).Err())
	require.NoError(t, rdb.Set(ctx, accountWaitKey(10), "2", time.Minute).Err())
	// 非法等待值（含 Lua tonumber 会误收的 inf / 1e2 / 0x10 / 空白）均须安全降级为 0。
	require.NoError(t, rdb.Set(ctx, accountWaitKey(11), "corrupt", time.Minute).Err())
	require.NoError(t, rdb.Set(ctx, accountWaitKey(12), "inf", time.Minute).Err())
	require.NoError(t, rdb.Set(ctx, accountWaitKey(13), "1e2", time.Minute).Err())
	require.NoError(t, rdb.Set(ctx, accountWaitKey(14), "0x10", time.Minute).Err())

	// GetAccountConcurrencyBatch: 严格 1 RTT，0 次独立 TIME 命令
	hook.reset()
	counts, err := cache.GetAccountConcurrencyBatch(ctx, []int64{10, 11})
	require.NoError(t, err)
	rtts, cmds, _ := hook.snapshot()
	require.Equal(t, 1, rtts, "GetAccountConcurrencyBatch must execute in 1 RTT, cmds=%v", cmds)
	require.Equal(t, 0, hook.countCommand("time"))
	require.Equal(t, map[int64]int{10: 2, 11: 0}, counts)

	// GetAccountsLoadBatch: 严格 1 RTT，0 次独立 TIME 命令
	hook.reset()
	loads, err := cache.GetAccountsLoadBatch(ctx, []service.AccountWithConcurrency{
		{ID: 10, MaxConcurrency: 4},
		{ID: 11, MaxConcurrency: 0},
		{ID: 12, MaxConcurrency: 4},
		{ID: 13, MaxConcurrency: 4},
		{ID: 14, MaxConcurrency: 4},
	})
	require.NoError(t, err)
	rtts, cmds, _ = hook.snapshot()
	require.Equal(t, 1, rtts, "GetAccountsLoadBatch must execute in 1 RTT, cmds=%v", cmds)
	require.Equal(t, 0, hook.countCommand("time"))
	require.Equal(t, 2, loads[10].CurrentConcurrency)
	require.Equal(t, 2, loads[10].WaitingCount)
	require.Equal(t, 100, loads[10].LoadRate)
	for _, malformedID := range []int64{11, 12, 13, 14} {
		require.Equal(t, 0, loads[malformedID].CurrentConcurrency)
		require.Equal(t, 0, loads[malformedID].WaitingCount, "malformed wait value on account %d must degrade to 0", malformedID)
		require.Equal(t, 0, loads[malformedID].LoadRate)
	}

	// GetUsersLoadBatch: 严格 1 RTT，0 次独立 TIME 命令
	ok, err = cache.AcquireUserSlot(ctx, 20, 5, "usr-1")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = cache.IncrementWaitCount(ctx, 20, 5)
	require.NoError(t, err)
	require.True(t, ok)
	hook.reset()
	userLoads, err := cache.GetUsersLoadBatch(ctx, []service.UserWithConcurrency{{ID: 20, MaxConcurrency: 4}})
	require.NoError(t, err)
	rtts, cmds, _ = hook.snapshot()
	require.Equal(t, 1, rtts, "GetUsersLoadBatch must execute in 1 RTT, cmds=%v", cmds)
	require.Equal(t, 0, hook.countCommand("time"))
	require.Equal(t, 1, userLoads[20].CurrentConcurrency)
	require.Equal(t, 1, userLoads[20].WaitingCount)
	require.Equal(t, 50, userLoads[20].LoadRate)

	// 空输入 0 RTT
	hook.reset()
	emptyCounts, err := cache.GetAccountConcurrencyBatch(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, emptyCounts)
	rtts, _, _ = hook.snapshot()
	require.Equal(t, 0, rtts)

	// 超过单块上限（>500）自动分块，所有结果完整返回。
	manyIDs := make([]int64, concurrencyBatchLuaChunkSize+5)
	for i := range manyIDs {
		manyIDs[i] = int64(1000 + i)
	}
	ok, err = cache.AcquireAccountSlot(ctx, manyIDs[len(manyIDs)-1], 2, "tail-slot")
	require.NoError(t, err)
	require.True(t, ok)
	hook.reset()
	manyCounts, err := cache.GetAccountConcurrencyBatch(ctx, manyIDs)
	require.NoError(t, err)
	require.Len(t, manyCounts, len(manyIDs))
	require.Equal(t, 1, manyCounts[manyIDs[len(manyIDs)-1]])
	require.Equal(t, 0, manyCounts[manyIDs[0]])
	rtts, _, _ = hook.snapshot()
	require.Equal(t, 2, rtts, "505 IDs should split into 2 Lua chunks of 500+5")
}
