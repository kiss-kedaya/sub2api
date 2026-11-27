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

func (r *liveP0MediaRepoErrWrapper) AbandonMediaBillingJob(ctx context.Context, id string) error {
	r.mu.Lock()
	r.abandonedIDs = append(r.abandonedIDs, id)
	r.mu.Unlock()
	return r.mediaJobMemoryRepo.AbandonMediaBillingJob(ctx, id)
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
				cfg:                  cfg,
				mediaBillingJobs:     wrapper,
				usageLogRepo:         usageRepo,
				billingCacheService:  billingCache,
				authCacheInvalidator: authInv,
			}

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
				cfg:                  cfg,
				usageBillingRepo:     billingRepo,
				usageLogRepo:         usageRepo,
				billingCacheService:  billingCache,
				authCacheInvalidator: authInv,
			}
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

	t.Run("conservative reserve scales with resolution and duration", func(t *testing.T) {
		require.Less(t, seedanceTokenBillingReserveTokens("480p", 5), seedanceTokenBillingReserveTokens("720p", 5))
		require.Less(t, seedanceTokenBillingReserveTokens("720p", 5), seedanceTokenBillingReserveTokens("1080p", 5))
		require.Less(t, seedanceTokenBillingReserveTokens("720p", 5), seedanceTokenBillingReserveTokens("720p", 10))
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

	// 2. Verify MediaBillingBilled is NEVER abandoned (money already deducted).
	billedJob := &MediaBillingJob{Status: MediaBillingBilled, Intent: &MediaBillingIntent{}, Attempts: 50}
	require.False(t, shouldAbandonMediaBillingSettlement(billedJob, ErrBalanceWithholdingFailed))

	// 3. Verify poison pill hitting ErrBalanceWithholdingFailed transitions to failed after cap
	// and never starves healthy jobs.
	ctx := context.Background()
	memRepo := &mediaJobMemoryRepo{jobs: map[string]*MediaBillingJob{}, balance: 100}
	wrapper := &liveP0MediaRepoErrWrapper{
		mediaJobMemoryRepo: memRepo,
		applyErr:           ErrBalanceWithholdingFailed,
	}
	poison := &MediaBillingJob{
		ID:          "poison_job_1",
		Kind:        MediaBillingKindGrokVideo,
		UserID:      10,
		APIKeyID:    20,
		AccountID:   30,
		GroupID:     40,
		Status:      MediaBillingReady,
		Attempts:    mediaBillingBalanceWithholdingMaxAttempts - 1, // next failure reaches cap
		AvailableAt: time.Now().Add(-time.Minute),
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
	require.Empty(t, wrapper.retriedDelays, "abandoned poison job must not be re-queued")
	require.Equal(t, MediaBillingFailed, memRepo.jobs[poison.ID].Status)

	// Before cap, job is retried with exponential backoff (not abandoned).
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
}

func liveP0Int64Ptr(v int64) *int64 { return &v }
