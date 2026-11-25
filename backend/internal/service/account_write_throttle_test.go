package service

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// wait 等所有已受理的后台写入落完（仅测试用：断言前必须等，否则读到的是半截状态）。
func (r *accountWriteRunner) wait() { r.wg.Wait() }

// writeGateClock 是可手动拨动的时钟。
type writeGateClock struct {
	mu sync.Mutex
	t  time.Time
}

func newWriteGateClock() *writeGateClock {
	return &writeGateClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
}

func (c *writeGateClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *writeGateClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// writeGateTestState 只有「相同即冗余」一种语义，用来单测 gate 本身。
type writeGateTestState int

func (s writeGateTestState) redundantTo(last writeGateTestState) bool { return s == last }

func newTestWriteGate() (*accountWriteGate[writeGateTestState], *writeGateClock) {
	clock := newWriteGateClock()
	return &accountWriteGate[writeGateTestState]{now: clock.Now}, clock
}

func TestAccountWriteThrottle_FirstWriteAllowed(t *testing.T) {
	gate, _ := newTestWriteGate()
	require.True(t, gate.Allow(1, 10))
}

func TestAccountWriteThrottle_SameStateWithinIntervalSkipped(t *testing.T) {
	gate, clock := newTestWriteGate()
	require.True(t, gate.Allow(1, 10))
	for i := 0; i < 5; i++ {
		clock.Advance(accountWriteGateInterval / 10)
		require.False(t, gate.Allow(1, 10), "第 %d 次相同内容应被跳过", i+1)
	}
}

func TestAccountWriteThrottle_ChangedStateAllowedImmediately(t *testing.T) {
	gate, _ := newTestWriteGate()
	require.True(t, gate.Allow(1, 10))
	require.True(t, gate.Allow(1, 11))
	// 记的是最新一次放行的内容
	require.False(t, gate.Allow(1, 11))
	require.True(t, gate.Allow(1, 10))
}

func TestAccountWriteThrottle_SameStateAllowedAfterInterval(t *testing.T) {
	gate, clock := newTestWriteGate()
	require.True(t, gate.Allow(1, 10))

	clock.Advance(accountWriteGateInterval - time.Nanosecond)
	require.False(t, gate.Allow(1, 10), "差 1ns 到期仍应跳过")

	clock.Advance(time.Nanosecond)
	require.True(t, gate.Allow(1, 10), "到期必须强制刷新一次")
	require.False(t, gate.Allow(1, 10), "刷新后重新计时")
}

func TestAccountWriteThrottle_ForgetAfterFailedWriteRetries(t *testing.T) {
	gate, _ := newTestWriteGate()
	require.True(t, gate.Allow(1, 10))
	// 写失败：撤销记账，同样内容下次必须重试
	gate.Forget(1)
	require.True(t, gate.Allow(1, 10))
	require.False(t, gate.Allow(1, 10))
}

func TestAccountWriteThrottle_AccountsAreIndependent(t *testing.T) {
	gate, _ := newTestWriteGate()
	require.True(t, gate.Allow(1, 10))
	require.True(t, gate.Allow(2, 10), "另一个账号不受影响")
	require.False(t, gate.Allow(1, 10))
	gate.Forget(2)
	require.False(t, gate.Allow(1, 10), "忘掉 2 不影响 1")
	require.True(t, gate.Allow(2, 10))
}

func TestAccountWriteThrottle_NonPositiveIDAndNilNeverThrottled(t *testing.T) {
	gate, _ := newTestWriteGate()
	for i := 0; i < 3; i++ {
		require.True(t, gate.Allow(0, 10))
		require.True(t, gate.Allow(-1, 10))
	}
	require.Zero(t, gate.size(), "id<=0 不记账")

	var nilGate *accountWriteGate[writeGateTestState]
	require.True(t, nilGate.Allow(1, 10))
	nilGate.Forget(1) // 不 panic
}

func TestAccountWriteThrottle_ZeroValueUsesRealClock(t *testing.T) {
	var gate accountWriteGate[writeGateTestState]
	require.True(t, gate.Allow(1, 10))
	require.False(t, gate.Allow(1, 10))
	gate.Forget(2) // 对空 map 删除不 panic
}

func TestAccountWriteThrottle_ConcurrentSameAccountAllowsOnce(t *testing.T) {
	gate, _ := newTestWriteGate()
	const goroutines = 100
	var allowed atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if gate.Allow(7, 10) {
				allowed.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	require.EqualValues(t, 1, allowed.Load(), "同一账号同样内容并发只能放行一次")
}

func TestAccountWriteThrottle_ConcurrentDifferentAccountsEachAllowedOnce(t *testing.T) {
	gate, _ := newTestWriteGate()
	const accounts, perAccount = 20, 20
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for id := 1; id <= accounts; id++ {
		for j := 0; j < perAccount; j++ {
			wg.Add(1)
			go func(id int64) {
				defer wg.Done()
				if gate.Allow(id, 10) {
					allowed.Add(1)
				}
			}(int64(id))
		}
	}
	wg.Wait()
	require.EqualValues(t, accounts, allowed.Load())
	require.Equal(t, accounts, gate.size())
}

func TestAccountWriteThrottle_SweepsExpiredRecords(t *testing.T) {
	gate, clock := newTestWriteGate()
	for id := int64(1); id <= 100; id++ {
		require.True(t, gate.Allow(id, 10))
	}
	require.Equal(t, 100, gate.size())

	// 过了一个间隔：下一次调用顺手清掉所有过期记录，只剩本次新记的这一条
	clock.Advance(accountWriteGateInterval + time.Second)
	require.True(t, gate.Allow(1000, 10))
	require.Equal(t, 1, gate.size(), "过期记录必须被清掉，防止按账号无限增长")

	// 被清掉的账号等同没有记录，照常放行
	require.True(t, gate.Allow(5, 10))
}

func TestAccountWriteThrottle_SweepKeepsFreshRecords(t *testing.T) {
	gate, clock := newTestWriteGate()
	require.True(t, gate.Allow(1, 10))
	clock.Advance(accountWriteGateInterval / 2)
	require.True(t, gate.Allow(2, 10))
	clock.Advance(accountWriteGateInterval/2 + time.Second) // 1 已过期、2 还新
	require.True(t, gate.Allow(3, 10))
	require.Equal(t, 2, gate.size(), "只清过期的 1，保留 2 和 3")
	require.False(t, gate.Allow(2, 10), "没过期的记录继续生效")
}

func TestAccountWriteThrottle_RecordMakesSameStateRedundant(t *testing.T) {
	gate, _ := newTestWriteGate()
	gate.Record(1, 10)
	require.False(t, gate.Allow(1, 10), "强制写入后同样内容被去抖")
	require.True(t, gate.Allow(1, 11))
	gate.Record(0, 10) // id<=0 不记账、不 panic
	require.Equal(t, 1, gate.size())
	var nilGate *accountWriteGate[writeGateTestState]
	nilGate.Record(1, 10)
}

func TestAccountWriteThrottleRunner_GoRejectsBusyAccountAndFullSlotsWithoutBlocking(t *testing.T) {
	var r accountWriteRunner
	r.maxInflight = 2
	release := make(chan struct{})
	started := make(chan int64, 4)
	task := func(id int64) func(context.Context) {
		return func(context.Context) {
			started <- id
			<-release
		}
	}

	require.True(t, r.Go(context.Background(), 1, task(1)))
	require.False(t, r.Go(context.Background(), 1, task(1)), "同一账号已有在途写入：不排队")
	require.True(t, r.Go(context.Background(), 2, task(2)))
	require.False(t, r.Go(context.Background(), 3, task(3)), "槽位已满：不排队")
	<-started
	<-started

	close(release)
	r.wait()
	require.True(t, r.Go(context.Background(), 3, func(context.Context) {}), "闭包结束后槽位和账号占用都已释放")
	r.wait()
}

func TestAccountWriteThrottleRunner_GoDetachesCancelKeepsValuesAndSetsTimeout(t *testing.T) {
	type ctxKey struct{}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), ctxKey{}, "kept"))
	cancel() // 客户端已断开，请求 ctx 已取消

	var (
		r           accountWriteRunner
		gotErr      error
		gotValue    any
		gotDeadline time.Time
		hasDeadline bool
	)
	require.True(t, r.Go(parent, 1, func(ctx context.Context) {
		gotErr = ctx.Err()
		gotValue = ctx.Value(ctxKey{})
		gotDeadline, hasDeadline = ctx.Deadline()
	}))
	r.wait()

	require.NoError(t, gotErr, "请求 ctx 的取消不能把后台写入取消")
	require.Equal(t, "kept", gotValue, "ctx 里的 value 要保留")
	require.True(t, hasDeadline)
	require.WithinDuration(t, time.Now().Add(accountWriteTimeout), gotDeadline, 2*time.Second, "后台写入有自己的时限")
}

func TestAccountWriteThrottleRunner_RecoversPanicAndReleasesAccount(t *testing.T) {
	var r accountWriteRunner
	require.True(t, r.Go(context.Background(), 1, func(context.Context) { panic("boom") }))
	r.wait() // 后台 goroutine 的 panic 没有 gin Recovery 兜底，必须在这里被接住，进程不能崩
	require.True(t, r.Go(context.Background(), 1, func(context.Context) {}), "panic 之后账号和槽位必须释放")
	r.wait()
}

func TestAccountWriteThrottleRunner_ExclusiveWaitsForInflightAndBlocksNewGo(t *testing.T) {
	var r accountWriteRunner
	release := make(chan struct{})
	started := make(chan struct{})
	require.True(t, r.Go(context.Background(), 1, func(context.Context) {
		close(started)
		<-release
	}))
	<-started

	entered := make(chan struct{})
	proceed := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		r.Exclusive(1, func() {
			close(entered)
			<-proceed
		})
	}()

	select {
	case <-entered:
		t.Fatal("Exclusive 必须等在途的后台写入结束")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	<-entered
	require.False(t, r.Go(context.Background(), 1, func(context.Context) {}), "Exclusive 持有期间该账号的后台写入被拒绝")
	require.True(t, r.Go(context.Background(), 2, func(context.Context) {}), "不影响别的账号")

	close(proceed)
	<-finished
	require.True(t, r.Go(context.Background(), 1, func(context.Context) {}), "Exclusive 结束后恢复")
	r.wait()
}

func TestAccountWriteThrottleRunner_ExclusiveRunsInlineOnFreeAccountAndReleasesOnPanic(t *testing.T) {
	var r accountWriteRunner
	ran := false
	r.Exclusive(1, func() { ran = true })
	require.True(t, ran)

	require.Panics(t, func() { r.Exclusive(2, func() { panic("boom") }) })
	require.True(t, r.Go(context.Background(), 2, func(context.Context) {}), "fn panic 后账号占用必须释放")
	r.wait()
}

func TestSessionWindowWrite_RedundantTo(t *testing.T) {
	start := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Hour)
	otherEnd := end.Add(time.Hour)
	otherStart := otherEnd.Add(-5 * time.Hour)

	windowed := func(s, e time.Time, status string) sessionWindowWrite {
		return newSessionWindowWrite(&s, &e, status)
	}
	statusOnly := func(status string) sessionWindowWrite { return newSessionWindowWrite(nil, nil, status) }

	cases := []struct {
		name string
		next sessionWindowWrite
		last sessionWindowWrite
		want bool
	}{
		{"同窗口同状态", windowed(start, end, "allowed"), windowed(start, end, "allowed"), true},
		{"状态变了", windowed(start, end, "allowed_warning"), windowed(start, end, "allowed"), false},
		{"窗口结束时间变了（新的 5h reset）", windowed(otherStart, otherEnd, "allowed"), windowed(start, end, "allowed"), false},
		{"只有结束时间变了", windowed(start, otherEnd, "allowed"), windowed(start, end, "allowed"), false},
		{"本次没带窗口、状态没变", statusOnly("allowed"), windowed(start, end, "allowed"), true},
		{"本次没带窗口、状态变了", statusOnly("rejected"), windowed(start, end, "allowed"), false},
		{"本次带窗口、上次只写过状态", windowed(start, end, "allowed"), statusOnly("allowed"), false},
		{"都只有状态", statusOnly("allowed"), statusOnly("allowed"), true},
		{"同一时刻不同时区表示", windowed(start.In(time.FixedZone("x", 8*3600)), end.In(time.FixedZone("x", 8*3600)), "allowed"), windowed(start, end, "allowed"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.next.redundantTo(tc.last))
		})
	}
}

func passiveHeaders(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

func mustPassiveSample(t *testing.T, kv ...string) passiveUsageSample {
	t.Helper()
	s, ok := parsePassiveUsageSample(passiveHeaders(kv...))
	require.True(t, ok)
	return s
}

func TestParsePassiveUsageSample(t *testing.T) {
	t.Run("没有任何采样头", func(t *testing.T) {
		_, ok := parsePassiveUsageSample(passiveHeaders("anthropic-ratelimit-unified-5h-status", "allowed"))
		require.False(t, ok)
	})
	t.Run("全部解析且 reset 毫秒归一成秒", func(t *testing.T) {
		s := mustPassiveSample(t,
			"anthropic-ratelimit-unified-5h-utilization", "0.15",
			"anthropic-ratelimit-unified-7d-utilization", "0.5",
			"anthropic-ratelimit-unified-7d-reset", "1900000000",
			"anthropic-ratelimit-unified-7d_oi-utilization", "0.87",
			"anthropic-ratelimit-unified-7d_oi-reset", "1900000001000",
		)
		require.Equal(t, passiveUsageSample{
			util5h:    optFloat{0.15, true},
			util7d:    optFloat{0.5, true},
			reset7d:   optInt{1900000000, true},
			util7dOI:  optFloat{0.87, true},
			reset7dOI: optInt{1900000001, true},
		}, s)
	})
	t.Run("解析失败的头当作没有", func(t *testing.T) {
		s := mustPassiveSample(t,
			"anthropic-ratelimit-unified-5h-utilization", "0.3",
			"anthropic-ratelimit-unified-7d-utilization", "abc",
			"anthropic-ratelimit-unified-7d-reset", "not-a-number",
		)
		require.True(t, s.util5h.ok)
		require.False(t, s.util7d.ok)
		require.False(t, s.reset7d.ok)
	})
}

func TestPassiveUsageSample_ExtraUpdatesKeepsKeysAndTypes(t *testing.T) {
	s := mustPassiveSample(t,
		"anthropic-ratelimit-unified-5h-utilization", "0.41",
		"anthropic-ratelimit-unified-7d_oi-utilization", "1.0",
		"anthropic-ratelimit-unified-7d_oi-reset", "1900000000",
	)
	updates := s.extraUpdates(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	require.Equal(t, map[string]any{
		"session_window_utilization":      0.41,
		"passive_usage_7d_oi_utilization": 1.0,
		"passive_usage_7d_oi_reset":       int64(1900000000),
		"passive_usage_sampled_at":        "2026-01-02T03:04:05Z",
	}, updates, "没出现的头不能写 nil，类型必须是 float64 / int64")
}

func TestPassiveUsageSample_RedundantTo(t *testing.T) {
	const (
		u5   = "anthropic-ratelimit-unified-5h-utilization"
		u7   = "anthropic-ratelimit-unified-7d-utilization"
		r7   = "anthropic-ratelimit-unified-7d-reset"
		u7oi = "anthropic-ratelimit-unified-7d_oi-utilization"
		r7oi = "anthropic-ratelimit-unified-7d_oi-reset"
	)
	base := []string{u5, "0.50", u7, "0.30", r7, "1900000000", u7oi, "0.20", r7oi, "1900000100"}
	with := func(overrides ...string) passiveUsageSample {
		kv := append([]string{}, base...)
		for i := 0; i+1 < len(overrides); i += 2 {
			replaced := false
			for j := 0; j+1 < len(kv); j += 2 {
				if kv[j] == overrides[i] {
					kv[j+1], replaced = overrides[i+1], true
				}
			}
			if !replaced {
				kv = append(kv, overrides[i], overrides[i+1])
			}
		}
		return mustPassiveSample(t, kv...)
	}
	last := with()

	cases := []struct {
		name string
		next passiveUsageSample
		last passiveUsageSample
		want bool
	}{
		{"完全相同", with(), last, true},
		{"5h 变化 0.005 < 0.01", with(u5, "0.505"), last, true},
		{"5h 变化 0.009 < 0.01", with(u5, "0.509"), last, true},
		{"5h 变化恰好 0.01 要写", with(u5, "0.51"), last, false},
		{"5h 下降 0.01 也要写", with(u5, "0.49"), last, false},
		{"5h 变化 0.02 要写", with(u5, "0.52"), last, false},
		{"7d 变化 0.01 要写", with(u7, "0.31"), last, false},
		{"7d_oi 变化 0.01 要写", with(u7oi, "0.21"), last, false},
		{"7d 变化不足 0.01", with(u7, "0.305"), last, true},
		{"7d reset 变了要写", with(r7, "1900000001"), last, false},
		{"7d_oi reset 变了要写", with(r7oi, "1900000101"), last, false},
		// 0.29-0.28 在浮点里是 0.00999…95，不能因此被当成没变
		{"浮点边界 0.29 比 0.28 差 1 个百分点要写", mustPassiveSample(t, u5, "0.29"), mustPassiveSample(t, u5, "0.28"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.next.redundantTo(tc.last))
		})
	}

	t.Run("头出现或消失要写", func(t *testing.T) {
		only5h := mustPassiveSample(t, u5, "0.50")
		require.False(t, only5h.redundantTo(last), "少了 7d 等头")
		require.False(t, last.redundantTo(only5h), "多了 7d 等头")
	})
	t.Run("reset 消失要写", func(t *testing.T) {
		noReset := mustPassiveSample(t, u5, "0.50", u7, "0.30", u7oi, "0.20")
		require.False(t, noReset.redundantTo(last))
	})
	t.Run("缓慢累积以上次写入值为基准", func(t *testing.T) {
		written := mustPassiveSample(t, u5, "0.500")
		require.True(t, mustPassiveSample(t, u5, "0.504").redundantTo(written))
		require.True(t, mustPassiveSample(t, u5, "0.508").redundantTo(written))
		require.False(t, mustPassiveSample(t, u5, "0.512").redundantTo(written), "累计偏离上次写入值 ≥0.01 必须写")
	})
}
