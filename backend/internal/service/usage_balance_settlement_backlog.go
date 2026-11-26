package service

import (
	"context"
	"sync/atomic"
	"time"
)

// 余额合并结算的积压观测与保护。
//
// 合并结算把扣费先落持久化队列（billing_balance_settlements），由 worker 异步合并成
// users.balance 更新。队列是否被及时消费是这个模式的唯一风险面：worker 卡住或跟不上时，
// users.balance 会落后于真实扣费，队列也会无界增长。这里提供三件事：
//
//  1. 用有界、走部分索引的查询读出队列深度与最老一笔的等待时间（观测）；
//  2. 积压超过阈值时让本进程的新请求退回逐请求同步扣费，带迟滞地恢复（保护）；
//  3. 把读数与是否已退回暴露给运维健康接口和周期日志。
//
// 开关 billing.balance_merge_settlement_enabled 关闭时，下面所有逻辑都不会启动：
// 没有采样 goroutine、没有额外查询，计费路径与改动前逐字节一致。

const (
	// usageBalanceSettlementBacklogSampleInterval 是积压采样周期。
	usageBalanceSettlementBacklogSampleInterval = time.Second
	// usageBalanceSettlementBacklogQueryTimeout 限制单次采样查询：库慢时宁可放弃这一次，
	// 也不能让采样自己变成堆积的查询。
	usageBalanceSettlementBacklogQueryTimeout = 2 * time.Second
	// usageBalanceSettlementBacklogLogEvery 限制「积压超阈值」这类重复告警的频率。
	usageBalanceSettlementBacklogLogEvery = 30 * time.Second
	// usageBalanceSettlementBacklogMinDepthCap 是深度计数的最小上限。深度读数最多数这么多行
	// （阈值的两倍与它取大），超过即视为「至少这么多」，避免队列很深时计数本身变成重查询。
	usageBalanceSettlementBacklogMinDepthCap = 20000
	// usageBalanceSettlementObserveBuffer 是结算结果交给低余额观察者的缓冲批数。
	usageBalanceSettlementObserveBuffer = 64
)

// UsageBalanceSettlementBacklog 是对结算队列的一次有界读数。
type UsageBalanceSettlementBacklog struct {
	// Pending 是 status=settlement_pending 的行数；PendingCapped 为 true 时只表示「至少这么多」。
	Pending       int64
	PendingCapped bool
	// OldestAge 是队首（按 available_at,id 排序的前若干行）里最老一笔扣费已等待的时间，
	// 队列为空时为 0。只看索引序的队首是近似：精确的 min(created_at) 没有索引，全表求最小值
	// 会扫描整个待结算集；队首近似在「worker 停了/慢了」时与精确值一致，在「整批反复失败」
	// 时因失败退避会让队首混入不同年龄的行，取其中最老的一笔仍能反映真实等待。
	OldestAge time.Duration
}

// UsageBalanceSettlementBacklogReader 由仓储实现：只读、走 idx_billing_balance_settlements_pending
// 部分索引、成本与队列深度无关（深度计数有上限，队首只取固定行数）。
type UsageBalanceSettlementBacklogReader interface {
	BalanceSettlementBacklog(ctx context.Context, depthCap int64) (UsageBalanceSettlementBacklog, error)
}

// 进程级状态。与 usageBalanceSettlementConsumerRunning 同一模式：计费仓储在请求路径上
// 只读原子变量，决不为此查库。
var (
	// usageBalanceSettlementBacklogFallback 为 true 表示本进程的新扣费必须绕开队列、同步扣费。
	usageBalanceSettlementBacklogFallback atomic.Bool
	// usageBalanceSettlementFallbackCharges 累计因积压退回而同步扣费的请求数（仅观测）。
	usageBalanceSettlementFallbackCharges atomic.Uint64
	// usageBalanceSettlementNotifierActive 为 true 表示本进程的结算 worker 会按每次结算的
	// 精确前后余额发出低余额提醒，请求路径因此不再用（会过期的）鉴权快照重复判断。
	usageBalanceSettlementNotifierActive atomic.Bool
)

// UsageBalanceSettlementBacklogFallbackActive 报告本进程是否因积压超阈值而把新扣费
// 退回同步路径。计费仓储在每次扣费时读它。
func UsageBalanceSettlementBacklogFallbackActive() bool {
	return usageBalanceSettlementBacklogFallback.Load()
}

// RecordUsageBalanceSettlementFallbackCharge 记一笔因积压退回而走同步路径的扣费。
func RecordUsageBalanceSettlementFallbackCharge() {
	usageBalanceSettlementFallbackCharges.Add(1)
}

// UsageBalanceSettlementNotifierActive 报告结算 worker 是否在负责低余额提醒。
func UsageBalanceSettlementNotifierActive() bool {
	return usageBalanceSettlementNotifierActive.Load()
}

// backlogOverLimit 判断队列是否已超过任一阈值（0 表示该维度不限制）。
func backlogOverLimit(b UsageBalanceSettlementBacklog, maxBacklog int64, maxAge time.Duration) bool {
	return (maxBacklog > 0 && b.Pending > maxBacklog) || (maxAge > 0 && b.OldestAge > maxAge)
}

// backlogCalm 判断队列是否已回落到阈值的一半以下（迟滞：退出条件比进入条件严格得多，
// 否则读数在阈值附近抖动时会来回切换）。
func backlogCalm(b UsageBalanceSettlementBacklog, maxBacklog int64, maxAge time.Duration) bool {
	return (maxBacklog <= 0 || b.Pending*2 < maxBacklog) && (maxAge <= 0 || b.OldestAge*2 < maxAge)
}

// nextBacklogFallbackState 给出下一个「是否退回同步扣费」状态：
// 未退回时，任一维度超限才退回；已退回时，两个维度都回落到阈值一半以下才恢复，
// 处于两者之间保持原状。
func nextBacklogFallbackState(active bool, b UsageBalanceSettlementBacklog, maxBacklog int64, maxAge time.Duration) bool {
	if active {
		return !backlogCalm(b, maxBacklog, maxAge)
	}
	return backlogOverLimit(b, maxBacklog, maxAge)
}

// usageBalanceSettlementDepthCap 计算深度计数上限：至少能数到阈值的两倍，才能同时支持
// 「超过阈值」与「回落到一半以下」两个判断。
func usageBalanceSettlementDepthCap(maxBacklog int64) int64 {
	depthCap := int64(usageBalanceSettlementBacklogMinDepthCap)
	if doubled := maxBacklog * 2; doubled > depthCap {
		depthCap = doubled
	}
	return depthCap
}

// UsageBalanceSettlementObserver 接收每次已提交的结算 flush 的逐用户结果，
// 用来在合并结算模式下按精确的前后余额发低余额提醒。
type UsageBalanceSettlementObserver interface {
	ObserveBalanceSettlements(results []UsageBalanceSettlementResult)
}

// UsageBalanceSettlementWorkerOptions 配置结算 worker 的灰度期保护。零值等于改动前的行为：
// 不采样积压、不退回、不发低余额提醒。
type UsageBalanceSettlementWorkerOptions struct {
	// MergeEnabled 镜像 billing.balance_merge_settlement_enabled。积压采样、同步退回和
	// 低余额观察者只在它为 true 时运行。
	MergeEnabled bool
	// MaxBacklog / MaxAge 是退回阈值，0 表示该维度不触发。
	MaxBacklog int64
	MaxAge     time.Duration
	// Observer 在每次成功的结算 flush 之后收到逐用户结果（异步、有界缓冲）。
	Observer UsageBalanceSettlementObserver
	// BacklogSampleInterval 覆盖默认的 1 秒采样周期（测试用）。
	BacklogSampleInterval time.Duration
}

// UsageBalanceSettlementHealth 是暴露在运维健康接口里的只读快照。
type UsageBalanceSettlementHealth struct {
	// MergeEnabled 为 false 表示本进程没有开启合并结算；此时下面的积压读数仍可参考
	// （例如预扣结算行），但不会有退回逻辑。
	MergeEnabled    bool `json:"merge_enabled"`
	ConsumerRunning bool `json:"consumer_running"`

	Pending       int64 `json:"pending"`
	PendingCapped bool  `json:"pending_capped,omitempty"`
	OldestAgeMs   int64 `json:"oldest_age_ms"`
	MaxBacklog    int64 `json:"max_backlog"`
	MaxAgeSeconds int64 `json:"max_age_seconds"`

	FallbackActive      bool       `json:"fallback_active"`
	FallbackSince       *time.Time `json:"fallback_since,omitempty"`
	FallbackTransitions uint64     `json:"fallback_transitions"`
	// FallbackCharges 是本进程启动以来因积压退回而同步扣费的请求数。
	FallbackCharges uint64 `json:"fallback_charges"`
	// LastSampleAt 是积压监控最近一次成功读数的时间；开关开启时它应当每秒刷新，
	// 停在很久以前说明监控停了（此时退回状态不再更新）。开关关闭时没有监控、该字段缺省。
	LastSampleAt *time.Time `json:"last_sample_at,omitempty"`

	Flushes        uint64 `json:"flushes"`
	Events         uint64 `json:"events"`
	Failures       uint64 `json:"failures"`
	SampleFailures uint64 `json:"sample_failures"`
	// ObserverDropped 是因低余额观察者来不及处理而丢弃的结算批数（正常应恒为 0）。
	ObserverDropped uint64 `json:"observer_dropped"`

	StatsError string `json:"stats_error,omitempty"`
}

type usageBalanceSettlementBacklogSnapshot struct {
	backlog   UsageBalanceSettlementBacklog
	sampledAt time.Time
}
