package handler

import (
	"context"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type balanceAwareConcurrencyCache struct {
	helperConcurrencyCacheStub
	acquired     bool
	acquireCalls int
	releaseCalls int
}

func (c *balanceAwareConcurrencyCache) AcquireUserBalanceSlot(_ context.Context, _ int64, _ int64, _ string, _, _ float64, _ string) (bool, error) {
	c.acquireCalls++
	return c.acquired, nil
}

func (c *balanceAwareConcurrencyCache) ReleaseUserBalanceSlot(_ context.Context, _ int64, _ int64, _ string, _ string) error {
	c.releaseCalls++
	return nil
}

type fixedBalanceReader struct {
	balance float64
}

func (r fixedBalanceReader) GetUserBalance(context.Context, int64) (float64, error) {
	return r.balance, nil
}

func TestAcquireUserSlotWithWaitRejectsWhenBalanceBudgetIsFull(t *testing.T) {
	cache := &balanceAwareConcurrencyCache{
		helperConcurrencyCacheStub: helperConcurrencyCacheStub{userSeq: []bool{true}},
		acquired:                   false,
	}
	helper := NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, 0)
	helper.SetBalanceReader(fixedBalanceReader{balance: 0.05})
	c, _ := newHelperTestContext("POST", "/v1/messages")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID:    77,
		Group: &service.Group{ID: 10, Platform: service.PlatformOpenAI},
	})

	release, err := helper.AcquireUserSlotWithWait(c, 1, 1, false, new(bool))
	require.Nil(t, release)
	var concurrencyErr *ConcurrencyError
	require.ErrorAs(t, err, &concurrencyErr)
	require.Equal(t, "balance", concurrencyErr.SlotType)
	require.Equal(t, 1, cache.acquireCalls)
	require.Equal(t, 1, cache.userReleaseCalls)
}

func TestAcquireUserSlotWithWaitSkipsBalanceBudgetForSubscription(t *testing.T) {
	cache := &balanceAwareConcurrencyCache{
		helperConcurrencyCacheStub: helperConcurrencyCacheStub{userSeq: []bool{true}},
		acquired:                   false,
	}
	helper := NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, 0)
	helper.SetBalanceReader(fixedBalanceReader{balance: 0})
	c, _ := newHelperTestContext("POST", "/v1/messages")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID:    77,
		Group: &service.Group{ID: 10, Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeSubscription},
	})
	c.Set(string(middleware2.ContextKeySubscription), &service.UserSubscription{ID: 1, GroupID: 10})

	release, err := helper.AcquireUserSlotWithWait(c, 1, 1, false, new(bool))
	require.NoError(t, err)
	require.NotNil(t, release)
	release()
	require.Zero(t, cache.acquireCalls)
}

func TestAcquireUserSlotWithWaitSkipsSubscriptionGroupWithoutContext(t *testing.T) {
	cache := &balanceAwareConcurrencyCache{
		helperConcurrencyCacheStub: helperConcurrencyCacheStub{userSeq: []bool{true}},
		acquired:                   false,
	}
	helper := NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, 0)
	helper.SetBalanceReader(fixedBalanceReader{balance: 0})
	c, _ := newHelperTestContext("POST", "/v1/messages")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID:    77,
		Group: &service.Group{ID: 10, Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeSubscription},
	})

	release, err := helper.AcquireUserSlotWithWait(c, 1, 1, false, new(bool))
	require.NoError(t, err)
	require.NotNil(t, release)
	release()
	require.Zero(t, cache.acquireCalls)
}

var _ userBalanceReader = fixedBalanceReader{}
