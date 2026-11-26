package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// 登录会话可观测：refresh token 轮换、会话绑定、登录发令牌兜底的进程内计数。
//
// 计数是 atomic 累加；第一次事件之后 60 秒汇总打一条 INFO（只在有变化时），之后空闲时不起任何
// goroutine / ticker。INFO 汇总里没有用户 ID；用户 ID 只出现在限频的 WARN 单条事件里。
// 任何日志都不记录令牌明文或哈希。

// authStat 是一个计数器的编号。refresh_* 里 ok 是成功总数，reuse_leeway_retry 与
// binding_rebind 是成功里的子集；其余是互斥的失败原因。leeway_fingerprint_ok / _reject 是宽限路径上
// 「本人」判定（浏览器家族 + 系统家族 + IP 家族 + 网段全相同）的通过 / 因指纹拒绝次数。
type authStat int

const (
	statRefreshOK authStat = iota
	statRefreshInvalid
	statRefreshExpired
	statRefreshSessionExpired
	statRefreshReuse
	statRefreshLeewayFingerprintReject
	statRefreshLeewayFingerprintOK
	statRefreshReuseLeewayRetry
	statRefreshBindingRebind
	statRefreshBindingRebindAuditSuppressed
	statRefreshBindingRevoke
	statRefreshIPFamilySwitch
	statRefreshRejected
	statRefreshUnavailable
	statAccessBindingMismatch
	statAccessBindingRevoke
	statAccessIPFamilySwitch
	statLoginTokenPairRetryOK
	statLoginTokenPairFallback
	statWarnSuppressed
	authStatCount
)

var authStatNames = [authStatCount]string{
	statRefreshOK:                           "refresh_ok",
	statRefreshInvalid:                      "refresh_invalid",
	statRefreshExpired:                      "refresh_expired",
	statRefreshSessionExpired:               "refresh_session_expired",
	statRefreshReuse:                        "refresh_reuse",
	statRefreshLeewayFingerprintReject:      "refresh_leeway_fingerprint_reject",
	statRefreshLeewayFingerprintOK:          "refresh_leeway_fingerprint_ok",
	statRefreshReuseLeewayRetry:             "refresh_reuse_leeway_retry",
	statRefreshBindingRebind:                "refresh_binding_rebind",
	statRefreshBindingRebindAuditSuppressed: "refresh_binding_rebind_audit_suppressed",
	statRefreshBindingRevoke:                "refresh_binding_revoke",
	statRefreshIPFamilySwitch:               "refresh_ip_family_switch",
	statRefreshRejected:                     "refresh_rejected",
	statRefreshUnavailable:                  "refresh_unavailable",
	statAccessBindingMismatch:               "access_binding_mismatch",
	statAccessBindingRevoke:                 "access_binding_revoke",
	statAccessIPFamilySwitch:                "access_ip_family_switch",
	statLoginTokenPairRetryOK:               "login_token_pair_retry_ok",
	statLoginTokenPairFallback:              "login_token_pair_fallback",
	statWarnSuppressed:                      "warn_suppressed",
}

const (
	authStatsFlushInterval = 60 * time.Second
	// 单条 WARN/ERROR 事件的限频：每种事件每个窗口最多这么多条，超出的只计数。
	authWarnWindow = time.Minute
	authWarnBurst  = 10
	// 指纹重绑的审计限频：同一用户 + 同一会话家族，每个窗口最多写一条审计行（计数照常累加）。
	// 多出口 NAT 的用户、或拿着令牌不停换网络的人，不会刷出大量审计行。
	rebindAuditWindow  = 10 * time.Minute
	rebindAuditMaxKeys = 4096
)

type authSessionStats struct {
	counts   [authStatCount]atomic.Int64
	armed    atomic.Bool
	interval time.Duration

	warnMu      sync.Mutex
	warnBuckets map[string]*authWarnBucket
	rebindAudit map[string]time.Time
}

type authWarnBucket struct {
	start time.Time
	n     int
}

func newAuthSessionStats(interval time.Duration) *authSessionStats {
	if interval <= 0 {
		interval = authStatsFlushInterval
	}
	return &authSessionStats{interval: interval, warnBuckets: make(map[string]*authWarnBucket), rebindAudit: make(map[string]time.Time)}
}

// inc 累加计数；这是自上次汇总以来的第一条事件时，安排一次 interval 之后的汇总。
func (st *authSessionStats) inc(stat authStat) {
	st.counts[stat].Add(1)
	if st.armed.CompareAndSwap(false, true) {
		time.AfterFunc(st.interval, st.flush)
	}
}

// flush 取走并清零所有计数；有变化时打一条 INFO 汇总。
func (st *authSessionStats) flush() {
	// 先放开再取数：取数之后到来的事件会重新安排下一次汇总，不会丢。
	st.armed.Store(false)
	args := make([]any, 0, 2*int(authStatCount)+2)
	changed := false
	for i := authStat(0); i < authStatCount; i++ {
		if n := st.counts[i].Swap(0); n > 0 {
			args = append(args, authStatNames[i], n)
			changed = true
		}
	}
	if !changed {
		return
	}
	args = append(args, "window_sec", int(st.interval/time.Second))
	slog.Info("auth session summary", args...)
}

// snapshot 返回自上次汇总以来尚未汇总的计数（只含非零项）。
func (st *authSessionStats) snapshot() map[string]int64 {
	out := make(map[string]int64)
	for i := authStat(0); i < authStatCount; i++ {
		if n := st.counts[i].Load(); n > 0 {
			out[authStatNames[i]] = n
		}
	}
	return out
}

// allowWarn 按事件种类限频；超出的事件只计入 warn_suppressed。
func (st *authSessionStats) allowWarn(kind string, now time.Time) bool {
	st.warnMu.Lock()
	w := st.warnBuckets[kind]
	if w == nil || now.Sub(w.start) >= authWarnWindow || now.Before(w.start) {
		w = &authWarnBucket{start: now}
		st.warnBuckets[kind] = w
	}
	w.n++
	allowed := w.n <= authWarnBurst
	st.warnMu.Unlock()
	if !allowed {
		st.inc(statWarnSuppressed)
	}
	return allowed
}

// allowRebindAudit 报告这次指纹重绑是否该写审计行：同一个 key（用户 + 会话家族）每个
// rebindAuditWindow 最多一条；超出的由调用方计入 refresh_binding_rebind_audit_suppressed。
// key 只在内存里，不进日志。表满（rebindAuditMaxKeys）时先清过期项，仍满就不再放行新 key，
// 内存因此有上限；代价只是这个窗口里多出来的新会话少写审计行（计数不受影响）。
func (st *authSessionStats) allowRebindAudit(key string, now time.Time) bool {
	st.warnMu.Lock()
	defer st.warnMu.Unlock()
	if last, ok := st.rebindAudit[key]; ok && !now.Before(last) && now.Sub(last) < rebindAuditWindow {
		return false
	}
	if len(st.rebindAudit) >= rebindAuditMaxKeys {
		for k, last := range st.rebindAudit {
			if now.Sub(last) >= rebindAuditWindow || now.Before(last) {
				delete(st.rebindAudit, k)
			}
		}
		if len(st.rebindAudit) >= rebindAuditMaxKeys {
			return false
		}
	}
	st.rebindAudit[key] = now
	return true
}

// authStats 返回本服务的计数器（按需创建；手写的 &AuthService{} 也能用）。
func (s *AuthService) authStats() *authSessionStats {
	s.statsOnce.Do(func() { s.stats = newAuthSessionStats(authStatsFlushInterval) })
	return s.stats
}

// warnEvent 打一条限频的 WARN 单条事件。args 不得包含令牌明文/哈希/IP/完整 UA。
func (s *AuthService) warnEvent(kind, msg string, args ...any) {
	st := s.authStats()
	if !st.allowWarn(kind, time.Now()) {
		return
	}
	slog.Warn(msg, args...)
}

// AuthStatsSnapshot 返回尚未汇总的进程内计数（只含非零项），供测试与诊断读取。
func (s *AuthService) AuthStatsSnapshot() map[string]int64 {
	return s.authStats().snapshot()
}

// NoteLoginTokenPairRetry 记录登录发令牌的一次重试结果（ok=true 表示重试成功，会话保住了 refresh token）。
func (s *AuthService) NoteLoginTokenPairRetry(ok bool) {
	if ok {
		s.authStats().inc(statLoginTokenPairRetryOK)
	}
}

// NoteLoginTokenPairFallback 记录「登录发 refresh token 失败、退回只发 access token」：
// 这样的会话在 access token 过期后必须重新登录，要计数并留一条限频 ERROR。
func (s *AuthService) NoteLoginTokenPairFallback(userID int64, cause error) {
	st := s.authStats()
	st.inc(statLoginTokenPairFallback)
	if !st.allowWarn("login_token_pair_fallback", time.Now()) {
		return
	}
	slog.Error("login issued access token only: refresh token could not be stored, the session ends when the access token expires",
		"user_id", userID, "error", cause)
}

// NoteAccessBindingMismatch 记录 access token 路径上的会话指纹不一致（revoked 表示同时撤销了会话家族）。
func (s *AuthService) NoteAccessBindingMismatch(userID int64, revoked bool) {
	st := s.authStats()
	st.inc(statAccessBindingMismatch)
	if revoked {
		st.inc(statAccessBindingRevoke)
		s.warnEvent("access_binding_revoke", "session binding mismatch on access token: session family revoked", "user_id", userID)
	}
}

// NoteAccessIPFamilySwitch 记录 access token 路径上「靠忽略 IP 家族才通过」的一次比较
// （令牌绑在 IPv4、请求来自 IPv6，或反过来）。只计数不打日志：正常用户在 WiFi 与蜂窝之间切换就会产生，
// 但它也是 loose 绑定最松的地方，量突然变大值得看一眼。
func (s *AuthService) NoteAccessIPFamilySwitch() {
	s.authStats().inc(statAccessIPFamilySwitch)
}

// isContextDone 报告错误是否只是调用方取消/超时（客户端断开不是服务端故障，不值得 WARN）。
func isContextDone(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
