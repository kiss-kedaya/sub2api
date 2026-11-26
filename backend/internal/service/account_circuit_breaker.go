package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

// 账号错误率断路器
//
// 事故背景：某个上游账号长时间返回 5xx，账号一直 active + schedulable，每个客户端请求失败后
// 都被立刻重新调度、同账号重试、再换号，客户端自己还在重试，把 CPU / Redis / PG 写入全部放大。
// 现有的 429 / 529 冷却只管那两个状态码，管不到 502/503/传输错误。
//
// 做法：每个进程为每个账号维护一个 5 分钟滑动窗口（10 个桶，原子计数、热路径无锁、无 Redis/DB 往返）。
// 窗口内「对该账号发起的上游尝试」数 >= MinRequests 且 失败率 严格大于 ErrorRate 时，把账号写成
// 临时不可调度 Cooldown（走现有 accounts.temp_unschedulable_* 机制，调度选号、粘连会话、
// 快照/outbox、后台账号列表都认它），冷却到期自动恢复。
//
// 多进程语义：每个进程只看自己的流量；熔断动作是一次幂等写（SQL 里 until 取 max，不会缩短已有的
// 更长冷却，也不碰手动 schedulable=false）。同一进程对同一账号在一次冷却期内最多写一次库。

const (
	// accountCircuitBreakerReasonPrefix 是写进 temp_unschedulable_reason 的固定前缀。
	accountCircuitBreakerReasonPrefix = "circuit_breaker:"

	// circuitBuckets 是滑动窗口的桶数；窗口长度 = 桶数 * 桶宽。
	circuitBuckets = 10

	// 单个桶用一个 uint64 打包：epoch(24) | total(20) | failed(20)，CAS 更新，轮转无锁。
	circuitEpochBits = 24
	circuitCountBits = 20
	circuitEpochMask = (uint64(1) << circuitEpochBits) - 1
	circuitCountMask = (uint64(1) << circuitCountBits) - 1

	circuitSweepInterval = time.Minute
	// circuitValveWindow 是安全阀统计「已被熔断账号数」的时间窗。
	circuitValveWindow = time.Minute
	// circuitValveRecheckDelay 安全阀拦下某账号后，隔多久才再评估它（避免每个失败请求都重算 + 刷日志）。
	circuitValveRecheckDelay = 10 * time.Second
	// circuitPersistRetryDelay 写库失败后，隔多久才允许再次尝试熔断该账号。
	circuitPersistRetryDelay = 5 * time.Second
	circuitPersistTimeout    = 5 * time.Second
	// circuitLogThrottle 同一账号同一类日志的最小间隔。
	circuitLogThrottle = time.Minute
	// circuitMaxInflightPersist 限制同时进行的写库 goroutine 数（熔断是低频事件，超过说明在大面积故障）。
	circuitMaxInflightPersist = 32
)

// AccountCircuitBreakerSettings 是归一化后的断路器参数。
type AccountCircuitBreakerSettings struct {
	Enabled          bool
	Window           time.Duration
	ErrorRate        float64
	MinRequests      int
	Cooldown         time.Duration
	BackoffEnabled   bool
	MaxCooldown      time.Duration
	ValveMinAccounts int
	ValveRatio       float64
}

// DefaultAccountCircuitBreakerSettings 返回与 config 默认值一致的参数。
func DefaultAccountCircuitBreakerSettings() AccountCircuitBreakerSettings {
	return AccountCircuitBreakerSettings{
		Enabled:          true,
		Window:           300 * time.Second,
		ErrorRate:        0.95,
		MinRequests:      20,
		Cooldown:         60 * time.Second,
		BackoffEnabled:   false,
		MaxCooldown:      300 * time.Second,
		ValveMinAccounts: 3,
		ValveRatio:       0.5,
	}
}

// AccountCircuitBreakerSettingsFromConfig 从配置取参数；非法值回落到默认值（宁可用默认值也不要因为
// 一个写错的数字把保护关掉或让进程起不来）。cfg 为 nil 或未启用时 Enabled=false。
func AccountCircuitBreakerSettingsFromConfig(cfg *config.Config) AccountCircuitBreakerSettings {
	s := DefaultAccountCircuitBreakerSettings()
	if cfg == nil {
		s.Enabled = false
		return s
	}
	c := cfg.Gateway.AccountCircuitBreaker
	s.Enabled = c.Enabled
	s.BackoffEnabled = c.BackoffEnabled
	if c.WindowSeconds > 0 {
		s.Window = time.Duration(c.WindowSeconds) * time.Second
	}
	if c.ErrorRate > 0 && c.ErrorRate < 1 {
		s.ErrorRate = c.ErrorRate
	}
	if c.MinRequests > 0 {
		s.MinRequests = c.MinRequests
	}
	if c.CooldownSeconds > 0 {
		s.Cooldown = time.Duration(c.CooldownSeconds) * time.Second
	}
	if c.MaxCooldownSeconds > 0 {
		s.MaxCooldown = time.Duration(c.MaxCooldownSeconds) * time.Second
	}
	if c.ValveMinAccounts > 0 {
		s.ValveMinAccounts = c.ValveMinAccounts
	}
	if c.ValveRatio > 0 && c.ValveRatio <= 1 {
		s.ValveRatio = c.ValveRatio
	}
	return s.normalized()
}

func (s AccountCircuitBreakerSettings) normalized() AccountCircuitBreakerSettings {
	d := DefaultAccountCircuitBreakerSettings()
	if s.Window < time.Duration(circuitBuckets) { // 窗口不足 10ns 无法切桶
		s.Window = d.Window
	}
	if s.ErrorRate <= 0 || s.ErrorRate >= 1 {
		s.ErrorRate = d.ErrorRate
	}
	if s.MinRequests <= 0 {
		s.MinRequests = d.MinRequests
	}
	if s.MinRequests > int(circuitCountMask) {
		s.MinRequests = int(circuitCountMask)
	}
	if s.Cooldown <= 0 {
		s.Cooldown = d.Cooldown
	}
	if s.MaxCooldown < s.Cooldown {
		s.MaxCooldown = s.Cooldown
	}
	if s.ValveMinAccounts <= 0 {
		s.ValveMinAccounts = d.ValveMinAccounts
	}
	if s.ValveRatio <= 0 || s.ValveRatio > 1 {
		s.ValveRatio = d.ValveRatio
	}
	return s
}

// AccountCircuitTrip 描述一次熔断，交给持久化回调。
type AccountCircuitTrip struct {
	AccountID        int64
	Platform         string
	AccountName      string
	Attempts         int
	Failures         int
	Window           time.Duration
	Cooldown         time.Duration
	Until            time.Time
	Reason           string
	ConsecutiveTrips int
}

// AccountCircuitBreakerStats 是进程内累计计数（只增不减），用于日志与测试。
type AccountCircuitBreakerStats struct {
	Observed       int64 // 计入窗口的尝试数
	Failures       int64 // 其中失败数
	IgnoredNoise   int64 // 不计入的尝试（客户端断开、本地 ctx、本机资源错误）
	IgnoredOpen    int64 // 熔断期间完成的在途尝试（不计入新窗口）
	Trips          int64
	ValveBlocked   int64
	Persisted      int64
	PersistFailed  int64
	Recoveries     int64
	TrackedAccount int64 // 当前被跟踪的账号数
}

type circuitStats struct {
	observed, failures, ignoredNoise, ignoredOpen atomic.Int64
	trips, valveBlocked, persisted, persistFailed atomic.Int64
	recoveries                                    atomic.Int64
}

// circuitLogger 只需要 Error 级别；测试里替换成记录器。
type circuitLogger interface {
	Error(msg string, fields ...zap.Field)
}

type zapCircuitLogger struct{}

func (zapCircuitLogger) Error(msg string, fields ...zap.Field) {
	logger.L().With(zap.String("component", "service.account_circuit_breaker")).Error(msg, fields...)
}

// circuitWindow 是 10 个桶的滑动窗口。每个桶一个 atomic.Uint64，打包 epoch/total/failed。
type circuitWindow struct {
	buckets [circuitBuckets]atomic.Uint64
}

func circuitPack(epoch, total, failed uint64) uint64 {
	return (epoch&circuitEpochMask)<<(2*circuitCountBits) | (total&circuitCountMask)<<circuitCountBits | (failed & circuitCountMask)
}

func circuitUnpack(v uint64) (epoch, total, failed uint64) {
	return v >> (2 * circuitCountBits), (v >> circuitCountBits) & circuitCountMask, v & circuitCountMask
}

// add 记录一次尝试。过期桶（epoch 不同）原地清零重用；比当前 epoch 更新的桶（时钟回拨）直接丢弃样本。
func (w *circuitWindow) add(epoch uint64, failed bool) {
	var f uint64
	if failed {
		f = 1
	}
	b := &w.buckets[epoch%circuitBuckets]
	masked := epoch & circuitEpochMask
	for {
		old := b.Load()
		oe, total, fail := circuitUnpack(old)
		var next uint64
		switch {
		case total == 0:
			next = circuitPack(masked, 1, f) // 空桶（初始或已清零）：直接占用
		case oe == masked:
			if total >= circuitCountMask {
				return // 桶饱和（单账号 3 万+/秒），丢弃而不是溢出
			}
			next = circuitPack(masked, total+1, fail+f)
		case (masked-oe)&circuitEpochMask < circuitEpochMask/2:
			next = circuitPack(masked, 1, f) // 旧桶：轮转
		default:
			return // 桶里是更新的 epoch：本样本已过期
		}
		if b.CompareAndSwap(old, next) {
			return
		}
	}
}

// sum 汇总最近 circuitBuckets 个 epoch（含当前）内的尝试数与失败数。
func (w *circuitWindow) sum(epoch uint64) (total, failed int) {
	masked := epoch & circuitEpochMask
	for i := range w.buckets {
		oe, t, f := circuitUnpack(w.buckets[i].Load())
		if t == 0 {
			continue
		}
		if (masked-oe)&circuitEpochMask < circuitBuckets {
			total += int(t)
			failed += int(f)
		}
	}
	return total, failed
}

func (w *circuitWindow) reset() {
	for i := range w.buckets {
		w.buckets[i].Store(0)
	}
}

// accountCircuitState 是单个账号的断路器状态。
type accountCircuitState struct {
	id     int64
	window circuitWindow

	// openUntil: unix 纳秒；0 = 闭合；非 0 且当前时间更早 = 熔断中（冷却期）。
	openUntil atomic.Int64
	// nextEval: 在此之前不评估熔断（安全阀拦下 / 写库失败后的退让）。
	nextEval  atomic.Int64
	lastEpoch atomic.Uint64
	platform  atomic.Pointer[string]
	name      atomic.Pointer[string]

	lastTripLog    atomic.Int64
	lastRecoverLog atomic.Int64

	openedNs atomic.Int64 // 本次熔断开始时间（恢复日志用）

	// tripMu 只在失败率越线后的低频路径上持有，保护下面的熔断记账字段。
	tripMu      sync.Mutex
	trips       int   // 连续熔断次数（窗口内再次熔断才累加）
	lastUntilNs int64 // 上次熔断的冷却结束时间
}

func (st *accountCircuitState) platformName() string {
	if p := st.platform.Load(); p != nil {
		return *p
	}
	return ""
}

func (st *accountCircuitState) accountName() string {
	if p := st.name.Load(); p != nil {
		return *p
	}
	return ""
}

// AccountCircuitBreaker 是进程内的账号错误率断路器。零值不可用，用 NewAccountCircuitBreaker 创建。
type AccountCircuitBreaker struct {
	enabled     bool
	window      time.Duration
	bucketNanos int64
	windowNanos int64
	rateBP      int64 // 失败率阈值，万分比；整数比较避免浮点边界误差
	minRequests int
	cooldown    time.Duration
	backoff     bool
	maxCooldown time.Duration
	valveMin    int
	valveBP     int64

	now     func() time.Time
	persist func(ctx context.Context, trip AccountCircuitTrip) error
	// async 用来异步执行写库；测试里可替换成同步执行。
	async func(func())
	log   circuitLogger

	states    sync.Map // int64 -> *accountCircuitState
	valve     circuitValve
	nextSweep atomic.Int64
	inflight  atomic.Int32
	stats     circuitStats
}

// NewAccountCircuitBreaker 创建断路器。persist 为 nil 时只做内存记账（纯逻辑测试用）。
func NewAccountCircuitBreaker(settings AccountCircuitBreakerSettings, persist func(ctx context.Context, trip AccountCircuitTrip) error) *AccountCircuitBreaker {
	s := settings.normalized()
	return &AccountCircuitBreaker{
		enabled:     s.Enabled,
		window:      s.Window,
		bucketNanos: int64(s.Window) / circuitBuckets,
		windowNanos: int64(s.Window),
		rateBP:      int64(s.ErrorRate*10000 + 0.5),
		minRequests: s.MinRequests,
		cooldown:    s.Cooldown,
		backoff:     s.BackoffEnabled,
		maxCooldown: s.MaxCooldown,
		valveMin:    s.ValveMinAccounts,
		valveBP:     int64(s.ValveRatio*10000 + 0.5),
		now:         time.Now,
		persist:     persist,
		async:       func(f func()) { go f() },
		log:         zapCircuitLogger{},
	}
}

// RunPersistSynchronouslyForTest 让熔断的写库在触发熔断的那次观察里同步完成（默认是异步 goroutine）。
// 仅供跨包的集成测试使用：这样「熔断之后的下一个请求」的行为是确定的，不依赖 goroutine 调度。
func (b *AccountCircuitBreaker) RunPersistSynchronouslyForTest() {
	if b != nil {
		b.async = func(f func()) { f() }
	}
}

// Enabled 报告断路器是否生效。
func (b *AccountCircuitBreaker) Enabled() bool { return b != nil && b.enabled }

// ObserveUpstreamAttempt 记录一次对账号的上游尝试的结果（实现 UpstreamAttemptObserver）。
//
// 只统计真实网关流量：请求 ctx 里必须带 API Key 鉴权中间件写入的用户 ID，
// 这样后台账号测试、额度/余额探针、模型列表同步等非用户流量不会污染窗口。
// 失败口径见 classifyUpstreamAttempt。
func (b *AccountCircuitBreaker) ObserveUpstreamAttempt(ctx context.Context, accountID int64, statusCode int, err error) {
	if b == nil || !b.enabled || accountID <= 0 || ctx == nil {
		return
	}
	if uid, _ := ctx.Value(ctxkey.UserID).(int64); uid <= 0 {
		return
	}
	switch classifyUpstreamAttempt(ctx.Err(), statusCode, err) {
	case attemptIgnored:
		b.stats.ignoredNoise.Add(1)
	case attemptFailed:
		b.record(accountID, ctx, true)
	default:
		b.record(accountID, ctx, false)
	}
}

func (b *AccountCircuitBreaker) stateFor(id int64) *accountCircuitState {
	if v, ok := b.states.Load(id); ok {
		return v.(*accountCircuitState) //nolint:errcheck // 只存这一种类型
	}
	v, _ := b.states.LoadOrStore(id, &accountCircuitState{id: id})
	return v.(*accountCircuitState) //nolint:errcheck // 只存这一种类型
}

func (b *AccountCircuitBreaker) record(id int64, ctx context.Context, failed bool) {
	nowN := b.now().UnixNano()
	st := b.stateFor(id)
	b.noteIdentity(st, ctx)

	if open := st.openUntil.Load(); open != 0 {
		if nowN < open {
			// 熔断期间完成的在途尝试反映的是熔断前的状态，不计入重新开始的窗口。
			b.stats.ignoredOpen.Add(1)
			return
		}
		b.recoverState(st, open, nowN)
	}

	epoch := uint64(nowN / b.bucketNanos)
	if st.lastEpoch.Load() != epoch {
		st.lastEpoch.Store(epoch)
	}
	st.window.add(epoch, failed)
	b.stats.observed.Add(1)
	if !failed {
		b.maybeSweep(nowN)
		return
	}
	b.stats.failures.Add(1)
	b.maybeTrip(st, nowN, epoch)
	b.maybeSweep(nowN)
}

// noteIdentity 记下账号的平台和名字（只在第一次看到时从 ctx 取，账号的平台不会变）。
func (b *AccountCircuitBreaker) noteIdentity(st *accountCircuitState, ctx context.Context) {
	if st.platform.Load() == nil {
		if p, _ := ctx.Value(ctxkey.Platform).(string); p != "" {
			st.platform.CompareAndSwap(nil, &p)
		}
	}
	if st.name.Load() == nil {
		if n, _ := ctx.Value(ctxkey.AccountName).(string); n != "" {
			st.name.CompareAndSwap(nil, &n)
		}
	}
}

// failureRateExceeded 严格大于阈值才算越线：failed/total > rate  <=>  failed*10000 > rateBP*total。
func (b *AccountCircuitBreaker) failureRateExceeded(total, failed int) bool {
	return total >= b.minRequests && int64(failed)*10000 > b.rateBP*int64(total)
}

func (b *AccountCircuitBreaker) maybeTrip(st *accountCircuitState, nowN int64, epoch uint64) {
	total, failed := st.window.sum(epoch)
	if !b.failureRateExceeded(total, failed) {
		return
	}
	if nowN < st.nextEval.Load() {
		return
	}
	b.trip(st, nowN, epoch)
}

func (b *AccountCircuitBreaker) trip(st *accountCircuitState, nowN int64, epoch uint64) {
	st.tripMu.Lock()
	defer st.tripMu.Unlock()

	// 抢到锁后重新判断：别的 goroutine 可能刚熔断过（状态已打开、窗口已清零）。
	if open := st.openUntil.Load(); open != 0 && nowN < open {
		return
	}
	if nowN < st.nextEval.Load() {
		return
	}
	total, failed := st.window.sum(epoch)
	if !b.failureRateExceeded(total, failed) {
		return
	}

	platform := st.platformName()
	if blocked, tripped, population := b.valveBlocks(platform, st.id, nowN, epoch); blocked {
		st.nextEval.Store(nowN + int64(circuitValveRecheckDelay))
		b.stats.valveBlocked.Add(1)
		if b.valve.shouldLog(platform, nowN) {
			b.log.Error("account_circuit_breaker.valve_blocked",
				zap.String("platform", platform),
				zap.Int64("account_id", st.id),
				zap.Int("tripped_accounts_1m", tripped),
				zap.Int("platform_accounts_considered", population),
				zap.Int("valve_min_accounts", b.valveMin),
				zap.Float64("valve_ratio", float64(b.valveBP)/10000),
				zap.String("note", "too many accounts of one platform failing at once: treated as a local/network problem, not tripping more accounts"),
			)
		}
		return
	}

	cooldown := b.nextCooldown(st, nowN)
	untilN := nowN + int64(cooldown)
	st.openUntil.Store(untilN)
	st.openedNs.Store(nowN)
	st.window.reset()
	st.nextEval.Store(0)
	b.valve.add(platform, st.id, nowN)
	b.stats.trips.Add(1)

	trip := AccountCircuitTrip{
		AccountID:        st.id,
		Platform:         platform,
		AccountName:      st.accountName(),
		Attempts:         total,
		Failures:         failed,
		Window:           b.window,
		Cooldown:         cooldown,
		Until:            time.Unix(0, untilN),
		ConsecutiveTrips: st.trips,
	}
	trip.Reason = fmt.Sprintf("%s %.1f%% of %d upstream attempts failed in %s",
		accountCircuitBreakerReasonPrefix, float64(failed)*100/float64(total), total, formatCircuitWindow(b.window))

	if last := st.lastTripLog.Load(); last == 0 || nowN-last >= int64(circuitLogThrottle) {
		st.lastTripLog.Store(nowN)
		b.log.Error("account_circuit_breaker.tripped",
			zap.Int64("account_id", trip.AccountID),
			zap.String("account_name", trip.AccountName),
			zap.String("platform", trip.Platform),
			zap.Int("attempts", trip.Attempts),
			zap.Int("failures", trip.Failures),
			zap.String("failure_rate", fmt.Sprintf("%.1f%%", float64(failed)*100/float64(total))),
			zap.Duration("window", trip.Window),
			zap.Duration("cooldown", trip.Cooldown),
			zap.Time("until", trip.Until),
			zap.Int("consecutive_trips", trip.ConsecutiveTrips),
			zap.String("reason", trip.Reason),
		)
	}
	b.persistTrip(st, trip)
}

// nextCooldown 计算本次冷却时长。只有开启 backoff 时才对「窗口内连续再次熔断」按 2 倍递增（上限 maxCooldown）。
// 调用方持有 st.tripMu。
func (b *AccountCircuitBreaker) nextCooldown(st *accountCircuitState, nowN int64) time.Duration {
	if st.trips > 0 && nowN-st.lastUntilNs <= b.windowNanos {
		st.trips++
	} else {
		st.trips = 1
	}
	cooldown := b.cooldown
	if b.backoff {
		for i := 1; i < st.trips && cooldown < b.maxCooldown; i++ {
			cooldown *= 2
		}
		if cooldown > b.maxCooldown {
			cooldown = b.maxCooldown
		}
	}
	st.lastUntilNs = nowN + int64(cooldown)
	return cooldown
}

func (b *AccountCircuitBreaker) persistTrip(st *accountCircuitState, trip AccountCircuitTrip) {
	if b.persist == nil {
		return
	}
	if b.inflight.Add(1) > circuitMaxInflightPersist {
		b.inflight.Add(-1)
		b.persistFailed(st, trip, fmt.Errorf("too many in-flight circuit breaker writes"))
		return
	}
	b.async(func() {
		defer b.inflight.Add(-1)
		ctx, cancel := context.WithTimeout(context.Background(), circuitPersistTimeout)
		defer cancel()
		if err := b.persistSafely(ctx, trip); err != nil {
			b.persistFailed(st, trip, err)
			return
		}
		b.stats.persisted.Add(1)
	})
}

// persistSafely 把持久化回调里的 panic 转成错误：熔断是保护机制，不能因为它把进程带崩。
func (b *AccountCircuitBreaker) persistSafely(ctx context.Context, trip AccountCircuitTrip) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("circuit breaker persist panic: %v", r)
		}
	}()
	return b.persist(ctx, trip)
}

// persistFailed 写库失败：撤销内存里的「已熔断」状态（否则内存说熔断了、库里还可调度，
// 在途重试被误停且没人再去写库），隔 circuitPersistRetryDelay 后下一个失败请求会重新触发。
func (b *AccountCircuitBreaker) persistFailed(st *accountCircuitState, trip AccountCircuitTrip, err error) {
	b.stats.persistFailed.Add(1)
	nowN := b.now().UnixNano()
	st.openUntil.Store(0)
	st.nextEval.Store(nowN + int64(circuitPersistRetryDelay))
	b.valve.forget(trip.Platform, trip.AccountID)
	b.log.Error("account_circuit_breaker.persist_failed",
		zap.Int64("account_id", trip.AccountID),
		zap.String("platform", trip.Platform),
		zap.Error(err),
	)
}

// recoverState 冷却到期：把状态改回闭合并打一条恢复日志。open 是调用方读到的 openUntil。
func (b *AccountCircuitBreaker) recoverState(st *accountCircuitState, open, nowN int64) {
	if !st.openUntil.CompareAndSwap(open, 0) {
		return
	}
	st.window.reset() // 冷却期内的残留不带进新窗口
	b.stats.recoveries.Add(1)
	if last := st.lastRecoverLog.Load(); last == 0 || nowN-last >= int64(circuitLogThrottle) {
		st.lastRecoverLog.Store(nowN)
		b.log.Error("account_circuit_breaker.recovered",
			zap.Int64("account_id", st.id),
			zap.String("account_name", st.accountName()),
			zap.String("platform", st.platformName()),
			zap.Duration("paused_for", time.Duration(nowN-st.openedNs.Load())),
			zap.String("note", "cooldown elapsed, account is schedulable again; needs a fresh window of attempts before it can be tripped again"),
		)
	}
}

// IsOpen 报告账号当前是否处于本进程的熔断冷却期。重试循环在再次打同一个账号之前用它判断。
func (b *AccountCircuitBreaker) IsOpen(accountID int64) bool {
	_, ok := b.OpenUntil(accountID)
	return ok
}

// OpenUntil 返回熔断冷却结束时间。
func (b *AccountCircuitBreaker) OpenUntil(accountID int64) (time.Time, bool) {
	if b == nil || !b.enabled || accountID <= 0 {
		return time.Time{}, false
	}
	v, ok := b.states.Load(accountID)
	if !ok {
		return time.Time{}, false
	}
	st := v.(*accountCircuitState) //nolint:errcheck // 只存这一种类型
	open := st.openUntil.Load()
	if open == 0 || b.now().UnixNano() >= open {
		return time.Time{}, false
	}
	return time.Unix(0, open), true
}

// Reset 清掉某账号的熔断状态与窗口（管理员手动「重置临时不可调度」时调用）。
func (b *AccountCircuitBreaker) Reset(accountID int64) {
	if b == nil || accountID <= 0 {
		return
	}
	v, ok := b.states.Load(accountID)
	if !ok {
		return
	}
	st := v.(*accountCircuitState) //nolint:errcheck // 只存这一种类型
	st.tripMu.Lock()
	defer st.tripMu.Unlock()
	st.openUntil.Store(0)
	st.nextEval.Store(0)
	st.trips = 0
	st.lastUntilNs = 0
	st.window.reset()
	b.valve.forget(st.platformName(), accountID)
}

// Stats 返回累计计数快照。
func (b *AccountCircuitBreaker) Stats() AccountCircuitBreakerStats {
	if b == nil {
		return AccountCircuitBreakerStats{}
	}
	var tracked int64
	b.states.Range(func(_, _ any) bool { tracked++; return true })
	return AccountCircuitBreakerStats{
		Observed:       b.stats.observed.Load(),
		Failures:       b.stats.failures.Load(),
		IgnoredNoise:   b.stats.ignoredNoise.Load(),
		IgnoredOpen:    b.stats.ignoredOpen.Load(),
		Trips:          b.stats.trips.Load(),
		ValveBlocked:   b.stats.valveBlocked.Load(),
		Persisted:      b.stats.persisted.Load(),
		PersistFailed:  b.stats.persistFailed.Load(),
		Recoveries:     b.stats.recoveries.Load(),
		TrackedAccount: tracked,
	}
}

// maybeSweep 每分钟由第一个到达的请求顺手清理一次：为冷却已到期且没有后续流量的账号补打恢复日志，
// 并回收长时间没有流量的账号状态（避免 map 随账号数无限增长）。没有后台 goroutine，也就没有需要 Stop 的东西。
func (b *AccountCircuitBreaker) maybeSweep(nowN int64) {
	next := b.nextSweep.Load()
	if nowN < next {
		return
	}
	if !b.nextSweep.CompareAndSwap(next, nowN+int64(circuitSweepInterval)) {
		return
	}
	b.sweep(nowN)
}

func (b *AccountCircuitBreaker) sweep(nowN int64) {
	idleNanos := 2 * b.windowNanos
	idleEpochs := uint64(idleNanos/b.bucketNanos) + 1
	curEpoch := uint64(nowN / b.bucketNanos)
	b.states.Range(func(k, v any) bool {
		st := v.(*accountCircuitState) //nolint:errcheck // 只存这一种类型
		if open := st.openUntil.Load(); open != 0 {
			if nowN < open {
				return true
			}
			b.recoverState(st, open, nowN)
		}
		if curEpoch-st.lastEpoch.Load() > idleEpochs {
			b.states.Delete(k)
		}
		return true
	})
	b.valve.prune(nowN)
}

func formatCircuitWindow(d time.Duration) string {
	switch {
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", int64(d/time.Minute))
	case d%time.Second == 0:
		return fmt.Sprintf("%ds", int64(d/time.Second))
	default:
		return d.String()
	}
}

// IsAccountCircuitBreakerReason 报告 temp_unschedulable_reason 是否由断路器写入。
func IsAccountCircuitBreakerReason(reason string) bool {
	return strings.HasPrefix(reason, accountCircuitBreakerReasonPrefix)
}

// ---- 安全阀 ----------------------------------------------------------------

type circuitTripRecord struct {
	accountID int64
	at        int64
}

// circuitValve 记录各平台最近一分钟内被熔断的账号，用来判断「是不是大面积同时失败」。
type circuitValve struct {
	mu      sync.Mutex
	trips   map[string][]circuitTripRecord
	lastLog map[string]int64
}

func (v *circuitValve) add(platform string, id int64, nowN int64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.trips == nil {
		v.trips = make(map[string][]circuitTripRecord)
	}
	v.trips[platform] = append(v.pruned(platform, nowN), circuitTripRecord{accountID: id, at: nowN})
}

func (v *circuitValve) forget(platform string, id int64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	list := v.trips[platform]
	kept := list[:0]
	for _, r := range list {
		if r.accountID != id {
			kept = append(kept, r)
		}
	}
	if len(kept) == 0 {
		delete(v.trips, platform)
		return
	}
	v.trips[platform] = kept
}

// recent 返回平台上最近一分钟内已被熔断的不同账号数（不含 exclude）。
func (v *circuitValve) recent(platform string, exclude int64, nowN int64) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	list := v.pruned(platform, nowN)
	seen := make(map[int64]struct{}, len(list))
	for _, r := range list {
		if r.accountID != exclude {
			seen[r.accountID] = struct{}{}
		}
	}
	return len(seen)
}

// pruned 丢掉过期记录并返回剩余的；调用方持有 v.mu。
func (v *circuitValve) pruned(platform string, nowN int64) []circuitTripRecord {
	list := v.trips[platform]
	cut := nowN - int64(circuitValveWindow)
	kept := list[:0]
	for _, r := range list {
		if r.at > cut {
			kept = append(kept, r)
		}
	}
	if len(kept) == 0 {
		delete(v.trips, platform)
		return nil
	}
	v.trips[platform] = kept
	return kept
}

func (v *circuitValve) prune(nowN int64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for platform := range v.trips {
		v.pruned(platform, nowN)
	}
	for platform, at := range v.lastLog {
		if nowN-at > int64(circuitLogThrottle) {
			delete(v.lastLog, platform)
		}
	}
}

// shouldLog 对同一平台的「安全阀拦截」日志限频。
func (v *circuitValve) shouldLog(platform string, nowN int64) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.lastLog == nil {
		v.lastLog = make(map[string]int64)
	}
	if last, ok := v.lastLog[platform]; ok && nowN-last < int64(circuitLogThrottle) {
		return false
	}
	v.lastLog[platform] = nowN
	return true
}

// valveBlocks 判断是否应当因为「同平台大面积同时失败」而放弃熔断这个账号。
//
//	tripped    = 本平台最近 1 分钟内已被熔断的其他账号数
//	population = 本平台参与统计的账号数 = 仍在接流量且样本数达标的账号（含本账号）+ tripped
//
// 拦截条件：tripped >= ValveMinAccounts 且 tripped/population >= ValveRatio。
// 小池子（已熔断不足 ValveMinAccounts 个）永远不会被安全阀拦，可以全部熔断。
func (b *AccountCircuitBreaker) valveBlocks(platform string, self int64, nowN int64, epoch uint64) (blocked bool, tripped, population int) {
	tripped = b.valve.recent(platform, self, nowN)
	if tripped < b.valveMin {
		return false, tripped, 0
	}
	active := 0
	b.states.Range(func(_, v any) bool {
		st := v.(*accountCircuitState) //nolint:errcheck // 只存这一种类型
		if st.platformName() != platform {
			return true
		}
		if open := st.openUntil.Load(); open != 0 && nowN < open {
			return true
		}
		if total, _ := st.window.sum(epoch); total >= b.minRequests {
			active++
		}
		return true
	})
	if active < 1 {
		active = 1 // 本账号自己
	}
	population = active + tripped
	return int64(tripped)*10000 >= b.valveBP*int64(population), tripped, population
}
