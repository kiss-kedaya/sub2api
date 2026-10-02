//go:build unit

package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestEstimateInflightReservationCost(t *testing.T) {
	billing := NewBillingService(&config.Config{}, nil)
	pricing, err := billing.GetModelPricing("claude-sonnet-4-5")
	require.NoError(t, err)

	cfg := config.InflightReservationConfig{DefaultMaxTokens: 8192, MaxOutputTokens: 64000, MaxInputTokens: 1000}

	// explicit max_tokens; body 400 bytes → 100 input tokens
	cost, ok := EstimateInflightReservationCost(billing, cfg, "claude-sonnet-4-5", 400, 1000, 1)
	require.True(t, ok)
	require.InDelta(t, 100*pricing.InputPricePerToken+1000*pricing.OutputPricePerToken, cost, 1e-12)

	// missing max_tokens → default; rate multiplier applied
	cost, ok = EstimateInflightReservationCost(billing, cfg, "claude-sonnet-4-5", 0, 0, 2)
	require.True(t, ok)
	require.InDelta(t, 2*8192*pricing.OutputPricePerToken, cost, 1e-12)

	// caps: input and output clamped
	cost, ok = EstimateInflightReservationCost(billing, cfg, "claude-sonnet-4-5", 1_000_000, 1_000_000, 1)
	require.True(t, ok)
	require.InDelta(t, 1000*pricing.InputPricePerToken+64000*pricing.OutputPricePerToken, cost, 1e-12)

	// free group / missing billing → fail open
	_, ok = EstimateInflightReservationCost(billing, cfg, "claude-sonnet-4-5", 10, 10, 0)
	require.False(t, ok)
	_, ok = EstimateInflightReservationCost(nil, cfg, "claude-sonnet-4-5", 10, 10, 1)
	require.False(t, ok)
	_, ok = EstimateInflightReservationCost(billing, cfg, "", 10, 10, 1)
	require.False(t, ok)
}

func TestReserveInflightBalance_NoReservationCacheFailsOpen(t *testing.T) {
	cfg := &config.Config{}
	cfg.Billing.InflightReservation.Enabled = true
	svc := NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)
	release, err := svc.ReserveInflightBalance(context.Background(), &User{ID: 1}, nil, nil, 100)
	require.NoError(t, err)
	release()
}

func TestReserveInflightBalance_SimpleModeDisabled(t *testing.T) {
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Billing.InflightReservation.Enabled = true
	svc := NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)
	require.False(t, svc.InflightReservationEnabled())
}

// ---------------------------------------------------------------------------
// estimate: same model / resolver as billing
// ---------------------------------------------------------------------------

func newInflightEstimateGateway(t *testing.T, channelService *ChannelService) *GatewayService {
	t.Helper()
	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, DefaultMaxTokens: 1000, MaxInputTokens: 200000, MaxOutputTokens: 128000}
	billing := NewBillingService(cfg, nil)
	return &GatewayService{
		cfg:            cfg,
		billingService: billing,
		resolver:       NewModelPricingResolver(channelService, billing),
		channelService: channelService,
	}
}

func TestInflightEstimate_ChannelAliasUsesMappedModel(t *testing.T) {
	groupID := int64(10)
	ch := Channel{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{groupID},
		ModelMapping: map[string]map[string]string{
			"anthropic": {"my-alias": "claude-sonnet-4-5"},
		},
	}
	cs := newTestChannelService(makeStandardRepo(ch, map[int64]string{groupID: "anthropic"}))
	svc := newInflightEstimateGateway(t, cs)
	apiKey := &APIKey{User: &User{ID: 1}, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformAnthropic, RateMultiplier: 1}}

	// Old estimate (client model, base pricing only) → 0 → reservation bypassed.
	_, ok := EstimateInflightReservationCost(svc.billingService, svc.cfg.Billing.InflightReservation, "my-alias", 4000, 1000, 1)
	require.False(t, ok, "precondition: alias has no base pricing")

	est, priced := svc.EstimateInflightReservation(context.Background(), apiKey, InflightEstimateRequest{Model: "my-alias", BodyBytes: 4000, MaxTokens: 1000})
	require.True(t, priced)
	require.Greater(t, est, 0.0)

	direct, _ := svc.EstimateInflightReservation(context.Background(), apiKey, InflightEstimateRequest{Model: "claude-sonnet-4-5", BodyBytes: 4000, MaxTokens: 1000})
	require.InDelta(t, direct, est, 1e-12, "alias must be estimated as the channel-mapped billing model")
}

func TestInflightEstimate_GroupPerRequestPricing(t *testing.T) {
	groupID := int64(20)
	price := 0.5
	group := &Group{ID: groupID, Platform: PlatformAnthropic, RateMultiplier: 2, ModelPricing: []ChannelModelPricing{
		{Models: []string{"custom-per-request"}, BillingMode: BillingModePerRequest, PerRequestPrice: &price},
	}}
	svc := newInflightEstimateGateway(t, nil)
	apiKey := &APIKey{User: &User{ID: 1}, GroupID: &groupID, Group: group}

	est, priced := svc.EstimateInflightReservation(context.Background(), apiKey, InflightEstimateRequest{Model: "custom-per-request", BodyBytes: 100})
	require.True(t, priced)
	require.InDelta(t, 1.0, est, 1e-12, "per_request 0.5 × group rate 2")

	// Wildcard group token pricing is honored as well.
	in, out := 1e-6, 2e-6
	group.ModelPricing = []ChannelModelPricing{{Models: []string{"wild-*"}, BillingMode: BillingModeToken, InputPrice: &in, OutputPrice: &out}}
	est, priced = svc.EstimateInflightReservation(context.Background(), apiKey, InflightEstimateRequest{Model: "wild-x", BodyBytes: 4000, MaxTokens: 500})
	require.True(t, priced)
	require.InDelta(t, (1000*in+500*out)*2, est, 1e-12)
}

func TestInflightEstimate_UnpricedIsReportedAndNonMeteredIsNot(t *testing.T) {
	svc := newInflightEstimateGateway(t, nil)
	apiKey := &APIKey{User: &User{ID: 1}}
	est, priced := svc.EstimateInflightReservation(context.Background(), apiKey, InflightEstimateRequest{Model: "totally-unknown-model-xyz"})
	require.False(t, priced)
	require.Zero(t, est)

	est, priced = svc.EstimateInflightReservation(context.Background(), apiKey, InflightEstimateRequest{})
	require.True(t, priced, "non-metered requests (e.g. media status lookups) are not 'unpriced'")
	require.Zero(t, est)
}

func TestInflightEstimate_MediaKinds(t *testing.T) {
	groupID := int64(30)
	p4k := 0.3
	search := 1000.0
	group := &Group{ID: groupID, Platform: PlatformOpenAI, RateMultiplier: 1, ImagePrice4K: &p4k, SearchPricePer1k: &search}
	svc := newInflightEstimateGateway(t, nil)
	apiKey := &APIKey{User: &User{ID: 1}, GroupID: &groupID, Group: group}

	est, priced := svc.EstimateInflightReservation(context.Background(), apiKey, InflightEstimateRequest{Model: "gpt-image-1", Kind: InflightEstimateImage, Units: 2})
	require.True(t, priced)
	require.GreaterOrEqual(t, est, 0.6, "image estimate uses the highest size tier × n")

	est, priced = svc.EstimateInflightReservation(context.Background(), apiKey, InflightEstimateRequest{Model: "grok-web-search", Kind: InflightEstimatePerRequest, SearchCalls: 1})
	require.True(t, priced)
	require.InDelta(t, 1.0, est, 1e-12)

	est, priced = svc.EstimateInflightReservation(context.Background(), apiKey, InflightEstimateRequest{Model: "realtime", Kind: InflightEstimateAudio, AudioMode: "realtime", AudioUnits: 1})
	require.True(t, priced)
	require.Greater(t, est, 0.0)
}

// ---------------------------------------------------------------------------
// reservation handle: hand-off to billing task, renewal
// ---------------------------------------------------------------------------

type memInflightCache struct {
	BillingCache
	mu        sync.Mutex
	balance   float64
	res       map[string]float64
	exp       map[string]time.Time
	renews    atomic.Int32
	deducts   atomic.Int32
	releaseCt atomic.Int32
}

func newMemInflightCache(balance float64) *memInflightCache {
	return &memInflightCache{balance: balance, res: map[string]float64{}, exp: map[string]time.Time{}}
}

func (m *memInflightCache) GetUserBalance(context.Context, int64) (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.balance, nil
}

func (m *memInflightCache) DeductUserBalance(_ context.Context, _ int64, amount float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.balance -= amount
	m.deducts.Add(1)
	return nil
}

func (m *memInflightCache) gc() {
	now := time.Now()
	for id, e := range m.exp {
		if !e.After(now) {
			delete(m.exp, id)
			delete(m.res, id)
		}
	}
}

func (m *memInflightCache) ReserveInflightBalance(_ context.Context, _ int64, id string, amount, balance float64, ttl time.Duration) (bool, float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gc()
	sum := 0.0
	for _, v := range m.res {
		sum += v
	}
	if len(m.res) > 0 && balance-sum < amount {
		return false, sum, nil
	}
	m.res[id] = amount
	m.exp[id] = time.Now().Add(ttl)
	return true, sum, nil
}

func (m *memInflightCache) ReleaseInflightBalance(_ context.Context, _ int64, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.res, id)
	delete(m.exp, id)
	m.releaseCt.Add(1)
	return nil
}

func (m *memInflightCache) RenewInflightBalance(_ context.Context, _ int64, id string, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.renews.Add(1)
	if _, ok := m.res[id]; !ok {
		return false, nil
	}
	m.exp[id] = time.Now().Add(ttl)
	return true, nil
}

func (m *memInflightCache) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gc()
	return len(m.res)
}

func newInflightSvc(t *testing.T, cache BillingCache, ttlSeconds int) *BillingCacheService {
	t.Helper()
	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: ttlSeconds}
	svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)
	return svc
}

func TestInflightReservation_HeldUntilBillingTaskDone(t *testing.T) {
	cache := newMemInflightCache(1)
	svc := newInflightSvc(t, cache, 60)
	user := &User{ID: 1}

	res, err := svc.ReserveInflight(context.Background(), user, nil, nil, 0.9)
	require.NoError(t, err)
	require.NotNil(t, res)
	taskDone := res.Acquire() // billing task submitted
	res.HandlerDone()         // handler returned
	require.Equal(t, 1, cache.count(), "reservation must survive handler return while billing is pending")

	_, err = svc.ReserveInflight(context.Background(), user, nil, nil, 0.9)
	require.ErrorIs(t, err, ErrInsufficientBalance)

	taskDone()
	taskDone() // idempotent
	require.Equal(t, 0, cache.count())
	require.Equal(t, int32(1), cache.releaseCt.Load(), "released exactly once")

	// No billing task → HandlerDone releases immediately; Acquire after release is a no-op.
	res2, err := svc.ReserveInflight(context.Background(), user, nil, nil, 0.1)
	require.NoError(t, err)
	res2.HandlerDone()
	res2.HandlerDone()
	require.Equal(t, 0, cache.count())
	res2.Acquire()()
	require.Equal(t, int32(2), cache.releaseCt.Load())

	// Nil handle is safe.
	var nilRes *InflightReservation
	nilRes.Acquire()()
	nilRes.HandlerDone()
	nilRes.Release()
}

func TestInflightReservation_RenewedWhileHandlerActive(t *testing.T) {
	cache := newMemInflightCache(1)
	svc := newInflightSvc(t, cache, 1)
	user := &User{ID: 2}

	res, err := svc.ReserveInflight(context.Background(), user, nil, nil, 0.9)
	require.NoError(t, err)
	time.Sleep(2500 * time.Millisecond) // > 2 × TTL
	require.Equal(t, 1, cache.count(), "renewal keeps the reservation alive past its TTL")
	require.Greater(t, cache.renews.Load(), int32(3))
	_, err = svc.ReserveInflight(context.Background(), user, nil, nil, 0.9)
	require.ErrorIs(t, err, ErrInsufficientBalance)

	done := res.Acquire()
	res.HandlerDone()
	renewsAtStop := cache.renews.Load()
	time.Sleep(1200 * time.Millisecond)
	require.Equal(t, renewsAtStop, cache.renews.Load(), "no renewal after handler end")
	require.Equal(t, 0, cache.count(), "post-handler hold is bounded by TTL")
	done()
}

func TestSyncBalanceCacheAfterDeduction_SynchronousWhenInflightEnabled(t *testing.T) {
	cache := newMemInflightCache(1)
	svc := newInflightSvc(t, cache, 60)
	p := &postUsageBillingParams{Cost: &CostBreakdown{ActualCost: 0.25}, User: &User{ID: 3}}
	syncBalanceCacheAfterDeduction(context.Background(), p, &billingDeps{billingCacheService: svc}, nil)
	require.Equal(t, int32(1), cache.deducts.Load(), "cache deduction must land before the billing task returns")
	bal, _ := cache.GetUserBalance(context.Background(), 3)
	require.InDelta(t, 0.75, bal, 1e-12)
}
