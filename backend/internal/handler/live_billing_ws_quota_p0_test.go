//go:build unit

package handler

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// --- Shared handler test doubles for Live P0/P1 Billing & Quota Tests ---

type liveP0WSBillingCache struct {
	wsBillingAuditCache
}

func (c *liveP0WSBillingCache) DeductUserBalance(_ context.Context, _ int64, amount float64) error {
	c.mu.Lock()
	c.balance -= amount
	c.mu.Unlock()
	return nil
}

func (c *liveP0WSBillingCache) InvalidateUserBalance(context.Context, int64) error {
	return nil
}

func (c *liveP0WSBillingCache) GetUserPlatformQuotaCache(context.Context, int64, string) (*service.UserPlatformQuotaCacheEntry, bool, error) {
	return nil, false, nil
}

type liveP0APIKeyLookupStub struct {
	mu       sync.Mutex
	key      *service.APIKey
	err      error
	groups   map[int64]*service.Group
	groupErr error
}

func (s *liveP0APIKeyLookupStub) GetByKey(_ context.Context, _ string) (*service.APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	if s.key == nil {
		return nil, nil
	}
	cp := *s.key
	return &cp, nil
}

func (s *liveP0APIKeyLookupStub) GetGroupByIDLite(_ context.Context, id int64) (*service.Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.groupErr != nil {
		return nil, s.groupErr
	}
	if s.groups == nil {
		return nil, nil
	}
	g, ok := s.groups[id]
	if !ok || g == nil {
		return nil, service.ErrGroupNotFound
	}
	return g, nil
}

type liveP0APIKeyRepoStub struct {
	service.APIKeyRepository
	mu  sync.Mutex
	key *service.APIKey
	err error
}

func (r *liveP0APIKeyRepoStub) set(key *service.APIKey, err error) {
	r.mu.Lock()
	r.key = key
	r.err = err
	r.mu.Unlock()
}

func (r *liveP0APIKeyRepoStub) GetByKeyForAuth(_ context.Context, _ string) (*service.APIKey, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	if r.key == nil {
		return nil, service.ErrAPIKeyNotFound
	}
	cp := *r.key
	if r.key.User != nil {
		u := *r.key.User
		cp.User = &u
	}
	if r.key.Group != nil {
		g := *r.key.Group
		cp.Group = &g
	}
	return &cp, nil
}

func (r *liveP0APIKeyRepoStub) GetByKey(ctx context.Context, key string) (*service.APIKey, error) {
	return r.GetByKeyForAuth(ctx, key)
}

type liveP0HandlerInflightCache struct {
	service.BillingCache
	mu            sync.Mutex
	balance       float64
	balanceRes    map[string]float64
	quotaSeeded   map[int64]bool
	quotaUsed     map[int64]float64
	quotaRes      map[int64]map[string]float64
	rateLimitData map[int64]*service.APIKeyRateLimitCacheData
}

func newLiveP0HandlerInflightCache(balance float64) *liveP0HandlerInflightCache {
	return &liveP0HandlerInflightCache{
		balance:       balance,
		balanceRes:    map[string]float64{},
		quotaSeeded:   map[int64]bool{},
		quotaUsed:     map[int64]float64{},
		quotaRes:      map[int64]map[string]float64{},
		rateLimitData: map[int64]*service.APIKeyRateLimitCacheData{},
	}
}

func (c *liveP0HandlerInflightCache) GetUserBalance(context.Context, int64) (float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.balance, nil
}

func (c *liveP0HandlerInflightCache) GetUserPlatformQuotaCache(context.Context, int64, string) (*service.UserPlatformQuotaCacheEntry, bool, error) {
	return nil, false, nil
}

func (c *liveP0HandlerInflightCache) GetAPIKeyRateLimit(_ context.Context, keyID int64) (*service.APIKeyRateLimitCacheData, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d, ok := c.rateLimitData[keyID]; ok && d != nil {
		cp := *d
		return &cp, nil
	}
	return nil, errors.New("miss")
}

func (c *liveP0HandlerInflightCache) InvalidateAPIKeyRateLimit(context.Context, int64) error {
	return nil
}

func (c *liveP0HandlerInflightCache) ReserveInflightBalance(_ context.Context, _ int64, id string, amount, balance float64, _ time.Duration) (bool, float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var sum float64
	for _, v := range c.balanceRes {
		sum += v
	}
	if len(c.balanceRes) > 0 && balance-sum < amount-1e-9 {
		return false, sum, nil
	}
	c.balanceRes[id] = amount
	return true, sum, nil
}

func (c *liveP0HandlerInflightCache) ReleaseInflightBalance(_ context.Context, _ int64, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.balanceRes, id)
	return nil
}

func (c *liveP0HandlerInflightCache) SeedAPIKeyQuotaUsage(_ context.Context, keyID int64, used float64, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.quotaSeeded[keyID] {
		c.quotaUsed[keyID] = used
		c.quotaSeeded[keyID] = true
	}
	return nil
}

func (c *liveP0HandlerInflightCache) ReserveAPIKeyQuota(_ context.Context, keyID int64, id string, estimate, quota float64, _ time.Duration) (bool, bool, float64, float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.quotaSeeded[keyID] {
		return false, false, 0, 0, nil
	}
	used := c.quotaUsed[keyID]
	var inflight float64
	for _, v := range c.quotaRes[keyID] {
		inflight += v
	}
	if used+inflight+estimate > quota+1e-9 {
		return true, false, used, inflight, nil
	}
	if c.quotaRes[keyID] == nil {
		c.quotaRes[keyID] = map[string]float64{}
	}
	c.quotaRes[keyID][id] = estimate
	return true, true, used, inflight, nil
}

func (c *liveP0HandlerInflightCache) ReleaseAPIKeyQuota(_ context.Context, keyID int64, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.quotaRes[keyID], id)
	return nil
}

func (c *liveP0HandlerInflightCache) IncrementAPIKeyQuotaUsage(_ context.Context, keyID int64, cost float64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.quotaSeeded[keyID] {
		c.quotaUsed[keyID] += cost
	}
	return nil
}

func (c *liveP0HandlerInflightCache) InvalidateAPIKeyQuotaUsage(_ context.Context, keyID int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.quotaSeeded, keyID)
	delete(c.quotaUsed, keyID)
	return nil
}

func (c *liveP0HandlerInflightCache) RenewAPIKeyQuota(context.Context, int64, string, time.Duration) (bool, error) {
	return true, nil
}

func (c *liveP0HandlerInflightCache) totalQuotaInflight(keyID int64) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	var sum float64
	for _, v := range c.quotaRes[keyID] {
		sum += v
	}
	return sum
}

func (c *liveP0HandlerInflightCache) totalBalanceInflight() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	var sum float64
	for _, v := range c.balanceRes {
		sum += v
	}
	return sum
}

type routeBindingEstimatingGatewayStub struct {
	routeBindingGatewayStub
	estimates map[int64]float64
}

func (s *routeBindingEstimatingGatewayStub) EstimateInflightReservation(_ context.Context, apiKey *service.APIKey, _ service.InflightEstimateRequest) (float64, bool) {
	if apiKey == nil || apiKey.GroupID == nil {
		return 0, false
	}
	v, ok := s.estimates[*apiKey.GroupID]
	return v, ok
}

// --- Fix 1a: OpenAI Responses WS Multi-Turn API Key Gate ---

func TestOpenAIWSTurnAPIKeyRefresh_FailsClosedOnDeletedRevokedExpiredQuotaUserInactive(t *testing.T) {
	groupID := int64(9)
	past := time.Now().Add(-time.Minute)
	baseConn := func() *service.APIKey {
		return &service.APIKey{
			ID:      101,
			UserID:  42,
			Key:     "test-ws-key-1",
			Status:  service.StatusAPIKeyActive,
			GroupID: &groupID,
			Group:   &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, RateMultiplier: 1.0},
			User:    &service.User{ID: 42, Status: service.StatusActive, Balance: 10},
		}
	}

	cases := []struct {
		name       string
		connMut    func(*service.APIKey)
		lookup     *liveP0APIKeyLookupStub
		wantErr    error
		wantStatus coderws.StatusCode
		wantReason string
	}{
		{
			name:       "deleted_not_found",
			lookup:     &liveP0APIKeyLookupStub{err: service.ErrAPIKeyNotFound},
			wantErr:    service.ErrAPIKeyNotFound,
			wantStatus: coderws.StatusPolicyViolation,
			wantReason: "api key is no longer valid",
		},
		{
			name:       "db_error_fails_closed_with_try_again_later",
			lookup:     &liveP0APIKeyLookupStub{err: errors.New("db timeout")},
			wantStatus: coderws.StatusTryAgainLater,
			wantReason: "temporary api key verification failure, please reconnect",
		},
		{
			name:       "nil_key_fails_closed",
			lookup:     &liveP0APIKeyLookupStub{key: nil},
			wantErr:    service.ErrAPIKeyNotFound,
			wantStatus: coderws.StatusPolicyViolation,
			wantReason: "api key is no longer valid",
		},
		{
			name:       "id_mismatch_fails_closed",
			lookup:     &liveP0APIKeyLookupStub{key: &service.APIKey{ID: 999, Status: service.StatusAPIKeyActive}},
			wantErr:    service.ErrAPIKeyNotFound,
			wantStatus: coderws.StatusPolicyViolation,
			wantReason: "api key is no longer valid",
		},
		{
			name:       "disabled_key_fails_closed",
			lookup:     &liveP0APIKeyLookupStub{key: &service.APIKey{ID: 101, Status: service.StatusAPIKeyDisabled}},
			wantErr:    errOpenAIWSAPIKeyDisabled,
			wantStatus: coderws.StatusPolicyViolation,
			wantReason: "api key disabled",
		},
		{
			name:       "inactive_revoked_key_fails_closed",
			lookup:     &liveP0APIKeyLookupStub{key: &service.APIKey{ID: 101, Status: service.StatusAPIKeyInactive}},
			wantErr:    errOpenAIWSAPIKeyDisabled,
			wantStatus: coderws.StatusPolicyViolation,
			wantReason: "api key disabled",
		},
		{
			name:       "quota_exhausted_status_fails_closed",
			lookup:     &liveP0APIKeyLookupStub{key: &service.APIKey{ID: 101, Status: service.StatusAPIKeyQuotaExhausted}},
			wantErr:    service.ErrAPIKeyQuotaExhausted,
			wantStatus: coderws.StatusPolicyViolation,
			wantReason: "api key quota exhausted",
		},
		{
			name:       "quota_used_reaches_limit_fails_closed",
			lookup:     &liveP0APIKeyLookupStub{key: &service.APIKey{ID: 101, Status: service.StatusAPIKeyActive, Quota: 1.0, QuotaUsed: 1.0}},
			wantErr:    service.ErrAPIKeyQuotaExhausted,
			wantStatus: coderws.StatusPolicyViolation,
			wantReason: "api key quota exhausted",
		},
		{
			name:       "expired_timestamp_fails_closed",
			lookup:     &liveP0APIKeyLookupStub{key: &service.APIKey{ID: 101, Status: service.StatusAPIKeyActive, ExpiresAt: &past}},
			wantErr:    service.ErrAPIKeyExpired,
			wantStatus: coderws.StatusPolicyViolation,
			wantReason: "api key expired",
		},
		{
			name: "nil_user_fails_closed",
			lookup: &liveP0APIKeyLookupStub{key: &service.APIKey{
				ID:     101,
				Status: service.StatusAPIKeyActive,
				User:   nil,
			}},
			wantErr:    errOpenAIWSUserDisabled,
			wantStatus: coderws.StatusPolicyViolation,
			wantReason: "user is no longer active",
		},
		{
			name: "inactive_user_fails_closed",
			lookup: &liveP0APIKeyLookupStub{key: &service.APIKey{
				ID:     101,
				Status: service.StatusAPIKeyActive,
				User:   &service.User{ID: 42, Status: service.StatusDisabled},
			}},
			wantErr:    errOpenAIWSUserDisabled,
			wantStatus: coderws.StatusPolicyViolation,
			wantReason: "user is no longer active",
		},
		{
			name: "inactive_group_fails_closed",
			lookup: &liveP0APIKeyLookupStub{key: &service.APIKey{
				ID:      101,
				Status:  service.StatusAPIKeyActive,
				GroupID: &groupID,
				Group:   &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusDisabled},
				User:    &service.User{ID: 42, Status: service.StatusActive},
			}},
			wantErr:    errOpenAIWSGroupDisabled,
			wantStatus: coderws.StatusPolicyViolation,
			wantReason: "group is no longer active",
		},
		{
			name: "removed_group_authorization_fails_closed",
			lookup: &liveP0APIKeyLookupStub{key: &service.APIKey{
				ID:      101,
				Status:  service.StatusAPIKeyActive,
				GroupID: func() *int64 { other := int64(99); return &other }(),
				Group:   &service.Group{ID: 99, Platform: service.PlatformOpenAI, Status: service.StatusActive},
				User:    &service.User{ID: 42, Status: service.StatusActive},
			}},
			wantErr:    errOpenAIWSGroupUnauthorized,
			wantStatus: coderws.StatusPolicyViolation,
			wantReason: "group is no longer authorized",
		},
		{
			name:       "empty_conn_key_fails_closed",
			connMut:    func(k *service.APIKey) { k.Key = "" },
			lookup:     &liveP0APIKeyLookupStub{key: baseConn()},
			wantErr:    service.ErrAPIKeyNotFound,
			wantStatus: coderws.StatusPolicyViolation,
			wantReason: "api key is no longer valid",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := baseConn()
			if tc.connMut != nil {
				tc.connMut(conn)
			}
			var keys openAIWSTurnBillingAPIKeys
			_, gotKey, err := keys.begin(context.Background(), tc.lookup, 2, conn)
			require.Error(t, err)
			require.Nil(t, gotKey, "must NEVER fall back to stale connection snapshot")
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			}
			gotStatus, gotReason := openAIWSAPIKeyCloseStatusAndReason(err)
			require.Equal(t, tc.wantStatus, gotStatus)
			require.Equal(t, tc.wantReason, gotReason)
		})
	}
}

func TestOpenAIWSTurnAPIKeyRefresh_RefreshesQuotaRateLimitsUserAndMatchingGroup(t *testing.T) {
	groupID := int64(9)
	otherGroupID := int64(17)
	conn := &service.APIKey{
		ID:               101,
		UserID:           42,
		Key:              "test-ws-key-refresh",
		Status:           service.StatusAPIKeyActive,
		Quota:            5.0,
		ConcurrencyLimit: 5,
		GroupID:          &groupID,
		Group:            &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, RateMultiplier: 1.0},
		User:             &service.User{ID: 42, Status: service.StatusActive, Balance: 10},
	}
	latestGroup := &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, RateMultiplier: 2.5}
	latestUser := &service.User{ID: 42, Status: service.StatusActive, Balance: 4.25}
	lookup := &liveP0APIKeyLookupStub{
		key: &service.APIKey{
			ID:               101,
			UserID:           42,
			Status:           service.StatusAPIKeyActive,
			Quota:            5.0,
			QuotaUsed:        3.2,
			RateLimit5h:      4.0,
			Usage5h:          1.5,
			ConcurrencyLimit: 2,
			GroupID:          &groupID,
			Group:            latestGroup,
			User:             latestUser,
		},
	}

	var keys openAIWSTurnBillingAPIKeys
	ctx := context.WithValue(context.Background(), ctxkey.Group, conn.Group)
	turnCtx, refreshed, err := keys.begin(ctx, lookup, 2, conn)
	require.NoError(t, err)
	require.NotNil(t, refreshed)
	require.InDelta(t, 3.2, refreshed.QuotaUsed, 1e-9)
	require.InDelta(t, 4.0, refreshed.RateLimit5h, 1e-9)
	require.InDelta(t, 1.5, refreshed.Usage5h, 1e-9)
	require.Equal(t, 2, refreshed.ConcurrencyLimit)
	require.Same(t, latestUser, refreshed.User)
	require.Same(t, latestGroup, refreshed.Group)
	require.Same(t, latestGroup, turnCtx.Value(ctxkey.Group))

	// Authorized smart-routed secondary group is preserved while quota/user/concurrency refresh.
	lookup.key.GroupID = &otherGroupID
	lookup.key.Group = &service.Group{ID: otherGroupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, RateMultiplier: 9.0}
	lookup.key.RouteGroupIDs = []int64{otherGroupID, groupID}
	_, refreshedRouted, err := keys.begin(ctx, lookup, 3, conn)
	require.NoError(t, err)
	require.Same(t, conn.Group, refreshedRouted.Group, "authorized secondary route group must be preserved")
	require.InDelta(t, 3.2, refreshedRouted.QuotaUsed, 1e-9)
	require.Equal(t, 2, refreshedRouted.ConcurrencyLimit)

	// Removing bound group from live key's authorized groups MUST fail closed (no unauthorized group bypass),
	// even if a stale GroupID field still references the removed group while RouteGroupIDs is authoritative.
	lookup.key.GroupID = &groupID
	lookup.key.RouteGroupIDs = []int64{otherGroupID}
	_, rejectedStalePrimary, err := keys.begin(ctx, lookup, 4, conn)
	require.ErrorIs(t, err, errOpenAIWSGroupUnauthorized)
	require.Nil(t, rejectedStalePrimary)

	lookup.key.GroupID = &otherGroupID
	lookup.key.RouteGroupIDs = []int64{otherGroupID}
	_, rejectedMoved, err := keys.begin(ctx, lookup, 5, conn)
	require.ErrorIs(t, err, errOpenAIWSGroupUnauthorized)
	require.Nil(t, rejectedMoved)
}

func TestOpenAIWSTurnAPIKeyRefresh_ValidatesBoundSecondaryRouteGroupLiveState(t *testing.T) {
	boundGroupID := int64(9)
	primaryGroupID := int64(17)
	conn := &service.APIKey{
		ID:      101,
		UserID:  42,
		Key:     "test-ws-secondary-route-group",
		Status:  service.StatusAPIKeyActive,
		GroupID: &boundGroupID,
		Group: &service.Group{
			ID:               boundGroupID,
			Platform:         service.PlatformOpenAI,
			SubscriptionType: service.SubscriptionTypeStandard,
			Status:           service.StatusActive,
			RateMultiplier:   1.0,
		},
		User: &service.User{ID: 42, Status: service.StatusActive, Balance: 10},
	}
	baseLatest := func() *service.APIKey {
		return &service.APIKey{
			ID:            101,
			UserID:        42,
			Status:        service.StatusAPIKeyActive,
			GroupID:       &primaryGroupID,
			Group:         &service.Group{ID: primaryGroupID, Platform: service.PlatformOpenAI, Status: service.StatusActive},
			RouteGroupIDs: []int64{primaryGroupID, boundGroupID},
			User:          &service.User{ID: 42, Status: service.StatusActive, Balance: 10},
		}
	}

	t.Run("active secondary route group refreshes group metadata and rate multiplier", func(t *testing.T) {
		freshSecondary := &service.Group{
			ID:               boundGroupID,
			Platform:         service.PlatformOpenAI,
			SubscriptionType: service.SubscriptionTypeStandard,
			Status:           service.StatusActive,
			RateMultiplier:   3.25,
		}
		lookup := &liveP0APIKeyLookupStub{
			key:    baseLatest(),
			groups: map[int64]*service.Group{boundGroupID: freshSecondary},
		}
		var keys openAIWSTurnBillingAPIKeys
		ctx := context.WithValue(context.Background(), ctxkey.Group, conn.Group)
		turnCtx, refreshed, err := keys.begin(ctx, lookup, 2, conn)
		require.NoError(t, err)
		require.Same(t, freshSecondary, refreshed.Group)
		require.Same(t, freshSecondary, turnCtx.Value(ctxkey.Group))
	})

	t.Run("disabled secondary route group fails closed", func(t *testing.T) {
		lookup := &liveP0APIKeyLookupStub{
			key: baseLatest(),
			groups: map[int64]*service.Group{
				boundGroupID: {
					ID:               boundGroupID,
					Platform:         service.PlatformOpenAI,
					SubscriptionType: service.SubscriptionTypeStandard,
					Status:           service.StatusDisabled,
				},
			},
		}
		var keys openAIWSTurnBillingAPIKeys
		_, got, err := keys.begin(context.Background(), lookup, 2, conn)
		require.ErrorIs(t, err, errOpenAIWSGroupDisabled)
		require.Nil(t, got)
	})

	t.Run("deleted secondary route group fails closed", func(t *testing.T) {
		lookup := &liveP0APIKeyLookupStub{
			key:    baseLatest(),
			groups: map[int64]*service.Group{},
		}
		var keys openAIWSTurnBillingAPIKeys
		_, got, err := keys.begin(context.Background(), lookup, 2, conn)
		require.ErrorIs(t, err, errOpenAIWSGroupDisabled)
		require.Nil(t, got)
	})

	t.Run("secondary route group platform or subscription change fails closed", func(t *testing.T) {
		lookup := &liveP0APIKeyLookupStub{
			key: baseLatest(),
			groups: map[int64]*service.Group{
				boundGroupID: {
					ID:               boundGroupID,
					Platform:         service.PlatformAnthropic,
					SubscriptionType: service.SubscriptionTypeStandard,
					Status:           service.StatusActive,
				},
			},
		}
		var keys openAIWSTurnBillingAPIKeys
		_, got, err := keys.begin(context.Background(), lookup, 2, conn)
		require.ErrorIs(t, err, errOpenAIWSGroupUnauthorized)
		require.Nil(t, got)
	})

	t.Run("transient DB error loading secondary route group fails closed with TryAgainLater", func(t *testing.T) {
		lookup := &liveP0APIKeyLookupStub{
			key:      baseLatest(),
			groupErr: errors.New("pg connection reset"),
		}
		var keys openAIWSTurnBillingAPIKeys
		_, got, err := keys.begin(context.Background(), lookup, 2, conn)
		require.Error(t, err)
		require.Nil(t, got)
		status, _ := openAIWSAPIKeyCloseStatusAndReason(err)
		require.Equal(t, coderws.StatusTryAgainLater, status)
	})
}

func TestOpenAIWSResponses_MultiTurnFailsClosedWhenKeyRevokedOrQuotaExhausted(t *testing.T) {
	gin.SetMode(gin.TestMode)

	past := time.Now().Add(-time.Minute)
	scenarios := []struct {
		name       string
		mutateTurn func(repo *liveP0APIKeyRepoStub, base *service.APIKey)
		wantReason string
	}{
		{
			name: "key_deleted_before_turn_2",
			mutateTurn: func(repo *liveP0APIKeyRepoStub, _ *service.APIKey) {
				repo.set(nil, service.ErrAPIKeyNotFound)
			},
			wantReason: "api key is no longer valid",
		},
		{
			name: "key_disabled_before_turn_2",
			mutateTurn: func(repo *liveP0APIKeyRepoStub, base *service.APIKey) {
				cp := *base
				cp.Status = service.StatusAPIKeyDisabled
				repo.set(&cp, nil)
			},
			wantReason: "api key disabled",
		},
		{
			name: "key_expired_before_turn_2",
			mutateTurn: func(repo *liveP0APIKeyRepoStub, base *service.APIKey) {
				cp := *base
				cp.ExpiresAt = &past
				repo.set(&cp, nil)
			},
			wantReason: "api key expired",
		},
		{
			name: "key_quota_exhausted_before_turn_2",
			mutateTurn: func(repo *liveP0APIKeyRepoStub, base *service.APIKey) {
				cp := *base
				cp.Quota = 1.0
				cp.QuotaUsed = 1.0
				repo.set(&cp, nil)
			},
			wantReason: "api key quota exhausted",
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			var upstreamTurns atomic.Int32
			upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer func() { _ = conn.CloseNow() }()
				for {
					_, _, err := conn.Read(r.Context())
					if err != nil {
						return
					}
					n := upstreamTurns.Add(1)
					resp := `{"type":"response.completed","response":{"id":"resp_turn_` + string(rune('0'+n)) + `","status":"completed","model":"gpt-5.1","usage":{"input_tokens":10,"output_tokens":5}}}`
					if err := conn.Write(r.Context(), coderws.MessageText, []byte(resp)); err != nil {
						return
					}
				}
			}))
			defer upstreamServer.Close()

			const userID = int64(5101)
			groupID := int64(6101)
			group := &service.Group{
				ID: groupID, Name: "ws-key-gate", Platform: service.PlatformOpenAI, Status: service.StatusActive,
				Hydrated: true, RateMultiplier: 1, SubscriptionType: service.SubscriptionTypeStandard,
			}
			user := &service.User{ID: userID, Status: service.StatusActive, Balance: 50}
			apiKey := &service.APIKey{
				ID: 7101, UserID: userID, Key: "test-key-must-not-leak",
				Status: service.StatusAPIKeyActive, GroupID: &groupID, Group: group, User: user,
			}

			keyRepo := &liveP0APIKeyRepoStub{key: apiKey}
			cfg := &config.Config{RunMode: config.RunModeStandard}
			cfg.Default.RateMultiplier = 1
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			cfg.Gateway.OpenAIWS.Enabled = true
			cfg.Gateway.OpenAIWS.APIKeyEnabled = true
			cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
			cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
			cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
			cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

			apiKeySvc := service.NewAPIKeyService(keyRepo, nil, nil, nil, nil, nil, cfg)
			cache := &liveP0WSBillingCache{wsBillingAuditCache: wsBillingAuditCache{balance: 50}}
			balAfter := 49.99
			ledger := &wsBillingAuditLedger{cache: &cache.wsBillingAuditCache, balanceAfter: &balAfter, firstSettled: make(chan struct{})}
			billingCache := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
			defer billingCache.Stop()

			accountRepo := &openAIWSUsageHandlerAccountRepoStub{account: service.Account{
				ID: 9910, Name: "ws-key-gate-test", Platform: service.PlatformOpenAI,
				Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{
					"api_key": "test-upstream-key", "base_url": upstreamServer.URL,
					"model_mapping": map[string]any{"gpt-5.1": "gpt-5.1"},
				},
				Extra: map[string]any{
					"openai_apikey_responses_websockets_v2_enabled": true,
					"openai_apikey_responses_websockets_v2_mode":    service.OpenAIWSIngressModePassthrough,
				},
			}}
			usageRepo := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 2)}
			gateway := service.NewOpenAIGatewayService(
				accountRepo, usageRepo, ledger, nil, nil, nil, nil, cfg, nil, nil,
				service.NewBillingService(cfg, nil), nil, billingCache, nil, &service.DeferredService{},
				nil, nil, nil, nil, nil, nil, nil,
			)
			concurrency := &concurrencyCacheMock{
				acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
				acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
			}
			h := &OpenAIGatewayHandler{
				gatewayService: gateway, billingCacheService: billingCache, apiKeyService: apiKeySvc,
				concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(concurrency), SSEPingFormatNone, time.Second),
			}

			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
				c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: userID, Concurrency: 2})
				c.Next()
			})
			router.GET("/openai/v1/responses", h.ResponsesWebSocket)
			srv := httptest.NewServer(router)
			defer srv.Close()

			dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
			client, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/openai/v1/responses", nil)
			cancelDial()
			require.NoError(t, err)
			defer func() { _ = client.CloseNow() }()

			writeTurn := func(payload string) {
				wCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				require.NoError(t, client.Write(wCtx, coderws.MessageText, []byte(payload)))
			}
			readTurn := func() error {
				rCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_, _, rErr := client.Read(rCtx)
				return rErr
			}

			// Turn 1 succeeds and bills.
			writeTurn(`{"type":"response.create","model":"gpt-5.1","input":"turn1"}`)
			require.NoError(t, readTurn())
			select {
			case <-ledger.firstSettled:
			case <-time.After(3 * time.Second):
				t.Fatal("turn 1 billing did not settle")
			}

			// Mutate live API key state before Turn 2.
			sc.mutateTurn(keyRepo, apiKey)

			// Turn 2 must fail closed with StatusPolicyViolation and NEVER hit upstream.
			writeTurn(`{"type":"response.create","model":"gpt-5.1","input":"turn2"}`)
			secondErr := readTurn()
			require.Error(t, secondErr)
			require.Equal(t, coderws.StatusPolicyViolation, coderws.CloseStatus(secondErr))
			require.Contains(t, strings.ToLower(secondErr.Error()), strings.ToLower(sc.wantReason))
			require.Equal(t, int32(1), upstreamTurns.Load(), "revoked/exhausted turn 2 must never be forwarded upstream")
		})
	}
}

func TestOpenAIWSTurnInflight_PreventsConcurrentQuotaOvershootAndReleasesCleanly(t *testing.T) {
	cache := newLiveP0HandlerInflightCache(100)
	cfg := &config.Config{RunMode: config.RunModeStandard}
	billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	defer billing.Stop()

	est := &countingEstimator{cost: 0.60, priced: true}
	apiKey := &service.APIKey{
		ID: 88, UserID: 12, Status: service.StatusAPIKeyActive, Quota: 1.00, QuotaUsed: 0,
		User: &service.User{ID: 12, Status: service.StatusActive, Balance: 100},
	}

	// Turn A reserves 0.60 against $1.00 quota.
	ctxA, doneA, err := reserveInflightBalanceCtx(context.Background(), billing, est, apiKey, nil, tokenInflightEstimate(nil, "gpt-5.1", []byte(`{}`)))
	require.NoError(t, err)
	require.InDelta(t, 0.60, cache.totalQuotaInflight(88), 1e-9)

	tracker := newOpenAIWSTurnInflight(nil, nil)
	tracker.set(2, ctxA, doneA)

	// Concurrent Turn B needs 0.60 -> 0.60 + 0.60 > 1.00 -> rejected!
	_, _, err = reserveInflightBalanceCtx(context.Background(), billing, est, apiKey, nil, tokenInflightEstimate(nil, "gpt-5.1", []byte(`{}`)))
	require.ErrorIs(t, err, service.ErrAPIKeyQuotaExhausted)

	// Releasing Turn A frees the inflight reservation cleanly so Turn B can proceed.
	tracker.release(2)
	require.Zero(t, cache.totalQuotaInflight(88))

	_, doneB, err := reserveInflightBalanceCtx(context.Background(), billing, est, apiKey, nil, tokenInflightEstimate(nil, "gpt-5.1", []byte(`{}`)))
	require.NoError(t, err)
	doneB()
	require.Zero(t, cache.totalQuotaInflight(88))
}

// --- Fix 1b: Smart-Route Quota & Balance Inflight Delta ---

func TestKeyRouteBinding_AdjustsQuotaAndBalanceInflightOnHigherMultiplierAndSubToBalanceSwitch(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("higher_multiplier_exceeds_quota_rejects_without_publishing_selected", func(t *testing.T) {
		c, key, selected := routeBindingFixture()
		key.Quota, selected.Quota = 0.25, 0.25
		key.User.Balance, selected.User.Balance = 10, 10

		cache := newLiveP0HandlerInflightCache(10)
		cfg := &config.Config{RunMode: config.RunModeStandard}
		cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
		billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
		defer billing.Stop()

		gw := &routeBindingEstimatingGatewayStub{estimates: map[int64]float64{9: 0.10, 17: 0.30}}
		done, err := reserveInflightBalance(c, billing, gw, key, nil, tokenInflightEstimate(c, "gpt-5.1", []byte(`{}`)))
		require.NoError(t, err)
		defer done()
		require.InDelta(t, 0.10, cache.totalQuotaInflight(key.ID), 1e-9)

		err = bindSelectedKeyRoute(c, gw, billing, nil, keyRouteBinding{Previous: key, Selected: selected, Model: "gpt-5.1"})
		require.ErrorIs(t, err, service.ErrAPIKeyQuotaExhausted)
		bound, _ := middleware2.GetAPIKeyFromContext(c)
		require.Same(t, key, bound, "rejected route switch must not publish selected key")
		require.InDelta(t, 0.10, cache.totalQuotaInflight(key.ID), 1e-9)
	})

	t.Run("higher_multiplier_reserves_only_delta_and_releases_both_with_usage_task", func(t *testing.T) {
		c, key, selected := routeBindingFixture()
		key.Quota, selected.Quota = 0.50, 0.50
		key.User.Balance, selected.User.Balance = 1.00, 1.00

		cache := newLiveP0HandlerInflightCache(1.00)
		cfg := &config.Config{RunMode: config.RunModeStandard}
		cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
		billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
		defer billing.Stop()

		gw := &routeBindingEstimatingGatewayStub{estimates: map[int64]float64{9: 0.10, 17: 0.30}}
		done, err := reserveInflightBalance(c, billing, gw, key, nil, tokenInflightEstimate(c, "gpt-5.1", []byte(`{}`)))
		require.NoError(t, err)

		require.NoError(t, bindSelectedKeyRoute(c, gw, billing, nil, keyRouteBinding{Previous: key, Selected: selected, Model: "gpt-5.1"}))
		require.InDelta(t, 0.30, cache.totalQuotaInflight(key.ID), 1e-9, "primary 0.10 + delta 0.20 = 0.30")
		require.InDelta(t, 0.30, cache.totalBalanceInflight(), 1e-9, "primary 0.10 + delta 0.20 = 0.30")

		// Handoff to async billing task keeps both primary & delta alive after handler exits.
		task, _ := wrapUsageRecordTaskContext(c.Request.Context(), func(context.Context) {})
		done()
		require.InDelta(t, 0.30, cache.totalQuotaInflight(key.ID), 1e-9)
		require.InDelta(t, 0.30, cache.totalBalanceInflight(), 1e-9)

		task(context.Background())
		require.Zero(t, cache.totalQuotaInflight(key.ID))
		require.Zero(t, cache.totalBalanceInflight())
	})

	t.Run("subscription_to_balance_group_creates_balance_reservation_and_releases_cleanly", func(t *testing.T) {
		c, key, selected := routeBindingFixture()
		key.Group.SubscriptionType = service.SubscriptionTypeSubscription
		selected.Group.SubscriptionType = service.SubscriptionTypeStandard
		sub := &service.UserSubscription{ID: 1, UserID: 42, GroupID: 9, Status: service.SubscriptionStatusActive}

		cache := newLiveP0HandlerInflightCache(1.00)
		cfg := &config.Config{RunMode: config.RunModeStandard}
		cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
		billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
		defer billing.Stop()

		gw := &routeBindingEstimatingGatewayStub{estimates: map[int64]float64{9: 0.10, 17: 0.30}}
		done, err := reserveInflightBalance(c, billing, gw, key, sub, tokenInflightEstimate(c, "gpt-5.1", []byte(`{}`)))
		require.NoError(t, err)
		require.Zero(t, cache.totalBalanceInflight(), "initial subscription group reserves zero balance")

		require.NoError(t, bindSelectedKeyRoute(c, gw, billing, nil, keyRouteBinding{
			Previous: key, Selected: selected, Subscription: &sub, Model: "gpt-5.1",
		}))
		require.InDelta(t, 0.30, cache.totalBalanceInflight(), 1e-9, "switching to balance group must reserve 0.30")
		done()
		require.Zero(t, cache.totalBalanceInflight())
	})

	t.Run("preauthorizer_failure_rolls_back_delta_reservation", func(t *testing.T) {
		c, key, selected := routeBindingFixture()
		key.Quota, selected.Quota = 1.00, 1.00
		cache := newLiveP0HandlerInflightCache(10)
		cfg := &config.Config{RunMode: config.RunModeStandard}
		cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
		billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
		defer billing.Stop()

		gw := &routeBindingEstimatingGatewayStub{estimates: map[int64]float64{9: 0.10, 17: 0.30}}
		done, err := reserveInflightBalance(c, billing, gw, key, nil, tokenInflightEstimate(c, "gpt-5.1", []byte(`{}`)))
		require.NoError(t, err)
		defer done()

		var guard *service.BalancePreauthorizationGuard
		preauth := &preauthorizerStub{requires: true, err: errors.New("preauth hold rejected")}
		err = bindSelectedKeyRoute(c, gw, billing, preauth, keyRouteBinding{
			Previous: key, Selected: selected, Guard: &guard, Body: []byte(`{"model":"gpt-5.1"}`), Model: "gpt-5.1",
		})
		require.Error(t, err)
		require.InDelta(t, 0.10, cache.totalQuotaInflight(key.ID), 1e-9, "delta must roll back on preauthorizer failure")
		require.InDelta(t, 0.10, cache.totalBalanceInflight(), 1e-9)
	})
}

// --- Fix 2 (Handler): Grok Realtime Billing Bypass + Concurrency Slots (GrokRealtime, GrokVoice, WebSearch) ---

func TestGrokRealtime_BillsTextOnlyAbnormalCloseAndMalformedFrame(t *testing.T) {
	gin.SetMode(gin.TestMode)

	scenarios := []struct {
		name          string
		upstreamEvent string
		clientAction  func(t *testing.T, client *coderws.Conn)
		wantBilled    bool
	}{
		{
			name:          "text_only_modalities_and_policy_violation_close_1008_still_billed",
			upstreamEvent: `{"type":"response.text.delta","delta":"text-only reply"}`,
			clientAction: func(t *testing.T, client *coderws.Conn) {
				time.Sleep(15 * time.Millisecond)
				_ = client.Close(coderws.StatusPolicyViolation, "client 1008")
			},
			wantBilled: true,
		},
		{
			name:          "transcript_and_custom_close_4000_still_billed",
			upstreamEvent: `{"type":"response.audio_transcript.delta","delta":"spoken words"}`,
			clientAction: func(t *testing.T, client *coderws.Conn) {
				time.Sleep(15 * time.Millisecond)
				_ = client.Close(coderws.StatusCode(4000), "custom close")
			},
			wantBilled: true,
		},
		{
			name:          "malformed_non_json_client_frame_after_output_still_billed",
			upstreamEvent: `{"type":"response.done","response":{"output":[{"id":"o1"}]}}`,
			clientAction: func(t *testing.T, client *coderws.Conn) {
				time.Sleep(15 * time.Millisecond)
				_ = client.Write(context.Background(), coderws.MessageText, []byte("not-valid-json"))
			},
			wantBilled: true,
		},
		{
			name:          "abrupt_client_disconnect_after_audio_still_billed",
			upstreamEvent: `{"type":"response.audio.delta","delta":"AAAA"}`,
			clientAction: func(t *testing.T, client *coderws.Conn) {
				time.Sleep(15 * time.Millisecond)
				_ = client.CloseNow()
			},
			wantBilled: true,
		},
		{
			name:          "no_billable_output_before_close_not_billed",
			upstreamEvent: `{"type":"session.created"}`,
			clientAction: func(t *testing.T, client *coderws.Conn) {
				time.Sleep(15 * time.Millisecond)
				_ = client.Close(coderws.StatusNormalClosure, "bye")
			},
			wantBilled: false,
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer func() { _ = conn.CloseNow() }()
				_ = conn.Write(r.Context(), coderws.MessageText, []byte(sc.upstreamEvent))
				_, _, _ = conn.Read(r.Context())
			}))
			defer upstreamServer.Close()

			const userID = int64(5201)
			groupID := int64(6201)
			rtPrice := 0.05
			group := &service.Group{
				ID: groupID, Name: "grok-rt-test", Platform: service.PlatformGrok, Status: service.StatusActive,
				Hydrated: true, RateMultiplier: 1, SubscriptionType: service.SubscriptionTypeStandard,
				AudioRealtimePricePerMin: &rtPrice,
			}
			apiKey := &service.APIKey{
				ID: 7201, UserID: userID, Status: service.StatusAPIKeyActive,
				GroupID: &groupID, Group: group,
				User: &service.User{ID: userID, Status: service.StatusActive, Balance: 50},
			}
			accountRepo := openAIImagesFailoverAccountRepo{accounts: []service.Account{{
				ID: 9920, Name: "grok-rt-test", Platform: service.PlatformGrok,
				Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
				GroupIDs: []int64{groupID}, Concurrency: 2,
				Credentials: map[string]any{"api_key": "test-grok-key", "base_url": upstreamServer.URL},
			}}}

			cfg := &config.Config{RunMode: config.RunModeStandard}
			cfg.Default.RateMultiplier = 1
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true

			cache := &liveP0WSBillingCache{wsBillingAuditCache: wsBillingAuditCache{balance: 50}}
			billingCache := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
			defer billingCache.Stop()
			usageRepo := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 2)}
			balAfter := 49.99
			ledger := &wsBillingAuditLedger{cache: &cache.wsBillingAuditCache, balanceAfter: &balAfter, firstSettled: make(chan struct{})}
			gateway := service.NewOpenAIGatewayService(
				accountRepo, usageRepo, ledger, nil, nil, nil, nil, cfg, nil, nil,
				service.NewBillingService(cfg, nil), nil, billingCache, nil, &service.DeferredService{},
				nil, nil, nil, nil, nil, nil, nil,
			)
			concurrency := &concurrencyCacheMock{
				acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
				acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
			}
			pool := service.NewUsageRecordWorkerPool(cfg)
			defer pool.Stop()

			h := &OpenAIGatewayHandler{
				gatewayService:        gateway,
				billingCacheService:   billingCache,
				apiKeyService:         &service.APIKeyService{},
				usageRecordWorkerPool: pool,
				concurrencyHelper:     NewConcurrencyHelper(service.NewConcurrencyService(concurrency), SSEPingFormatNone, time.Second),
			}

			handlerDone := make(chan struct{})
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
				c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: userID, Concurrency: 2})
				c.Next()
			})
			router.GET("/v1/realtime", func(c *gin.Context) {
				defer close(handlerDone)
				h.GrokRealtime(c)
			})
			srv := httptest.NewServer(router)
			defer srv.Close()

			dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
			client, dialResp, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/realtime?model=grok-voice-latest", nil)
			cancelDial()
			if err != nil && dialResp != nil && dialResp.Body != nil {
				var buf bytes.Buffer
				_, _ = buf.ReadFrom(dialResp.Body)
				t.Fatalf("dial failed: %v (status=%d body=%s)", err, dialResp.StatusCode, buf.String())
			}
			require.NoError(t, err)
			defer func() { _ = client.CloseNow() }()

			// Wait for the upstream event to arrive before triggering client close / malformed frame.
			readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
			_, _, err = client.Read(readCtx)
			cancelRead()
			require.NoError(t, err)

			sc.clientAction(t, client)

			if sc.wantBilled {
				select {
				case log := <-usageRepo.created:
					require.NotNil(t, log)
					require.Equal(t, userID, log.UserID)
					require.Greater(t, log.ActualCost, 0.0, "realtime usage must record non-zero actual cost")
				case <-time.After(3 * time.Second):
					t.Fatal("expected GrokRealtime usage log to be recorded")
				}
			} else {
				select {
				case <-handlerDone:
				case <-time.After(3 * time.Second):
					t.Fatal("expected GrokRealtime handler to finish after client close")
				}
				pool.Stop()
				select {
				case log := <-usageRepo.created:
					t.Fatalf("unexpected usage log for non-billable session: %+v", log)
				default:
				}
			}
		})
	}
}

type liveP0FullConcurrencyCache struct {
	concurrencyCacheMock
}

func (m *liveP0FullConcurrencyCache) IncrementWaitCount(context.Context, int64, int) (bool, error) {
	return false, nil
}

func TestGrokRealtimeGrokVoiceAndWebSearch_EnforceUserAndAPIKeyConcurrencySlots(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const userID = int64(5301)
	groupID := int64(6301)
	grokGroup := &service.Group{ID: groupID, Platform: service.PlatformGrok, Status: service.StatusActive}
	apiKey := &service.APIKey{
		ID: 7301, UserID: userID, Status: service.StatusAPIKeyActive,
		GroupID: &groupID, Group: grokGroup,
		User: &service.User{ID: userID, Status: service.StatusActive, Balance: 100, Concurrency: 1},
	}

	// Simulate user concurrency slot + wait queue completely saturated.
	fullConcurrency := &liveP0FullConcurrencyCache{
		concurrencyCacheMock: concurrencyCacheMock{
			acquireUserSlotFn: func(context.Context, int64, int, string) (bool, error) { return false, nil },
		},
	}
	helper := NewConcurrencyHelper(service.NewConcurrencyService(fullConcurrency), SSEPingFormatNone, time.Second)
	cfg := &config.Config{RunMode: config.RunModeStandard}
	billingCache := service.NewBillingCacheService(newLiveP0HandlerInflightCache(100), nil, nil, nil, nil, nil, cfg, nil)
	defer billingCache.Stop()

	openAIH := &OpenAIGatewayHandler{
		gatewayService:      &service.OpenAIGatewayService{},
		billingCacheService: billingCache,
		apiKeyService:       &service.APIKeyService{},
		concurrencyHelper:   helper,
	}

	t.Run("GrokRealtime_rejects_when_user_slot_full", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		req := httptest.NewRequest(http.MethodGet, "/v1/realtime", nil)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		c.Request = req
		c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: userID, Concurrency: 1})

		openAIH.GrokRealtime(c)
		require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
	})

	t.Run("GrokVoice_rejects_when_user_slot_full", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", bytes.NewBufferString(`{"text":"hello"}`))
		c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: userID, Concurrency: 1})

		openAIH.GrokVoice(c, "tts")
		require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
	})

	gatewayH := &GatewayHandler{
		gatewayService:      &service.GatewayService{},
		billingCacheService: billingCache,
		concurrencyHelper:   helper,
	}

	for _, isXSearch := range []bool{false, true} {
		label := "web_search"
		if isXSearch {
			label = "x_search"
		}
		t.Run("WebSearch_"+label+"_rejects_when_user_slot_full", func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+label, bytes.NewBufferString(`{"query":"golang"}`))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set("grok_x_search_endpoint", isXSearch)
			c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: userID, Concurrency: 1})

			gatewayH.WebSearch(c)
			require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
		})
	}
}

// --- Fix 4 (Handler): BatchImageHandler.Submit Concurrency, Quota & Rate-Limit Gates ---

func TestBatchImageHandlerSubmit_EnforcesConcurrencyQuotaAndRateLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const userID = int64(5401)
	groupID := int64(6401)
	group := &service.Group{ID: groupID, Platform: service.PlatformGemini, Status: service.StatusActive}

	newSubmitContext := func(key *service.APIKey) (*gin.Context, *httptest.ResponseRecorder) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/batches", bytes.NewBufferString(`{"model":"gemini-2.5-flash-image","items":[{"custom_id":"1","prompt":"hi"}]}`))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set(string(middleware2.ContextKeyAPIKey), key)
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: userID, Concurrency: 1})
		return c, rec
	}

	t.Run("rejects_when_user_concurrency_slot_full", func(t *testing.T) {
		fullConcurrency := &liveP0FullConcurrencyCache{
			concurrencyCacheMock: concurrencyCacheMock{
				acquireUserSlotFn: func(context.Context, int64, int, string) (bool, error) { return false, nil },
			},
		}
		h := &BatchImageHandler{
			openAI: &OpenAIGatewayHandler{
				concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(fullConcurrency), SSEPingFormatNone, time.Second),
			},
		}
		key := &service.APIKey{ID: 7401, UserID: userID, Status: service.StatusAPIKeyActive, GroupID: &groupID, Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Balance: 100}}
		c, rec := newSubmitContext(key)
		h.Submit(c)
		require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
	})

	t.Run("rejects_expired_api_key", func(t *testing.T) {
		past := time.Now().Add(-time.Hour)
		h := &BatchImageHandler{openAI: &OpenAIGatewayHandler{}}
		key := &service.APIKey{ID: 7402, UserID: userID, Status: service.StatusAPIKeyActive, ExpiresAt: &past, GroupID: &groupID, Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Balance: 100}}
		c, rec := newSubmitContext(key)
		h.Submit(c)
		require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	})

	t.Run("rejects_quota_exhausted_api_key", func(t *testing.T) {
		h := &BatchImageHandler{openAI: &OpenAIGatewayHandler{}}
		key := &service.APIKey{ID: 7403, UserID: userID, Status: service.StatusAPIKeyActive, Quota: 1.0, QuotaUsed: 1.0, GroupID: &groupID, Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Balance: 100}}
		c, rec := newSubmitContext(key)
		h.Submit(c)
		require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
	})

	t.Run("rejects_rate_limit_5h_exceeded_via_billing_eligibility", func(t *testing.T) {
		now := time.Now()
		cache := newLiveP0HandlerInflightCache(100)
		cache.rateLimitData[7404] = &service.APIKeyRateLimitCacheData{
			Usage5h:  2.0,
			Window5h: now.Unix(),
		}
		cfg := &config.Config{RunMode: config.RunModeStandard}
		billingCache := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
		defer billingCache.Stop()

		h := &BatchImageHandler{openAI: &OpenAIGatewayHandler{billingCacheService: billingCache}}
		key := &service.APIKey{
			ID: 7404, UserID: userID, Status: service.StatusAPIKeyActive,
			RateLimit5h: 1.0, Usage5h: 2.0, Window5hStart: &now,
			GroupID: &groupID, Group: group,
			User: &service.User{ID: userID, Status: service.StatusActive, Balance: 100},
		}
		c, rec := newSubmitContext(key)
		h.Submit(c)
		require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
	})

	t.Run("fails_closed_without_panic_when_user_is_nil", func(t *testing.T) {
		cache := newLiveP0HandlerInflightCache(100)
		cfg := &config.Config{RunMode: config.RunModeStandard}
		billingCache := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
		defer billingCache.Stop()

		h := &BatchImageHandler{openAI: &OpenAIGatewayHandler{billingCacheService: billingCache}}
		key := &service.APIKey{
			ID: 7405, UserID: userID, Status: service.StatusAPIKeyActive,
			GroupID: &groupID, Group: group, User: nil,
		}
		c, rec := newSubmitContext(key)
		require.NotPanics(t, func() { h.Submit(c) })
		require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), "USER_CONTEXT_REQUIRED")
	})

	t.Run("idempotent_retry_returns_existing_batch_even_when_balance_is_now_zero", func(t *testing.T) {
		cache := newLiveP0HandlerInflightCache(0) // balance depleted by original batch!
		cfg := &config.Config{RunMode: config.RunModeStandard}
		cfg.BatchImage.Enabled = true
		billingCache := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
		defer billingCache.Stop()

		reqHash := service.HashBatchImageSubmitRequest(service.BatchImageSubmitRequest{
			Model:            "gemini-2.5-flash-image",
			TaskName:         "retry-task",
			ResponseMimeType: "image/png",
			ImageSize:        "1K",
			Items:            []service.BatchImageSubmitItem{{CustomID: "1", Prompt: "hi"}},
		})
		idemKey := "idem-retry-1"
		apiKeyID := int64(7406)
		existing := &service.BatchImageJob{
			BatchID:        "imgbatch_existing_retry",
			UserID:         userID,
			APIKeyID:       &apiKeyID,
			Status:         service.BatchImageJobStatusRunning,
			Provider:       service.BatchImageProviderVertex,
			Model:          "gemini-2.5-flash-image",
			TaskName:       "retry-task",
			ItemCount:      1,
			EstimatedCost:  0.15,
			IdempotencyKey: &idemKey,
			RequestHash:    &reqHash,
			CreatedAt:      time.Now(),
		}
		batchRepo := &liveP0HandlerBatchImageRepoStub{existing: existing}
		groupRepo := &liveP0HandlerBatchImageGroupRepoStub{group: &service.Group{
			ID: groupID, Platform: service.PlatformGemini, Status: service.StatusActive, AllowBatchImageGeneration: true,
		}}
		batchSvc := &service.BatchImagePublicService{
			Repo:        batchRepo,
			AccountRepo: &liveP0HandlerBatchImageAccountRepoStub{},
			GroupRepo:   groupRepo,
			Config:      cfg,
		}
		h := &BatchImageHandler{
			service: batchSvc,
			openAI:  &OpenAIGatewayHandler{billingCacheService: billingCache},
		}
		key := &service.APIKey{
			ID: apiKeyID, UserID: userID, Status: service.StatusAPIKeyActive,
			GroupID: &groupID, Group: group,
			User: &service.User{ID: userID, Status: service.StatusActive, Balance: 0},
		}
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/batches", bytes.NewBufferString(`{"model":"gemini-2.5-flash-image","task_name":"retry-task","items":[{"custom_id":"1","prompt":"hi"}]}`))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.Header.Set("Idempotency-Key", idemKey)
		c.Set(string(middleware2.ContextKeyAPIKey), key)
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: userID, Concurrency: 1})
		h.Submit(c)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), "imgbatch_existing_retry")
	})
}

type liveP0HandlerBatchImageRepoStub struct {
	service.BatchImageRepository
	existing *service.BatchImageJob
}

func (r *liveP0HandlerBatchImageRepoStub) GetBatchImageJobByIdempotencyKey(_ context.Context, userID, apiKeyID int64, key string) (*service.BatchImageJob, error) {
	if r.existing != nil && r.existing.UserID == userID && r.existing.APIKeyID != nil && *r.existing.APIKeyID == apiKeyID &&
		r.existing.IdempotencyKey != nil && *r.existing.IdempotencyKey == key {
		return r.existing, nil
	}
	return nil, service.ErrBatchImageJobNotFound
}

type liveP0HandlerBatchImageGroupRepoStub struct {
	group *service.Group
}

func (r *liveP0HandlerBatchImageGroupRepoStub) GetByIDLite(context.Context, int64) (*service.Group, error) {
	return r.group, nil
}

type liveP0HandlerBatchImageAccountRepoStub struct {
	service.BatchImageAccountSelectionRepository
}

type liveP0APIKeySlotConcurrencyCache struct {
	concurrencyCacheMock
	seenAPIKeyLimit int
}

func (m *liveP0APIKeySlotConcurrencyCache) TrackAPIKeySlot(context.Context, int64, string) error {
	return nil
}

func (m *liveP0APIKeySlotConcurrencyCache) GetAPIKeyConcurrencyBatch(_ context.Context, ids []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(ids))
	for _, id := range ids {
		out[id] = 0
	}
	return out, nil
}

func (m *liveP0APIKeySlotConcurrencyCache) AcquireAPIKeySlot(_ context.Context, _ int64, maxConcurrency int, _ string) (bool, error) {
	m.seenAPIKeyLimit = maxConcurrency
	return true, nil
}

func (m *liveP0APIKeySlotConcurrencyCache) ReleaseAPIKeySlot(context.Context, int64, string) error {
	return nil
}

func (m *liveP0APIKeySlotConcurrencyCache) APIKeySlotTTL() time.Duration {
	return 30 * time.Second
}

func (m *liveP0APIKeySlotConcurrencyCache) APIKeySlotRefreshInterval() time.Duration {
	return 5 * time.Second
}

func (m *liveP0APIKeySlotConcurrencyCache) RefreshAPIKeySlot(context.Context, int64, string) (bool, error) {
	return true, nil
}

func TestTryAcquireUserSlotForAPIKeyWithLimit_UsesRefreshedLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &liveP0APIKeySlotConcurrencyCache{
		concurrencyCacheMock: concurrencyCacheMock{
			acquireUserSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
		},
	}
	helper := NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, time.Second)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ownerCtx, cancelOwner := service.WithAPIKeyAdmissionOwner(context.Background())
	defer cancelOwner()
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil).WithContext(ownerCtx)
	// Stale handshake API key in Gin context has ConcurrencyLimit=10, while refreshed turnKey has ConcurrencyLimit=2.
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 777, ConcurrencyLimit: 10})

	rel, acquired, err := helper.TryAcquireUserSlotForAPIKeyWithLimit(c, 42, 5, 777, 2)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, rel)
	rel()
	require.Equal(t, 2, cache.seenAPIKeyLimit, "must enforce refreshed turnKey.ConcurrencyLimit rather than stale Gin context limit")
}

// --- Fix 5 (Handler): Gemini CachedContent Pre-Check Before CreateUpstream ---

func TestEstimateGeminiCachedContentInputTokens_MultimodalAndFallback(t *testing.T) {
	// 4MB image => ~5.33MB base64 string. Structural estimation must NOT treat base64 bytes as 1.33M tokens.
	bigBase64 := strings.Repeat("A", 5_333_333)
	inlineCamel := `{"model":"models/gemini-2.5-pro","contents":[{"role":"user","parts":[{"text":"describe"},{"inlineData":{"mimeType":"image/png","data":"` + bigBase64 + `"}}]}]}`
	tokensCamel := estimateGeminiCachedContentInputTokens([]byte(inlineCamel))
	require.Equal(t, len("describe")/4+geminiCachedContentInlineDataPartTokens, tokensCamel)
	require.Less(t, tokensCamel, 2000, "4MB inlineData image must NOT estimate ~1.33M tokens")

	inlineSnake := `{"model":"models/gemini-2.5-pro","system_instruction":{"parts":[{"text":"sys"}]},"contents":[{"role":"user","parts":[{"inline_data":{"mime_type":"image/jpeg","data":"` + bigBase64 + `"}}]}]}`
	tokensSnake := estimateGeminiCachedContentInputTokens([]byte(inlineSnake))
	require.Equal(t, geminiCachedContentInlineDataPartTokens, tokensSnake)

	// fileData / file_data with empty text still gets conservative non-zero floor.
	fileURI := `{"model":"models/gemini-2.5-pro","contents":[{"role":"user","parts":[{"fileData":{"mimeType":"video/mp4","fileUri":"gs://bucket/video.mp4"}}]}]}`
	require.Equal(t, geminiCachedContentFileDataPartTokens, estimateGeminiCachedContentInputTokens([]byte(fileURI)))
	fileURISnake := `{"model":"models/gemini-2.5-pro","contents":[{"role":"user","parts":[{"file_data":{"mime_type":"application/pdf","file_uri":"gs://bucket/doc.pdf"}}]}]}`
	require.Equal(t, geminiCachedContentFileDataPartTokens, estimateGeminiCachedContentInputTokens([]byte(fileURISnake)))

	// Multimodal functionResponse with nested inlineData must also avoid dividing base64 by 4.
	fnMultimodal := `{"model":"models/gemini-2.5-pro","contents":[{"role":"user","parts":[{"functionResponse":{"name":"load_img","response":{"ok":true},"parts":[{"inlineData":{"mimeType":"image/png","data":"` + bigBase64 + `"}}]}}]}]}`
	tokensFn := estimateGeminiCachedContentInputTokens([]byte(fnMultimodal))
	require.GreaterOrEqual(t, tokensFn, geminiCachedContentInlineDataPartTokens)
	require.Less(t, tokensFn, 2000, "multimodal functionResponse.parts[].inlineData must NOT estimate ~1.33M tokens")

	// Malformed JSON falls back to len(body)/4.
	malformed := strings.Repeat("{", 400)
	require.Equal(t, 100, estimateGeminiCachedContentInputTokens([]byte(malformed)))
}

func TestGeminiCachedContentsCreate_RejectsInsufficientBalanceBeforeCreateUpstream(t *testing.T) {
	// With cachedBillingInputPrice=1.25/1M and cachedBillingStoragePrice=4.50/1M/hr,
	// a 400KB text body estimates 100,000 tokens -> 100k * (1.25 + 4.50*24)/1M = $10.925 > $1.00 balance.
	env := newCachedBillingEnv(t, 1.0, 900)
	env.upstream.createBody = cachedBillingCreateBody(time.Now().Add(24 * time.Hour))
	largeBody := `{"model":"models/gemini-2.5-pro","ttl":"86400s","contents":[{"role":"user","parts":[{"text":"` + strings.Repeat("a", 400_000) + `"}]}]}`

	rec := env.serve(http.MethodPost, "/v1beta/cachedContents", largeBody, nil, env.h.GeminiCachedContentsCreate)
	require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "RESOURCE_EXHAUSTED")
	require.Contains(t, rec.Body.String(), "insufficient balance")
	require.Equal(t, 0, env.upstream.methodCalls(http.MethodPost), "must reject BEFORE calling CreateUpstream")
	require.Equal(t, 0, env.upstream.methodCalls(http.MethodDelete), "no orphan upstream cache should be created")
	require.Empty(t, env.repo.records)
	require.Empty(t, env.usage.totals())
}

func TestGeminiCachedContentsCreate_AllowsLargeBase64InlineDataWithSufficientBalance(t *testing.T) {
	// 500KB base64 image in inlineData estimates 1024 tokens (~$0.112 for 24h), NOT 125k tokens ($13.65).
	// Even with $1.00 balance (which would reject 125k tokens = $13.65), 1024 tokens = $0.112 must SUCCEED!
	env := newCachedBillingEnv(t, 1.0, 900)
	env.upstream.createBody = `{"name":"cachedContents/up_img","expireTime":"` + time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339) + `","usageMetadata":{"totalTokenCount":1024}}`
	imgBody := `{"model":"models/gemini-2.5-pro","ttl":"86400s","contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"` + strings.Repeat("A", 500_000) + `"}}]}]}`

	rec := env.serve(http.MethodPost, "/v1beta/cachedContents", imgBody, nil, env.h.GeminiCachedContentsCreate)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, env.upstream.methodCalls(http.MethodPost))
}

func TestGeminiCachedContentsCreate_DeltaRejectReleasesHoldAfterInputUsageBilling(t *testing.T) {
	// Balance = $0.50: estimated 1024 tokens (~$0.112) passes precheck and creates upstream cache,
	// but upstream reports 100,000 tokens (~$10.925 > $0.50) -> delta reservation fails!
	// Handler must delete orphan upstream cache, bill input tokens, release precheck hold cleanly,
	// and return 429 RESOURCE_EXHAUSTED so a later affordable request still succeeds.
	env := newCachedBillingEnv(t, 0.50, 900)
	env.upstream.createBody = `{"name":"cachedContents/up_oversize","expireTime":"` + time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339) + `","usageMetadata":{"totalTokenCount":100000}}`
	imgBody := `{"model":"models/gemini-2.5-pro","ttl":"86400s","contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"` + strings.Repeat("A", 10_000) + `"}}]}]}`

	rec := env.serve(http.MethodPost, "/v1beta/cachedContents", imgBody, nil, env.h.GeminiCachedContentsCreate)
	require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "RESOURCE_EXHAUSTED")
	require.Equal(t, 1, env.upstream.methodCalls(http.MethodPost))
	require.Equal(t, 1, env.upstream.methodCalls(http.MethodDelete), "orphan upstream cache must be deleted on delta rejection")
	require.Len(t, env.usage.totals(), 1, "consumed input tokens must still be billed on delta rejection")

	// Subsequent request costing ~$0.112 must succeed (proving precheckRes was released cleanly, not leaked).
	env.upstream.createBody = `{"name":"cachedContents/up_small","expireTime":"` + time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339) + `","usageMetadata":{"totalTokenCount":1024}}`
	rec2 := env.serve(http.MethodPost, "/v1beta/cachedContents", imgBody, nil, env.h.GeminiCachedContentsCreate)
	require.Equal(t, http.StatusOK, rec2.Code, rec2.Body.String())
}
