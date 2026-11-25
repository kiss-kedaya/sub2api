package service

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// throttleCountingRepo 统计写次数（含失败的尝试），并可让接下来 N 次写失败。
// 写库在后台 goroutine 里发生，计数字段只在 svc.accountWrites.wait() 之后读。
// 其余方法沿用 sessionWindowMockRepo（意外调用会 panic）。
type throttleCountingRepo struct {
	*sessionWindowMockRepo
	windowAttempts int
	extraAttempts  int
	failWindow     int
	failExtra      int
}

func (r *throttleCountingRepo) UpdateSessionWindow(ctx context.Context, id int64, start, end *time.Time, status string) error {
	r.windowAttempts++
	if r.failWindow > 0 {
		r.failWindow--
		return errors.New("db down")
	}
	return r.sessionWindowMockRepo.UpdateSessionWindow(ctx, id, start, end, status)
}

func (r *throttleCountingRepo) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	r.extraAttempts++
	if r.failExtra > 0 {
		r.failExtra--
		return errors.New("db down")
	}
	return r.sessionWindowMockRepo.UpdateExtra(ctx, id, updates)
}

func (r *throttleCountingRepo) SetRateLimited(context.Context, int64, time.Time) error { return nil }
func (r *throttleCountingRepo) SetModelRateLimit(context.Context, int64, string, time.Time, ...string) error {
	return nil
}

func newThrottleCountingRepo() *throttleCountingRepo {
	return &throttleCountingRepo{sessionWindowMockRepo: &sessionWindowMockRepo{}}
}

// atomicWriteRepo 是并发安全的写计数桩。
type atomicWriteRepo struct {
	*sessionWindowMockRepo
	windowWrites atomic.Int32
	extraWrites  atomic.Int32
}

func (r *atomicWriteRepo) UpdateSessionWindow(context.Context, int64, *time.Time, *time.Time, string) error {
	r.windowWrites.Add(1)
	return nil
}

func (r *atomicWriteRepo) UpdateExtra(context.Context, int64, map[string]any) error {
	r.extraWrites.Add(1)
	return nil
}

// blockingWriteRepo 模拟 DB 卡住：每次写都阻塞，直到 release 被关闭或满 5 秒。
type blockingWriteRepo struct {
	*sessionWindowMockRepo
	release      chan struct{}
	windowWrites atomic.Int32
	extraWrites  atomic.Int32
}

func newBlockingWriteRepo() *blockingWriteRepo {
	return &blockingWriteRepo{sessionWindowMockRepo: &sessionWindowMockRepo{}, release: make(chan struct{})}
}

func (r *blockingWriteRepo) block() {
	select {
	case <-r.release:
	case <-time.After(5 * time.Second):
	}
}

func (r *blockingWriteRepo) UpdateSessionWindow(context.Context, int64, *time.Time, *time.Time, string) error {
	r.block()
	r.windowWrites.Add(1)
	return nil
}

func (r *blockingWriteRepo) UpdateExtra(context.Context, int64, map[string]any) error {
	r.block()
	r.extraWrites.Add(1)
	return nil
}

// opLogRepo 按完成先后记录写库操作；windowBlock 非 nil 时 UpdateSessionWindow 会先发
// windowStarted 信号再阻塞到它被关闭（模拟 DB 慢）。
type opLogRepo struct {
	*sessionWindowMockRepo
	mu            sync.Mutex
	ops           []string
	windowBlock   chan struct{}
	windowStarted chan struct{}
}

func newOpLogRepo() *opLogRepo {
	return &opLogRepo{sessionWindowMockRepo: &sessionWindowMockRepo{}}
}

func (r *opLogRepo) record(op string) {
	r.mu.Lock()
	r.ops = append(r.ops, op)
	r.mu.Unlock()
}

func (r *opLogRepo) log() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ops...)
}

func (r *opLogRepo) UpdateSessionWindow(_ context.Context, _ int64, start, end *time.Time, status string) error {
	if r.windowStarted != nil {
		r.windowStarted <- struct{}{}
	}
	if r.windowBlock != nil {
		<-r.windowBlock
	}
	op := "window:" + status
	if start != nil || end != nil {
		op += "+range"
	}
	r.record(op)
	return nil
}

func (r *opLogRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	if v, ok := updates["session_window_utilization"]; ok && v == nil {
		r.record("clear")
		return nil
	}
	r.record("sample")
	return nil
}

func (r *opLogRepo) SetRateLimited(context.Context, int64, time.Time) error {
	r.record("ratelimited")
	return nil
}

// newThrottleTestService 返回时钟可控的 RateLimitService（去抖间隔用手动时钟判断）。
func newThrottleTestService(repo AccountRepository) (*RateLimitService, *writeGateClock) {
	svc := NewRateLimitService(repo, nil, nil, nil, nil)
	clock := newWriteGateClock()
	svc.sessionWindowWrites.now = clock.Now
	svc.passiveUsageWrites.now = clock.Now
	return svc, clock
}

// processSuccess 模拟一个成功响应，并等后台写入落完（UpdateSessionWindow 本身不等 DB）。
func processSuccess(svc *RateLimitService, account *Account, headers http.Header) {
	svc.UpdateSessionWindow(context.Background(), account, headers)
	svc.accountWrites.wait()
}

// anthropicSuccessHeaders 构造一次成功响应的限流头：5h 状态/重置/用量，可附带其它头。
func anthropicSuccessHeaders(status string, reset time.Time, util5h string, extra ...string) http.Header {
	h := http.Header{}
	h.Set("anthropic-ratelimit-unified-5h-status", status)
	h.Set("anthropic-ratelimit-unified-5h-reset", strconv.FormatInt(reset.Unix(), 10))
	if util5h != "" {
		h.Set("anthropic-ratelimit-unified-5h-utilization", util5h)
	}
	for i := 0; i+1 < len(extra); i += 2 {
		h.Set(extra[i], extra[i+1])
	}
	return h
}

// 稳态账号：库里的窗口结束时间 == 响应头里的 reset，不需要改窗口。
func steadyStateAccount(id int64, reset time.Time) *Account {
	end := reset
	return &Account{ID: id, Platform: PlatformAnthropic, Type: AccountTypeOAuth, SessionWindowEnd: &end}
}

// legacy429Headers 是不会触发「窗口耗尽」分支、走老的 rejected 直写路径的 429 头。
func legacy429Headers(reset5h time.Time) http.Header {
	return anthropicSuccessHeaders("allowed", reset5h, "0.41",
		"anthropic-ratelimit-unified-7d-reset", strconv.FormatInt(reset5h.Add(72*time.Hour).Unix(), 10),
		"anthropic-ratelimit-unified-7d-status", "allowed",
		"anthropic-ratelimit-unified-7d-utilization", "0.56",
	)
}

func TestUpdateSessionWindow_TenThousandResponsesWriteOnlyOnce(t *testing.T) {
	// 模拟热点账号：同一账号 1 万个成功响应，窗口/状态/用量都没变。
	// 旧实现每个响应写 2 次（窗口状态 1 次 + 被动采样 1 次）= 2 万次整行 UPDATE。
	const responses = 10000
	reset := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	repo := &sessionWindowMockRepo{}
	svc := newRateLimitServiceForTest(repo)
	account := steadyStateAccount(77, reset)
	headers := anthropicSuccessHeaders("allowed", reset, "0.42",
		"anthropic-ratelimit-unified-7d-utilization", "0.30",
		"anthropic-ratelimit-unified-7d-reset", strconv.FormatInt(reset.Add(72*time.Hour).Unix(), 10),
		"anthropic-ratelimit-unified-7d_oi-utilization", "0.20",
		"anthropic-ratelimit-unified-7d_oi-reset", strconv.FormatInt(reset.Add(72*time.Hour).Unix(), 10),
	)

	for i := 0; i < responses; i++ {
		svc.UpdateSessionWindow(context.Background(), account, headers)
	}
	svc.accountWrites.wait()

	writes := len(repo.sessionWindowCalls) + len(repo.updateExtraCalls)
	t.Logf("%d 个响应：repo 写次数 %d（旧实现 %d）", responses, writes, responses*2)
	require.LessOrEqual(t, writes, 9, "1 万次响应的写库次数必须降到个位数")
	require.Equal(t, 1, len(repo.sessionWindowCalls), "窗口状态只写一次")
	require.Equal(t, 1, len(repo.updateExtraCalls), "被动采样只写一次")

	// 第一次写的内容与历史一致：状态照写、窗口没变所以不带 start/end、采样带全部头。
	call := repo.sessionWindowCalls[0]
	require.Equal(t, "allowed", call.Status)
	require.Nil(t, call.Start)
	require.Nil(t, call.End)
	updates := repo.updateExtraCalls[0].Updates
	require.Equal(t, 0.42, updates["session_window_utilization"])
	require.Equal(t, 0.30, updates["passive_usage_7d_utilization"])
	require.Equal(t, 0.20, updates["passive_usage_7d_oi_utilization"])
	require.Contains(t, updates, "passive_usage_sampled_at")
}

func TestUpdateSessionWindow_RepeatedResponsesWriteOnceThenRefreshAfterInterval(t *testing.T) {
	reset := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	repo := newThrottleCountingRepo()
	svc, clock := newThrottleTestService(repo)
	account := steadyStateAccount(1, reset)
	headers := anthropicSuccessHeaders("allowed", reset, "0.42")

	for i := 0; i < 50; i++ {
		processSuccess(svc, account, headers)
	}
	require.Equal(t, 1, repo.windowAttempts)
	require.Equal(t, 1, repo.extraAttempts)

	clock.Advance(accountWriteGateInterval - time.Second)
	processSuccess(svc, account, headers)
	require.Equal(t, 1, repo.windowAttempts, "没到间隔不写")
	require.Equal(t, 1, repo.extraAttempts)

	clock.Advance(time.Second)
	processSuccess(svc, account, headers)
	require.Equal(t, 2, repo.windowAttempts, "到间隔强制刷新窗口状态")
	require.Equal(t, 2, repo.extraAttempts, "到间隔强制刷新采样（passive_usage_sampled_at）")

	for i := 0; i < 50; i++ {
		processSuccess(svc, account, headers)
	}
	require.Equal(t, 2, repo.windowAttempts)
	require.Equal(t, 2, repo.extraAttempts)
}

func TestUpdateSessionWindow_StatusChangeWritesWindowButNotSample(t *testing.T) {
	reset := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	repo := newThrottleCountingRepo()
	svc, _ := newThrottleTestService(repo)
	account := steadyStateAccount(1, reset)

	for i := 0; i < 3; i++ {
		processSuccess(svc, account, anthropicSuccessHeaders("allowed", reset, "0.42"))
	}
	require.Equal(t, 1, repo.windowAttempts)

	for i := 0; i < 3; i++ {
		processSuccess(svc, account, anthropicSuccessHeaders("allowed_warning", reset, "0.42"))
	}
	require.Equal(t, 2, repo.windowAttempts, "状态 allowed→allowed_warning 要立刻写")
	require.Equal(t, "allowed_warning", repo.sessionWindowCalls[1].Status)
	require.Equal(t, 1, repo.extraAttempts, "用量没变，采样不重复写")

	processSuccess(svc, account, anthropicSuccessHeaders("allowed", reset, "0.42"))
	require.Equal(t, 3, repo.windowAttempts, "状态回到 allowed 也要写")
}

func TestUpdateSessionWindow_WindowChangeWritesOncePerNewWindow(t *testing.T) {
	reset1 := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	reset2 := reset1.Add(30 * time.Minute)
	repo := newThrottleCountingRepo()
	svc, _ := newThrottleTestService(repo)
	// 库里是旧窗口 reset1，响应头已经是 reset2（修正预测值 / 窗口被上游改了）
	account := steadyStateAccount(1, reset1)

	for i := 0; i < 20; i++ {
		processSuccess(svc, account, anthropicSuccessHeaders("allowed", reset2, "0.42"))
	}
	require.Equal(t, 1, repo.windowAttempts, "新 reset 只写一次，调度快照滞后时重复的不再写")
	call := repo.sessionWindowCalls[0]
	require.NotNil(t, call.End)
	require.True(t, call.End.Equal(reset2))
	require.NotNil(t, call.Start)
	require.True(t, call.Start.Equal(reset2.Add(-5*time.Hour)))

	reset3 := reset2.Add(time.Hour)
	processSuccess(svc, account, anthropicSuccessHeaders("allowed", reset3, "0.42"))
	require.Equal(t, 2, repo.windowAttempts, "又一个新 reset 要立刻写")
}

func TestUpdateSessionWindow_UtilizationDeltaThreshold(t *testing.T) {
	reset := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	repo := newThrottleCountingRepo()
	svc, _ := newThrottleTestService(repo)
	account := steadyStateAccount(1, reset)
	call := func(util string) {
		processSuccess(svc, account, anthropicSuccessHeaders("allowed", reset, util))
	}
	lastWritten := func() any {
		return repo.updateExtraCalls[len(repo.updateExtraCalls)-1].Updates["session_window_utilization"]
	}

	call("0.50")
	require.Equal(t, 1, repo.extraAttempts, "首次必写")
	call("0.505")
	require.Equal(t, 1, repo.extraAttempts, "变化 0.005 < 0.01 丢弃")
	call("0.509")
	require.Equal(t, 1, repo.extraAttempts, "变化 0.009 < 0.01 丢弃")
	require.Equal(t, 0.50, lastWritten())

	call("0.51")
	require.Equal(t, 2, repo.extraAttempts, "变化 0.01 要写")
	require.Equal(t, 0.51, lastWritten(), "写的是最新值")
	call("0.515")
	require.Equal(t, 2, repo.extraAttempts, "与上次写入值 0.51 比只差 0.005")
	call("0.52")
	require.Equal(t, 3, repo.extraAttempts, "累计偏离上次写入值 0.01 要写")
	call("0.40")
	require.Equal(t, 4, repo.extraAttempts, "下降同样算变化")
}

func TestUpdateSessionWindow_SampleResetChangeWritesImmediately(t *testing.T) {
	reset := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	resetOI := reset.Add(72 * time.Hour)
	repo := newThrottleCountingRepo()
	svc, _ := newThrottleTestService(repo)
	account := steadyStateAccount(1, reset)
	call := func(oiReset time.Time) {
		processSuccess(svc, account, anthropicSuccessHeaders("allowed", reset, "0.42",
			"anthropic-ratelimit-unified-7d_oi-utilization", "0.20",
			"anthropic-ratelimit-unified-7d_oi-reset", strconv.FormatInt(oiReset.Unix(), 10)))
	}

	call(resetOI)
	call(resetOI)
	require.Equal(t, 1, repo.extraAttempts)
	call(resetOI.Add(7 * 24 * time.Hour))
	require.Equal(t, 2, repo.extraAttempts, "*_reset 变了（7d_oi 窗口翻页）要立刻写")
	require.EqualValues(t, resetOI.Add(7*24*time.Hour).Unix(), repo.updateExtraCalls[1].Updates["passive_usage_7d_oi_reset"])
}

func TestUpdateSessionWindow_WindowResetStillClearsAndResamples(t *testing.T) {
	// 5h 窗口翻页：旧采样必须清空，而且清空后的第一次采样必须落库，
	// 哪怕用量和翻页前上一次写入的值一模一样（0.50）、间隔也没到。
	reset1 := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	repo := newThrottleCountingRepo()
	svc, _ := newThrottleTestService(repo)

	processSuccess(svc, steadyStateAccount(1, reset1), anthropicSuccessHeaders("allowed", reset1, "0.50"))
	require.Equal(t, 1, repo.windowAttempts)
	require.Equal(t, 1, repo.extraAttempts)

	// 窗口过期：库里的 SessionWindowEnd 已在过去 → needInitWindow；响应头给出新窗口 reset2。
	expired := time.Now().Add(-time.Minute)
	staleAccount := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeOAuth, SessionWindowEnd: &expired}
	reset2 := time.Now().Add(5 * time.Hour).Truncate(time.Second)
	headers := anthropicSuccessHeaders("allowed", reset2, "0.50")

	processSuccess(svc, staleAccount, headers)

	require.Equal(t, 2, repo.windowAttempts, "新窗口要写")
	require.True(t, repo.sessionWindowCalls[1].End.Equal(reset2))
	require.Equal(t, 3, repo.extraAttempts, "清空 + 重新采样各一次")
	clearUpdates := repo.updateExtraCalls[1].Updates
	for _, key := range []string{
		"session_window_utilization", "passive_usage_7d_utilization", "passive_usage_7d_reset",
		"passive_usage_7d_oi_utilization", "passive_usage_7d_oi_reset", "passive_usage_sampled_at",
	} {
		val, present := clearUpdates[key]
		require.True(t, present && val == nil, "%s 必须被清成 nil", key)
	}
	require.Equal(t, 0.50, repo.updateExtraCalls[2].Updates["session_window_utilization"], "清空后立刻重新采样")

	// 调度快照还没刷新（账号对象仍是过期窗口）时，后续响应不能再清空、不能重复写。
	for i := 0; i < 20; i++ {
		processSuccess(svc, staleAccount, headers)
	}
	require.Equal(t, 2, repo.windowAttempts)
	require.Equal(t, 3, repo.extraAttempts, "快照滞后期间不能反复清空采样")
}

func TestUpdateSessionWindow_WindowResetRunsClearThenWindowThenSampleInOneClosure(t *testing.T) {
	reset1 := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	repo := newOpLogRepo()
	svc, _ := newThrottleTestService(repo)

	processSuccess(svc, steadyStateAccount(1, reset1), anthropicSuccessHeaders("allowed", reset1, "0.50"))
	expired := time.Now().Add(-time.Minute)
	staleAccount := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeOAuth, SessionWindowEnd: &expired}
	reset2 := time.Now().Add(5 * time.Hour).Truncate(time.Second)
	processSuccess(svc, staleAccount, anthropicSuccessHeaders("allowed", reset2, "0.50"))

	require.Equal(t, []string{
		"window:allowed", "sample", // 稳态：窗口没变，只写状态 + 采样
		"clear", "window:allowed+range", "sample", // 翻页：清空必须先于窗口和新采样，否则新采样会被清掉
	}, repo.log())
}

func TestUpdateSessionWindow_FailedWritesAreRetriedNotRecorded(t *testing.T) {
	reset := time.Now().Add(3 * time.Hour).Truncate(time.Second)

	t.Run("窗口状态写失败下次重试", func(t *testing.T) {
		repo := newThrottleCountingRepo()
		repo.failWindow = 2
		svc, _ := newThrottleTestService(repo)
		account := steadyStateAccount(1, reset)
		headers := anthropicSuccessHeaders("allowed", reset, "0.42")

		processSuccess(svc, account, headers)
		processSuccess(svc, account, headers)
		require.Equal(t, 2, repo.windowAttempts, "失败后每个请求都继续尝试")
		processSuccess(svc, account, headers)
		require.Equal(t, 3, repo.windowAttempts, "第 3 次成功")
		processSuccess(svc, account, headers)
		require.Equal(t, 3, repo.windowAttempts, "成功之后才开始去抖")
		require.Equal(t, 1, repo.extraAttempts, "采样不受窗口写失败影响，只写一次")
	})

	t.Run("被动采样写失败下次重试", func(t *testing.T) {
		repo := newThrottleCountingRepo()
		repo.failExtra = 1
		svc, _ := newThrottleTestService(repo)
		account := steadyStateAccount(1, reset)
		headers := anthropicSuccessHeaders("allowed", reset, "0.42")

		processSuccess(svc, account, headers)
		require.Equal(t, 1, repo.extraAttempts)
		require.Empty(t, repo.updateExtraCalls, "第一次失败，没有落库")
		processSuccess(svc, account, headers)
		require.Equal(t, 2, repo.extraAttempts, "失败不能记成已写入")
		require.Len(t, repo.updateExtraCalls, 1)
		processSuccess(svc, account, headers)
		require.Equal(t, 2, repo.extraAttempts)
		require.Equal(t, 1, repo.windowAttempts, "窗口状态已成功，不重复写")
	})
}

func TestUpdateSessionWindow_ThousandConcurrentCallsOneRound(t *testing.T) {
	reset := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	hammer := func(svc *RateLimitService, account *Account, headers http.Header) {
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < 1000; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				svc.UpdateSessionWindow(context.Background(), account, headers)
			}()
		}
		close(start)
		wg.Wait()
		svc.accountWrites.wait()
	}

	t.Run("只有窗口状态：1000 次并发恰好 1 次 DB 写", func(t *testing.T) {
		repo := &atomicWriteRepo{sessionWindowMockRepo: &sessionWindowMockRepo{}}
		svc := NewRateLimitService(repo, nil, nil, nil, nil)
		hammer(svc, steadyStateAccount(1, reset), anthropicSuccessHeaders("allowed", reset, ""))

		require.EqualValues(t, 1, repo.windowWrites.Load())
		require.EqualValues(t, 0, repo.extraWrites.Load())
	})

	t.Run("窗口状态 + 采样：1000 次并发只有 1 轮（窗口 1 次 + 采样 1 次）", func(t *testing.T) {
		repo := &atomicWriteRepo{sessionWindowMockRepo: &sessionWindowMockRepo{}}
		svc := NewRateLimitService(repo, nil, nil, nil, nil)
		hammer(svc, steadyStateAccount(1, reset), anthropicSuccessHeaders("allowed", reset, "0.42"))

		require.EqualValues(t, 1, repo.windowWrites.Load())
		require.EqualValues(t, 1, repo.extraWrites.Load())
	})
}

func TestUpdateSessionWindow_AccountsAreThrottledIndependently(t *testing.T) {
	reset := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	repo := newThrottleCountingRepo()
	svc, _ := newThrottleTestService(repo)
	headers := anthropicSuccessHeaders("allowed", reset, "0.42")

	for round := 0; round < 3; round++ {
		for id := int64(1); id <= 5; id++ {
			processSuccess(svc, steadyStateAccount(id, reset), headers)
		}
	}
	require.Equal(t, 5, repo.windowAttempts, "每个账号各写一次")
	require.Equal(t, 5, repo.extraAttempts)
}

func TestUpdateSessionWindow_DoesNotWaitForSlowDatabase(t *testing.T) {
	// DB 写阻塞（热点行锁排队）时，请求路径不能被卡住：UpdateSessionWindow 只做 map 查找。
	reset := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	repo := newBlockingWriteRepo()
	svc := NewRateLimitService(repo, nil, nil, nil, nil)
	headers := anthropicSuccessHeaders("allowed", reset, "0.42")

	fastest, slowest := time.Hour, time.Duration(0)
	for id := int64(1); id <= 5; id++ {
		begin := time.Now()
		svc.UpdateSessionWindow(context.Background(), steadyStateAccount(id, reset), headers)
		elapsed := time.Since(begin)
		if elapsed < fastest {
			fastest = elapsed
		}
		if elapsed > slowest {
			slowest = elapsed
		}
	}
	// 取最快的一次对 10ms：整机被别的任务拖慢时单次调用可能被调度打断，
	// 但只要有一次能在 10ms 内返回，就说明调用本身没有在等 DB（等 DB 会每次都卡满 5 秒）。
	require.Less(t, fastest, 10*time.Millisecond)
	require.Less(t, slowest, time.Second, "库阻塞 5 秒，调用不能跟着等")
	require.EqualValues(t, 0, repo.windowWrites.Load()+repo.extraWrites.Load(), "此刻库写入还卡着，调用已经返回")

	close(repo.release)
	svc.accountWrites.wait()
	require.EqualValues(t, 5, repo.windowWrites.Load(), "库恢复后后台写入照常完成")
	require.EqualValues(t, 5, repo.extraWrites.Load())
}

func TestUpdateSessionWindow_DropsWhenSlotsFullAndRetriesLater(t *testing.T) {
	reset := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	repo := newBlockingWriteRepo()
	svc := NewRateLimitService(repo, nil, nil, nil, nil)
	svc.accountWrites.maxInflight = 2
	headers := anthropicSuccessHeaders("allowed", reset, "0.42")

	// 账号 1、2 占满两个槽位（写库卡着）。
	svc.UpdateSessionWindow(context.Background(), steadyStateAccount(1, reset), headers)
	svc.UpdateSessionWindow(context.Background(), steadyStateAccount(2, reset), headers)

	// 账号 3 没有槽位：丢弃，不排队、不阻塞。
	begin := time.Now()
	svc.UpdateSessionWindow(context.Background(), steadyStateAccount(3, reset), headers)
	require.Less(t, time.Since(begin), time.Second, "槽位满时调用不能阻塞")

	close(repo.release)
	svc.accountWrites.wait()
	require.EqualValues(t, 2, repo.windowWrites.Load(), "账号 3 的写入被丢弃，没有产生 DB 写")
	require.EqualValues(t, 2, repo.extraWrites.Load())

	// 丢弃时撤销了去抖记账：账号 3 的下一个响应会再试，这次有槽位了。
	processSuccess(svc, steadyStateAccount(3, reset), headers)
	require.EqualValues(t, 3, repo.windowWrites.Load())
	require.EqualValues(t, 3, repo.extraWrites.Load())
}

func TestUpdateSessionWindow_BusyAccountDropsChangedStateAndRetries(t *testing.T) {
	reset := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	repo := newOpLogRepo()
	repo.windowBlock = make(chan struct{})
	repo.windowStarted = make(chan struct{}, 4)
	svc, _ := newThrottleTestService(repo)
	account := steadyStateAccount(1, reset)

	svc.UpdateSessionWindow(context.Background(), account, anthropicSuccessHeaders("allowed", reset, "0.42"))
	<-repo.windowStarted // 后台闭包已经卡在写窗口状态上

	// 同一账号还有在途写入时又来一个状态变化的响应：不排队、不乱序，直接丢弃。
	begin := time.Now()
	svc.UpdateSessionWindow(context.Background(), account, anthropicSuccessHeaders("allowed_warning", reset, "0.42"))
	require.Less(t, time.Since(begin), time.Second)

	close(repo.windowBlock)
	svc.accountWrites.wait()
	require.Equal(t, []string{"window:allowed", "sample"}, repo.log(), "被丢弃的那次没有写库")

	// 丢弃时撤销了记账：下一个响应（allowed_warning）会把状态写进去，且采样没变不重复写。
	processSuccess(svc, account, anthropicSuccessHeaders("allowed_warning", reset, "0.42"))
	require.Equal(t, []string{"window:allowed", "sample", "window:allowed_warning"}, repo.log())
}

func TestUpdateSessionWindow_RateLimited429WriteInvalidatesWindowGate(t *testing.T) {
	// 成功响应记下「allowed」→ 429 路径直接把库里写成 rejected →
	// 之后的成功响应必须把状态写回 allowed，不能被「和上次一样」挡掉。
	reset := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	repo := newThrottleCountingRepo()
	svc, _ := newThrottleTestService(repo)
	account := steadyStateAccount(1, reset)
	ok := anthropicSuccessHeaders("allowed", reset, "0.42")

	processSuccess(svc, account, ok)
	processSuccess(svc, account, ok)
	require.Equal(t, 1, repo.windowAttempts)

	svc.HandleUpstreamError(context.Background(), account, http.StatusTooManyRequests, legacy429Headers(reset), nil, "claude-opus-5")
	require.Equal(t, 2, repo.windowAttempts, "429 路径直接写了 rejected")
	require.Equal(t, "rejected", repo.sessionWindowCalls[1].Status)

	processSuccess(svc, account, ok)
	require.Equal(t, 3, repo.windowAttempts, "之后的成功响应必须把状态写回 allowed")
	require.Equal(t, "allowed", repo.sessionWindowCalls[2].Status)
}

func TestUpdateSessionWindow_429WaitsForInflightAsyncWriteToKeepOrder(t *testing.T) {
	// 同一账号先后写 allowed 与 rejected 的顺序必须对：成功响应的后台写入还卡在 DB 上时，
	// 429 路径要等它写完再写 rejected，不能让较早的 allowed 反过来盖掉 rejected。
	reset := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	repo := newOpLogRepo()
	repo.windowBlock = make(chan struct{})
	repo.windowStarted = make(chan struct{}, 4)
	svc, _ := newThrottleTestService(repo)
	account := steadyStateAccount(1, reset)

	svc.UpdateSessionWindow(context.Background(), account, anthropicSuccessHeaders("allowed", reset, "0.42"))
	<-repo.windowStarted // allowed 的窗口写入在途（卡住）

	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.HandleUpstreamError(context.Background(), account, http.StatusTooManyRequests, legacy429Headers(reset), nil, "claude-opus-5")
	}()
	select {
	case <-done:
		t.Fatal("429 路径写 rejected 不能越过在途的 allowed 写入")
	case <-time.After(100 * time.Millisecond):
	}

	close(repo.windowBlock)
	<-done
	svc.accountWrites.wait()

	var windowOps []string
	for _, op := range repo.log() {
		if strings.HasPrefix(op, "window:") || op == "sample" {
			windowOps = append(windowOps, op)
		}
	}
	require.Equal(t, []string{"window:allowed", "sample", "window:rejected+range"}, windowOps,
		"allowed 的整轮后台写入先完成，rejected 最后落库")
}

func TestHandleUpstreamError_Fable429PassiveUsageBypassesThrottle(t *testing.T) {
	// 7d_oi 用量 0.995 → 1.0（差 0.005，去抖会丢）。429 之后 Fable 不再调度到该账号，
	// 不会再有响应来补这一次采样，所以必须强制写，否则 7d F 进度条冻结在 0.995。
	now := time.Now()
	reset5h := now.Add(2 * time.Hour).Truncate(time.Second)
	resetOI := now.Add(80 * time.Hour).Truncate(time.Second)
	repo := newThrottleCountingRepo()
	svc, _ := newThrottleTestService(repo)
	account := &Account{ID: 42, Type: AccountTypeOAuth, Platform: PlatformAnthropic, SessionWindowEnd: &reset5h}

	processSuccess(svc, account, anthropicSuccessHeaders("allowed", reset5h, "0.41",
		"anthropic-ratelimit-unified-7d_oi-utilization", "0.995",
		"anthropic-ratelimit-unified-7d_oi-reset", strconv.FormatInt(resetOI.Unix(), 10)))
	require.Equal(t, 1, repo.extraAttempts)

	limited := anthropicSuccessHeaders("allowed", reset5h, "0.41",
		"anthropic-ratelimit-unified-7d_oi-status", "rejected",
		"anthropic-ratelimit-unified-7d_oi-surpassed-threshold", "1.0",
		"anthropic-ratelimit-unified-7d_oi-utilization", "1.0",
		"anthropic-ratelimit-unified-7d_oi-reset", strconv.FormatInt(resetOI.Unix(), 10),
		"anthropic-ratelimit-unified-reset", strconv.FormatInt(resetOI.Unix(), 10),
	)
	svc.HandleUpstreamError(context.Background(), account, http.StatusTooManyRequests, limited,
		[]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"limit"}}`), "claude-fable-5")

	require.Equal(t, 2, repo.extraAttempts, "429 采样必须绕过去抖")
	require.Equal(t, 1.0, repo.updateExtraCalls[1].Updates["passive_usage_7d_oi_utilization"])

	// 强制写入之后同样内容照常被去抖
	processSuccess(svc, account, limited)
	require.Equal(t, 2, repo.extraAttempts)
}
