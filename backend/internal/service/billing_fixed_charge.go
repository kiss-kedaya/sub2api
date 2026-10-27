package service

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
)

// InflightScopeReservationCache serializes known monetary commitments across
// instances. It is independent of the optional estimate-based admission switch.
type InflightScopeReservationCache interface {
	ReserveInflightScope(context.Context, string, string, float64, time.Duration) (float64, error)
	ReleaseInflightScope(context.Context, string, string) error
}

type fixedChargeReservationCache struct {
	cache  InflightScopeReservationCache
	scopes []string
}

func (c *fixedChargeReservationCache) ReserveInflightBalance(context.Context, int64, string, float64, float64, time.Duration) (bool, float64, error) {
	return false, 0, ErrBillingServiceUnavailable
}

func (c *fixedChargeReservationCache) ReleaseInflightBalance(ctx context.Context, _ int64, requestID string) error {
	var first error
	for _, scope := range c.scopes {
		if err := c.cache.ReleaseInflightScope(ctx, scope, requestID); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// ReserveFixedCharge checks the complete, known charge without changing normal
// generation admission or capping the amount to an estimated reservation limit.
// Registering each commitment before authoritative reads closes the concurrent
// create/extend gap; a failed Redis read never silently disables this check.
func (s *BillingCacheService) ReserveFixedCharge(ctx context.Context, user *User, apiKey *APIKey, group *Group, subscription *UserSubscription, amount float64) (*InflightReservation, error) {
	if s == nil || user == nil || math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 {
		return nil, ErrBillingServiceUnavailable
	}
	if amount == 0 || (s.cfg != nil && s.cfg.RunMode == config.RunModeSimple) {
		return nil, nil
	}
	cache, ok := s.cache.(InflightScopeReservationCache)
	if !ok || cache == nil {
		return nil, ErrBillingServiceUnavailable
	}
	adapter := &fixedChargeReservationCache{cache: cache}
	r := &InflightReservation{cache: adapter, userID: user.ID, requestID: uuid.NewString(), amount: amount, ttl: defaultInflightReservationTTL}
	r.refs.Store(1)
	reserve := func(scope string) (float64, error) {
		// Include even a partially completed registration in best-effort cleanup.
		adapter.scopes = append(adapter.scopes, scope)
		pending, err := cache.ReserveInflightScope(ctx, scope, r.requestID, amount, r.ttl)
		if err != nil {
			return 0, ErrBillingServiceUnavailable.WithCause(err)
		}
		return pending + amount, nil
	}
	subscriptionBill := group != nil && group.IsSubscriptionType() && subscription != nil
	var err error
	if subscriptionBill {
		var pending float64
		pending, err = reserve(fmt.Sprintf("subscription:%d", subscription.ID))
		if err == nil {
			err = s.checkFixedChargeSubscription(ctx, user.ID, group, pending)
		}
	} else {
		var pending, balance float64
		pending, err = reserve(fmt.Sprintf("user:%d", user.ID))
		if err == nil {
			if s.userRepo != nil {
				balance, err = s.getUserBalanceFromDB(ctx, user.ID)
			} else {
				balance, err = s.GetUserBalance(ctx, user.ID)
			}
			if err != nil {
				err = ErrBillingServiceUnavailable.WithCause(err)
			} else if balance < pending {
				err = ErrInsufficientBalance
			}
		}
	}
	if err == nil && apiKey != nil && (apiKey.Quota > 0 || apiKey.HasRateLimits()) {
		var pending float64
		pending, err = reserve(fmt.Sprintf("apikey:%d", apiKey.ID))
		if err == nil {
			err = s.checkFixedChargeAPIKey(ctx, apiKey, pending)
		}
	}
	if err != nil {
		r.Release()
		return nil, err
	}
	return r, nil
}

func (s *BillingCacheService) checkFixedChargeSubscription(ctx context.Context, userID int64, group *Group, pending float64) error {
	read := s.GetSubscriptionStatus
	if s.subRepo != nil {
		read = s.getSubscriptionFromDB
	}
	data, err := read(ctx, userID, group.ID)
	if err != nil || data == nil {
		return ErrBillingServiceUnavailable
	}
	if data.Status != SubscriptionStatusActive {
		return ErrSubscriptionInvalid
	}
	if !data.ExpiresAt.IsZero() && !data.ExpiresAt.After(time.Now()) {
		return ErrSubscriptionInvalid
	}
	if group.HasDailyLimit() && data.DailyUsage+pending > *group.DailyLimitUSD {
		return ErrDailyLimitExceeded
	}
	if group.HasWeeklyLimit() && data.WeeklyUsage+pending > *group.WeeklyLimitUSD {
		return ErrWeeklyLimitExceeded
	}
	if group.HasMonthlyLimit() && data.MonthlyUsage+pending > *group.MonthlyLimitUSD {
		return ErrMonthlyLimitExceeded
	}
	return nil
}

func (s *BillingCacheService) checkFixedChargeAPIKey(ctx context.Context, key *APIKey, pending float64) error {
	data := &APIKeyRateLimitData{QuotaUsed: key.QuotaUsed}
	if s.apiKeyRateLimitLoader != nil {
		var err error
		data, err = s.apiKeyRateLimitLoader.GetRateLimitData(ctx, key.ID)
		if err != nil || data == nil {
			return ErrBillingServiceUnavailable
		}
	} else if key.HasRateLimits() {
		// Isolated test/embedded adapters can provide the same rate-window data.
		cached, err := s.cache.GetAPIKeyRateLimit(ctx, key.ID)
		if err != nil || cached == nil {
			return ErrBillingServiceUnavailable
		}
		data.Usage5h, data.Usage1d, data.Usage7d = cached.Usage5h, cached.Usage1d, cached.Usage7d
		if cached.Window5h > 0 {
			v := time.Unix(cached.Window5h, 0)
			data.Window5hStart = &v
		}
		if cached.Window1d > 0 {
			v := time.Unix(cached.Window1d, 0)
			data.Window1dStart = &v
		}
		if cached.Window7d > 0 {
			v := time.Unix(cached.Window7d, 0)
			data.Window7dStart = &v
		}
	}
	exceeds := func(used, limit float64) bool { return limit > 0 && used+pending > limit }
	switch {
	case exceeds(data.QuotaUsed, key.Quota):
		return ErrAPIKeyQuotaExhausted
	case exceeds(data.EffectiveUsage5h(), key.RateLimit5h):
		return ErrAPIKeyRateLimit5hExceeded
	case exceeds(data.EffectiveUsage1d(), key.RateLimit1d):
		return ErrAPIKeyRateLimit1dExceeded
	case exceeds(data.EffectiveUsage7d(), key.RateLimit7d):
		return ErrAPIKeyRateLimit7dExceeded
	}
	return nil
}
