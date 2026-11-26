package service

import (
	"context"
	"errors"
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
//
// 样本口径：同一个客户端请求在同一个账号上无论重试几次只算一个样本（最后一次尝试的结果为准），
// 所以「一个用户把低流量账号反复重试打坏」不会让账号看起来失败率很高。
// 快速熔断：最近 2 个桶（约 30–60 秒）里样本数 >= fast_trip_min_requests 且没有一次成功时直接熔断，
// 不用等失败率在 5 分钟窗口里慢慢越线（长期健康的账号突然全坏时窗口会被旧的成功样本稀释）。
// 安全阀：同平台还有健康账号时一律熔断；只有「参与统计的账号都在失败」才视为本机/网络问题而放弃熔断。
// 自动恢复：库里的 temp_unschedulable_until 到期后，调度快照并不会自己重建，所以熔断写库成功后
// 本进程再安排一个「冷却到期 + 2 秒」的刷新，往 scheduler outbox 写一条账号变更事件。

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

	// circuitMinRequestsFloor 是 min_requests 配置值的下限：设成 1、2 会让一两个偶发错误就把账号摘掉。
	circuitMinRequestsFloor = 5
	// circuitFastBuckets 是快速熔断统计的最近桶数（默认桶宽 30 秒，即最近 30–60 秒）。
	circuitFastBuckets = 2

	// circuitRecoveryGrace 是冷却到期之后再多等的时间，保证刷新时 SQL 里的 NOW() 已经越过 until。
	circuitRecoveryGrace = 2 * time.Second
	// circuitRecoveryRefreshAttempts 是刷新事件写 outbox 失败时的最多尝试次数（间隔 circuitPersistRetryDelay）。
	circuitRecoveryRefreshAttempts = 3

	// 请求级样本去重：只记「失败」样本，成功的重试会把它撤销。
	circuitRequestShards     = 16
	circuitRequestTTL        = 3 * time.Minute
	circuitRequestShardMax   = 4096 // 单分片容量上限；满了就不再去重（宁可多计，也不无限占内存）
	circuitRequestSweepEvery = 10 * time.Second
)

// AccountCircuitBreakerSettings 是归一化后的断路器参数。
type AccountCircuitBreakerSettings struct {
	Enabled        bool
	Window         time.Duration
	ErrorRate      float64
	MinRequests    int
	Cooldown       time.Duration
	BackoffEnabled bool
	MaxCooldown    time.Duration
	// ValveMinAccounts 是安全阀的参与账号门槛：同平台参与统计的账号（含本账号）不少于这个数、
	// 并且没有任何一个健康账号时，才视为本机/网络问题而不再熔断（见 valveBlocks）。
	ValveMinAccounts int
	// FastTripMinRequests 是快速熔断门槛：最近 2 个桶里至少这么多次尝试且一次成功都没有就熔断；0 = 关闭。
	FastTripMinRequests int
}

// DefaultAccountCircuitBreakerSettings 返回与 config 默认值一致的参数。
func DefaultAccountCircuitBreakerSettings() AccountCircuitBreakerSettings {
	return AccountCircuitBreakerSettings{
		Enabled:             true,
		Window:              300 * time.Second,
		ErrorRate:           0.95,
		MinRequests:         20,
		Cooldown:            60 * time.Second,
		BackoffEnabled:      false,
		MaxCooldown:         300 * time.Second,
		ValveMinAccounts:    3,
		FastTripMinRequests: 100,
	}
}

// circuitConfigWarner 输出「配置值被调整」的 WARN；测试里替换成记录器。
var circuitConfigWarner = func(msg string, fields ...zap.Field) {
	logger.L().With(zap.String("component", "service.account_circuit_breaker")).Warn(msg, fields...)
}

// warnCircuitConfig 记下一条「配置值被调整」的 WARN：静默改写运维填的值会让人以为配置没生效。
func warnCircuitConfig(key string, configured, effective any, note string) {
	circuitConfigWarner("account_circuit_breaker.config_adjusted",
		zap.String("key", "gateway.account_circuit_breaker."+key),
		zap.Any("configured", configured),
		zap.Any("effective", effective),
		zap.String("note", note),
	)
}

// circuitErrorRateFromConfig 解释配置里的失败率阈值：
//   - (0,1)：直接使用；
//   - (1,100)：按百分数解释（95 -> 0.95）并打 WARN。把 ERROR_RATE=95 写成百分数是最常见的笔误，
//     回落到默认值会悄悄改变运维的本意；
//   - 0（没配）：用默认值；
//   - 其余（负数、恰好 1 或 100、超过 100）：回落到默认值并打 WARN。严格大于 100% 永远不会触发。
func circuitErrorRateFromConfig(v, def float64) float64 {
	switch {
	case v > 0 && v < 1:
		return v
	case v > 1 && v < 100:
		warnCircuitConfig("error_rate", v, v/100, "values above 1 are read as percentages (95 -> 0.95); use a fraction like 0.95")
		return v / 100
	case v == 0:
		return def
	default:
		warnCircuitConfig("error_rate", v, def, "must be a fraction in (0,1) (or a percentage in (1,100)); falling back to the default")
		return def
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
	s.ErrorRate = circuitErrorRateFromConfig(c.ErrorRate, s.ErrorRate)
	if c.MinRequests > 0 {
		s.MinRequests = c.MinRequests
		if c.MinRequests < circuitMinRequestsFloor {
			warnCircuitConfig("min_requests", c.MinRequests, circuitMinRequestsFloor, "raised to the floor: a handful of errors must not pause an account")
			s.MinRequests = circuitMinRequestsFloor
		}
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
	// 0 / 负数 = 关闭快速熔断（默认值 100 由 viper 默认值提供，所以「没配」不会走到这里）。
	s.FastTripMinRequests = max(c.FastTripMinRequests, 0)
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
	if s.FastTripMinRequests < 0 {
		s.FastTripMinRequests = 0
	}
	if s.FastTripMinRequests > int(circuitCountMask) {
		s.FastTripMinRequests = int(circuitCountMask)
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
	// FastTrip 为 true 表示由快速熔断触发（最近 2 个桶没有一次成功）。
	FastTrip bool
}

// AccountCircuitBreakerStats 是进程内累计计数（只增不减），用于日志与测试。
type AccountCircuitBreakerStats struct {
	Observed              int64 // 计入窗口的尝试数
	Failures              int64 // 其中失败数
	IgnoredNoise          int64 // 不计入的尝试（客户端断开、本地 ctx、本机资源错误、共享容量 503）
	IgnoredOpen           int64 // 熔断期间完成的在途尝试（不计入新窗口）
	Superseded            int64 // 被同一请求在同一账号上的后一次尝试取代的样本
	Trips                 int64
	FastTrips             int64 // 其中由快速熔断触发的次数
	ValveBlocked          int64
	Persisted             int64
	PersistFailed         int64
	Recoveries            int64
	RecoveryRefreshes     int64 // 冷却到期后成功通知调度快照重建的次数
	RecoveryRefreshFailed int64
	RequestSamples        int64 // 当前记着的请求级失败样本数（有上界：每分片 4096 条，3 分钟过期）
	TrackedAccount        int64 // 当前被跟踪的账号数
}

type circuitStats struct {
	observed, failures, ignoredNoise, ignoredOpen, superseded atomic.Int64
	trips, fastTrips, valveBlocked, persisted, persistFailed  atomic.Int64
	recoveries, recoveryRefreshed, recoveryRefreshFailed      atomic.Int64
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

// sub 撤销之前记入 epoch 桶的一次失败尝试（同一请求在同一账号上被后一次尝试取代）。
// 桶已经轮转（epoch 不同）说明那次样本早已滑出窗口，什么也不做。
func (w *circuitWindow) sub(epoch uint64) {
	b := &w.buckets[epoch%circuitBuckets]
	masked := epoch & circuitEpochMask
	for {
		old := b.Load()
		oe, total, fail := circuitUnpack(old)
		if oe != masked || total == 0 || fail == 0 {
			return
		}
		if b.CompareAndSwap(old, circuitPack(masked, total-1, fail-1)) {
			return
		}
	}
}

// sum 汇总最近 circuitBuckets 个 epoch（含当前）内的尝试数与失败数。
func (w *circuitWindow) sum(epoch uint64) (total, failed int) {
	return w.sumRecent(epoch, circuitBuckets)
}

// sumRecent 汇总最近 n 个 epoch（含当前）内的尝试数与失败数。
func (w *circuitWindow) sumRecent(epoch, n uint64) (total, failed int) {
	masked := epoch & circuitEpochMask
	for i := range w.buckets {
		oe, t, f := circuitUnpack(w.buckets[i].Load())
		if t == 0 {
			continue
		}
		if (masked-oe)&circuitEpochMask < n {
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
	fastMin     int

	now     func() time.Time
	persist func(ctx context.Context, trip AccountCircuitTrip) error
	// async 用来异步执行写库；测试里可替换成同步执行。
	async func(func())
	log   circuitLogger
	// afterFunc 安排冷却到期后的快照刷新；测试里替换成假时钟。
	afterFunc func(d time.Duration, f func())
	// recoveryRefresh 通知调度快照重建账号所在的桶（nil = 不通知）。
	recoveryRefresh func(ctx context.Context, accountID int64) error

	states     sync.Map // int64 -> *accountCircuitState
	reqSamples circuitRequestSamples
	valve      circuitValve
	nextSweep  atomic.Int64
	inflight   atomic.Int32
	stats      circuitStats
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
		fastMin:     s.FastTripMinRequests,
		now:         time.Now,
		persist:     persist,
		async:       func(f func()) { go f() },
		log:         zapCircuitLogger{},
		afterFunc:   func(d time.Duration, f func()) { time.AfterFunc(d, f) },
	}
}

// SetRecoveryRefresher 设置「冷却到期后通知调度快照重建这个账号所在的桶」的回调（启动时调用一次）。
func (b *AccountCircuitBreaker) SetRecoveryRefresher(refresh func(ctx context.Context, accountID int64) error) {
	if b != nil {
		b.recoveryRefresh = refresh
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

// isGatewayAttempt 判断这次上游请求是不是真实的网关用户流量：
//   - ctx 里有 API Key 鉴权中间件写入的用户 ID；或者
//   - ctx 里有 handler 选号后写入的「当前账号 ID」且正是这次请求的账号（Gemini 原生路由的鉴权中间件
//     不写用户 ID，但同样会经过选号）。
//
// 后台账号测试、额度/余额探针、模型列表同步等非用户流量两者都没有，不会污染窗口。
func isGatewayAttempt(ctx context.Context, accountID int64) bool {
	if uid, _ := ctx.Value(ctxkey.UserID).(int64); uid > 0 {
		return true
	}
	aid, _ := ctx.Value(ctxkey.AccountID).(int64)
	return aid == accountID
}

// attemptPlatform 取这次尝试的平台标签：只采信 ctx 里「当前账号 ID」与被调用账号一致时写入的平台。
func attemptPlatform(ctx context.Context, accountID int64) string {
	if aid, _ := ctx.Value(ctxkey.AccountID).(int64); aid == accountID {
		p, _ := ctx.Value(ctxkey.Platform).(string)
		return p
	}
	return ""
}

// ObserveUpstreamAttempt 记录一次对账号的上游尝试的结果（实现 UpstreamAttemptObserver）。
//
// 只统计真实网关流量（见 isGatewayAttempt）。失败口径见 classifyUpstreamAttempt。
func (b *AccountCircuitBreaker) ObserveUpstreamAttempt(ctx context.Context, accountID int64, statusCode int, err error) {
	b.observe(ctx, accountID, statusCode, err, nil)
}

// WantsAttemptBody 报告这次尝试是否需要响应体前缀才能准确分类（实现 UpstreamAttemptBodyObserver）。
// 只有少数状态码在特定平台上会被误判：Anthropic 的组织禁用 400、Grok 的内容安全 403、Antigravity 的容量不足 503。
// 传输层只在这里返回 true 时才预读响应体，其余请求不多读一个字节。
func (b *AccountCircuitBreaker) WantsAttemptBody(ctx context.Context, accountID int64, statusCode int) bool {
	if b == nil || !b.enabled || accountID <= 0 || ctx == nil {
		return false
	}
	if !isGatewayAttempt(ctx, accountID) {
		return false
	}
	return attemptBodyRefinable(attemptPlatform(ctx, accountID), statusCode)
}

// ObserveUpstreamAttemptBody 与 ObserveUpstreamAttempt 相同，但带响应体前缀用于改判（实现 UpstreamAttemptBodyObserver）。
func (b *AccountCircuitBreaker) ObserveUpstreamAttemptBody(ctx context.Context, accountID int64, statusCode int, err error, bodyPrefix []byte) {
	b.observe(ctx, accountID, statusCode, err, bodyPrefix)
}

func (b *AccountCircuitBreaker) observe(ctx context.Context, accountID int64, statusCode int, err error, body []byte) {
	if b == nil || !b.enabled || accountID <= 0 || ctx == nil {
		return
	}
	if !isGatewayAttempt(ctx, accountID) {
		return
	}
	outcome := classifyUpstreamAttempt(ctx.Err(), statusCode, err)
	if (outcome == attemptFailed || outcome == attemptOK) && err == nil && len(body) > 0 {
		outcome = refineAttemptOutcome(outcome, attemptPlatform(ctx, accountID), statusCode, body)
	}
	switch outcome {
	case attemptIgnored:
		b.stats.ignoredNoise.Add(1)
	case attemptFailed:
		b.record(accountID, ctx, true)
	default:
		b.record(accountID, ctx, false)
	}
}

// stateFor 取（必要时创建）账号状态。map 里只会存 *accountCircuitState，类型断言失败不可能发生，
// 但仍然按 comma-ok 写，失败时退回一个临时状态而不是 panic。
func (b *AccountCircuitBreaker) stateFor(id int64) *accountCircuitState {
	if v, ok := b.states.Load(id); ok {
		if st, ok := v.(*accountCircuitState); ok {
			return st
		}
	}
	fresh := &accountCircuitState{id: id}
	actual, _ := b.states.LoadOrStore(id, fresh)
	if st, ok := actual.(*accountCircuitState); ok {
		return st
	}
	return fresh
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
	b.addSample(st, ctx, epoch, nowN, failed)
	b.stats.observed.Add(1)
	if !failed {
		b.maybeSweep(nowN)
		return
	}
	b.stats.failures.Add(1)
	b.maybeTrip(st, nowN, epoch)
	b.maybeSweep(nowN)
}

// addSample 把本次尝试记入窗口。同一个客户端请求（ctx 里的 request id）在同一个账号上无论重试几次
// 只保留最后一次尝试的结果：前一次的失败样本会被撤销。没有 request id 的 ctx 不去重。
func (b *AccountCircuitBreaker) addSample(st *accountCircuitState, ctx context.Context, epoch uint64, nowN int64, failed bool) {
	if reqID, _ := ctx.Value(ctxkey.RequestID).(string); reqID != "" {
		var prevEpoch uint64
		var had bool
		if failed {
			prevEpoch, had = b.reqSamples.recordFailure(reqID, st.id, epoch, nowN)
		} else {
			prevEpoch, had = b.reqSamples.clearOnSuccess(reqID, st.id)
		}
		if had {
			st.window.sub(prevEpoch)
			b.stats.superseded.Add(1)
		}
	}
	st.window.add(epoch, failed)
}

// noteIdentity 记下账号的平台和名字（只在第一次看到时从 ctx 取，账号的平台不会变）。
// 只采信 ctx 里「当前账号 ID」与本账号一致时写入的平台/名字，避免把别的账号的标签贴到这个账号上。
func (b *AccountCircuitBreaker) noteIdentity(st *accountCircuitState, ctx context.Context) {
	if aid, _ := ctx.Value(ctxkey.AccountID).(int64); aid != st.id {
		return
	}
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

// tripVerdict 是一次越线评估的结果：依据的样本数/失败数，以及它们覆盖的时间窗。
type tripVerdict struct {
	trip          bool
	fast          bool
	total, failed int
	window        time.Duration
}

// evaluate 判断账号现在是否应该熔断：
//  1. 5 分钟窗口内样本数 >= min_requests 且失败率严格大于阈值；
//  2. 快速熔断：最近 2 个桶里样本数 >= fast_trip_min_requests 且一次成功都没有。
func (b *AccountCircuitBreaker) evaluate(st *accountCircuitState, epoch uint64) tripVerdict {
	total, failed := st.window.sum(epoch)
	if b.failureRateExceeded(total, failed) {
		return tripVerdict{trip: true, total: total, failed: failed, window: b.window}
	}
	// 窗口里连 fast_trip_min_requests 次失败都没有，最近 2 个桶里就不可能有，省掉第二次汇总。
	if b.fastMin > 0 && failed >= b.fastMin {
		rt, rf := st.window.sumRecent(epoch, circuitFastBuckets)
		if rt >= b.fastMin && rf == rt {
			return tripVerdict{trip: true, fast: true, total: rt, failed: rf, window: time.Duration(circuitFastBuckets * b.bucketNanos)}
		}
	}
	return tripVerdict{total: total, failed: failed, window: b.window}
}

func (b *AccountCircuitBreaker) maybeTrip(st *accountCircuitState, nowN int64, epoch uint64) {
	if nowN < st.nextEval.Load() {
		return
	}
	if !b.evaluate(st, epoch).trip {
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
	v := b.evaluate(st, epoch)
	if !v.trip {
		return
	}
	total, failed := v.total, v.failed

	platform := st.platformName()
	if blocked, vs := b.valveBlocks(platform, st.id, nowN, epoch); blocked {
		st.nextEval.Store(nowN + int64(circuitValveRecheckDelay))
		b.stats.valveBlocked.Add(1)
		if b.valve.shouldLog(platform, nowN) {
			b.log.Error("account_circuit_breaker.valve_blocked",
				zap.String("platform", platform),
				zap.Int64("account_id", st.id),
				zap.Int("participating_accounts", vs.participating),
				zap.Int("healthy_accounts", vs.healthy),
				zap.Int("tripped_accounts_1m", vs.tripped),
				zap.Int("valve_min_accounts", b.valveMin),
				zap.String("note", "every account of this platform that has enough traffic is failing: treated as a local/network problem, not tripping more accounts"),
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
	if v.fast {
		b.stats.fastTrips.Add(1)
	}

	trip := AccountCircuitTrip{
		AccountID:        st.id,
		Platform:         platform,
		AccountName:      st.accountName(),
		Attempts:         total,
		Failures:         failed,
		Window:           v.window,
		Cooldown:         cooldown,
		Until:            time.Unix(0, untilN),
		ConsecutiveTrips: st.trips,
		FastTrip:         v.fast,
	}
	trip.Reason = fmt.Sprintf("%s %.1f%% of %d upstream attempts failed in %s",
		accountCircuitBreakerReasonPrefix, float64(failed)*100/float64(total), total, formatCircuitWindow(v.window))

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
			zap.Bool("fast_trip", trip.FastTrip),
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
		b.persistFailed(st, trip, errors.New("too many in-flight circuit breaker writes"))
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
		b.scheduleRecoveryRefresh(trip)
	})
}

// scheduleRecoveryRefresh 在冷却到期（再加 circuitRecoveryGrace）之后往 scheduler outbox 写一条账号变更事件。
//
// 为什么需要：调度快照构建桶的 SQL 会排除 temp_unschedulable_until 还没到期的账号，而到期这件事本身
// 没有任何事件——不刷新的话，账号要等下一次全量重建（默认 300 秒一次，配成 0 则永远不会）才会回到桶里，
// 「60 秒自动恢复」就变成了最长 5 分钟。每个进程各自安排一次；事件是幂等的，重复只是多一次重建。
// 进程在冷却期内重启会丢掉这个定时器，退回到全量重建的间隔（已在文档里写明）。
func (b *AccountCircuitBreaker) scheduleRecoveryRefresh(trip AccountCircuitTrip) {
	if b.recoveryRefresh == nil || b.afterFunc == nil {
		return
	}
	after := trip.Until.Sub(b.now())
	if after < 0 {
		after = 0
	}
	b.afterRecoveryRefresh(trip.AccountID, after+circuitRecoveryGrace, 1)
}

func (b *AccountCircuitBreaker) afterRecoveryRefresh(accountID int64, after time.Duration, attempt int) {
	b.afterFunc(after, func() {
		ctx, cancel := context.WithTimeout(context.Background(), circuitPersistTimeout)
		defer cancel()
		err := b.refreshSafely(ctx, accountID)
		if err == nil {
			b.stats.recoveryRefreshed.Add(1)
			return
		}
		b.stats.recoveryRefreshFailed.Add(1)
		b.log.Error("account_circuit_breaker.recovery_refresh_failed",
			zap.Int64("account_id", accountID),
			zap.Int("attempt", attempt),
			zap.Error(err),
			zap.String("note", "the account will reappear in the scheduler snapshot at the next full rebuild at the latest"),
		)
		if attempt < circuitRecoveryRefreshAttempts {
			b.afterRecoveryRefresh(accountID, circuitPersistRetryDelay, attempt+1)
		}
	})
}

// refreshSafely 把刷新回调里的 panic 转成错误：定时器 goroutine 里的 panic 会带崩整个进程。
func (b *AccountCircuitBreaker) refreshSafely(ctx context.Context, accountID int64) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("circuit breaker recovery refresh panic: %v", r)
		}
	}()
	return b.recoveryRefresh(ctx, accountID)
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
	st, ok := v.(*accountCircuitState)
	if !ok {
		return time.Time{}, false
	}
	open := st.openUntil.Load()
	if open == 0 || b.now().UnixNano() >= open {
		return time.Time{}, false
	}
	return time.Unix(0, open), true
}

// EarliestRecovery 返回本进程里最早结束冷却的熔断账号还要多久恢复；没有账号在冷却中时 ok=false。
// 网关自己生成的「没有可用账号」503 用它给 Retry-After 一个有依据的数字（这是个提示：
// 它不区分平台，最早恢复的那个账号不一定能服务当前请求）。
func (b *AccountCircuitBreaker) EarliestRecovery() (time.Duration, bool) {
	if b == nil || !b.enabled {
		return 0, false
	}
	nowN := b.now().UnixNano()
	var best int64
	b.states.Range(func(_, v any) bool {
		st, ok := v.(*accountCircuitState)
		if !ok {
			return true
		}
		if open := st.openUntil.Load(); open > nowN && (best == 0 || open < best) {
			best = open
		}
		return true
	})
	if best == 0 {
		return 0, false
	}
	return time.Duration(best - nowN), true
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
	st, ok := v.(*accountCircuitState)
	if !ok {
		return
	}
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
		Observed:              b.stats.observed.Load(),
		Failures:              b.stats.failures.Load(),
		IgnoredNoise:          b.stats.ignoredNoise.Load(),
		IgnoredOpen:           b.stats.ignoredOpen.Load(),
		Superseded:            b.stats.superseded.Load(),
		Trips:                 b.stats.trips.Load(),
		FastTrips:             b.stats.fastTrips.Load(),
		ValveBlocked:          b.stats.valveBlocked.Load(),
		Persisted:             b.stats.persisted.Load(),
		PersistFailed:         b.stats.persistFailed.Load(),
		Recoveries:            b.stats.recoveries.Load(),
		RecoveryRefreshes:     b.stats.recoveryRefreshed.Load(),
		RecoveryRefreshFailed: b.stats.recoveryRefreshFailed.Load(),
		RequestSamples:        int64(b.reqSamples.count()),
		TrackedAccount:        tracked,
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
		st, ok := v.(*accountCircuitState)
		if !ok {
			return true
		}
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
	b.reqSamples.purge(nowN)
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

// valveStats 是安全阀判断时看到的平台状况（用于日志与测试）。
type valveStats struct {
	participating int // 本账号 + 最近 1 分钟已被熔断的其他账号 + 仍在接流量且样本数达标的其他账号
	healthy       int // 参与统计的其他账号里「健康」的个数（自己不满足熔断条件，且最近 2 个桶里至少有一次成功）
	tripped       int // 最近 1 分钟内已被熔断的其他账号数
}

// valveBlocks 判断是否应当因为「整个平台都在失败」而放弃熔断这个账号。
//
// 安全阀要拦的是本机/网络故障：这时同平台所有账号一起失败，逐个熔断只会把部分故障放大成全站不可用。
// 反过来，只要同平台还有一个「健康」账号，说明网络和平台都是好的，问题出在这个账号自己身上，必须熔断：
// 6 个账号坏了 4 个、另外 2 个好的还在接流量时，4 个坏账号都应该被摘掉。
//
// 健康账号 = 仍在接流量（不在冷却中）、5 分钟窗口样本数达标、自己不满足熔断条件（失败率没越线、也没有触发快速熔断），
// 并且最近 2 个桶里至少有一次成功。最后一条是为了不被「长期健康历史稀释」骗过：一个账号 5 分钟窗口里的失败率
// 还很低，但最近一分钟已经一次成功都没有时，它是在失败，不是健康证据。
//
// 拦截条件：healthy == 0 且 participating >= ValveMinAccounts。
// 参与账号不足 ValveMinAccounts 个的小池子永远不拦（事故现场就是 2 个账号：必须能全部熔断）。
// 样本数不足的低流量账号既不算参与也不算健康（看不出它好不好）。
func (b *AccountCircuitBreaker) valveBlocks(platform string, self int64, nowN int64, epoch uint64) (bool, valveStats) {
	vs := valveStats{tripped: b.valve.recent(platform, self, nowN)}
	vs.participating = 1 + vs.tripped
	b.states.Range(func(_, v any) bool {
		st, ok := v.(*accountCircuitState)
		if !ok || st.id == self || st.platformName() != platform {
			return true
		}
		if open := st.openUntil.Load(); open != 0 && nowN < open {
			return true // 已在熔断中：已经算在 tripped 里
		}
		total, _ := st.window.sum(epoch)
		if total < b.minRequests {
			return true
		}
		vs.participating++
		recentTotal, recentFailed := st.window.sumRecent(epoch, circuitFastBuckets)
		if recentTotal > recentFailed && !b.evaluate(st, epoch).trip {
			vs.healthy++
		}
		return true
	})
	return vs.healthy == 0 && vs.participating >= b.valveMin, vs
}

// ---- 请求级样本去重 ------------------------------------------------------------

type circuitRequestKey struct {
	requestID string
	accountID int64
}

type circuitRequestSample struct {
	epoch uint64
	at    int64 // unix 纳秒，用于过期
}

type circuitRequestShard struct {
	mu        sync.Mutex
	samples   map[circuitRequestKey]circuitRequestSample
	nextSweep int64
	size      atomic.Int64 // 与 len(samples) 同步；成功路径用它免锁判断「这个分片里有没有失败样本」
}

// circuitRequestSamples 记住「每个（客户端请求，账号）最近一次被记入窗口的失败」，
// 让同一请求在同一账号上的后一次尝试能撤销前一次的样本。只存失败样本，所以健康流量几乎不占内存。
type circuitRequestSamples struct {
	shards [circuitRequestShards]circuitRequestShard
}

// circuitRequestShardIndex 用 FNV-1a 把 request id 分到分片（不分配内存）。
func circuitRequestShardIndex(requestID string) int {
	h := uint32(2166136261)
	for i := 0; i < len(requestID); i++ {
		h ^= uint32(requestID[i])
		h *= 16777619
	}
	return int(h % circuitRequestShards)
}

func (sh *circuitRequestShard) purgeLocked(nowN int64) {
	cut := nowN - int64(circuitRequestTTL)
	for k, v := range sh.samples {
		if v.at < cut {
			delete(sh.samples, k)
		}
	}
	sh.size.Store(int64(len(sh.samples)))
}

// recordFailure 记下这次失败，并返回同一（请求，账号）之前记入的失败样本所在的 epoch。
func (c *circuitRequestSamples) recordFailure(requestID string, accountID int64, epoch uint64, nowN int64) (prevEpoch uint64, had bool) {
	sh := &c.shards[circuitRequestShardIndex(requestID)]
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if sh.samples == nil {
		sh.samples = make(map[circuitRequestKey]circuitRequestSample)
	}
	if nowN >= sh.nextSweep {
		sh.purgeLocked(nowN)
		sh.nextSweep = nowN + int64(circuitRequestSweepEvery)
	}
	key := circuitRequestKey{requestID: requestID, accountID: accountID}
	prev, ok := sh.samples[key]
	if !ok && len(sh.samples) >= circuitRequestShardMax {
		return 0, false // 满了：不再去重
	}
	sh.samples[key] = circuitRequestSample{epoch: epoch, at: nowN}
	sh.size.Store(int64(len(sh.samples)))
	return prev.epoch, ok
}

// clearOnSuccess 这次尝试成功了：撤销同一（请求，账号）之前记入的失败样本，并返回它所在的 epoch。
func (c *circuitRequestSamples) clearOnSuccess(requestID string, accountID int64) (prevEpoch uint64, had bool) {
	sh := &c.shards[circuitRequestShardIndex(requestID)]
	if sh.size.Load() == 0 {
		return 0, false
	}
	sh.mu.Lock()
	defer sh.mu.Unlock()
	key := circuitRequestKey{requestID: requestID, accountID: accountID}
	prev, ok := sh.samples[key]
	if !ok {
		return 0, false
	}
	delete(sh.samples, key)
	sh.size.Store(int64(len(sh.samples)))
	return prev.epoch, true
}

// purge 清掉所有过期的请求级样本（由 sweep 每分钟调用一次，保证没有新流量时也不留着）。
func (c *circuitRequestSamples) purge(nowN int64) {
	for i := range c.shards {
		sh := &c.shards[i]
		sh.mu.Lock()
		sh.purgeLocked(nowN)
		sh.mu.Unlock()
	}
}

// count 返回当前记着的请求级样本数（测试与排障用）。
func (c *circuitRequestSamples) count() int {
	n := 0
	for i := range c.shards {
		n += int(c.shards[i].size.Load())
	}
	return n
}
