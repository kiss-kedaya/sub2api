//go:build unit

package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type smartRouteEligibilityCache struct {
	billingCacheWorkerStub
	balance                                       float64
	sub                                           *SubscriptionCacheData
	quota                                         *UserPlatformQuotaCacheEntry
	rate                                          *APIKeyRateLimitCacheData
	balanceCalls, subCalls, quotaCalls, rateCalls int
	userID, groupID                               int64
	platform                                      string
}

func (c *smartRouteEligibilityCache) GetUserBalance(_ context.Context, userID int64) (float64, error) {
	c.balanceCalls++
	c.userID = userID
	return c.balance, nil
}

func (c *smartRouteEligibilityCache) GetSubscriptionCache(_ context.Context, userID, groupID int64) (*SubscriptionCacheData, error) {
	c.subCalls++
	c.userID, c.groupID = userID, groupID
	return c.sub, nil
}

func (c *smartRouteEligibilityCache) GetUserPlatformQuotaCache(_ context.Context, userID int64, platform string) (*UserPlatformQuotaCacheEntry, bool, error) {
	c.quotaCalls++
	c.userID, c.platform = userID, platform
	return c.quota, true, nil
}

func (c *smartRouteEligibilityCache) GetAPIKeyRateLimit(_ context.Context, _ int64) (*APIKeyRateLimitCacheData, error) {
	c.rateCalls++
	return c.rate, nil
}

type smartRouteRPMRepo struct {
	rpmOverrideRepoStub
	userID, groupID int64
}

func (r *smartRouteRPMRepo) GetRPMOverrideByUserAndGroup(ctx context.Context, userID, groupID int64) (*int, error) {
	r.userID, r.groupID = userID, groupID
	return r.rpmOverrideRepoStub.GetRPMOverrideByUserAndGroup(ctx, userID, groupID)
}

func smartRouteEligibilityFixture() (*BillingCacheService, *smartRouteEligibilityCache, *userRPMCacheStub, *smartRouteRPMRepo, *APIKey, *UserSubscription) {
	key, sub := smartRouteSubscriptionKey()
	key.Group.SubscriptionType = SubscriptionTypeStandard
	key.Group.RPMLimit = 10
	key.User.RPMLimit = 1
	zero := 0
	key.User.UserGroupRPMOverride = &zero
	key.User.UserGroupRPMOverrideLoaded = true
	now := time.Now()
	cache := &smartRouteEligibilityCache{
		balance: 10,
		sub:     &SubscriptionCacheData{Status: SubscriptionStatusActive, ExpiresAt: sub.ExpiresAt},
		quota: &UserPlatformQuotaCacheEntry{SchemaVersion: UserPlatformQuotaCacheSchemaV1,
			DailyWindowStart: &now, WeeklyWindowStart: &now, MonthlyWindowStart: &now},
		rate: &APIKeyRateLimitCacheData{Window5h: now.Unix(), Window1d: now.Unix(), Window7d: now.Unix()},
	}
	rpm := &userRPMCacheStub{userCounts: []int{999}}
	repo := &smartRouteRPMRepo{}
	svc := &BillingCacheService{cache: cache, cfg: &config.Config{}, userRPMCache: rpm,
		userGroupRateRepo: repo, userPlatformQuotaRepo: &fakeQuotaRepo{}}
	return svc, cache, rpm, repo, key, sub
}

func TestSmartBillingRouteEligibilityRechecksTargetRPMOnly(t *testing.T) {
	for _, name := range []string{"target-group", "target-override", "target-unlimited", "confirmed-nil-primary"} {
		t.Run(name, func(t *testing.T) {
			svc, cache, rpm, repo, key, _ := smartRouteEligibilityFixture()
			wantErr := ErrGroupRPMExceeded
			wantCount := int32(1)
			switch name {
			case "target-group", "confirmed-nil-primary":
				rpm.userGroupCounts = []int{11}
			case "target-override":
				v := 2
				repo.override = &v
				rpm.userGroupCounts = []int{3}
			case "target-unlimited":
				v := 0
				repo.override = &v
				wantErr = nil
				wantCount = 0
			}
			if name == "confirmed-nil-primary" {
				key.User.UserGroupRPMOverride = nil
			}
			before := *key.User
			err := svc.CheckAPIKeyRouteEligibility(context.Background(), key.User, key, key.Group, nil, PlatformGrok)
			if wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, wantErr)
			}
			require.Equal(t, wantCount, atomic.LoadInt32(&rpm.userGroupCalls))
			require.Zero(t, atomic.LoadInt32(&rpm.userCalls))
			require.Equal(t, int32(1), atomic.LoadInt32(&repo.calls))
			require.Equal(t, key.UserID, repo.userID)
			require.Equal(t, *key.GroupID, repo.groupID)
			require.Equal(t, PlatformGrok, cache.platform)
			require.Equal(t, before, *key.User)
		})
	}
}

func TestSmartBillingRouteEligibilityRejectsBeforeRPM(t *testing.T) {
	for _, name := range []string{"balance", "platform-daily", "platform-weekly", "platform-monthly", "key-quota", "key-expiry", "key-5h", "key-1d", "key-7d", "subscription-missing", "subscription-group", "subscription-user", "subscription-expired", "subscription-quota", "subscription-repo-missing"} {
		t.Run(name, func(t *testing.T) {
			svc, cache, rpm, _, key, validSub := smartRouteEligibilityFixture()
			var sub *UserSubscription
			var wantErr error
			one := 1.0
			switch name {
			case "balance":
				cache.balance = 0
				wantErr = ErrInsufficientBalance
			case "platform-daily":
				cache.quota.DailyLimitUSD = &one
				cache.quota.DailyUsageUSD = 1
				wantErr = ErrUserPlatformDailyQuotaExhausted
			case "platform-weekly":
				cache.quota.WeeklyLimitUSD = &one
				cache.quota.WeeklyUsageUSD = 1
				wantErr = ErrUserPlatformWeeklyQuotaExhausted
			case "platform-monthly":
				cache.quota.MonthlyLimitUSD = &one
				cache.quota.MonthlyUsageUSD = 1
				wantErr = ErrUserPlatformMonthlyQuotaExhausted
			case "key-quota":
				key.Quota, key.QuotaUsed = 1, 1
				wantErr = ErrAPIKeyQuotaExhausted
			case "key-expiry":
				expired := time.Now().Add(-time.Second)
				key.ExpiresAt = &expired
				wantErr = ErrAPIKeyExpired
			case "key-5h":
				key.RateLimit5h, cache.rate.Usage5h = 1, 1
				wantErr = ErrAPIKeyRateLimit5hExceeded
			case "key-1d":
				key.RateLimit1d, cache.rate.Usage1d = 1, 1
				wantErr = ErrAPIKeyRateLimit1dExceeded
			case "key-7d":
				key.RateLimit7d, cache.rate.Usage7d = 1, 1
				wantErr = ErrAPIKeyRateLimit7dExceeded
			default:
				key.Group.SubscriptionType = SubscriptionTypeSubscription
				sub = validSub
				wantErr = ErrSubscriptionInvalid
				switch name {
				case "subscription-missing":
					sub = nil
				case "subscription-group":
					sub.GroupID++
				case "subscription-user":
					sub.UserID++
				case "subscription-expired":
					cache.sub.ExpiresAt = time.Now().Add(-time.Second)
				case "subscription-quota":
					key.Group.DailyLimitUSD = &one
					cache.sub.DailyUsage = 1
					wantErr = ErrDailyLimitExceeded
				case "subscription-repo-missing":
					cache.sub = nil
					wantErr = ErrBillingServiceUnavailable
				}
			}
			require.ErrorIs(t, svc.CheckAPIKeyRouteEligibility(context.Background(), key.User, key, key.Group, sub, PlatformOpenAI), wantErr)
			require.Zero(t, atomic.LoadInt32(&rpm.userGroupCalls))
			require.Zero(t, atomic.LoadInt32(&rpm.userCalls))
		})
	}
}

func TestSmartBillingRouteEligibilitySubscriptionUsesTargetAndSkipsBalanceQuota(t *testing.T) {
	svc, cache, rpm, _, key, sub := smartRouteEligibilityFixture()
	key.Group.SubscriptionType = SubscriptionTypeSubscription
	cache.balance = 0
	key.RateLimit5h = 1
	require.NoError(t, svc.CheckAPIKeyRouteEligibility(context.Background(), key.User, key, key.Group, sub, PlatformOpenAI))
	require.Zero(t, cache.balanceCalls)
	require.Zero(t, cache.quotaCalls)
	require.Equal(t, 1, cache.subCalls)
	require.Equal(t, *key.GroupID, cache.groupID)
	require.Equal(t, 1, cache.rateCalls)
	require.Equal(t, int32(1), atomic.LoadInt32(&rpm.userGroupCalls))
	require.Zero(t, atomic.LoadInt32(&rpm.userCalls))
}
