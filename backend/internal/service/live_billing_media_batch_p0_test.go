//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// --- Shared test doubles for live billing P0/P1 service tests ---

type liveP0BillingCacheStub struct {
	BillingCache
	mu                      sync.Mutex
	balance                 float64
	balanceInvalidations    []int64
	subscriptionInvalidated int
	quotaUsed               map[int64]float64
	quotaSeeded             map[int64]bool
	quotaInflight           map[int64]map[string]float64
	quotaIncrements         map[int64]float64
	rateLimitUpdates        map[int64]float64
	rateLimitData           map[int64]*APIKeyRateLimitCacheData
	balanceInflight         map[string]float64
}

func newLiveP0BillingCacheStub(balance float64) *liveP0BillingCacheStub {
	return &liveP0BillingCacheStub{
		balance:          balance,
		quotaUsed:        map[int64]float64{},
		quotaSeeded:      map[int64]bool{},
		quotaInflight:    map[int64]map[string]float64{},
		quotaIncrements:  map[int64]float64{},
		rateLimitUpdates: map[int64]float64{},
		rateLimitData:    map[int64]*APIKeyRateLimitCacheData{},
		balanceInflight:  map[string]float64{},
	}
}

func (c *liveP0BillingCacheStub) GetUserBalance(context.Context, int64) (float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.balance, nil
}

func (c *liveP0BillingCacheStub) SetUserBalance(_ context.Context, _ int64, bal float64) error {
	c.mu.Lock()
	c.balance = bal
	c.mu.Unlock()
	return nil
}

func (c *liveP0BillingCacheStub) DeductUserBalance(_ context.Context, _ int64, amount float64) error {
	c.mu.Lock()
	c.balance -= amount
	c.mu.Unlock()
	return nil
}

func (c *liveP0BillingCacheStub) SetUserBalanceIfLower(_ context.Context, _ int64, bal float64) error {
	c.mu.Lock()
	if bal < c.balance {
		c.balance = bal
	}
	c.mu.Unlock()
	return nil
}

func (c *liveP0BillingCacheStub) InvalidateUserBalance(_ context.Context, userID int64) error {
	c.mu.Lock()
	c.balanceInvalidations = append(c.balanceInvalidations, userID)
	c.mu.Unlock()
	return nil
}

func (c *liveP0BillingCacheStub) InvalidateSubscriptionCache(context.Context, int64, int64) error {
	c.mu.Lock()
	c.subscriptionInvalidated++
	c.mu.Unlock()
	return nil
}

func (c *liveP0BillingCacheStub) GetAPIKeyRateLimit(_ context.Context, keyID int64) (*APIKeyRateLimitCacheData, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d, ok := c.rateLimitData[keyID]; ok && d != nil {
		cp := *d
		return &cp, nil
	}
	return nil, errors.New("rate limit cache miss")
}

func (c *liveP0BillingCacheStub) SetAPIKeyRateLimit(_ context.Context, keyID int64, data *APIKeyRateLimitCacheData) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if data != nil {
		cp := *data
		c.rateLimitData[keyID] = &cp
	}
	return nil
}

func (c *liveP0BillingCacheStub) UpdateAPIKeyRateLimitUsage(_ context.Context, keyID int64, cost float64) error {
	c.mu.Lock()
	c.rateLimitUpdates[keyID] += cost
	c.mu.Unlock()
	return nil
}

func (c *liveP0BillingCacheStub) InvalidateAPIKeyRateLimit(context.Context, int64) error { return nil }

func (c *liveP0BillingCacheStub) SeedAPIKeyQuotaUsage(_ context.Context, keyID int64, quotaUsed float64, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.quotaSeeded[keyID] {
		c.quotaUsed[keyID] = quotaUsed
		c.quotaSeeded[keyID] = true
	}
	return nil
}

func (c *liveP0BillingCacheStub) ReserveAPIKeyQuota(_ context.Context, keyID int64, reqID string, estimate, quota float64, _ time.Duration) (bool, bool, float64, float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.quotaSeeded[keyID] {
		return false, false, 0, 0, nil
	}
	used := c.quotaUsed[keyID]
	var inflight float64
	for _, v := range c.quotaInflight[keyID] {
		inflight += v
	}
	if used+inflight+estimate > quota+1e-9 {
		return true, false, used, inflight, nil
	}
	if c.quotaInflight[keyID] == nil {
		c.quotaInflight[keyID] = map[string]float64{}
	}
	c.quotaInflight[keyID][reqID] = estimate
	return true, true, used, inflight, nil
}

func (c *liveP0BillingCacheStub) ReleaseAPIKeyQuota(_ context.Context, keyID int64, reqID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.quotaInflight[keyID], reqID)
	return nil
}

func (c *liveP0BillingCacheStub) IncrementAPIKeyQuotaUsage(_ context.Context, keyID int64, cost float64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.quotaIncrements[keyID] += cost
	if c.quotaSeeded[keyID] {
		c.quotaUsed[keyID] += cost
	}
	return nil
}

func (c *liveP0BillingCacheStub) InvalidateAPIKeyQuotaUsage(_ context.Context, keyID int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.quotaSeeded, keyID)
	delete(c.quotaUsed, keyID)
	return nil
}

func (c *liveP0BillingCacheStub) RenewAPIKeyQuota(context.Context, int64, string, time.Duration) (bool, error) {
	return true, nil
}

func (c *liveP0BillingCacheStub) ReserveInflightBalance(_ context.Context, _ int64, reqID string, amount, balance float64, _ time.Duration) (bool, float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var sum float64
	for _, v := range c.balanceInflight {
		sum += v
	}
	if len(c.balanceInflight) > 0 && balance-sum < amount {
		return false, sum, nil
	}
	c.balanceInflight[reqID] = amount
	return true, sum, nil
}

func (c *liveP0BillingCacheStub) ReleaseInflightBalance(_ context.Context, _ int64, reqID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.balanceInflight, reqID)
	return nil
}

type liveP0AuthInvalidatorStub struct {
	mu          sync.Mutex
	evictedKeys []int64
	evictedUser []int64
	byIDResult  bool
}

func (s *liveP0AuthInvalidatorStub) InvalidateAuthCacheByKey(context.Context, string)      {}
func (s *liveP0AuthInvalidatorStub) InvalidateAuthCacheByHash(context.Context, string)     {}
func (s *liveP0AuthInvalidatorStub) InvalidateAuthCacheForAPIKey(context.Context, *APIKey) {}
func (s *liveP0AuthInvalidatorStub) InvalidateAuthCacheByGroupID(context.Context, int64)   {}

func (s *liveP0AuthInvalidatorStub) InvalidateAuthCacheByUserID(_ context.Context, userID int64) {
	s.mu.Lock()
	s.evictedUser = append(s.evictedUser, userID)
	s.mu.Unlock()
}

func (s *liveP0AuthInvalidatorStub) InvalidateAuthCacheByAPIKeyID(_ context.Context, apiKeyID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictedKeys = append(s.evictedKeys, apiKeyID)
	return s.byIDResult
}

type liveP0BatchQuotaUpdaterStub struct {
	mu              sync.Mutex
	quotaIncrements map[int64]float64
	rateIncrements  map[int64]float64
}

func (u *liveP0BatchQuotaUpdaterStub) UpdateQuotaUsed(_ context.Context, apiKeyID int64, cost float64) error {
	u.mu.Lock()
	if u.quotaIncrements == nil {
		u.quotaIncrements = map[int64]float64{}
	}
	u.quotaIncrements[apiKeyID] += cost
	u.mu.Unlock()
	return nil
}

func (u *liveP0BatchQuotaUpdaterStub) UpdateRateLimitUsage(_ context.Context, apiKeyID int64, cost float64) error {
	u.mu.Lock()
	if u.rateIncrements == nil {
		u.rateIncrements = map[int64]float64{}
	}
	u.rateIncrements[apiKeyID] += cost
	u.mu.Unlock()
	return nil
}

type liveP0UsageBillingRepoStub struct {
	UsageBillingRepository
	mu      sync.Mutex
	result  *UsageBillingApplyResult
	err     error
	applied int
}

func (r *liveP0UsageBillingRepoStub) Apply(_ context.Context, _ *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	r.applied++
	if r.result != nil {
		cp := *r.result
		return &cp, nil
	}
	return &UsageBillingApplyResult{Applied: true}, nil
}

type liveP0MediaRepoErrWrapper struct {
	*mediaJobMemoryRepo
	applyErr      error
	applyRes      *UsageBillingApplyResult
	abandonedIDs  []string
	retriedDelays []time.Duration
}

func (r *liveP0MediaRepoErrWrapper) ApplyMediaBillingJob(ctx context.Context, id string) (*UsageBillingApplyResult, error) {
	if r.applyErr != nil {
		return nil, r.applyErr
	}
	if r.applyRes != nil {
		r.mu.Lock()
		if job := r.jobs[id]; job != nil && job.Status == MediaBillingReady {
			job.Status = MediaBillingBilled
		}
		r.mu.Unlock()
		cp := *r.applyRes
		return &cp, nil
	}
	return r.mediaJobMemoryRepo.ApplyMediaBillingJob(ctx, id)
}

func (r *liveP0MediaRepoErrWrapper) AbandonMediaBillingJob(ctx context.Context, id string) (bool, error) {
	abandoned, err := r.mediaJobMemoryRepo.AbandonMediaBillingJob(ctx, id)
	r.mu.Lock()
	if abandoned {
		r.abandonedIDs = append(r.abandonedIDs, id)
	}
	r.mu.Unlock()
	return abandoned, err
}

func (r *liveP0MediaRepoErrWrapper) RetryMediaBillingJob(ctx context.Context, id string, delay time.Duration) error {
	r.mu.Lock()
	r.retriedDelays = append(r.retriedDelays, delay)
	r.mu.Unlock()
	return r.mediaJobMemoryRepo.RetryMediaBillingJob(ctx, id, delay)
}

// --- Fix 2 (Service): Grok Realtime text-only / transcript output billability & STT sizeFloor ---

func TestGrokRealtimeUpstreamEventIsBillable_CoversTextTranscriptAndAudio(t *testing.T) {
	billableEvents := []string{
		`{"type":"response.audio.delta","delta":"AAAA"}`,
		`{"type":"response.output_audio.delta","audio":"BBBB"}`,
		`{"type":"response.audio_transcript.delta","delta":"hello"}`,
		`{"type":"response.audio_transcript.done","transcript":"full transcript"}`,
		`{"type":"response.output_audio_transcript.delta","delta":"hi"}`,
		`{"type":"response.text.delta","delta":"text-only reply"}`,
		`{"type":"response.text.done","text":"final text"}`,
		`{"type":"response.output_text.delta","delta":"chunk"}`,
		`{"type":"response.function_call_arguments.delta","delta":"{\"q\":1}"}`,
		`{"type":"conversation.item.input_audio_transcription.completed","transcript":"spoken"}`,
		`{"type":"response.content_part.done","part":{"type":"text","text":"answer"}}`,
		`{"type":"response.output_item.done","item":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`,
		`{"type":"response.output_item.done","item":{"type":"function_call","name":"search","arguments":"{}"}}`,
		`{"type":"response.done","response":{"output":[{"id":"item_1"}]}}`,
		`{"type":"response.done","response":{"output":[],"usage":{"output_tokens":12}}}`,
	}
	for _, msg := range billableEvents {
		require.True(t, grokRealtimeUpstreamEventIsBillable([]byte(msg)), "event should be billable: %s", msg)
	}

	nonBillableEvents := []string{
		`not-json`,
		`{"type":"session.created"}`,
		`{"type":"session.updated"}`,
		`{"type":"error","error":{"message":"upstream failure"}}`,
		`{"type":"response.created"}`,
		`{"type":"response.text.delta","delta":"   "}`,
		`{"type":"response.audio_transcript.delta","delta":""}`,
		`{"type":"response.output_item.done","item":{"role":"user","content":[{"type":"input_text","text":"hi"}]}}`,
		`{"type":"response.done","response":{"status":"failed","output":[]}}`,
	}
	for _, msg := range nonBillableEvents {
		require.False(t, grokRealtimeUpstreamEventIsBillable([]byte(msg)), "event must not be billable: %s", msg)
	}

	// Preserve existing grokRealtimeEventHasAudio contract (transcript is not raw audio).
	require.False(t, grokRealtimeEventHasAudio([]byte(`{"type":"response.audio_transcript.delta","delta":"hi"}`)))
}

func TestEstimateGrokVoiceAudioUsage_STTFallbackSizeFloorNotMaskedByElapsed(t *testing.T) {
	// 320,000 bytes => sizeFloor = 20 seconds (~16KB/s).
	largeAudio := make([]byte, 320_000)

	t.Run("small elapsed does not mask sizeFloor when upstream duration missing", func(t *testing.T) {
		usage := estimateGrokVoiceAudioUsage("stt", largeAudio, "multipart/form-data", []byte(`{"text":"hello"}`), 50*time.Millisecond)
		require.NotNil(t, usage)
		require.Equal(t, "stt", usage.Mode)
		require.InDelta(t, 20.0/3600.0, usage.DurationOrUnits, 1e-9)
	})

	t.Run("larger elapsed overrides smaller sizeFloor", func(t *testing.T) {
		smallAudio := make([]byte, 1600) // 0.1s sizeFloor
		usage := estimateGrokVoiceAudioUsage("stt", smallAudio, "multipart/form-data", []byte(`{"text":"hello"}`), 45*time.Second)
		require.NotNil(t, usage)
		require.InDelta(t, 45.0/3600.0, usage.DurationOrUnits, 1e-9)
	})

	t.Run("upstream reported duration takes precedence", func(t *testing.T) {
		usage := estimateGrokVoiceAudioUsage("stt", largeAudio, "multipart/form-data", []byte(`{"text":"hello","duration":12.5}`), 50*time.Millisecond)
		require.NotNil(t, usage)
		require.InDelta(t, 12.5/3600.0, usage.DurationOrUnits, 1e-9)
	})

	t.Run("absurd client duration_seconds is clamped to sane maximum", func(t *testing.T) {
		body := []byte(`{"duration_seconds":999999999}`)
		usage := estimateGrokVoiceAudioUsage("stt", body, "application/json", []byte(`{"text":"ok"}`), 10*time.Millisecond)
		require.NotNil(t, usage)
		require.InDelta(t, maxGrokSTTFallbackSeconds/3600.0, usage.DurationOrUnits, 1e-9)
	})
}

// --- Fix 3 (BILL-H3): Media Async Billing & Gemini CachedContent Redis Key Quota + Immediate Eviction ---

func TestSettleMediaBillingJob_UpdatesRedisAPIKeyQuotaAndEvictsAuthCacheOnExhaustion(t *testing.T) {
	for _, tc := range []struct {
		name           string
		subscriptionID *int64
		byIDEvict      bool
	}{
		{name: "balance_mode_evicts_by_key_id", subscriptionID: nil, byIDEvict: true},
		{name: "subscription_mode_evicts_by_user_fallback", subscriptionID: liveP0Int64Ptr(88), byIDEvict: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := newLiveP0BillingCacheStub(100)
			cfg := &config.Config{RunMode: config.RunModeStandard}
			billingCache := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billingCache.Stop)
			authInv := &liveP0AuthInvalidatorStub{byIDResult: tc.byIDEvict}

			memRepo := &mediaJobMemoryRepo{jobs: map[string]*MediaBillingJob{}, balance: 100}
			wrapper := &liveP0MediaRepoErrWrapper{
				mediaJobMemoryRepo: memRepo,
				applyRes:           &UsageBillingApplyResult{Applied: true, APIKeyQuotaExhausted: true},
			}
			usageRepo := &mediaUsageMemoryRepo{}
			// Intentionally leave deferredService == nil to prove Redis quota & eviction
			// do not depend on deferredService or fake APIKey.Quota > 0.
			svc := &OpenAIGatewayService{
				cfg:                 cfg,
				mediaBillingJobs:    wrapper,
				usageLogRepo:        usageRepo,
				billingCacheService: billingCache,
			}
			svc.SetAuthCacheInvalidator(authInv)

			job := &MediaBillingJob{
				ID:        "media_quota_test_" + tc.name,
				Kind:      MediaBillingKindGrokVideo,
				UserID:    42,
				APIKeyID:  77,
				AccountID: 9,
				GroupID:   11,
				Status:    MediaBillingReady,
				Intent: &MediaBillingIntent{
					Command: UsageBillingCommand{
						RequestID:       "grok-video:test:" + tc.name,
						UserID:          42,
						APIKeyID:        77,
						AccountID:       9,
						SubscriptionID:  tc.subscriptionID,
						BalanceCost:     0.50,
						APIKeyQuotaCost: 0.50,
					},
					Usage: UsageLog{
						RequestID:  "grok-video:test:" + tc.name,
						UserID:     42,
						APIKeyID:   77,
						AccountID:  9,
						ActualCost: 0.50,
						TotalCost:  0.50,
					},
				},
			}
			memRepo.jobs[job.ID] = job

			require.NoError(t, svc.settleMediaBillingJob(context.Background(), job, nil))
			require.InDelta(t, 0.50, cache.quotaIncrements[77], 1e-9, "Redis apikey:quota must be incremented")
			require.Equal(t, []int64{77}, authInv.evictedKeys)
			if tc.byIDEvict {
				require.Empty(t, authInv.evictedUser)
			} else {
				require.Equal(t, []int64{42}, authInv.evictedUser)
			}

			// Replay with Applied=false must NOT double-increment Redis quota.
			wrapper.applyRes = &UsageBillingApplyResult{Applied: false}
			job.Status = MediaBillingReady
			require.NoError(t, svc.settleMediaBillingJob(context.Background(), job, nil))
			require.InDelta(t, 0.50, cache.quotaIncrements[77], 1e-9, "idempotent replay must not double-increment")
		})
	}
}

func TestApplyCachedContentCharge_UpdatesRedisAPIKeyQuotaAndEvictsAuthCacheOnExhaustion(t *testing.T) {
	for _, subID := range []*int64{nil, liveP0Int64Ptr(99)} {
		name := "balance"
		if subID != nil {
			name = "subscription"
		}
		t.Run(name, func(t *testing.T) {
			cache := newLiveP0BillingCacheStub(100)
			cfg := &config.Config{RunMode: config.RunModeStandard}
			billingCache := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billingCache.Stop)
			authInv := &liveP0AuthInvalidatorStub{byIDResult: true}
			billingRepo := &liveP0UsageBillingRepoStub{
				result: &UsageBillingApplyResult{Applied: true, APIKeyQuotaExhausted: true},
			}
			usageRepo := &mediaUsageMemoryRepo{}

			svc := &GatewayService{
				cfg:                 cfg,
				usageBillingRepo:    billingRepo,
				usageLogRepo:        usageRepo,
				billingCacheService: billingCache,
			}
			svc.SetAuthCacheInvalidator(authInv)
			groupID := int64(5)
			reqID := GeminiCachedContentUsageRequestIDPrefix + "create:pub_" + name
			intent := &MediaBillingIntent{
				Command: UsageBillingCommand{
					RequestID:       reqID,
					UserID:          15,
					APIKeyID:        66,
					AccountID:       7,
					SubscriptionID:  subID,
					BalanceCost:     1.25,
					APIKeyQuotaCost: 1.25,
				},
				Usage: UsageLog{
					RequestID:  reqID,
					UserID:     15,
					APIKeyID:   66,
					AccountID:  7,
					GroupID:    &groupID,
					ActualCost: 1.25,
					TotalCost:  1.25,
				},
			}

			require.NoError(t, svc.ApplyCachedContentCharge(context.Background(), intent))
			require.InDelta(t, 1.25, cache.quotaIncrements[66], 1e-9)
			require.Equal(t, []int64{66}, authInv.evictedKeys)

			// Replay with Applied=false must not double-increment.
			billingRepo.result = &UsageBillingApplyResult{Applied: false}
			require.NoError(t, svc.ApplyCachedContentCharge(context.Background(), intent))
			require.InDelta(t, 1.25, cache.quotaIncrements[66], 1e-9)
		})
	}
}

// --- Fix 4 (Service): Batch Image Balance Cache Invalidation + Quota/RateLimit Enforcement ---

func TestBatchImageLifecycle_InvalidatesUserBalanceCacheOnAllMutations(t *testing.T) {
	ctx := context.Background()
	cache := newLiveP0BillingCacheStub(50)
	cfg := &config.Config{RunMode: config.RunModeStandard}
	billingCache := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)

	// Wire BillingCache via APIKeyService.SetRateLimitCacheInvalidator to test zero-wire auto-resolution!
	apiKeySvc := &APIKeyService{}
	apiKeySvc.SetRateLimitCacheInvalidator(billingCache)
	require.Same(t, billingCache, apiKeySvc.BillingCacheService())

	// 1. Submit + Cancel
	svc, repo, _, _, _ := newTestBatchImagePublicService(true)
	svc.BillingCache = billingCache
	got, err := svc.Submit(ctx, testBatchImageOwner(), BatchImageSubmitRequest{
		Model: "gemini-2.5-flash-image",
		Items: []BatchImageSubmitItem{{CustomID: "c1", Prompt: "cat"}},
	}, "")
	require.NoError(t, err)
	require.Equal(t, []int64{11}, cache.balanceInvalidations, "Submit must invalidate billing:balance:{uid}")

	// Clear ProviderJobName so Cancel takes the immediate hold-release branch.
	repo.jobs[got.ID].ProviderJobName = nil
	_, err = svc.Cancel(ctx, testBatchImageOwner(), got.ID)
	require.NoError(t, err)
	require.Equal(t, []int64{11, 11}, cache.balanceInvalidations, "Cancel must invalidate billing:balance:{uid}")

	// 2. Settle
	settleRepo := newFakeBatchImageRepository()
	settleJob := testSettlingBatchImageJob("imgbatch_inv_settle")
	settleJob.UserID = 11
	settleJob.SuccessCount = 2
	settleJob.FailCount = 0
	settleJob.ItemCount = 2
	settleRepo.jobs[settleJob.BatchID] = settleJob
	settleBilling := &fakeBatchImageBillingRepo{}
	quotaUpdater := &liveP0BatchQuotaUpdaterStub{}
	settleSvc := &BatchImageSettlementService{
		Repo:               settleRepo,
		BillingRepo:        settleBilling,
		Pricing:            &fakeBatchImagePricingResolver{unitPrice: 0.25},
		BillingCache:       billingCache,
		APIKeyQuotaUpdater: quotaUpdater,
		AuthCache:          &fakeBatchImageAuthCacheInvalidator{},
	}
	_, err = settleSvc.Settle(ctx, settleJob.BatchID)
	require.NoError(t, err)
	require.Equal(t, []int64{11, 11, 11}, cache.balanceInvalidations, "Settle must invalidate billing:balance:{uid}")
	require.InDelta(t, 0.50, quotaUpdater.quotaIncrements[321], 1e-9, "Settle must update PG quota_used")
	require.InDelta(t, 0.50, quotaUpdater.rateIncrements[321], 1e-9, "Settle must update PG rate_limit usage")
	require.InDelta(t, 0.50, cache.quotaIncrements[321], 1e-9, "Settle must increment Redis apikey:quota")

	// Idempotent settlement replay (Applied=false) must NOT double-increment quota!
	replayJob := testSettlingBatchImageJob("imgbatch_inv_replay")
	replayJob.UserID = 11
	settleRepo.jobs[replayJob.BatchID] = replayJob
	settleBilling.alreadyApplied = map[string]bool{BatchImageCaptureRequestID(replayJob.BatchID): true}
	_, err = settleSvc.Settle(ctx, replayJob.BatchID)
	require.NoError(t, err)
	require.InDelta(t, 0.50, quotaUpdater.quotaIncrements[321], 1e-9, "idempotent replay must not double-increment PG quota")
	require.InDelta(t, 0.50, cache.quotaIncrements[321], 1e-9, "idempotent replay must not double-increment Redis quota")

	// 3. Fail (exhausted settlement)
	failJob := testSettlingBatchImageJob("imgbatch_inv_fail")
	failJob.UserID = 11
	failJob.RetryCount = batchImageSettlementMaxRetries
	errCode := "SETTLEMENT_BILLING_FAILED"
	failJob.LastErrorCode = &errCode
	settleRepo.jobs[failJob.BatchID] = failJob
	_, err = settleSvc.Settle(ctx, failJob.BatchID)
	require.ErrorIs(t, err, ErrBatchImageSettlementBillingFailed)
	require.Len(t, cache.balanceInvalidations, 5, "failExhaustedSettlement must invalidate billing:balance:{uid}")

	// 4. Processor releaseTerminalHold
	proc := &BatchImageProviderProcessor{
		Repo:         repo,
		BillingRepo:  &fakeBatchImageBillingRepo{},
		BillingCache: billingCache,
		AuthCache:    &fakeBatchImageAuthCacheInvalidator{},
	}
	apiKeyID := int64(22)
	holdAmt := 0.50
	require.NoError(t, proc.releaseTerminalHold(ctx, &BatchImageJob{
		BatchID: "imgbatch_term", UserID: 11, APIKeyID: &apiKeyID, Status: BatchImageJobStatusFailed, HoldAmount: &holdAmt,
	}))
	require.Len(t, cache.balanceInvalidations, 6, "releaseTerminalHold must invalidate billing:balance:{uid}")
}

func TestBatchImageSubmit_EnforcesAPIKeyQuotaOutstandingHoldsAndRateLimits(t *testing.T) {
	ctx := context.Background()
	cache := newLiveP0BillingCacheStub(100)
	cfg := &config.Config{RunMode: config.RunModeStandard}
	billingCache := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)

	svc, _, _, _, _ := newTestBatchImagePublicService(true)
	svc.BillingCache = billingCache

	// Unit price = 0.25, holdMultiplier = 0.6 -> HoldAmount per 1-item batch = 0.15
	req := BatchImageSubmitRequest{
		Model: "gemini-2.5-flash-image",
		Items: []BatchImageSubmitItem{{CustomID: "c1", Prompt: "dog"}},
	}

	t.Run("expired key rejected", func(t *testing.T) {
		past := time.Now().Add(-time.Hour)
		owner := testBatchImageOwner()
		owner.APIKey = &APIKey{ID: 22, UserID: 11, Status: StatusAPIKeyActive, ExpiresAt: &past}
		_, err := svc.Submit(ctx, owner, req, "")
		require.ErrorIs(t, err, ErrAPIKeyExpired)
	})

	t.Run("quota exhausted by outstanding unsettled hold rejected", func(t *testing.T) {
		svc2, _, _, _, _ := newTestBatchImagePublicService(true)
		svc2.BillingCache = billingCache
		owner := testBatchImageOwner()
		owner.APIKey = &APIKey{ID: 22, UserID: 11, Status: StatusAPIKeyActive, Quota: 0.20, QuotaUsed: 0}

		// First submit holds 0.15 <= 0.20 -> allowed.
		_, err := svc2.Submit(ctx, owner, req, "")
		require.NoError(t, err)

		// Second submit needs 0.15 + outstanding 0.15 = 0.30 > 0.20 -> rejected!
		_, err = svc2.Submit(ctx, owner, req, "")
		require.ErrorIs(t, err, ErrAPIKeyQuotaExhausted)
	})

	t.Run("rate limit 5h exceeded rejected", func(t *testing.T) {
		now := time.Now()
		owner := testBatchImageOwner()
		owner.APIKey = &APIKey{
			ID: 23, UserID: 11, Status: StatusAPIKeyActive,
			RateLimit5h: 0.10, Usage5h: 0.0, Window5hStart: &now,
		}
		_, err := svc.Submit(ctx, owner, req, "")
		require.ErrorIs(t, err, ErrAPIKeyRateLimit5hExceeded)
	})

	t.Run("concurrent submit guarded by Redis apikey quota inflight", func(t *testing.T) {
		svc3, _, _, _, _ := newTestBatchImagePublicService(true)
		svc3.BillingCache = billingCache
		// Simulate an in-flight quota reservation already holding 0.10 of 0.20 quota.
		cache.quotaSeeded[24] = true
		cache.quotaUsed[24] = 0
		cache.quotaInflight[24] = map[string]float64{"other-req": 0.10}

		owner := testBatchImageOwner()
		owner.APIKeyID = 24
		owner.APIKey = &APIKey{ID: 24, UserID: 11, Status: StatusAPIKeyActive, Quota: 0.20, QuotaUsed: 0}
		_, err := svc3.Submit(ctx, owner, req, "")
		require.ErrorIs(t, err, ErrAPIKeyQuotaExhausted)
	})
}

// --- Fix 5 (Service): Seedance Conservative Freeze + Media Worker Backoff & Poison Pill Cap ---

func TestSeedanceTokenBilledVideo_ReservesConservativeHoldAndBlocksLowBalance(t *testing.T) {
	ctx := context.Background()

	t.Run("conservative reserve scales with resolution and duration and defaults unspecified to 720p/10s", func(t *testing.T) {
		require.Equal(t, float64(11_000*5), seedanceTokenBillingReserveTokens("480p", 5))
		require.Equal(t, float64(24_000*5), seedanceTokenBillingReserveTokens("720p", 5))
		require.Equal(t, float64(50_000*5), seedanceTokenBillingReserveTokens("1080p", 5))
		require.Equal(t, float64(24_000*10), seedanceTokenBillingReserveTokens("", -1), "empty resolution and auto duration must reserve 720p * 10s, never 480p * 8s")
		require.Equal(t, float64(24_000*5), seedanceTokenBillingReserveTokens("4k", 5), "unknown resolution must conservatively default to 720p")
		require.Less(t, seedanceTokenBillingReserveTokens("480p", 5), seedanceTokenBillingReserveTokens("720p", 5))
		require.Less(t, seedanceTokenBillingReserveTokens("720p", 5), seedanceTokenBillingReserveTokens("1080p", 5))
		require.Less(t, seedanceTokenBillingReserveTokens("720p", 5), seedanceTokenBillingReserveTokens("720p", 10))
	})

	t.Run("inflight video estimation uses Seedance token reserve before generic CalculateVideoCost", func(t *testing.T) {
		cfg := &config.Config{RunMode: config.RunModeStandard}
		cfg.Default.RateMultiplier = 1
		groupID := int64(17)
		group := &Group{
			ID:                   groupID,
			Platform:             PlatformOpenAI,
			Status:               StatusActive,
			Hydrated:             true,
			RateMultiplier:       1,
			VideoRateIndependent: true,
			VideoRateMultiplier:  1,
			// Unrelated cheap video price that must NOT mask token-billed Seedance.
			VideoModelPrices: map[string]map[string]float64{
				VideoPriceFamilyGrokImagineVideo: {"480p": 0.001, "720p": 0.001},
			},
		}
		key := &APIKey{ID: 71, UserID: 42, User: &User{ID: 42}, GroupID: &groupID, Group: group}
		resolver := newOpenAITokenImageChannelPricingResolverForTest(t, groupID, "seedance-test")
		deps := inflightEstimateDeps{
			cfg:      cfg,
			billing:  NewBillingService(cfg, nil),
			resolver: resolver,
		}
		cost, priced := deps.estimate(ctx, key, InflightEstimateRequest{
			Model:                "seedance-test",
			Kind:                 InflightEstimateVideo,
			Units:                1,
			VideoProvider:        VideoProviderSeedance,
			VideoResolution:      "",
			VideoDurationSeconds: -1,
		})
		require.True(t, priced)
		// 24_000 * 10s = 240_000 tokens * $15/1M = $3.60 (not masked by $0.001/s video price).
		require.InDelta(t, 240_000*0.000015, cost, 1e-9)
	})

	t.Run("$0.01 balance is rejected before upstream submission", func(t *testing.T) {
		svc, repo, input, pending := newMediaBillingServiceTest(t)
		repo.balance = 0.01
		input.User.Balance = 0.01
		input.Account.Platform = PlatformOpenAI
		input.Result.Model, input.Result.BillingModel = "seedance-test", "seedance-test"
		input.APIKey.Group.Platform = PlatformOpenAI
		svc.resolver = newOpenAITokenImageChannelPricingResolverForTest(t, pending.GroupID, "seedance-test")
		pending.Provider, pending.Model, pending.BillingModel = VideoProviderSeedance, "seedance-test", "seedance-test"
		pending.VideoResolution = "720p"
		pending.VideoDurationSeconds = 5

		job, err := svc.CreateGrokVideoBillingJob(ctx, input, pending)
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.Nil(t, job)
		require.InDelta(t, 0.01, repo.balance, 1e-9, "rejected job must not deduct balance")
	})
}

func TestMediaBillingWorker_ExponentialBackoffAndAbandonsPoisonPill(t *testing.T) {
	// 1. Verify exponential backoff schedule for settlement retries (and preserved polling backoff).
	wantSettlement := []time.Duration{
		1 * time.Minute,
		2 * time.Minute,
		5 * time.Minute,
		15 * time.Minute,
		30 * time.Minute,
		60 * time.Minute,
		60 * time.Minute,
	}
	for attempts, want := range wantSettlement {
		job := &MediaBillingJob{Kind: MediaBillingKindGrokVideo, Status: MediaBillingReady, Intent: &MediaBillingIntent{}, Attempts: attempts}
		require.Equal(t, want, mediaBillingRecoveryDelay(job, ErrBalanceWithholdingFailed), "attempts=%d", attempts)
	}
	// Polling schedule remains 1m, 2m, 4m, 8m, 15m.
	pollJob := &MediaBillingJob{Kind: MediaBillingKindGrokVideo, Status: MediaBillingSubmitted, Intent: nil, Attempts: 2}
	require.Equal(t, 4*time.Minute, mediaBillingRecoveryDelay(pollJob, errors.New("503")))

	// 2. Verify MediaBillingBilled is NEVER abandoned (money already deducted)
	// and generic transient DB/network errors NEVER permanently abandon paid revenue.
	billedJob := &MediaBillingJob{Status: MediaBillingBilled, Intent: &MediaBillingIntent{}, Attempts: 50}
	require.False(t, shouldAbandonMediaBillingSettlement(billedJob, ErrBalanceWithholdingFailed))
	transientJob := &MediaBillingJob{Status: MediaBillingReady, Intent: &MediaBillingIntent{}, Attempts: 50}
	require.False(t, shouldAbandonMediaBillingSettlement(transientJob, errors.New("temporary pg disconnect")),
		"generic transient DB/network errors must remain on bounded 60m backoff instead of abandoning revenue")
	require.Equal(t, 60*time.Minute, mediaBillingRecoveryDelay(transientJob, errors.New("temporary pg disconnect")))

	// 3. Verify poison pill hitting ErrBalanceWithholdingFailed transitions to
	// MediaBillingReconciliation (NOT MediaBillingFailed) with 24h cooldown,
	// leaves the worker hot-loop, and STILL lets a user who tops up settle later.
	ctx := context.Background()
	memRepo := &mediaJobMemoryRepo{jobs: map[string]*MediaBillingJob{}, balance: 100}
	wrapper := &liveP0MediaRepoErrWrapper{
		mediaJobMemoryRepo: memRepo,
		applyErr:           ErrBalanceWithholdingFailed,
	}
	poison := &MediaBillingJob{
		ID:             "poison_job_1",
		Kind:           MediaBillingKindGrokVideo,
		UserID:         10,
		APIKeyID:       20,
		AccountID:      30,
		GroupID:        40,
		Status:         MediaBillingReady,
		ReservedAmount: 2.0,
		Attempts:       mediaBillingBalanceWithholdingMaxAttempts - 1, // next failure reaches cap
		AvailableAt:    time.Now().Add(-time.Minute),
		Intent: &MediaBillingIntent{
			Command: UsageBillingCommand{RequestID: "grok-video:poison", UserID: 10, APIKeyID: 20, AccountID: 30, BalanceCost: 5},
			Usage:   UsageLog{RequestID: "grok-video:poison", UserID: 10, APIKeyID: 20, AccountID: 30, ActualCost: 5},
		},
	}
	memRepo.jobs[poison.ID] = poison

	svc := &OpenAIGatewayService{
		cfg:              &config.Config{RunMode: config.RunModeStandard},
		mediaBillingJobs: wrapper,
		usageLogRepo:     &mediaUsageMemoryRepo{},
	}
	svc.recoverMediaBillingJobs(ctx)

	require.Equal(t, []string{"poison_job_1"}, wrapper.abandonedIDs)
	require.Empty(t, wrapper.retriedDelays, "quarantined poison job must not be re-queued into 60s loop")
	require.Equal(t, MediaBillingReconciliation, memRepo.jobs[poison.ID].Status)
	require.True(t, memRepo.jobs[poison.ID].AvailableAt.After(time.Now().Add(23*time.Hour)))

	// After the user tops up their balance, SettleGrokVideoBillingJob must still
	// settle the quarantined reconciliation job instead of returning "already refunded".
	wrapper.applyErr = nil
	reconciled, err := memRepo.GetMediaBillingJobByID(ctx, poison.ID)
	require.NoError(t, err)
	require.NoError(t, svc.SettleGrokVideoBillingJob(ctx, reconciled, &OpenAIForwardResult{VideoCount: 1, VideoDurationSeconds: 5}))
	require.Equal(t, MediaBillingDone, memRepo.jobs[poison.ID].Status)
	require.Equal(t, 1, memRepo.applied)
	require.InDelta(t, 97.0, memRepo.balance, 1e-9, "balance must capture net (5.0 - 2.0 hold)")

	// 4. Before cap, job is retried with exponential backoff (not abandoned).
	wrapper.applyErr = ErrBalanceWithholdingFailed
	poison2 := &MediaBillingJob{
		ID:          "poison_job_backoff",
		Kind:        MediaBillingKindGrokVideo,
		UserID:      10,
		APIKeyID:    20,
		AccountID:   30,
		GroupID:     40,
		Status:      MediaBillingReady,
		Attempts:    2, // expects 5m delay
		AvailableAt: time.Now().Add(-time.Minute),
		Intent:      poison.Intent,
	}
	memRepo.jobs[poison2.ID] = poison2
	svc.recoverMediaBillingJobs(ctx)
	require.Equal(t, MediaBillingReady, memRepo.jobs[poison2.ID].Status)
	require.Equal(t, []time.Duration{5 * time.Minute}, wrapper.retriedDelays)

	// 5. Video that polled 20 times resets Attempts=0 when moving into ready, so
	// a same-tick settlement failure starts at backoff step 0 (1m) and is NOT abandoned.
	svcPoll, repoPoll, inputPoll, pendingPoll := newMediaBillingServiceTest(t)
	createdJob, err := svcPoll.CreateGrokVideoBillingJob(ctx, inputPoll, pendingPoll)
	require.NoError(t, err)
	require.NoError(t, svcPoll.SubmitGrokVideoBillingJob(ctx, createdJob, "task_polled_20", pendingPoll))
	repoPoll.jobs[createdJob.ID].Attempts = 20
	pollWrapper := &liveP0MediaRepoErrWrapper{
		mediaJobMemoryRepo: repoPoll,
		applyErr:           ErrBalanceWithholdingFailed,
	}
	svcPoll.mediaBillingJobs = pollWrapper
	polledJob, err := repoPoll.GetMediaBillingJobByID(ctx, createdJob.ID)
	require.NoError(t, err)
	require.Equal(t, 20, polledJob.Attempts)
	err = svcPoll.SettleGrokVideoBillingJob(ctx, polledJob, &OpenAIForwardResult{VideoCount: 1, VideoDurationSeconds: 6})
	require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
	require.Equal(t, MediaBillingReady, polledJob.Status)
	require.Equal(t, 0, polledJob.Attempts, "in-memory Attempts must reset to 0 on PrepareMediaBillingIntent")
	require.Equal(t, 0, repoPoll.jobs[createdJob.ID].Attempts, "stored Attempts must reset to 0 on PrepareMediaBillingIntent")
	require.False(t, shouldAbandonMediaBillingSettlement(polledJob, err), "first settlement failure after 20 polls must not abandon")
	require.Equal(t, 1*time.Minute, mediaBillingRecoveryDelay(polledJob, err), "first settlement failure must start at 1m backoff")
}

func TestSettleMediaBillingJob_UpdatesQuotaCacheBeforePreauthFinalizeFailure(t *testing.T) {
	cache := newLiveP0BillingCacheStub(100)
	cfg := &config.Config{RunMode: config.RunModeStandard}
	billingCache := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	authInv := &liveP0AuthInvalidatorStub{byIDResult: true}

	memRepo := &mediaJobMemoryRepo{jobs: map[string]*MediaBillingJob{}, balance: 100}
	wrapper := &liveP0MediaRepoErrWrapper{
		mediaJobMemoryRepo: memRepo,
		applyRes:           &UsageBillingApplyResult{Applied: true, APIKeyQuotaExhausted: true},
	}
	// usageBillingRepo is nil -> RecoverBalancePreauthorization fails during Finalize!
	svc := &OpenAIGatewayService{
		cfg:                 cfg,
		mediaBillingJobs:    wrapper,
		usageLogRepo:        &mediaUsageMemoryRepo{},
		billingCacheService: billingCache,
	}
	svc.SetAuthCacheInvalidator(authInv)

	job := &MediaBillingJob{
		ID:        "media_preauth_finalize_fail",
		Kind:      MediaBillingKindImage,
		UserID:    42,
		APIKeyID:  78,
		AccountID: 9,
		GroupID:   11,
		Status:    MediaBillingReady,
		Intent: &MediaBillingIntent{
			Command: UsageBillingCommand{
				RequestID:            "image:finalize-fail",
				UserID:               42,
				APIKeyID:             78,
				AccountID:            9,
				BalanceCost:          0.75,
				APIKeyQuotaCost:      0.75,
				BalancePreauthorized: true,
			},
			Usage: UsageLog{
				RequestID:  "image:finalize-fail",
				UserID:     42,
				APIKeyID:   78,
				AccountID:  9,
				ActualCost: 0.75,
				TotalCost:  0.75,
			},
		},
	}
	memRepo.jobs[job.ID] = job

	err := svc.settleMediaBillingJob(context.Background(), job, nil)
	require.Error(t, err, "expected preauth recovery/finalize failure when usageBillingRepo is nil")
	require.Equal(t, MediaBillingBilled, job.Status, "job status must advance to billed once Apply commits")
	require.InDelta(t, 0.75, cache.quotaIncrements[78], 1e-9, "Redis apikey:quota must update BEFORE Finalize can fail")
	require.Equal(t, []int64{78}, authInv.evictedKeys, "exhausted key auth cache must evict BEFORE Finalize can fail")
}

type liveP0BatchOrderRecorder struct {
	mu               sync.Mutex
	events           []string
	quotaFailOnce    bool
	rateFailOnce     bool
	quotaCalls       int
	rateCalls        int
	quotaTotal       float64
	rateTotal        float64
	lastAuthCtxErr   error
	authCtxHasExpire bool
}

func (r *liveP0BatchOrderRecorder) UpdateQuotaUsed(_ context.Context, _ int64, cost float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.quotaCalls++
	r.events = append(r.events, "pg_quota")
	if r.quotaFailOnce && r.quotaCalls == 1 {
		return errors.New("transient pg quota error")
	}
	r.quotaTotal += cost
	return nil
}

func (r *liveP0BatchOrderRecorder) UpdateRateLimitUsage(_ context.Context, _ int64, cost float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rateCalls++
	r.events = append(r.events, "pg_rate")
	if r.rateFailOnce && r.rateCalls == 1 {
		return errors.New("transient pg rate error")
	}
	r.rateTotal += cost
	return nil
}

func (r *liveP0BatchOrderRecorder) InvalidateAuthCacheByKey(context.Context, string)      {}
func (r *liveP0BatchOrderRecorder) InvalidateAuthCacheByHash(context.Context, string)     {}
func (r *liveP0BatchOrderRecorder) InvalidateAuthCacheForAPIKey(context.Context, *APIKey) {}
func (r *liveP0BatchOrderRecorder) InvalidateAuthCacheByGroupID(context.Context, int64)   {}
func (r *liveP0BatchOrderRecorder) InvalidateAuthCacheByUserID(ctx context.Context, _ int64) {
	r.mu.Lock()
	r.events = append(r.events, "auth_invalidate")
	r.lastAuthCtxErr = ctx.Err()
	_, r.authCtxHasExpire = ctx.Deadline()
	r.mu.Unlock()
}

type liveP0InspectingBatchProvider struct {
	BatchImageProvider
	onSubmit func()
}

func (p *liveP0InspectingBatchProvider) Submit(ctx context.Context, job *BatchImageJob, account *Account, input BatchImageInput) (*BatchProviderJob, error) {
	if p.onSubmit != nil {
		p.onSubmit()
	}
	return p.BatchImageProvider.Submit(ctx, job, account, input)
}

func TestBatchImageHardening_FailClosedSumNoDoubleCountAndSettlementOrdering(t *testing.T) {
	ctx := context.Background()
	cache := newLiveP0BillingCacheStub(100)
	cfg := &config.Config{RunMode: config.RunModeStandard}
	billingCache := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)

	req := BatchImageSubmitRequest{
		Model: "gemini-2.5-flash-image",
		Items: []BatchImageSubmitItem{{CustomID: "c1", Prompt: "owl"}},
	}

	t.Run("SumOutstandingHoldByAPIKey failure fails closed", func(t *testing.T) {
		svc, repo, _, _, _ := newTestBatchImagePublicService(true)
		svc.BillingCache = billingCache
		repo.sumHoldErr = errors.New("db down")
		owner := testBatchImageOwner()
		owner.APIKey = &APIKey{ID: 22, UserID: 11, Status: StatusAPIKeyActive, Quota: 10.0}
		_, err := svc.Submit(ctx, owner, req, "")
		require.ErrorIs(t, err, ErrBillingServiceUnavailable)
	})

	t.Run("Redis quota hold released immediately after CreateBatchImageJob so concurrent submit is not double-counted", func(t *testing.T) {
		svc, _, _, _, provider := newTestBatchImagePublicService(true)
		svc.BillingCache = billingCache
		// Quota = 0.35, each 1-item batch holds 0.15.
		// Two concurrent batches need 0.30 <= 0.35; if Job A kept Redis hold during
		// provider.Submit, Job B would see 0.15 (PG) + 0.15 (Redis) + 0.15 (new) = 0.45 > 0.35 and falsely fail!
		owner := testBatchImageOwner()
		owner.APIKey = &APIKey{ID: 25, UserID: 11, Status: StatusAPIKeyActive, Quota: 0.35, QuotaUsed: 0}
		owner.APIKeyID = 25

		var concurrentErr error
		var inflightDuringSubmit float64
		var nestedCalled bool
		svc.ProviderRegistry = NewBatchImageProviderRegistry(&liveP0InspectingBatchProvider{
			BatchImageProvider: provider,
			onSubmit: func() {
				if nestedCalled {
					return
				}
				nestedCalled = true
				cache.mu.Lock()
				for _, v := range cache.quotaInflight[25] {
					inflightDuringSubmit += v
				}
				cache.mu.Unlock()
				// Submit a 2nd concurrent batch while Job A is in provider.Submit!
				_, concurrentErr = svc.Submit(ctx, owner, req, "")
			},
		})

		_, err := svc.Submit(ctx, owner, req, "")
		require.NoError(t, err)
		require.Zero(t, inflightDuringSubmit, "temporary Redis quota hold must be released once durable PG row exists")
		require.NoError(t, concurrentErr, "concurrent 2nd batch must NOT see double-counted hold for Job A")
	})

	t.Run("settlement updates PG quota & rate limit with retry BEFORE invalidating auth cache using detached context", func(t *testing.T) {
		rec := &liveP0BatchOrderRecorder{quotaFailOnce: true, rateFailOnce: true}
		settleRepo := newFakeBatchImageRepository()
		job := testSettlingBatchImageJob("imgbatch_order_check")
		job.UserID = 11
		job.SuccessCount = 2
		job.FailCount = 0
		job.ItemCount = 2
		settleRepo.jobs[job.BatchID] = job
		settleSvc := &BatchImageSettlementService{
			Repo:               settleRepo,
			BillingRepo:        &fakeBatchImageBillingRepo{},
			Pricing:            &fakeBatchImagePricingResolver{unitPrice: 0.25},
			BillingCache:       billingCache,
			APIKeyQuotaUpdater: rec,
			AuthCache:          rec,
		}
		_, err := settleSvc.Settle(ctx, job.BatchID)
		require.NoError(t, err)
		require.Equal(t, 2, rec.quotaCalls, "transient PG quota update error must retry")
		require.Equal(t, 2, rec.rateCalls, "transient PG rate-limit update error must retry")
		require.InDelta(t, 0.50, rec.quotaTotal, 1e-9, "must not double-apply quota")
		require.InDelta(t, 0.50, rec.rateTotal, 1e-9, "must not double-apply rate limit")
		require.Equal(t, []string{"pg_quota", "pg_quota", "pg_rate", "pg_rate", "auth_invalidate"}, rec.events,
			"PG quota & rate-limit updates must finish BEFORE auth cache invalidation")

		// Verify detached context on canceled caller ctx (P2-2).
		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()
		invalidateBatchImageUserBalanceAndAuthCache(canceledCtx, billingCache, rec, 11)
		require.NoError(t, rec.lastAuthCtxErr, "auth cache invalidation must use context.WithoutCancel")
		require.True(t, rec.authCtxHasExpire, "auth cache invalidation must have its own timeout")
	})
}

type liveP0RateLimitLoaderStub struct {
	data *APIKeyRateLimitData
}

func (l *liveP0RateLimitLoaderStub) GetRateLimitData(context.Context, int64) (*APIKeyRateLimitData, error) {
	cp := *l.data
	return &cp, nil
}

type liveP0FallbackCtxCheckStub struct {
	byIDCtx   context.Context
	byUserErr error
	byIDEnded bool
	hasLimit  bool
}

func (s *liveP0FallbackCtxCheckStub) InvalidateAuthCacheByKey(context.Context, string)      {}
func (s *liveP0FallbackCtxCheckStub) InvalidateAuthCacheByHash(context.Context, string)     {}
func (s *liveP0FallbackCtxCheckStub) InvalidateAuthCacheForAPIKey(context.Context, *APIKey) {}
func (s *liveP0FallbackCtxCheckStub) InvalidateAuthCacheByGroupID(context.Context, int64)   {}
func (s *liveP0FallbackCtxCheckStub) InvalidateAuthCacheByAPIKeyID(ctx context.Context, _ int64) bool {
	s.byIDCtx = ctx
	return false
}
func (s *liveP0FallbackCtxCheckStub) InvalidateAuthCacheByUserID(ctx context.Context, _ int64) {
	s.byIDEnded = s.byIDCtx != nil && s.byIDCtx.Err() != nil
	s.byUserErr = ctx.Err()
	_, s.hasLimit = ctx.Deadline()
}

func TestCodeRabbitQuickWins_NoRedisClobberFreshFallbackAndAttachAcquireRace(t *testing.T) {
	t.Run("CheckAPIKeyChargeEligibility does not clobber fresher Redis rate-limit counters with lagging PG snapshot", func(t *testing.T) {
		now := time.Now()
		cache := newLiveP0BillingCacheStub(100)
		// Redis has fresher 5h usage = 4.5 (near limit 5.0), while PG is lagging at 1.0.
		cache.rateLimitData[901] = &APIKeyRateLimitCacheData{
			Usage5h:  4.5,
			Window5h: now.Unix(),
		}
		loader := &liveP0RateLimitLoaderStub{
			data: &APIKeyRateLimitData{
				QuotaUsed:     1.0,
				Usage5h:       1.0,
				Window5hStart: &now,
			},
		}
		cfg := &config.Config{RunMode: config.RunModeStandard}
		svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
		t.Cleanup(svc.Stop)
		svc.apiKeyRateLimitLoader = loader

		key := &APIKey{
			ID:            901,
			UserID:        11,
			Status:        StatusAPIKeyActive,
			Quota:         10.0,
			RateLimit5h:   5.0,
			Usage5h:       1.0,
			Window5hStart: &now,
		}
		// Projected +1.0 exceeds fresher Redis 4.5 + 1.0 = 5.5 > 5.0 -> MUST reject!
		err := svc.CheckAPIKeyChargeEligibility(context.Background(), key, 1.0)
		require.ErrorIs(t, err, ErrAPIKeyRateLimit5hExceeded)
		// Redis must STILL hold 4.5, never overwritten with lagging DB 1.0.
		require.InDelta(t, 4.5, cache.rateLimitData[901].Usage5h, 1e-9)
	})

	t.Run("invalidateExhaustedAPIKeyAuthCache gives user fallback its own fresh timeout", func(t *testing.T) {
		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()
		fallbackCheck := &liveP0FallbackCtxCheckStub{}
		invalidateExhaustedAPIKeyAuthCache(canceledCtx, fallbackCheck, 42, 77)
		require.True(t, fallbackCheck.byIDEnded, "by-ID context must be canceled before fallback runs")
		require.NoError(t, fallbackCheck.byUserErr, "user fallback must receive its own fresh non-canceled context")
		require.True(t, fallbackCheck.hasLimit, "user fallback must have its own timeout deadline")
	})

	t.Run("InflightReservation Attach after Acquire keeps extra alive until active Acquire done() runs", func(t *testing.T) {
		cache := newLiveP0BillingCacheStub(100)
		primary := &InflightReservation{cache: cache, userID: 1, requestID: "primary", amount: 1.0, ttl: time.Minute}
		primary.refs.Store(1)
		cache.balanceInflight["primary"] = 1.0

		extra := &InflightReservation{cache: cache, userID: 1, requestID: "extra", amount: 2.0, ttl: time.Minute}
		extra.refs.Store(1)
		cache.balanceInflight["extra"] = 2.0

		// Acquire BEFORE Attach (previously caused HandlerDone to immediately release extra!).
		done := primary.Acquire()
		primary.Attach(extra)
		primary.HandlerDone()

		cache.mu.Lock()
		_, primaryAlive := cache.balanceInflight["primary"]
		_, extraAlive := cache.balanceInflight["extra"]
		cache.mu.Unlock()
		require.True(t, primaryAlive, "primary must remain held while Acquire is active")
		require.True(t, extraAlive, "extra attached after Acquire must NOT be released early by HandlerDone")

		done()
		cache.mu.Lock()
		_, primaryAfter := cache.balanceInflight["primary"]
		_, extraAfter := cache.balanceInflight["extra"]
		cache.mu.Unlock()
		require.False(t, primaryAfter)
		require.False(t, extraAfter, "extra must release once last Acquire done() drops primary refs to 0")
	})
}

func liveP0Int64Ptr(v int64) *int64 { return &v }
