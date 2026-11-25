package service

import (
	"context"
	"log/slog"
	"math"
	"net/http"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// 热点 Anthropic 账号每个成功响应都会把同一行 accounts 整行重写两次（会话窗口状态 +
// 被动用量采样），热行被行锁串成车队，WAL 和 TOAST 也跟着放大；而且这两次写是在写 SSE
// 响应头之前同步做的，DB 一慢首字就被卡住、还占着连接池。
//
// 两层处理：
//  1. accountWriteGate：进程内按账号记住「上次成功写入的内容」，内容没变且没到刷新间隔就不写；
//  2. accountWriteRunner：真要写的内容放进后台闭包有界异步执行，请求 goroutine 只做 map 查找，永不等 DB。
//
// 两台机器各一个进程、各自处理；内容不变时最坏每进程每账号每个间隔写 1 轮。
const (
	// accountWriteGateInterval 是同样内容最长隔多久强制刷新一次（刷新 passive_usage_sampled_at，
	// 也顺带纠正被别的路径改掉的库值）。展示和阈值调度读到的被动用量最多落后这么久。
	accountWriteGateInterval = 60 * time.Second
	// passiveUsageMinDelta 是 utilization（0-1 小数）变化多少算「变了」：1 个百分点。
	passiveUsageMinDelta = 0.01

	// accountWriteMaxInflight 是同时在途的后台写闭包数上限；满了直接丢弃，不排队。
	accountWriteMaxInflight = 16
	// accountWriteTimeout 是一个后台写闭包（清空 + 窗口 + 采样三步合计）的 DB 时限。
	accountWriteTimeout = 3 * time.Second
	// accountWriteExclusiveWait 是 Exclusive 最多等在途后台写入多久。闭包最迟 accountWriteTimeout 就会被
	// ctx 掐掉，所以正常情况下远等不到这么久；只有 DB 驱动不理 ctx、闭包一直不返回时才会走到这里。
	accountWriteExclusiveWait = accountWriteTimeout + time.Second
	// accountWriteWarnEvery 是「Exclusive 等超时」这条 warn 日志的最小间隔：库卡死时每个 429 都会走到，
	// 一条一行会把日志淹掉。
	accountWriteWarnEvery = 10 * time.Second
)

// accountWriteState 是一次待写入的内容。redundantTo 报告：上次成功写入 last 之后，
// 本次是否没有带来新信息（没有新信息且没到刷新间隔就可以不写）。
type accountWriteState[S any] interface {
	redundantTo(last S) bool
}

type accountWriteRecord[S any] struct {
	state S
	at    time.Time
}

// accountWriteGate 按账号记住上次放行的内容，零值可用。
// ponytail: 单把互斥锁 + 每个间隔全表清扫一次；账号数到万级再按 id 分片。
type accountWriteGate[S accountWriteState[S]] struct {
	now func() time.Time // nil 用 time.Now；测试注入

	mu        sync.Mutex
	last      map[int64]accountWriteRecord[S]
	nextSweep time.Time
}

func (g *accountWriteGate[S]) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

// Allow 报告这次是否要写库。放行时先记账（并发的同样内容只放行第一个），
// 写失败或被丢弃必须调 Forget，不能把没写成功的内容记成已写入。id<=0 不节流。
func (g *accountWriteGate[S]) Allow(id int64, next S) bool {
	if g == nil || id <= 0 {
		return true
	}
	now := g.clock()

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.last == nil {
		g.last = make(map[int64]accountWriteRecord[S])
	}
	if now.After(g.nextSweep) {
		// 超过一个间隔的记录等于没有记录（反正要写），留着只会占内存。
		for k, rec := range g.last {
			if now.Sub(rec.at) >= accountWriteGateInterval {
				delete(g.last, k)
			}
		}
		g.nextSweep = now.Add(accountWriteGateInterval)
	}
	if rec, ok := g.last[id]; ok && now.Sub(rec.at) < accountWriteGateInterval && next.redundantTo(rec.state) {
		return false
	}
	g.last[id] = accountWriteRecord[S]{state: next, at: now}
	return true
}

// Record 无条件记账：绕过去抖强制写入时用，让之后的同样内容被去抖。
func (g *accountWriteGate[S]) Record(id int64, next S) {
	if g == nil || id <= 0 {
		return
	}
	now := g.clock()
	g.mu.Lock()
	if g.last == nil {
		g.last = make(map[int64]accountWriteRecord[S])
	}
	g.last[id] = accountWriteRecord[S]{state: next, at: now}
	g.mu.Unlock()
}

// Forget 忘掉某账号的记录：写失败/被丢弃时撤销记账，或别的路径已经改了库、下一次必须重写。
func (g *accountWriteGate[S]) Forget(id int64) {
	if g == nil {
		return
	}
	g.mu.Lock()
	delete(g.last, id)
	g.mu.Unlock()
}

func (g *accountWriteGate[S]) size() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.last)
}

// accountWriteRunner 把账号行写入放到后台有界执行，零值可用。
//   - 同一账号同一时刻最多一个在途闭包，所以同账号的库写入天然有序；
//   - 同时在途的闭包不超过 accountWriteMaxInflight；
//   - 账号已有在途写入或槽位已满时 Go 立刻返回 false：不排队、不阻塞，调用方撤销去抖记账，
//     下一个响应会再试。
//
// ponytail: 每个闭包一个 goroutine，没有常驻 worker 和队列；闭包量再大就改成定长 worker 池。
type accountWriteRunner struct {
	// 下面三项为 0 时用对应的 accountWrite* 常量；测试可调小。
	maxInflight   int
	writeTimeout  time.Duration
	exclusiveWait time.Duration

	mu       sync.Mutex
	running  int
	inflight map[int64]chan struct{} // 账号 → 该账号当前写入结束时关闭
	wg       sync.WaitGroup

	lastExclusiveWarn atomic.Int64 // Unix 纳秒：上一条「Exclusive 等超时」日志的时间
}

// Go 在后台执行 fn，返回是否已受理。ctx 只用来继承 value：取消信号被剥掉（客户端断开不能
// 把写入取消），改用 accountWriteTimeout 限时。
//
// fn panic、或执行到时限（里面的写入可能没写完）时调用 onFail（可为 nil）：调用方要在这里撤销
// 去抖记账，否则没落库的内容被记成「已写入」，同账号同内容的后续响应会被压制到记账到期。
// fn 里自己能看到的写失败（返回 error）仍由 fn 自己撤销。
func (r *accountWriteRunner) Go(ctx context.Context, id int64, fn func(ctx context.Context), onFail func()) bool {
	limit := r.maxInflight
	if limit <= 0 {
		limit = accountWriteMaxInflight
	}
	timeout := r.writeTimeout
	if timeout <= 0 {
		timeout = accountWriteTimeout
	}
	r.mu.Lock()
	if _, busy := r.inflight[id]; busy || r.running >= limit {
		r.mu.Unlock()
		return false
	}
	if r.inflight == nil {
		r.inflight = make(map[int64]chan struct{})
	}
	done := make(chan struct{})
	r.inflight[id] = done
	r.running++
	r.wg.Add(1)
	r.mu.Unlock()

	bg := context.WithoutCancel(ctx)
	go func() {
		defer func() {
			r.release(id, done)
			r.mu.Lock()
			r.running--
			r.mu.Unlock()
			r.wg.Done()
		}()
		wctx, cancel := context.WithTimeout(bg, timeout)
		defer cancel()
		// 这里的 panic 没有 gin Recovery 兜底，会直接带崩整个进程；吞掉之后要让调用方撤销记账。
		// 这个 defer 比 cancel 后注册、先执行，所以 wctx.Err() 反映的是 fn 自己跑的时候有没有超时。
		finished := false
		defer func() {
			p := recover()
			if p != nil {
				slog.Error("account_write_panic", "account_id", id, "panic", p, "stack", string(debug.Stack()))
			}
			if onFail != nil && (p != nil || !finished || wctx.Err() != nil) {
				onFail()
			}
		}()
		fn(wctx)
		finished = true
	}()
	return true
}

// Exclusive 等该账号在途的后台写入结束后，在当前 goroutine 里同步执行 fn；fn 执行期间该账号
// 的后台写入会被拒绝（丢弃、之后重试）。用于 429 这类必须强制落库、又要与后台写入保持先后顺序
// （同一账号先写 allowed 再写 rejected）的路径。fn 里不能再调 Exclusive（会死锁）。
//
// 等待有上限（accountWriteExclusiveWait）：后台写入最迟 accountWriteTimeout 就被 ctx 掐掉，正常
// 等不到上限；只有 DB 驱动不理 ctx、闭包一直不返回时才会超时。429 的 rejected 状态必须落库，
// 所以超时后不再等、也不占用账号，直接执行 fn（不再保证与那个卡住的后台写入的先后顺序），并打一条限频 warn。
func (r *accountWriteRunner) Exclusive(id int64, fn func()) {
	own, ok := r.acquire(id)
	if ok {
		defer r.release(id, own)
	}
	fn()
}

// acquire 等到该账号没有在途写入，然后占住它。等满 exclusiveWait 仍没等到就返回 (nil, false)，
// 调用方此时没有占用账号（也就不能 release）。
func (r *accountWriteRunner) acquire(id int64) (own chan struct{}, ok bool) {
	wait := r.exclusiveWait
	if wait <= 0 {
		wait = accountWriteExclusiveWait
	}
	var timer *time.Timer // 只在真的要等时才建，一次 Exclusive 共用一个总时限
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		r.mu.Lock()
		done, busy := r.inflight[id]
		if !busy {
			if r.inflight == nil {
				r.inflight = make(map[int64]chan struct{})
			}
			own = make(chan struct{})
			r.inflight[id] = own
			r.mu.Unlock()
			return own, true
		}
		r.mu.Unlock()
		if timer == nil {
			timer = time.NewTimer(wait)
		}
		select {
		case <-done:
		case <-timer.C:
			r.warnExclusiveWaitTimeout(id, wait)
			return nil, false
		}
	}
}

func (r *accountWriteRunner) warnExclusiveWaitTimeout(id int64, waited time.Duration) {
	now := time.Now().UnixNano()
	if last := r.lastExclusiveWarn.Load(); now-last >= int64(accountWriteWarnEvery) && r.lastExclusiveWarn.CompareAndSwap(last, now) {
		slog.Warn("account_write_exclusive_wait_timeout", "account_id", id, "waited", waited,
			"detail", "in-flight background write never returned; writing without waiting for it")
	}
}

func (r *accountWriteRunner) release(id int64, done chan struct{}) {
	r.mu.Lock()
	delete(r.inflight, id)
	r.mu.Unlock()
	close(done)
}

// sessionWindowWrite 是一次 accounts.session_window_* 写入。start/end 零值表示这次只写状态、没带窗口。
type sessionWindowWrite struct {
	start, end time.Time
	status     string
}

func newSessionWindowWrite(start, end *time.Time, status string) sessionWindowWrite {
	w := sessionWindowWrite{status: status}
	if start != nil {
		w.start = *start
	}
	if end != nil {
		w.end = *end
	}
	return w
}

// redundantTo：状态没变，且本次要么没带窗口，要么带的窗口就是上次写进去的。
func (w sessionWindowWrite) redundantTo(last sessionWindowWrite) bool {
	if w.status != last.status {
		return false
	}
	if w.start.IsZero() && w.end.IsZero() {
		return true
	}
	return w.start.Equal(last.start) && w.end.Equal(last.end)
}

type optFloat struct {
	v  float64
	ok bool
}

// near：两次都没有，或都有且相差不足 passiveUsageMinDelta。
// 容差 1e-9 吸收浮点误差，否则 0.29 与 0.28 会因差值 0.00999…95 被当成没变。
func (a optFloat) near(b optFloat) bool {
	if a.ok != b.ok {
		return false
	}
	return !a.ok || math.Abs(a.v-b.v) < passiveUsageMinDelta-1e-9
}

type optInt struct {
	v  int64
	ok bool
}

// passiveUsageSample 是一次响应头里能采到的 5h/7d/7d_oi 被动用量。
type passiveUsageSample struct {
	util5h, util7d, util7dOI optFloat // utilization，0-1 小数
	reset7d, reset7dOI       optInt   // reset，Unix 秒
}

func headerUtilization(h http.Header, name string) optFloat {
	v, err := strconv.ParseFloat(h.Get(name), 64)
	if err != nil {
		return optFloat{}
	}
	return optFloat{v: v, ok: true}
}

func headerResetUnix(h http.Header, name string) optInt {
	ts, err := strconv.ParseInt(h.Get(name), 10, 64)
	if err != nil {
		return optInt{}
	}
	// 毫秒时间戳（秒级约 1e9，毫秒约 1e12）
	if ts > 1e11 {
		ts /= 1000
	}
	return optInt{v: ts, ok: true}
}

// parsePassiveUsageSample 解析被动采样头；一个都没有时 ok=false。
func parsePassiveUsageSample(h http.Header) (s passiveUsageSample, ok bool) {
	s.util5h = headerUtilization(h, "anthropic-ratelimit-unified-5h-utilization")
	s.util7d = headerUtilization(h, "anthropic-ratelimit-unified-7d-utilization")
	s.reset7d = headerResetUnix(h, "anthropic-ratelimit-unified-7d-reset")
	s.util7dOI = headerUtilization(h, "anthropic-ratelimit-unified-7d_oi-utilization")
	s.reset7dOI = headerResetUnix(h, "anthropic-ratelimit-unified-7d_oi-reset")
	ok = s.util5h.ok || s.util7d.ok || s.reset7d.ok || s.util7dOI.ok || s.reset7dOI.ok
	return s, ok
}

// extraUpdates 生成写入 accounts.extra 的合并补丁（键名和值类型与历史一致）。
func (s passiveUsageSample) extraUpdates(sampledAt time.Time) map[string]any {
	updates := make(map[string]any, 6)
	if s.util5h.ok {
		updates["session_window_utilization"] = s.util5h.v
	}
	if s.util7d.ok {
		updates["passive_usage_7d_utilization"] = s.util7d.v
	}
	if s.reset7d.ok {
		updates["passive_usage_7d_reset"] = s.reset7d.v
	}
	if s.util7dOI.ok {
		updates["passive_usage_7d_oi_utilization"] = s.util7dOI.v
	}
	if s.reset7dOI.ok {
		updates["passive_usage_7d_oi_reset"] = s.reset7dOI.v
	}
	updates["passive_usage_sampled_at"] = sampledAt.UTC().Format(time.RFC3339)
	return updates
}

// redundantTo：reset 一个都没变（含有/无），utilization 每项都与上次写入值相差不足 1 个百分点。
func (s passiveUsageSample) redundantTo(last passiveUsageSample) bool {
	return s.reset7d == last.reset7d && s.reset7dOI == last.reset7dOI &&
		s.util5h.near(last.util5h) && s.util7d.near(last.util7d) && s.util7dOI.near(last.util7dOI)
}
