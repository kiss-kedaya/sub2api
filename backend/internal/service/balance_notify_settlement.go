package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// 合并结算模式下的低余额提醒。
//
// 合并结算让请求的计费事务不再返回扣费后的余额（NewBalance=nil），请求路径只剩请求开始时
// 的鉴权快照可用：快照最长可滞后数分钟，用它判断「向下穿越阈值」既会在一个窗口内对每个
// 请求都重复触发（没有冷却），也会在快照已经低于阈值时整次漏掉。
//
// 这里改由结算 worker 触发：每次结算 flush 的 UPDATE ... RETURNING 同时给出该用户结算后的
// 余额（NewBalance）和本次合并扣掉的总额（Amount），结算前的余额恰好是 NewBalance+Amount，
// 所以「这次 flush 是否把余额从阈值之上压到阈值之下」是精确判断，且每次穿越恰好落在一次
// flush 里——和逐请求同步扣费时的语义一致，热路径上没有任何新增查询。
// 判断需要的用户提醒偏好（开关/阈值/类型/累计充值）从同一条 UPDATE 的 RETURNING 带出，
// 只有真的穿越阈值时才会再读一次用户取收件人。

const (
	// settlementLowBalanceCooldown 是同一用户两次「余额不足」提醒之间的最短间隔。穿越本身
	// 已经是「从阈值之上到之下」的边沿触发；冷却兜住的是余额在阈值附近来回的情形
	// （小额充值又扣掉、退款后再扣），不让同一个人一小时里收到一串提醒。
	settlementLowBalanceCooldown = 30 * time.Minute
	// settlementLowBalanceCooldownMaxEntries 超过这个条目数时清理已过期的冷却记录，
	// 让冷却表的大小与「最近半小时被提醒过的用户数」同阶，不会随时间无界增长。
	settlementLowBalanceCooldownMaxEntries = 4096
	// settlementLowBalanceLoadTimeout 限制一次穿越判断里读设置/读用户（含重试）的总耗时。
	settlementLowBalanceLoadTimeout = 3 * time.Second
	// settlementLowBalanceLoadAttempts 是穿越后读用户取收件人的最多尝试次数。穿越是只会出现
	// 一次的边沿（之后的 flush 看到的都已经在阈值以下），读用户偶发失败不能成为这条提醒丢失的原因。
	settlementLowBalanceLoadAttempts = 3
	// settlementLowBalanceLoadBackoff 是重试间隔的基数（第 n 次重试前等 n 倍）。
	settlementLowBalanceLoadBackoff = 150 * time.Millisecond
)

// balanceNotifyUserReader 是提醒服务取完整用户（收件人、用户名）所需的最小接口。
type balanceNotifyUserReader interface {
	GetByID(ctx context.Context, id int64) (*User, error)
}

// SetUserReader 注入读取用户的仓储。合并结算的低余额提醒只在真的穿越阈值时才读一次用户；
// 未注入时结算观察者什么也不做。
func (s *BalanceNotifyService) SetUserReader(reader balanceNotifyUserReader) {
	s.userReader = reader
}

// settlementAlertTracker 是按用户的提醒冷却表。
type settlementAlertTracker struct {
	mu sync.Mutex
	at map[int64]time.Time
}

// claim 在冷却期外登记一次提醒并返回 true；冷却期内返回 false。
func (t *settlementAlertTracker) claim(userID int64, now time.Time, cooldown time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if last, ok := t.at[userID]; ok && now.Sub(last) < cooldown {
		return false
	}
	if t.at == nil {
		t.at = make(map[int64]time.Time)
	}
	if len(t.at) >= settlementLowBalanceCooldownMaxEntries {
		for id, last := range t.at {
			if now.Sub(last) >= cooldown {
				delete(t.at, id)
			}
		}
	}
	t.at[userID] = now
	return true
}

// release 撤销一次登记（提醒没能真正发出时，让下一次穿越仍有机会提醒）。
func (t *settlementAlertTracker) release(userID int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.at, userID)
}

func (s *BalanceNotifyService) settlementNow() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

// ObserveBalanceSettlements 实现 UsageBalanceSettlementObserver：对一次已提交的结算 flush
// 里的每个用户判断是否向下穿越了余额提醒阈值，穿越且不在冷却期内才发提醒。
func (s *BalanceNotifyService) ObserveBalanceSettlements(results []UsageBalanceSettlementResult) {
	if s == nil || s.userReader == nil || s.emailService == nil || s.settingRepo == nil {
		return
	}
	for i := range results {
		s.observeSettledBalance(&results[i])
	}
}

func (s *BalanceNotifyService) observeSettledBalance(r *UsageBalanceSettlementResult) {
	if !r.NotifyEnabled {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), settlementLowBalanceLoadTimeout)
	defer cancel()

	// 提醒偏好来自结算那条 UPDATE 的 RETURNING，不需要读库就能解析出有效阈值。
	probe := &User{
		ID:                         r.UserID,
		BalanceNotifyEnabled:       true,
		BalanceNotifyThreshold:     r.NotifyThreshold,
		BalanceNotifyThresholdType: r.NotifyThresholdType,
		TotalRecharged:             r.TotalRecharged,
	}
	threshold, rechargeURL, ok := s.resolveUserEffectiveThreshold(ctx, probe)
	if !ok {
		return
	}
	newBalance := r.NewBalance
	oldBalance := newBalance + r.Amount
	if !crossedDownward(oldBalance, newBalance, threshold) {
		return
	}
	if !s.settlementAlerts.claim(r.UserID, s.settlementNow(), settlementLowBalanceCooldown) {
		return
	}
	user, err := s.loadAlertUser(ctx, r.UserID)
	if err != nil {
		s.settlementAlerts.release(r.UserID)
		slog.Warn("settlement low balance alert skipped: user unavailable", "user_id", r.UserID, "error", err)
		return
	}
	if !user.BalanceNotifyEnabled {
		// 用户在这次 flush 之后刚把提醒关掉。
		return
	}
	s.dispatchBalanceLowEmail(ctx, user, newBalance, threshold, rechargeURL)
}

// loadAlertUser 读取收件人所需的完整用户，瞬时失败时有限次重试。用户已不存在时不重试。
func (s *BalanceNotifyService) loadAlertUser(ctx context.Context, userID int64) (*User, error) {
	backoff := settlementLowBalanceLoadBackoff
	if s.loadRetryBackoff > 0 {
		backoff = s.loadRetryBackoff
	}
	var lastErr error
	for attempt := 0; attempt < settlementLowBalanceLoadAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, errors.Join(lastErr, ctx.Err())
			case <-time.After(time.Duration(attempt) * backoff):
			}
		}
		user, err := s.userReader.GetByID(ctx, userID)
		if err == nil && user != nil && user.ID == userID {
			return user, nil
		}
		if err == nil {
			err = ErrUserNotFound
		}
		lastErr = err
		if errors.Is(err, ErrUserNotFound) {
			break
		}
	}
	return nil, lastErr
}
