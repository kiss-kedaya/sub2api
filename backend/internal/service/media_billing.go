package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type preparedMediaUsageKey struct{}
type asyncImageTaskIDKey struct{}

var ErrMediaBillingPending = errors.New("media billing is pending settlement")

func ContextWithAsyncImageTaskID(ctx context.Context, taskID string) context.Context {
	return context.WithValue(ctx, asyncImageTaskIDKey{}, strings.TrimSpace(taskID))
}

func (s *OpenAIGatewayService) IsImageTaskBillingComplete(ctx context.Context, taskID string) (bool, error) {
	if !s.HasDurableMediaBilling() {
		return false, nil
	}
	job, err := s.mediaBillingJobs.GetMediaBillingJobByID(ctx, "image:"+strings.TrimSpace(taskID))
	if errors.Is(err, ErrMediaBillingJobNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return job.Status == MediaBillingDone, nil
}

type preparedMediaUsage struct{ intent *MediaBillingIntent }

func capturePreparedMediaUsage(ctx context.Context, usage *UsageLog, input *OpenAIRecordUsageInput, cost *CostBreakdown, accountRate float64, subscriptionBill bool) bool {
	if ctx == nil {
		return false
	}
	capture, ok := ctx.Value(preparedMediaUsageKey{}).(*preparedMediaUsage)
	if !ok {
		return false
	}
	command := buildUsageBillingCommand(usage.RequestID, usage, &postUsageBillingParams{
		Cost: cost, APIKey: input.APIKey, User: input.User, Account: input.Account,
		Subscription: input.Subscription, IsSubscriptionBill: subscriptionBill,
		AccountRateMultiplier: accountRate, APIKeyService: input.APIKeyService,
		RequestPayloadHash: input.RequestPayloadHash,
	})
	if command != nil {
		_, command.BalancePreauthorized = BalancePreauthorizationGuardFromContext(ctx)
		capture.intent = &MediaBillingIntent{Command: *command, Usage: *usage, QuotaPlatform: input.QuotaPlatform}
		capture.intent.Usage.User = nil
		capture.intent.Usage.APIKey = nil
		capture.intent.Usage.Account = nil
		capture.intent.Usage.Group = nil
		capture.intent.Usage.Subscription = nil
		if cost != nil {
			amounts := &capture.intent.RawAmounts
			if subscriptionBill {
				amounts[1] = cost.ActualCost
			} else {
				amounts[0] = cost.ActualCost
			}
			if input.APIKeyService != nil && input.APIKey.Quota > 0 {
				amounts[2] = cost.ActualCost
			}
			if input.APIKeyService != nil && input.APIKey.HasRateLimits() {
				amounts[3] = cost.ActualCost
			}
			if input.Account.IsAPIKeyOrBedrock() && input.Account.HasAnyQuotaLimit() {
				amounts[4] = cost.TotalCost * accountRate
			}
		}
	}
	return true
}

func (s *OpenAIGatewayService) prepareMediaUsage(ctx context.Context, input *OpenAIRecordUsageInput) (*MediaBillingIntent, error) {
	capture := &preparedMediaUsage{}
	if err := s.RecordUsage(context.WithValue(ctx, preparedMediaUsageKey{}, capture), input); err != nil {
		return nil, err
	}
	if capture.intent == nil {
		return nil, errors.New("media usage could not be prepared")
	}
	return capture.intent, nil
}

func (s *OpenAIGatewayService) HasDurableMediaBilling() bool {
	return s != nil && s.mediaBillingJobs != nil && (s.cfg == nil || s.cfg.RunMode != config.RunModeSimple)
}

// RecordMediaUsage makes the immutable charge recoverable before settling it.
// The caller must not publish an asynchronous result until this returns nil.
func (s *OpenAIGatewayService) RecordMediaUsage(ctx context.Context, input *OpenAIRecordUsageInput) error {
	if !s.HasDurableMediaBilling() {
		return s.RecordUsage(ctx, input)
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	intent, err := s.prepareMediaUsage(ctx, input)
	if err != nil {
		return err
	}
	job := &MediaBillingJob{ID: "media_" + HashUsageRequestPayload([]byte(fmt.Sprintf("%d:%s", intent.Command.APIKeyID, intent.Command.RequestID))), Kind: MediaBillingKindImage,
		UserID: intent.Command.UserID, APIKeyID: intent.Command.APIKeyID, AccountID: intent.Command.AccountID,
		GroupID: derefGroupID(intent.Usage.GroupID), SubscriptionID: intent.Command.SubscriptionID,
		Status: MediaBillingReady, Intent: intent, CreatedAt: time.Now(), AvailableAt: time.Now()}
	if taskID, ok := ctx.Value(asyncImageTaskIDKey{}).(string); ok && taskID != "" {
		job.ID = "image:" + taskID
	}
	job, err = s.mediaBillingJobs.CreateMediaBillingJob(ctx, job)
	if err != nil {
		return err
	}
	var owned *BalancePreauthorizationGuard
	if guard, ok := BalancePreauthorizationGuardFromContext(ctx); ok {
		var transferred bool
		owned, transferred = guard.TransferToWorker()
		if !transferred {
			return ErrBalancePreauthorizationOwnershipTransferred
		}
	}
	if err := s.settleMediaBillingJob(ctx, job, owned); err != nil {
		return errors.Join(ErrMediaBillingPending, err)
	}
	return nil
}

func (s *OpenAIGatewayService) settleMediaBillingJob(ctx context.Context, job *MediaBillingJob, guard *BalancePreauthorizationGuard) error {
	if job == nil || job.Intent == nil {
		return errors.New("media settlement intent is missing")
	}
	result, err := s.mediaBillingJobs.ApplyMediaBillingJob(ctx, job.ID)
	if err != nil {
		return err
	}
	intent := job.Intent
	cmd := &intent.Command
	if cmd.BalancePreauthorized {
		if guard != nil {
			err = guard.Finalize(ctx, cmd.BalanceCost, cmd.RequestFingerprint)
		} else {
			preauthorizer := NewBalancePreauthorizationService(s.cfg, s.billingService, s.billingCacheService, s.usageBillingRepo)
			err = preauthorizer.RecoverBalancePreauthorization(ctx, BalancePreauthorizationRecord{
				RequestID: cmd.RequestID, APIKeyID: cmd.APIKeyID, UserID: cmd.UserID,
				RequestFingerprint: cmd.RequestFingerprint, Amount: cmd.BalanceCost, Status: BalanceSettlementFinalizationPending,
			})
		}
		if err != nil {
			return err
		}
	}
	if s.billingCacheService != nil {
		if result != nil && result.NewBalance != nil && s.billingCacheService.balanceBelowEligibilityThreshold(*result.NewBalance) {
			s.billingCacheService.setBalanceCacheAtMost(ctx, cmd.UserID, *result.NewBalance)
		} else if !cmd.BalancePreauthorized {
			_ = s.billingCacheService.InvalidateUserBalance(ctx, cmd.UserID)
		}
		if cmd.SubscriptionID != nil {
			_ = s.billingCacheService.InvalidateSubscription(ctx, cmd.UserID, job.GroupID)
			if result != nil && result.Applied && cmd.APIKeyRateLimitCost > 0 {
				s.billingCacheService.QueueUpdateAPIKeyRateLimitUsage(cmd.APIKeyID, cmd.APIKeyRateLimitCost)
			}
		}
	}
	if result != nil && result.Applied {
		key := &APIKey{ID: cmd.APIKeyID, UserID: cmd.UserID, GroupID: &job.GroupID}
		if cmd.APIKeyRateLimitCost > 0 {
			key.RateLimit5h = 1
		}
		params := &postUsageBillingParams{Cost: &CostBreakdown{ActualCost: intent.Usage.ActualCost, TotalCost: intent.Usage.TotalCost},
			User: &User{ID: cmd.UserID}, APIKey: key, Account: &Account{ID: cmd.AccountID, Type: cmd.AccountType},
			IsSubscriptionBill: cmd.SubscriptionID != nil, Platform: intent.QuotaPlatform}
		// Balance and subscription caches above are invalidated from the committed
		// ledger; do not enqueue a second subscription increment here.
		if s.billingCacheService != nil && s.deferredService != nil {
			params.IsSubscriptionBill = false
			if cmd.SubscriptionID != nil {
				params.Cost.ActualCost = 0
			}
			finalizePostUsageBilling(ctx, params, s.billingDeps(), result, true)
		}
	}
	if s.usageLogRepo == nil {
		return errors.New("media usage log repository is unavailable")
	}
	usage := intent.Usage
	if _, err := s.usageLogRepo.Create(ctx, &usage); err != nil {
		return err
	}
	return s.mediaBillingJobs.CompleteMediaBillingJob(ctx, job.ID)
}

type grokVideoBillingSnapshot struct {
	Pending   GrokVideoPendingBilling `json:"pending"`
	Unit      MediaBillingIntent      `json:"unit"`
	PerSecond bool                    `json:"per_second"`
}

func scaleVideoMediaIntent(unit MediaBillingIntent, factor float64, seconds int) MediaBillingIntent {
	i := unit
	for n, amount := range []*float64{&i.Command.BalanceCost, &i.Command.SubscriptionCost, &i.Command.APIKeyQuotaCost,
		&i.Command.APIKeyRateLimitCost, &i.Command.AccountQuotaCost} {
		*amount = QuantizeUsageBillingAmount(unit.RawAmounts[n] * factor)
	}
	for _, amount := range []*float64{&i.Usage.InputCost, &i.Usage.OutputCost, &i.Usage.ImageInputCost, &i.Usage.ImageOutputCost,
		&i.Usage.CacheCreationCost, &i.Usage.CacheReadCost, &i.Usage.TotalCost, &i.Usage.ActualCost} {
		*amount *= factor
	}
	i.Usage.ActualCost = i.Command.BalanceCost + i.Command.SubscriptionCost
	if i.Usage.AccountStatsCost != nil {
		value := *i.Usage.AccountStatsCost * factor
		i.Usage.AccountStatsCost = &value
	}
	i.Usage.VideoDurationSeconds = &seconds
	i.Command.RequestFingerprint = ""
	i.Command.Normalize()
	return i
}

func (s *OpenAIGatewayService) CreateGrokVideoBillingJob(ctx context.Context, input *OpenAIRecordUsageInput, pending GrokVideoPendingBilling) (*MediaBillingJob, error) {
	if !s.HasDurableMediaBilling() {
		return nil, nil
	}
	copyInput := *input
	result := *input.Result
	result.RequestID = "grok-video-pending:" + uuid.NewString()
	result.ResponseID = result.RequestID
	result.VideoCount, result.VideoDurationSeconds = 1, 1
	result.VideoResolution = NormalizeVideoBillingResolutionOrDefault(pending.VideoResolution)
	copyInput.Result = &result
	unit, err := s.prepareMediaUsage(ctx, &copyInput)
	if err != nil {
		return nil, err
	}
	if unit.Usage.BillingMode == nil || *unit.Usage.BillingMode == string(BillingModeToken) {
		return nil, errors.New("asynchronous video requires duration or request pricing")
	}
	result.VideoDurationSeconds = 2
	two, err := s.prepareMediaUsage(ctx, &copyInput)
	if err != nil {
		return nil, err
	}
	perSecond := two.Usage.TotalCost != unit.Usage.TotalCost || two.Usage.ActualCost != unit.Usage.ActualCost
	seconds := NormalizeVideoBillingDurationSecondsOrDefault(pending.VideoDurationSeconds)
	factor := 1.0
	if perSecond {
		factor = float64(seconds)
	}
	estimated := scaleVideoMediaIntent(*unit, factor, seconds)
	snapshot, err := json.Marshal(grokVideoBillingSnapshot{Pending: pending, Unit: *unit, PerSecond: perSecond})
	if err != nil {
		return nil, err
	}
	job := &MediaBillingJob{ID: "video_" + uuid.NewString(), Kind: MediaBillingKindGrokVideo,
		UserID: input.User.ID, APIKeyID: input.APIKey.ID, AccountID: input.Account.ID, GroupID: pending.GroupID,
		SubscriptionID: unit.Command.SubscriptionID, Status: MediaBillingCreating,
		ReservedAmount: estimated.Command.BalanceCost + estimated.Command.SubscriptionCost, Snapshot: snapshot,
		CreatedAt: time.Now(), AvailableAt: time.Now().Add(time.Minute)}
	job, err = s.mediaBillingJobs.CreateMediaBillingJob(ctx, job)
	if err == nil && s.billingCacheService != nil {
		_ = s.billingCacheService.InvalidateUserBalance(ctx, input.User.ID)
	}
	return job, err
}

func (s *OpenAIGatewayService) SubmitGrokVideoBillingJob(ctx context.Context, job *MediaBillingJob, taskID string, pending GrokVideoPendingBilling) error {
	if job == nil {
		return nil
	}
	var snapshot grokVideoBillingSnapshot
	if err := json.Unmarshal(job.Snapshot, &snapshot); err != nil {
		return err
	}
	snapshot.Pending = pending
	snapshot.Pending.DurableJobID = job.ID
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return s.mediaBillingJobs.SubmitMediaBillingJob(ctx, job.ID, taskID, payload)
}

func (s *OpenAIGatewayService) FailGrokVideoBillingJob(ctx context.Context, job *MediaBillingJob) error {
	if job == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := s.mediaBillingJobs.FailMediaBillingJob(ctx, job.ID)
	if err == nil && s.billingCacheService != nil {
		_ = s.billingCacheService.InvalidateUserBalance(ctx, job.UserID)
	}
	return err
}

func (s *OpenAIGatewayService) LoadGrokVideoBillingJob(ctx context.Context, userID, keyID int64, taskID string) (*MediaBillingJob, error) {
	if !s.HasDurableMediaBilling() {
		return nil, nil
	}
	job, err := s.mediaBillingJobs.GetMediaBillingJob(ctx, userID, keyID, taskID)
	if errors.Is(err, ErrMediaBillingJobNotFound) {
		return nil, nil
	}
	return job, err
}

func GrokVideoBillingJobPending(job *MediaBillingJob) (*GrokVideoPendingBilling, error) {
	if job == nil {
		return nil, nil
	}
	var snapshot grokVideoBillingSnapshot
	if err := json.Unmarshal(job.Snapshot, &snapshot); err != nil {
		return nil, err
	}
	pending := snapshot.Pending
	pending.DurableJobID = job.ID
	return &pending, nil
}

func (s *OpenAIGatewayService) SettleGrokVideoBillingJob(ctx context.Context, job *MediaBillingJob, result *OpenAIForwardResult) error {
	if job == nil || result == nil || result.VideoCount <= 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if job.Status == MediaBillingDone {
		return nil
	}
	if job.Status == MediaBillingFailed {
		return errors.New("video task was already refunded")
	}
	if job.Intent == nil {
		var snapshot grokVideoBillingSnapshot
		if err := json.Unmarshal(job.Snapshot, &snapshot); err != nil {
			return err
		}
		seconds := result.VideoDurationSeconds
		if seconds <= 0 {
			seconds = snapshot.Pending.VideoDurationSeconds
		}
		seconds = NormalizeVideoBillingDurationSecondsOrDefault(seconds)
		factor := 1.0
		if snapshot.PerSecond {
			factor = float64(seconds)
		}
		intent := scaleVideoMediaIntent(snapshot.Unit, factor, seconds)
		intent.Command.RequestID = StableGrokVideoBillingRequestID(job.UpstreamTaskID)
		intent.Command.RequestPayloadHash = HashUsageRequestPayload([]byte(job.UpstreamTaskID))
		intent.Command.RequestFingerprint = ""
		intent.Command.Normalize()
		intent.Usage.RequestID = intent.Command.RequestID
		intent.Usage.CreatedAt = time.Now()
		duration := int(time.Since(job.CreatedAt).Milliseconds())
		intent.Usage.DurationMs = &duration
		var err error
		job.Intent, err = s.mediaBillingJobs.PrepareMediaBillingIntent(ctx, job.ID, &intent)
		if err != nil {
			return err
		}
	}
	return s.settleMediaBillingJob(ctx, job, nil)
}

type grokMediaBeforePublishKey struct{}
type GrokMediaBeforePublish func(context.Context, *OpenAIForwardResult) error

func ContextWithGrokMediaBeforePublish(ctx context.Context, callback GrokMediaBeforePublish) context.Context {
	return context.WithValue(ctx, grokMediaBeforePublishKey{}, callback)
}

func runGrokMediaBeforePublish(ctx context.Context, result *OpenAIForwardResult) error {
	if callback, ok := ctx.Value(grokMediaBeforePublishKey{}).(GrokMediaBeforePublish); ok && callback != nil {
		if err := callback(ctx, result); err != nil {
			return ErrBillingServiceUnavailable.WithCause(err)
		}
	}
	return nil
}

func (s *OpenAIGatewayService) StartMediaBillingWorker() {
	if !s.HasDurableMediaBilling() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.mediaBillingCancel = cancel
	s.mediaBillingDone = make(chan struct{})
	go func() {
		defer close(s.mediaBillingDone)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			s.recoverMediaBillingJobs(ctx)
		}
	}()
}

func (s *OpenAIGatewayService) recoverMediaBillingJobs(ctx context.Context) {
	// Acquire each lease just before processing so slower jobs cannot exhaust
	// the leases of other jobs waiting in the same batch.
	for processed := 0; processed < 16 && ctx.Err() == nil; processed++ {
		listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		jobs, err := s.mediaBillingJobs.ListMediaBillingJobs(listCtx, 1)
		cancel()
		if err != nil {
			logger.L().Error("media_billing.list_failed", zap.Error(err))
			return
		}
		if len(jobs) == 0 {
			return
		}
		for _, job := range jobs {
			if ctx.Err() != nil {
				return
			}
			workCtx, stop := context.WithTimeout(ctx, 30*time.Second)
			if job.Intent != nil {
				err = s.settleMediaBillingJob(workCtx, job, nil)
			} else {
				err = s.pollGrokVideoBillingJob(workCtx, job)
			}
			stop()
			if err != nil {
				logger.L().Warn("media_billing.retry", zap.String("job_id", job.ID), zap.Error(err))
			}
			retryCtx, retryCancel := context.WithTimeout(ctx, 3*time.Second)
			delay := 15 * time.Second
			if err != nil {
				delay = time.Minute
			}
			_ = s.mediaBillingJobs.RetryMediaBillingJob(retryCtx, job.ID, delay)
			retryCancel()
		}
	}
}

type mediaStatusWriter struct {
	headers http.Header
	body    []byte
	status  int
}

func (w *mediaStatusWriter) Header() http.Header    { return w.headers }
func (w *mediaStatusWriter) WriteHeader(status int) { w.status = status }
func (w *mediaStatusWriter) Write(body []byte) (int, error) {
	if len(w.body)+len(body) > 1<<20 {
		return 0, errors.New("video status too large")
	}
	w.body = append(w.body, body...)
	return len(body), nil
}

func (s *OpenAIGatewayService) pollGrokVideoBillingJob(ctx context.Context, job *MediaBillingJob) error {
	if job.Kind != MediaBillingKindGrokVideo || job.UpstreamTaskID == "" {
		return errors.New("video job has no upstream task")
	}
	account, err := s.GetGrokMediaBoundAccount(ctx, job.AccountID)
	if err != nil {
		return err
	}
	writer := &mediaStatusWriter{headers: make(http.Header)}
	c, _ := gin.CreateTestContext(writer)
	c.Request, err = http.NewRequestWithContext(ctx, http.MethodGet, "/v1/videos/"+job.UpstreamTaskID, nil)
	if err != nil {
		return err
	}
	result, err := s.ForwardGrokMedia(ctx, c, account, GrokMediaEndpointVideoStatus, job.UpstreamTaskID, nil, "")
	if err != nil {
		return err
	}
	if result != nil && result.VideoCount > 0 {
		return s.SettleGrokVideoBillingJob(ctx, job, result)
	}
	var body struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(writer.body, &body) != nil {
		return errors.New("invalid video status response")
	}
	switch strings.ToLower(body.Status) {
	case "failed", "cancelled", "canceled":
		return s.FailGrokVideoBillingJob(ctx, job)
	case "expired":
		return errors.New("expired video requires billing reconciliation")
	case "pending", "processing", "running", "queued", "in_progress", "":
		return nil
	default:
		return fmt.Errorf("unrecognized video task state")
	}
}
