//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// ---- 测试替身 ---------------------------------------------------------------

// circuitClock 是可控的假时钟。起点对齐到 30 秒桶边界，桶数学才可预测。
type circuitClock struct {
	mu sync.Mutex
	t  time.Time
}

func newCircuitClock() *circuitClock {
	return &circuitClock{t: time.Unix(1_800_000_000-1_800_000_000%300, 0)}
}

func (c *circuitClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *circuitClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type circuitLogRecorder struct {
	mu     sync.Mutex
	events []string
}

func (r *circuitLogRecorder) Error(msg string, _ ...zap.Field) {
	r.mu.Lock()
	r.events = append(r.events, msg)
	r.mu.Unlock()
}

func (r *circuitLogRecorder) count(event string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e == event {
			n++
		}
	}
	return n
}

type circuitPersistRecorder struct {
	mu    sync.Mutex
	trips []AccountCircuitTrip
	err   error
	calls int
}

func (r *circuitPersistRecorder) persist(_ context.Context, trip AccountCircuitTrip) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.err != nil {
		return r.err
	}
	r.trips = append(r.trips, trip)
	return nil
}

func (r *circuitPersistRecorder) tripCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.trips)
}

func (r *circuitPersistRecorder) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func (r *circuitPersistRecorder) setErr(err error) {
	r.mu.Lock()
	r.err = err
	r.mu.Unlock()
}

type circuitHarness struct {
	b     *AccountCircuitBreaker
	clock *circuitClock
	store *circuitPersistRecorder
	logs  *circuitLogRecorder
}

func newCircuitHarness(t *testing.T, mutate func(*AccountCircuitBreakerSettings)) *circuitHarness {
	t.Helper()
	settings := DefaultAccountCircuitBreakerSettings()
	if mutate != nil {
		mutate(&settings)
	}
	h := &circuitHarness{clock: newCircuitClock(), store: &circuitPersistRecorder{}, logs: &circuitLogRecorder{}}
	h.b = NewAccountCircuitBreaker(settings, h.store.persist)
	h.b.now = h.clock.Now
	h.b.async = func(f func()) { f() } // 同步执行写库，断言才确定
	h.b.log = h.logs
	return h
}

// circuitCtxFor 模拟 API Key 鉴权 + handler 选号之后的网关请求 ctx（用户 ID、当前账号 ID、平台、账号名）。
func circuitCtxFor(id int64, platform, name string) context.Context {
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(7))
	ctx = context.WithValue(ctx, ctxkey.AccountID, id)
	ctx = context.WithValue(ctx, ctxkey.Platform, platform)
	return context.WithValue(ctx, ctxkey.AccountName, name)
}

// circuitCtx 是不绑定具体账号的版本（只有用户 ID）：账号平台/名字不会被记下。
func circuitCtx(platform, name string) context.Context {
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(7))
	ctx = context.WithValue(ctx, ctxkey.Platform, platform)
	return context.WithValue(ctx, ctxkey.AccountName, name)
}

// feed 对账号记 n 次同一结果的尝试。
func (h *circuitHarness) feed(id int64, platform string, status, n int) {
	ctx := circuitCtxFor(id, platform, fmt.Sprintf("acct-%d", id))
	for i := 0; i < n; i++ {
		h.b.ObserveUpstreamAttempt(ctx, id, status, nil)
	}
}

// ---- 窗口数学 ---------------------------------------------------------------

func TestCircuitWindowPackRoundTrip(t *testing.T) {
	for _, tc := range []struct{ epoch, total, failed uint64 }{
		{0, 0, 0}, {1, 1, 1}, {circuitEpochMask, circuitCountMask, circuitCountMask}, {123456, 1000, 999},
	} {
		e, total, failed := circuitUnpack(circuitPack(tc.epoch, tc.total, tc.failed))
		require.Equal(t, tc.epoch&circuitEpochMask, e)
		require.Equal(t, tc.total, total)
		require.Equal(t, tc.failed, failed)
	}
}

func TestCircuitWindowRotationAndExpiry(t *testing.T) {
	var w circuitWindow
	const base = uint64(5_000_000) // 任意起点
	// 当前桶里的样本
	for i := 0; i < 5; i++ {
		w.add(base, i%2 == 0) // 3 失败 2 成功
	}
	total, failed := w.sum(base)
	require.Equal(t, 5, total)
	require.Equal(t, 3, failed)

	// 同一窗口内较晚的桶：累加
	w.add(base+9, true)
	total, failed = w.sum(base + 9)
	require.Equal(t, 6, total, "epoch base..base+9 are still inside the window")
	require.Equal(t, 4, failed)

	// base+10：最老的桶（base）滑出窗口，只剩 base+9 的样本
	total, failed = w.sum(base + 10)
	require.Equal(t, 1, total, "bucket older than 10 epochs must drop out of the sum")
	require.Equal(t, 1, failed)

	// 在 base+10 写入会轮转 base 所在槽位（同一个 idx），旧数据被清零重用
	w.add(base+10, false)
	total, failed = w.sum(base + 10)
	require.Equal(t, 2, total)
	require.Equal(t, 1, failed)

	// 很久之后：全部过期
	total, failed = w.sum(base + 1000)
	require.Zero(t, total)
	require.Zero(t, failed)
}

func TestCircuitWindowEmptyBucketAcceptsAnyEpoch(t *testing.T) {
	// 回归：空桶 epoch 字段是 0，当前 epoch 取模后可能落在「看起来更新」的半区；
	// 空桶必须直接占用，不能把样本当成过期丢掉。
	for _, epoch := range []uint64{1, 8_388_607, 8_388_608, 12_000_000, 16_777_215, 16_777_216, 59_999_999} {
		var w circuitWindow
		w.add(epoch, true)
		total, failed := w.sum(epoch)
		require.Equal(t, 1, total, "epoch %d", epoch)
		require.Equal(t, 1, failed, "epoch %d", epoch)
	}
}

func TestCircuitWindowDropsSampleOlderThanBucket(t *testing.T) {
	var w circuitWindow
	w.add(100, true)
	w.add(90, true) // 与 100 同一槽位（idx 0），但样本比桶里的数据旧：丢弃，不能覆盖新数据
	total, _ := w.sum(100)
	require.Equal(t, 1, total)
}

func TestCircuitWindowConcurrentAdds(t *testing.T) {
	var w circuitWindow
	const goroutines, per = 32, 5000
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				w.add(42, (g+i)%4 == 0)
			}
		}(g)
	}
	wg.Wait()
	total, failed := w.sum(42)
	require.Equal(t, goroutines*per, total)
	require.Equal(t, goroutines*per/4, failed)
}

// ---- 触发阈值：严格大于 + 最小样本 --------------------------------------------

// seed 直接往当前桶里写样本（不触发评估），用来构造精确的边界状态。
func (h *circuitHarness) seed(id int64, platform string, total, failed int) {
	st := h.b.stateFor(id)
	ctx := circuitCtxFor(id, platform, fmt.Sprintf("acct-%d", id))
	h.b.noteIdentity(st, ctx)
	epoch := uint64(h.b.now().UnixNano() / h.b.bucketNanos)
	st.lastEpoch.Store(epoch)
	for i := 0; i < total; i++ {
		st.window.add(epoch, i < failed)
	}
}

func TestCircuitBreakerTripThresholdTable(t *testing.T) {
	// total/failed 是「最后一次失败观察之后」窗口里的计数（含这次观察）。
	cases := []struct {
		name          string
		total, failed int
		wantTripped   bool
	}{
		{"19 of 19 failed: sample too small", 19, 19, false},
		{"20 of 20 failed", 20, 20, true},
		{"exactly 95% (19/20) does not trip", 20, 19, false},
		{"21 samples, 20 failed (95.2%) trips", 21, 20, true},
		{"exactly 95% (38/40) does not trip", 40, 38, false},
		{"97.5% (39/40) trips", 40, 39, true},
		{"exactly 95% (95/100) does not trip", 100, 95, false},
		{"96% (96/100) trips", 100, 96, true},
		{"exactly 95% (190/200) does not trip", 200, 190, false},
		{"95.5% (191/200) trips", 200, 191, true},
		{"exactly 95% (950/1000) does not trip", 1000, 950, false},
		{"95.1% (951/1000) trips", 1000, 951, true},
		{"healthy 50%", 100, 50, false},
		{"5% failures", 100, 5, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newCircuitHarness(t, nil)
			h.seed(1, "anthropic", tc.total-1, tc.failed-1) // 前面的样本：不评估
			h.feed(1, "anthropic", 502, 1)                  // 最后一次失败触发评估
			require.Equal(t, tc.wantTripped, h.b.IsOpen(1))
			if tc.wantTripped {
				require.Equal(t, 1, h.store.tripCount())
				require.Equal(t, tc.total, h.store.trips[0].Attempts)
				require.Equal(t, tc.failed, h.store.trips[0].Failures)
			} else {
				require.Zero(t, h.store.callCount())
			}
		})
	}
}

func TestCircuitBreakerMinRequestsBoundary(t *testing.T) {
	// 100% 失败但样本不足 min_requests：不熔断；刚好够：熔断。
	h := newCircuitHarness(t, nil)
	h.feed(1, "anthropic", 502, 19)
	require.False(t, h.b.IsOpen(1), "19 samples < min_requests(20)")
	h.feed(1, "anthropic", 502, 1)
	require.True(t, h.b.IsOpen(1), "20 samples == min_requests")

	h2 := newCircuitHarness(t, func(s *AccountCircuitBreakerSettings) { s.MinRequests = 100 })
	h2.feed(1, "anthropic", 502, 99)
	require.False(t, h2.b.IsOpen(1))
	h2.feed(1, "anthropic", 502, 1)
	require.True(t, h2.b.IsOpen(1))
}

func TestCircuitBreakerSuccessesNeverEvaluateTrip(t *testing.T) {
	// 失败率只在失败时评估；纯成功流量不会触发任何写库。
	h := newCircuitHarness(t, nil)
	h.feed(1, "anthropic", 200, 5000)
	require.Zero(t, h.store.callCount())
	require.False(t, h.b.IsOpen(1))
}

func TestCircuitBreakerTripAction(t *testing.T) {
	h := newCircuitHarness(t, nil)
	start := h.clock.Now()
	h.feed(1, "anthropic", 200, 1)
	h.feed(1, "anthropic", 502, 100)
	// 1 次成功 + 前 19 次失败不足 20 个样本；第 19 次失败是第 20 个样本，此时 19/20 = 95% 不触发；
	// 第 20 次失败时 20/21 = 95.2% > 95% 触发，随后在途完成的 80 次尝试被忽略。
	require.Equal(t, 1, h.store.tripCount(), "one write per cooldown, however many failures follow")
	trip := h.store.trips[0]
	require.Equal(t, int64(1), trip.AccountID)
	require.Equal(t, "anthropic", trip.Platform)
	require.Equal(t, "acct-1", trip.AccountName)
	require.Equal(t, 21, trip.Attempts)
	require.Equal(t, 20, trip.Failures)
	require.Equal(t, start.Add(60*time.Second), trip.Until)
	require.Equal(t, "circuit_breaker: 95.2% of 21 upstream attempts failed in 5m", trip.Reason)
	require.True(t, IsAccountCircuitBreakerReason(trip.Reason))
	require.Equal(t, 1, h.logs.count("account_circuit_breaker.tripped"))

	until, open := h.b.OpenUntil(1)
	require.True(t, open)
	require.Equal(t, trip.Until, until)

	stats := h.b.Stats()
	require.Equal(t, int64(1), stats.Trips)
	require.Equal(t, int64(80), stats.IgnoredOpen)
	require.Equal(t, int64(1), stats.Persisted)
}

func TestCircuitBreakerRecoveryAndFreshWindow(t *testing.T) {
	h := newCircuitHarness(t, nil)
	h.feed(1, "anthropic", 502, 20)
	require.True(t, h.b.IsOpen(1))
	require.Equal(t, 1, h.store.tripCount())

	// 冷却未到：仍然打开
	h.clock.Advance(59 * time.Second)
	require.True(t, h.b.IsOpen(1))
	// 冷却到期：自动恢复，不需要任何人工
	h.clock.Advance(2 * time.Second)
	require.False(t, h.b.IsOpen(1), "cooldown elapsed: account is schedulable again without any manual step")

	// 恢复后需要重新积累 >= min_requests 个新样本。熔断前的 20 个失败不能被拿来立刻再熔断。
	h.feed(1, "anthropic", 502, 1)
	require.Equal(t, 1, h.store.tripCount(), "stale pre-trip samples must not re-trip the account immediately")
	require.Equal(t, 1, h.logs.count("account_circuit_breaker.recovered"))
	h.feed(1, "anthropic", 502, 18) // 累计 19 个新样本
	require.Equal(t, 1, h.store.tripCount())
	h.feed(1, "anthropic", 502, 1) // 第 20 个新样本，且仍 100% 失败
	require.Equal(t, 2, h.store.tripCount(), "re-trips once a fresh window of failing attempts accumulates")
	// 再次熔断的冷却时间仍是 60 秒（默认不做指数退避）
	require.Equal(t, 60*time.Second, h.store.trips[1].Cooldown)
}

func TestCircuitBreakerRecoveredAccountThatSucceedsStaysClosed(t *testing.T) {
	h := newCircuitHarness(t, nil)
	h.feed(1, "anthropic", 502, 20)
	h.clock.Advance(61 * time.Second)
	h.feed(1, "anthropic", 200, 30)
	h.feed(1, "anthropic", 502, 5)
	require.False(t, h.b.IsOpen(1))
	require.Equal(t, 1, h.store.tripCount())
}

func TestCircuitBreakerWindowSlides(t *testing.T) {
	t.Run("old failures age out of the 5 minute window", func(t *testing.T) {
		h := newCircuitHarness(t, nil)
		h.feed(1, "anthropic", 502, 19) // 不足 20 个样本，不触发
		h.clock.Advance(300 * time.Second)
		h.feed(1, "anthropic", 502, 1)
		require.False(t, h.b.IsOpen(1), "failures older than the window must not count")
		require.Zero(t, h.store.callCount())
	})
	t.Run("failures inside the window accumulate across buckets", func(t *testing.T) {
		h := newCircuitHarness(t, nil)
		h.feed(1, "anthropic", 502, 19)
		h.clock.Advance(240 * time.Second) // 仍在 300 秒窗口内（第 8 个桶）
		h.feed(1, "anthropic", 502, 1)
		require.True(t, h.b.IsOpen(1))
	})
	t.Run("last bucket of the window still counts, the next one does not", func(t *testing.T) {
		h := newCircuitHarness(t, nil)
		h.feed(1, "anthropic", 502, 10)
		h.clock.Advance(270 * time.Second) // epoch+9：仍在窗口内
		h.feed(1, "anthropic", 502, 10)
		require.True(t, h.b.IsOpen(1))

		h2 := newCircuitHarness(t, nil)
		h2.feed(1, "anthropic", 502, 10)
		h2.clock.Advance(300 * time.Second) // epoch+10：最老的桶已滑出
		h2.feed(1, "anthropic", 502, 10)
		require.False(t, h2.b.IsOpen(1))
	})
}

func TestCircuitBreakerBackoffIsOptIn(t *testing.T) {
	t.Run("disabled: always the base cooldown", func(t *testing.T) {
		h := newCircuitHarness(t, nil)
		for i := 0; i < 4; i++ {
			h.feed(1, "anthropic", 502, 20)
			h.clock.Advance(61 * time.Second)
		}
		require.Equal(t, 4, h.store.tripCount())
		for _, trip := range h.store.trips {
			require.Equal(t, 60*time.Second, trip.Cooldown)
		}
	})
	t.Run("enabled: doubles on consecutive trips, capped, resets after a quiet window", func(t *testing.T) {
		h := newCircuitHarness(t, func(s *AccountCircuitBreakerSettings) {
			s.BackoffEnabled = true
			s.MaxCooldown = 300 * time.Second
		})
		var got []time.Duration
		for i := 0; i < 5; i++ {
			h.feed(1, "anthropic", 502, 20)
			got = append(got, h.store.trips[len(h.store.trips)-1].Cooldown)
			h.clock.Advance(got[len(got)-1] + time.Second)
		}
		require.Equal(t, []time.Duration{60 * time.Second, 120 * time.Second, 240 * time.Second, 300 * time.Second, 300 * time.Second}, got)

		// 安静一个完整窗口之后再熔断：回到基础冷却
		h.clock.Advance(10 * time.Minute)
		h.feed(1, "anthropic", 502, 20)
		require.Equal(t, 60*time.Second, h.store.trips[len(h.store.trips)-1].Cooldown)
	})
}

// ---- 不计入的尝试 ------------------------------------------------------------

type timeoutNetErr struct{}

func (timeoutNetErr) Error() string   { return "i/o timeout" }
func (timeoutNetErr) Timeout() bool   { return true }
func (timeoutNetErr) Temporary() bool { return true }

var _ net.Error = timeoutNetErr{}

func TestClassifyUpstreamAttemptTable(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, cancel2 := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel2()

	cases := []struct {
		name   string
		ctxErr error
		status int
		err    error
		want   attemptOutcome
	}{
		// 2xx/3xx
		{"200", nil, 200, nil, attemptOK},
		{"201", nil, 201, nil, attemptOK},
		{"204", nil, 204, nil, attemptOK},
		{"301", nil, 301, nil, attemptOK},
		{"304", nil, 304, nil, attemptOK},
		// 请求自身问题的 4xx：上游活着
		{"400", nil, 400, nil, attemptOK},
		{"404", nil, 404, nil, attemptOK},
		{"405", nil, 405, nil, attemptOK},
		{"408", nil, 408, nil, attemptOK},
		{"409", nil, 409, nil, attemptOK},
		{"413", nil, 413, nil, attemptOK},
		{"422", nil, 422, nil, attemptOK},
		// 账号对我们不可用
		{"401", nil, 401, nil, attemptFailed},
		{"402", nil, 402, nil, attemptFailed},
		{"403", nil, 403, nil, attemptFailed},
		{"407 proxy auth", nil, 407, nil, attemptFailed},
		{"429", nil, 429, nil, attemptFailed},
		// 5xx
		{"500", nil, 500, nil, attemptFailed},
		{"502", nil, 502, nil, attemptFailed},
		{"503", nil, 503, nil, attemptFailed},
		{"504", nil, 504, nil, attemptFailed},
		{"520", nil, 520, nil, attemptFailed},
		{"521", nil, 521, nil, attemptFailed},
		{"522", nil, 522, nil, attemptFailed},
		{"523", nil, 523, nil, attemptFailed},
		{"524", nil, 524, nil, attemptFailed},
		{"526", nil, 526, nil, attemptFailed},
		{"529 overloaded", nil, 529, nil, attemptFailed},
		{"530", nil, 530, nil, attemptFailed},
		{"501 not implemented is the request's problem", nil, 501, nil, attemptOK},
		{"505 http version is the request's problem", nil, 505, nil, attemptOK},
		// 没有响应也没有错误：没有信息
		{"no response", nil, 0, nil, attemptIgnored},
		// 传输层错误：失败
		{"EOF", nil, 0, io.EOF, attemptFailed},
		{"unexpected EOF", nil, 0, io.ErrUnexpectedEOF, attemptFailed},
		{"connection refused", nil, 0, &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, attemptFailed},
		{"connection reset", nil, 0, &net.OpError{Op: "read", Err: syscall.ECONNRESET}, attemptFailed},
		{"upstream response header timeout (net.Error)", nil, 0, timeoutNetErr{}, attemptFailed},
		{"deadline exceeded that is not our ctx", nil, 0, context.DeadlineExceeded, attemptFailed},
		{"dns error", nil, 0, &net.DNSError{Err: "no such host", Name: "example.invalid", IsNotFound: true}, attemptFailed},
		// 不计入：客户端断开 / 本地 ctx 取消 / 本地超时 / 本机资源耗尽
		{"client disconnected (ctx canceled)", canceled.Err(), 0, context.Canceled, attemptIgnored},
		{"client disconnected, transport reports a different error", canceled.Err(), 0, io.ErrUnexpectedEOF, attemptIgnored},
		{"our own deadline", expired.Err(), 0, context.DeadlineExceeded, attemptIgnored},
		{"canceled error with live ctx", nil, 0, context.Canceled, attemptIgnored},
		{"wrapped canceled", nil, 0, fmt.Errorf("Post: %w", context.Canceled), attemptIgnored},
		{"too many open files", nil, 0, &net.OpError{Op: "dial", Err: syscall.EMFILE}, attemptIgnored},
		{"ephemeral ports exhausted", nil, 0, &net.OpError{Op: "dial", Err: syscall.EADDRNOTAVAIL}, attemptIgnored},
		{"no buffer space", nil, 0, &net.OpError{Op: "dial", Err: syscall.ENOBUFS}, attemptIgnored},
		// 拿到响应时 ctx 状态不改变结论（上游确实返回了 502）
		{"502 even if the client left right after", canceled.Err(), 502, nil, attemptFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, classifyUpstreamAttempt(tc.ctxErr, tc.status, tc.err))
		})
	}
}

func TestCircuitBreakerIgnoresNoiseThroughObserver(t *testing.T) {
	canceledCtx, cancel := context.WithCancel(circuitCtx("anthropic", "a"))
	cancel()

	cases := []struct {
		name string
		ctx  context.Context
		err  error
	}{
		{"client disconnected", canceledCtx, context.Canceled},
		{"client disconnected with transport noise", canceledCtx, io.ErrUnexpectedEOF},
		{"canceled error but live ctx", circuitCtx("anthropic", "a"), context.Canceled},
		{"local fd exhaustion", circuitCtx("anthropic", "a"), &net.OpError{Op: "dial", Err: syscall.EMFILE}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newCircuitHarness(t, nil)
			for i := 0; i < 500; i++ {
				h.b.ObserveUpstreamAttempt(tc.ctx, 1, 0, tc.err)
			}
			require.False(t, h.b.IsOpen(1), "noise must never trip an account")
			require.Zero(t, h.store.callCount())
			require.Equal(t, int64(500), h.b.Stats().IgnoredNoise)
			require.Zero(t, h.b.Stats().Observed)
		})
	}
}

func TestCircuitBreakerIgnoresNonGatewayTraffic(t *testing.T) {
	// 后台账号测试 / 额度探针 / 模型同步没有 API Key 鉴权写入的用户 ID，不能污染窗口。
	h := newCircuitHarness(t, nil)
	for i := 0; i < 500; i++ {
		h.b.ObserveUpstreamAttempt(context.Background(), 1, 502, nil)
	}
	require.False(t, h.b.IsOpen(1))
	require.Zero(t, h.b.Stats().Observed)
	// 账号 ID <= 0 同理
	h.b.ObserveUpstreamAttempt(circuitCtxFor(0, "anthropic", "x"), 0, 502, nil)
	require.Zero(t, h.b.Stats().Observed)
}

func TestCircuitBreakerGatewayMarkerAcceptsSelectedAccountContexts(t *testing.T) {
	// Gemini 原生路由的鉴权中间件不写用户 ID，但 handler 选号后会写「当前账号 ID」：同样是真实网关流量。
	geminiStyle := func(id int64) context.Context {
		ctx := context.WithValue(context.Background(), ctxkey.AccountID, id)
		return context.WithValue(ctx, ctxkey.Platform, "gemini")
	}
	h := newCircuitHarness(t, nil)
	for i := 0; i < 20; i++ {
		h.b.ObserveUpstreamAttempt(geminiStyle(1), 1, 502, nil)
	}
	require.True(t, h.b.IsOpen(1), "a request carrying only the selected-account id is gateway traffic")
	require.Equal(t, "gemini", h.store.trips[0].Platform)

	// 当前账号 ID 与被调用的账号对不上：不是这个账号的网关尝试，不计入，也不会把标签贴过去。
	h2 := newCircuitHarness(t, nil)
	for i := 0; i < 50; i++ {
		h2.b.ObserveUpstreamAttempt(geminiStyle(99), 1, 502, nil)
	}
	require.False(t, h2.b.IsOpen(1))
	require.Zero(t, h2.b.Stats().Observed)

	// 有用户 ID 但没有账号 ID：计入，只是不知道平台（归在「未知平台」桶）。
	h3 := newCircuitHarness(t, nil)
	for i := 0; i < 20; i++ {
		h3.b.ObserveUpstreamAttempt(circuitCtx("anthropic", "x"), 1, 502, nil)
	}
	require.True(t, h3.b.IsOpen(1))
	require.Equal(t, "", h3.store.trips[0].Platform, "labels from another account's context are never adopted")
}

func TestCircuitBreakerCountsGenuineTransportErrors(t *testing.T) {
	h := newCircuitHarness(t, nil)
	ctx := circuitCtxFor(1, "anthropic", "a")
	for i := 0; i < 20; i++ {
		h.b.ObserveUpstreamAttempt(ctx, 1, 0, timeoutNetErr{})
	}
	require.True(t, h.b.IsOpen(1))
}

func TestCircuitBreakerDisabled(t *testing.T) {
	h := newCircuitHarness(t, func(s *AccountCircuitBreakerSettings) { s.Enabled = false })
	h.feed(1, "anthropic", 502, 1000)
	require.False(t, h.b.IsOpen(1))
	require.Zero(t, h.store.callCount())
	require.Zero(t, h.b.Stats().Observed)
	var nilBreaker *AccountCircuitBreaker
	nilBreaker.ObserveUpstreamAttempt(circuitCtx("a", "b"), 1, 502, nil) // nil 安全
	require.False(t, nilBreaker.IsOpen(1))
	require.False(t, nilBreaker.Enabled())
}

// ---- 持久化失败 / 重置 ---------------------------------------------------------

func TestCircuitBreakerPersistFailureRevertsAndRetriesLater(t *testing.T) {
	h := newCircuitHarness(t, nil)
	h.store.setErr(errors.New("db down"))
	h.feed(1, "anthropic", 502, 20)
	require.Equal(t, 1, h.store.callCount())
	require.False(t, h.b.IsOpen(1), "memory must not claim the account is paused when the DB write failed")
	require.Equal(t, 1, h.logs.count("account_circuit_breaker.persist_failed"))
	require.Equal(t, int64(1), h.b.Stats().PersistFailed)

	// 失败后 5 秒内不再尝试写库（避免打爆已经出问题的库）
	h.clock.Advance(2 * time.Second)
	h.feed(1, "anthropic", 502, 40)
	require.Equal(t, 1, h.store.callCount(), "no write retry before the retry delay")

	// 库恢复；延迟过后下一个越线的失败重新触发
	h.store.setErr(nil)
	h.clock.Advance(4 * time.Second)
	h.feed(1, "anthropic", 502, 25)
	require.True(t, h.b.IsOpen(1))
	require.Equal(t, 1, h.store.tripCount())
}

func TestCircuitBreakerPersistPanicDoesNotCrash(t *testing.T) {
	h := newCircuitHarness(t, nil)
	h.b.persist = func(context.Context, AccountCircuitTrip) error { panic("boom") }
	require.NotPanics(t, func() { h.feed(1, "anthropic", 502, 20) })
	require.False(t, h.b.IsOpen(1))
	require.Equal(t, int64(1), h.b.Stats().PersistFailed)
}

func TestCircuitBreakerReset(t *testing.T) {
	h := newCircuitHarness(t, nil)
	h.feed(1, "anthropic", 502, 20)
	require.True(t, h.b.IsOpen(1))
	h.b.Reset(1) // 管理员手动清除临时不可调度
	require.False(t, h.b.IsOpen(1))
	// 窗口也被清空：需要重新积累样本
	h.feed(1, "anthropic", 502, 19)
	require.Equal(t, 1, h.store.tripCount())
	h.b.Reset(999) // 未知账号不 panic
}

// ---- 安全阀 ------------------------------------------------------------------

// valveScenario 让 bad 个账号全失败、good 个账号全成功，按轮交错喂 rounds 轮（模拟同一时间段的并行流量）。
func (h *circuitHarness) valveScenario(platform string, firstID int64, bad, good, rounds int) (badIDs, goodIDs []int64) {
	for i := 0; i < bad; i++ {
		badIDs = append(badIDs, firstID+int64(i))
	}
	for i := 0; i < good; i++ {
		goodIDs = append(goodIDs, firstID+int64(bad+i))
	}
	for r := 0; r < rounds; r++ {
		for _, id := range goodIDs {
			h.feed(id, platform, 200, 1)
		}
		for _, id := range badIDs {
			h.feed(id, platform, 502, 1)
		}
	}
	return badIDs, goodIDs
}

func (h *circuitHarness) openCount(ids []int64) int {
	n := 0
	for _, id := range ids {
		if h.b.IsOpen(id) {
			n++
		}
	}
	return n
}

func TestCircuitBreakerValveNeverBlocksWhileAHealthyAccountExists(t *testing.T) {
	// 同平台还有健康账号（仍在接流量、失败率没越线）说明网络和平台都是好的：坏账号一个不剩全部熔断。
	cases := []struct {
		name      string
		bad, good int
	}{
		{"4 of 6 bad", 4, 2},
		{"6 of 12 bad", 6, 6},
		{"11 of 12 bad, a single healthy account", 11, 1},
		{"2 of 3 bad", 2, 1},
		{"3 of 4 bad", 3, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newCircuitHarness(t, nil)
			bad, good := h.valveScenario("anthropic", 1, tc.bad, tc.good, 40)
			require.Equal(t, tc.bad, h.openCount(bad), "every failing account must be paused while a healthy one exists")
			require.Zero(t, h.openCount(good), "healthy accounts are never paused")
			require.Equal(t, tc.bad, h.store.tripCount())
			require.Zero(t, h.logs.count("account_circuit_breaker.valve_blocked"))
			require.Zero(t, h.b.Stats().ValveBlocked)
		})
	}
}

func TestCircuitBreakerValveBlocksWhenEveryParticipatingAccountIsFailing(t *testing.T) {
	// 12 个账号全部在失败（本机网络故障的样子）：开头两个账号按小池子规模熔断，之后安全阀接管，
	// 不再继续把整个平台摘光；ERROR 日志按平台限频，只打一条。
	h := newCircuitHarness(t, nil)
	bad, _ := h.valveScenario("anthropic", 1, 12, 0, 40)
	require.LessOrEqual(t, h.openCount(bad), 2, "valve_min_accounts(3) participants all failing: stop at 2 trips")
	require.GreaterOrEqual(t, h.openCount(bad), 1)
	require.Equal(t, 1, h.logs.count("account_circuit_breaker.valve_blocked"), "valve log is rate limited per platform")
	require.Greater(t, h.b.Stats().ValveBlocked, int64(0))

	// 其他平台不受影响：同一个进程里 openai 账号照常熔断
	h.feed(100, "openai", 502, 20)
	require.True(t, h.b.IsOpen(100))
}

func TestCircuitBreakerValveDoesNotLimitSmallPools(t *testing.T) {
	// 事故现场就是 2 个账号：小池子必须能全部熔断，安全阀不能挡住它。
	h := newCircuitHarness(t, nil)
	for i := 0; i < 20; i++ {
		h.feed(1, "anthropic", 503, 1)
		h.feed(2, "anthropic", 502, 1)
	}
	require.True(t, h.b.IsOpen(1))
	require.True(t, h.b.IsOpen(2))
	require.Zero(t, h.logs.count("account_circuit_breaker.valve_blocked"))
}

func TestCircuitBreakerValveSeesLowTrafficAndOtherPlatformsAsNoEvidence(t *testing.T) {
	// 样本数不足的低流量账号既不算参与也不算健康（看不出它好不好）；别的平台的健康账号同样不算。
	h := newCircuitHarness(t, nil)
	h.feed(10, "anthropic", 200, 5)
	h.feed(11, "anthropic", 200, 5)
	h.feed(12, "openai", 200, 100)
	for i := 0; i < 20; i++ {
		for id := int64(1); id <= 4; id++ {
			h.feed(id, "anthropic", 502, 1)
		}
	}
	require.LessOrEqual(t, h.openCount([]int64{1, 2, 3, 4}), 2, "4 failing participants, no healthy participant on this platform: the valve holds")
	require.GreaterOrEqual(t, h.logs.count("account_circuit_breaker.valve_blocked"), 1)
}

func TestCircuitBreakerValveRecoversWhenAHealthyAccountAppears(t *testing.T) {
	h := newCircuitHarness(t, nil)
	bad, _ := h.valveScenario("anthropic", 1, 6, 0, 40)
	require.Greater(t, h.b.Stats().ValveBlocked, int64(0))
	var stuck int64
	for _, id := range bad {
		if !h.b.IsOpen(id) {
			stuck = id
			break
		}
	}
	require.NotZero(t, stuck)

	// 一个健康账号出现（有足够的成功样本）：被安全阀拦下的账号在复查延迟之后下一次失败就会熔断。
	h.feed(200, "anthropic", 200, 25)
	h.clock.Advance(11 * time.Second)
	h.feed(stuck, "anthropic", 502, 1)
	require.True(t, h.b.IsOpen(stuck), "a healthy account exists now: the failing one must be paused")
}

func TestCircuitBreakerValveForgetsOldTrips(t *testing.T) {
	h := newCircuitHarness(t, nil)
	// 先熔断两个账号；第三个账号越线时「参与账号 = 自己 + 2 个刚熔断的 = 3、且没有健康账号」，被安全阀拦下。
	for id := int64(1); id <= 3; id++ {
		h.feed(id, "anthropic", 502, 20)
	}
	require.Equal(t, 2, h.store.tripCount())
	require.False(t, h.b.IsOpen(3))
	// 过了安全阀统计窗口（1 分钟）后，这两次熔断不再计入；第三个账号再失败时可以熔断。
	h.clock.Advance(61 * time.Second)
	h.feed(3, "anthropic", 502, 1)
	require.True(t, h.b.IsOpen(3))
	require.Equal(t, 3, h.store.tripCount())
}

// ---- 内存：没有后台 goroutine，空闲账号状态会被回收 -------------------------------

func TestCircuitBreakerSweepEvictsIdleAccountsAndLogsRecovery(t *testing.T) {
	h := newCircuitHarness(t, nil)
	for id := int64(1); id <= 200; id++ {
		h.feed(id, "anthropic", 200, 1)
	}
	h.feed(500, "anthropic", 502, 20) // 熔断后再没有流量
	require.Equal(t, int64(201), h.b.Stats().TrackedAccount)
	require.True(t, h.b.IsOpen(500))

	h.clock.Advance(11 * time.Minute) // 超过 2 个窗口：所有空闲账号都过期
	h.feed(9999, "anthropic", 200, 1) // 触发一次顺手清理
	stats := h.b.Stats()
	require.Equal(t, 1, h.logs.count("account_circuit_breaker.recovered"), "recovery is logged exactly once even with no traffic after the cooldown")
	require.Equal(t, int64(1), stats.TrackedAccount, "idle account states must be reclaimed, otherwise the map grows with every account ever seen")
	require.Equal(t, int64(1), stats.Recoveries)
}

func TestCircuitBreakerSweepKeepsStatesThatAreStillInUse(t *testing.T) {
	h := newCircuitHarness(t, nil)
	h.feed(1, "anthropic", 200, 1)
	h.clock.Advance(3 * time.Minute)
	h.feed(2, "anthropic", 200, 1)
	h.clock.Advance(3 * time.Minute) // 账号 1 空闲 6 分钟（< 2 个窗口 = 10 分钟）
	h.feed(3, "anthropic", 200, 1)
	require.Equal(t, int64(3), h.b.Stats().TrackedAccount)
}

// ---- 日志限频 -----------------------------------------------------------------

func TestCircuitBreakerLogsAreRateLimitedPerAccount(t *testing.T) {
	h := newCircuitHarness(t, func(s *AccountCircuitBreakerSettings) { s.Cooldown = 5 * time.Second })
	for i := 0; i < 6; i++ { // 6 次熔断/恢复，总共不到 1 分钟
		h.feed(1, "anthropic", 502, 20)
		h.clock.Advance(6 * time.Second)
	}
	require.Equal(t, 6, h.store.tripCount())
	require.Equal(t, 1, h.logs.count("account_circuit_breaker.tripped"), "one ERROR line per account per minute, not one per trip")
	h.feed(1, "anthropic", 502, 1) // 触发上一次的恢复检测
	require.LessOrEqual(t, h.logs.count("account_circuit_breaker.recovered"), 1)
}

// ---- 并发 ---------------------------------------------------------------------

func TestCircuitBreakerConcurrentObservationsTripOnce(t *testing.T) {
	store := &circuitPersistRecorder{}
	b := NewAccountCircuitBreaker(DefaultAccountCircuitBreakerSettings(), store.persist)
	b.log = &circuitLogRecorder{}
	ctx := circuitCtxFor(1, "anthropic", "a")

	var wg sync.WaitGroup
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				b.ObserveUpstreamAttempt(ctx, 1, 502, nil)
			}
		}()
	}
	wg.Wait()
	require.Eventually(t, func() bool { return store.callCount() >= 1 }, 2*time.Second, 5*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, 1, store.callCount(), "exactly one write for a burst of 128000 failing attempts on one account")
	require.True(t, b.IsOpen(1))
}

func TestCircuitBreakerConcurrentManyAccountsRace(t *testing.T) {
	store := &circuitPersistRecorder{}
	b := NewAccountCircuitBreaker(DefaultAccountCircuitBreakerSettings(), store.persist)
	b.log = &circuitLogRecorder{}
	var wg sync.WaitGroup
	var observed atomic.Int64
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 3000; i++ {
				id := int64(1 + (g+i)%40)
				ctx := circuitCtxFor(id, "anthropic", "a")
				status := 200
				if id%2 == 0 {
					status = 502 // 偶数账号全失败，奇数账号全成功
				}
				b.ObserveUpstreamAttempt(ctx, id, status, nil)
				observed.Add(1)
				if i%500 == 0 {
					b.IsOpen(id)
					b.Stats()
				}
			}
		}(g)
	}
	wg.Wait()
	for id := int64(1); id <= 40; id += 2 {
		require.False(t, b.IsOpen(id), "healthy account %d must never be tripped", id)
	}
	require.Equal(t, int64(32*3000), observed.Load())
}

// ---- 参数 ---------------------------------------------------------------------

type circuitConfigWarning struct {
	msg    string
	fields map[string]any
}

// captureCircuitConfigWarnings 接管「配置值被调整」的 WARN 输出，返回的切片随调用累积。
func captureCircuitConfigWarnings(t *testing.T) *[]circuitConfigWarning {
	t.Helper()
	old := circuitConfigWarner
	got := &[]circuitConfigWarning{}
	circuitConfigWarner = func(msg string, fields ...zap.Field) {
		enc := zapcore.NewMapObjectEncoder()
		for _, f := range fields {
			f.AddTo(enc)
		}
		*got = append(*got, circuitConfigWarning{msg: msg, fields: enc.Fields})
	}
	t.Cleanup(func() { circuitConfigWarner = old })
	return got
}

func circuitSettingsFor(c config.AccountCircuitBreakerConfig) AccountCircuitBreakerSettings {
	cfg := &config.Config{}
	c.Enabled = true
	cfg.Gateway.AccountCircuitBreaker = c
	return AccountCircuitBreakerSettingsFromConfig(cfg)
}

func TestAccountCircuitBreakerSettingsFromConfig(t *testing.T) {
	t.Run("nil config disables", func(t *testing.T) {
		require.False(t, AccountCircuitBreakerSettingsFromConfig(nil).Enabled)
	})
	t.Run("zero-value config struct is disabled (tests that build a bare Config keep old behavior)", func(t *testing.T) {
		require.False(t, AccountCircuitBreakerSettingsFromConfig(&config.Config{}).Enabled)
	})
	t.Run("config values are honored", func(t *testing.T) {
		warnings := captureCircuitConfigWarnings(t)
		cfg := &config.Config{}
		cfg.Gateway.AccountCircuitBreaker = config.AccountCircuitBreakerConfig{
			Enabled: true, WindowSeconds: 120, ErrorRate: 0.9, MinRequests: 50, CooldownSeconds: 30,
			BackoffEnabled: true, MaxCooldownSeconds: 600, ValveMinAccounts: 4, FastTripMinRequests: 250,
		}
		s := AccountCircuitBreakerSettingsFromConfig(cfg)
		require.True(t, s.Enabled)
		require.Equal(t, 120*time.Second, s.Window)
		require.Equal(t, 0.9, s.ErrorRate)
		require.Equal(t, 50, s.MinRequests)
		require.Equal(t, 30*time.Second, s.Cooldown)
		require.True(t, s.BackoffEnabled)
		require.Equal(t, 600*time.Second, s.MaxCooldown)
		require.Equal(t, 4, s.ValveMinAccounts)
		require.Equal(t, 250, s.FastTripMinRequests)
		require.Empty(t, *warnings, "valid values never warn")
	})
	t.Run("invalid numbers fall back to defaults instead of disabling protection", func(t *testing.T) {
		warnings := captureCircuitConfigWarnings(t)
		s := circuitSettingsFor(config.AccountCircuitBreakerConfig{
			WindowSeconds: -1, ErrorRate: 150, MinRequests: 0, CooldownSeconds: -5,
			MaxCooldownSeconds: -1, ValveMinAccounts: -2, FastTripMinRequests: -9,
		})
		d := DefaultAccountCircuitBreakerSettings()
		require.Equal(t, d.Window, s.Window)
		require.Equal(t, d.ErrorRate, s.ErrorRate)
		require.Equal(t, d.MinRequests, s.MinRequests)
		require.Equal(t, d.Cooldown, s.Cooldown)
		require.Equal(t, d.MaxCooldown, s.MaxCooldown)
		require.Equal(t, d.ValveMinAccounts, s.ValveMinAccounts)
		require.Zero(t, s.FastTripMinRequests, "a negative fast trip threshold means off")
		require.Len(t, *warnings, 1, "the unusable error_rate is reported")
	})
	t.Run("defaults match the documented values", func(t *testing.T) {
		d := DefaultAccountCircuitBreakerSettings()
		require.True(t, d.Enabled)
		require.Equal(t, 300*time.Second, d.Window)
		require.Equal(t, 0.95, d.ErrorRate)
		require.Equal(t, 20, d.MinRequests)
		require.Equal(t, 60*time.Second, d.Cooldown)
		require.False(t, d.BackoffEnabled)
		require.Equal(t, 3, d.ValveMinAccounts)
		require.Equal(t, 100, d.FastTripMinRequests)
	})
}

func TestAccountCircuitBreakerErrorRateAcceptsPercentagesWithAWarning(t *testing.T) {
	cases := []struct {
		name     string
		in       float64
		want     float64
		wantWarn bool
	}{
		{"fraction", 0.95, 0.95, false},
		{"unset", 0, 0.95, false},
		{"percentage 95", 95, 0.95, true},
		{"percentage 99.5", 99.5, 0.995, true},
		{"percentage 50", 50, 0.5, true},
		{"percentage just above 1", 1.5, 0.015, true},
		{"exactly 1 is ambiguous", 1, 0.95, true},
		{"100 percent can never be exceeded", 100, 0.95, true},
		{"beyond 100", 150, 0.95, true},
		{"negative", -0.5, 0.95, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			warnings := captureCircuitConfigWarnings(t)
			s := circuitSettingsFor(config.AccountCircuitBreakerConfig{ErrorRate: tc.in})
			require.InDelta(t, tc.want, s.ErrorRate, 1e-9)
			if tc.wantWarn {
				require.Len(t, *warnings, 1)
				require.Equal(t, "account_circuit_breaker.config_adjusted", (*warnings)[0].msg)
				require.Equal(t, "gateway.account_circuit_breaker.error_rate", (*warnings)[0].fields["key"])
				require.InDelta(t, tc.want, (*warnings)[0].fields["effective"], 1e-9, "the log carries the value actually in effect")
			} else {
				require.Empty(t, *warnings)
			}
		})
	}
}

func TestAccountCircuitBreakerMinRequestsFloor(t *testing.T) {
	for _, tc := range []struct {
		in, want int
		warn     bool
	}{
		{1, 5, true}, {2, 5, true}, {4, 5, true}, {5, 5, false}, {6, 6, false}, {20, 20, false}, {0, 20, false}, {-3, 20, false},
	} {
		warnings := captureCircuitConfigWarnings(t)
		s := circuitSettingsFor(config.AccountCircuitBreakerConfig{MinRequests: tc.in})
		require.Equal(t, tc.want, s.MinRequests, "min_requests=%d", tc.in)
		require.Equal(t, tc.warn, len(*warnings) == 1, "min_requests=%d", tc.in)
	}
}

func TestAccountCircuitBreakerFastTripSettingIsOptOut(t *testing.T) {
	require.Equal(t, 0, circuitSettingsFor(config.AccountCircuitBreakerConfig{FastTripMinRequests: 0}).FastTripMinRequests, "explicit 0 turns it off")
	require.Equal(t, 100, circuitSettingsFor(config.AccountCircuitBreakerConfig{FastTripMinRequests: 100}).FastTripMinRequests)
	require.Equal(t, 100, DefaultAccountCircuitBreakerSettings().FastTripMinRequests, "on by default (config supplies 100 when the key is absent)")
}

func TestIsAccountCircuitBreakerReason(t *testing.T) {
	require.True(t, IsAccountCircuitBreakerReason("circuit_breaker: 100.0% of 20 upstream attempts failed in 5m"))
	require.False(t, IsAccountCircuitBreakerReason(""))
	require.False(t, IsAccountCircuitBreakerReason(`{"status_code":429}`))
	require.False(t, IsAccountCircuitBreakerReason("openai_apikey_health_breaker"))
}

func TestFormatCircuitWindow(t *testing.T) {
	require.Equal(t, "5m", formatCircuitWindow(300*time.Second))
	require.Equal(t, "1m", formatCircuitWindow(time.Minute))
	require.Equal(t, "90s", formatCircuitWindow(90*time.Second))
	require.Equal(t, "1.5s", formatCircuitWindow(1500*time.Millisecond))
}

// ---- 基准 ---------------------------------------------------------------------

// 记录一次结果的开销：热路径上每个上游尝试多付这一次。
func BenchmarkAccountCircuitBreakerObserveSuccess(b *testing.B) {
	cb := NewAccountCircuitBreaker(DefaultAccountCircuitBreakerSettings(), nil)
	ctx := circuitCtxFor(42, "anthropic", "bench")
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			cb.ObserveUpstreamAttempt(ctx, 42, 200, nil)
		}
	})
}

func BenchmarkAccountCircuitBreakerObserveSuccessSerial(b *testing.B) {
	cb := NewAccountCircuitBreaker(DefaultAccountCircuitBreakerSettings(), nil)
	ctx := circuitCtxFor(42, "anthropic", "bench")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		cb.ObserveUpstreamAttempt(ctx, 42, 200, nil)
	}
}

// 失败路径还要汇总窗口（10 次原子读）；用不会越线的失败率 + 大样本门槛隔离出「评估」的开销。
func BenchmarkAccountCircuitBreakerObserveFailureSerial(b *testing.B) {
	settings := DefaultAccountCircuitBreakerSettings()
	settings.MinRequests = 1 << 19
	settings.FastTripMinRequests = 0
	cb := NewAccountCircuitBreaker(settings, nil)
	ctx := circuitCtxFor(42, "anthropic", "bench")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		cb.ObserveUpstreamAttempt(ctx, 42, 502, nil)
	}
}

func BenchmarkAccountCircuitBreakerObserveManyAccountsParallel(b *testing.B) {
	cb := NewAccountCircuitBreaker(DefaultAccountCircuitBreakerSettings(), nil)
	ctxs := make([]context.Context, 501)
	for i := range ctxs {
		ctxs[i] = circuitCtxFor(int64(i), "anthropic", "bench")
	}
	var next atomic.Int64
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		id := next.Add(1)
		i := int64(0)
		for pb.Next() {
			i++
			acct := 1 + (id*7+i)%500
			cb.ObserveUpstreamAttempt(ctxs[acct], acct, 200, nil)
		}
	})
}
