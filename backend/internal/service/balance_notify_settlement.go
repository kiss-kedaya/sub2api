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
// 仅在记录的时间戳与本次登记一致时删除，避免慢路径回滚误删更新的冷却占位。
func (t *settlementAlertTracker) release(userID int64, claimedAt time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if last, ok := t.at[userID]; ok && last.Equal(claimedAt) {
		delete(t.at, userID)
	}
}

func (s *BalanceNotifyService) settlementNow() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

// ObserveBalanceSettlements 实现 UsageBalanceSettlementObserver：对一次已提交的结算 flush
// 里的每个用户判断是否向下穿越了余额提醒阈值，穿越且不在冷却期内才发提醒。
func (s *BalanceNotifyService) ObserveBalanceSettlements(ctx context.Context, results []UsageBalanceSettlementResult) {
	if s == nil || s.userReader == nil || s.emailService == nil || s.settingRepo == nil || len(results) == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return
	}
	hasCandidate := false
	for i := range results {
		if results[i].NotifyEnabled && results[i].Amount > 0 {
			hasCandidate = true
			break
		}
	}
	if !hasCandidate {
		return
	}
	cfgCtx, cancel := context.WithTimeout(ctx, settlementLowBalanceLoadTimeout)
	globalEnabled, globalThreshold, rechargeURL := s.getBalanceNotifyConfig(cfgCtx)
	cancel()
	if !globalEnabled || ctx.Err() != nil {
		return
	}
	var siteName string
	var siteNameLoaded bool
	for i := range results {
		if ctx.Err() != nil {
			return
		}
		s.observeSettledBalance(ctx, &results[i], globalThreshold, rechargeURL, &siteName, &siteNameLoaded)
	}
}

func (s *BalanceNotifyService) observeSettledBalance(
	ctx context.Context,
	r *UsageBalanceSettlementResult,
	globalThreshold float64,
	rechargeURL string,
	siteName *string,
	siteNameLoaded *bool,
) {
	if !r.NotifyEnabled {
		return
	}
	// 提醒偏好来自结算那条 UPDATE 的 RETURNING，全局设置已在批次入口读过一次，
	// 这里不需要任何额外的读库查询就能解析出有效阈值。
	probe := &User{
		ID:                         r.UserID,
		BalanceNotifyEnabled:       true,
		BalanceNotifyThreshold:     r.NotifyThreshold,
		BalanceNotifyThresholdType: r.NotifyThresholdType,
		TotalRecharged:             r.TotalRecharged,
	}
	threshold, ok := resolveUserEffectiveThresholdFromGlobal(probe, globalThreshold)
	if !ok {
		return
	}
	newBalance := r.NewBalance
	oldBalance := newBalance + r.Amount
	if !crossedDownward(oldBalance, newBalance, threshold) {
		return
	}
	if ctx.Err() != nil {
		return
	}
	claimedAt := s.settlementNow()
	if !s.settlementAlerts.claim(r.UserID, claimedAt, settlementLowBalanceCooldown) {
		return
	}
	userCtx, cancel := context.WithTimeout(ctx, settlementLowBalanceLoadTimeout)
	defer cancel()

	user, err := s.loadAlertUser(userCtx, r.UserID)
	if err != nil {
		s.settlementAlerts.release(r.UserID, claimedAt)
		slog.Warn("settlement low balance alert skipped: user unavailable", "user_id", r.UserID, "error", err)
		return
	}
	if !user.BalanceNotifyEnabled {
		// 用户在这次 flush 之后刚把提醒关掉；未发出提醒时释放冷却槽。
		s.settlementAlerts.release(r.UserID, claimedAt)
		return
	}
	if ctx.Err() != nil {
		s.settlementAlerts.release(r.UserID, claimedAt)
		return
	}
	if !*siteNameLoaded {
		*siteName = s.getSiteName(userCtx)
		if ctx.Err() != nil {
			s.settlementAlerts.release(r.UserID, claimedAt)
			return
		}
		*siteNameLoaded = true
	}
	s.dispatchBalanceLowEmailWithSiteName(user, newBalance, threshold, *siteName, rechargeURL)
}

// loadAlertUser 读取收件人所需的完整用户，瞬时失败时有限次重试。用户已不存在或上下文取消时不重试。
func (s *BalanceNotifyService) loadAlertUser(ctx context.Context, userID int64) (*User, error) {
	backoff := settlementLowBalanceLoadBackoff
	if s.loadRetryBackoff > 0 {
		backoff = s.loadRetryBackoff
	}
	var lastErr error
	for attempt := 0; attempt < settlementLowBalanceLoadAttempts; attempt++ {
		if ctx.Err() != nil {
			return nil, errors.Join(lastErr, ctx.Err())
		}
		if attempt > 0 {
			timer := time.NewTimer(time.Duration(attempt) * backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, errors.Join(lastErr, ctx.Err())
			case <-timer.C:
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
		if errors.Is(err, ErrUserNotFound) || ctx.Err() != nil {
			break
		}
	}
	return nil, lastErr
}
