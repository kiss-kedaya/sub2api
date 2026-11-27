//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	"go.uber.org/zap"
)

// --- Shared real-state-machine preauthorization harness for handler tests ---

type preauthEntrypointsLedger struct {
	service.UsageBillingRepository
	mu            sync.Mutex
	walletBalance map[int64]float64
	holds         map[string]float64
	settlements   map[string]*service.BalancePreauthorizationRecord
	applied       map[string]string
	applyCalls    int
	finalizedIDs  []string
	refundedIDs   []string
	toppedUpIDs   []string
	topUpCalls    int
}

func newPreauthEntrypointsLedger(userID int64, balance float64) *preauthEntrypointsLedger {
	return &preauthEntrypointsLedger{
		walletBalance: map[int64]float64{userID: balance},
		holds:         map[string]float64{},
		settlements:   map[string]*service.BalancePreauthorizationRecord{},
		applied:       map[string]string{},
	}
}

func preauthEntrypointsKey(requestID string, apiKeyID int64) string {
	return fmt.Sprintf("%s#%d", requestID, apiKeyID)
}

func (l *preauthEntrypointsLedger) balance(userID int64) float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.walletBalance[userID]
}

func (l *preauthEntrypointsLedger) setBalance(userID int64, v float64) {
	l.mu.Lock()
	l.walletBalance[userID] = v
	l.mu.Unlock()
}

func (l *preauthEntrypointsLedger) activeHoldCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.holds)
}

func (l *preauthEntrypointsLedger) settlementSnapshot(requestID string, apiKeyID int64) *service.BalancePreauthorizationRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.settlements[preauthEntrypointsKey(requestID, apiKeyID)]
	if !ok || rec == nil {
		return nil
	}
	cp := *rec
	return &cp
}

func (l *preauthEntrypointsLedger) finalizedSnapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.finalizedIDs))
	copy(out, l.finalizedIDs)
	return out
}

func (l *preauthEntrypointsLedger) refundedSnapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.refundedIDs))
	copy(out, l.refundedIDs)
	return out
}

func (l *preauthEntrypointsLedger) PrepareBalancePreauthorization(_ context.Context, cmd *service.BalancePreauthorizationCommand) (*service.BalancePreauthorizationRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := preauthEntrypointsKey(cmd.RequestID, cmd.APIKeyID)
	if existing, ok := l.settlements[k]; ok {
		cp := *existing
		return &cp, nil
	}
	rec := &service.BalancePreauthorizationRecord{
		RequestID:                cmd.RequestID,
		APIKeyID:                 cmd.APIKeyID,
		UserID:                   cmd.UserID,
		HoldAmount:               cmd.HoldAmount,
		Status:                   service.BalanceSettlementPrepared,
		AuthorizationFingerprint: cmd.AuthorizationFingerprint,
	}
	l.settlements[k] = rec
	cp := *rec
	return &cp, nil
}

func (l *preauthEntrypointsLedger) MarkBalancePreauthorizationAuthorized(_ context.Context, requestID string, apiKeyID int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if rec, ok := l.settlements[preauthEntrypointsKey(requestID, apiKeyID)]; ok && rec.Status == service.BalanceSettlementPrepared {
		rec.Status = service.BalanceSettlementAuthorized
	}
	return nil
}

func (l *preauthEntrypointsLedger) AdvanceBalancePreauthorizationHold(_ context.Context, requestID string, apiKeyID int64, holdAmount float64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if rec, ok := l.settlements[preauthEntrypointsKey(requestID, apiKeyID)]; ok && holdAmount > rec.HoldAmount {
		rec.HoldAmount = holdAmount
	}
	return nil
}

func (l *preauthEntrypointsLedger) BeginBalancePreauthorizationFinalization(_ context.Context, requestID string, apiKeyID int64, actualAmount float64, requestFingerprint string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if rec, ok := l.settlements[preauthEntrypointsKey(requestID, apiKeyID)]; ok {
		rec.Amount = actualAmount
		rec.RequestFingerprint = requestFingerprint
		rec.Status = service.BalanceSettlementFinalizationPending
	}
	return nil
}

func (l *preauthEntrypointsLedger) CompleteBalancePreauthorizationSettlement(_ context.Context, requestID string, apiKeyID int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if rec, ok := l.settlements[preauthEntrypointsKey(requestID, apiKeyID)]; ok && rec.Status == service.BalanceSettlementFinalizationPending {
		rec.Status = service.BalanceSettlementPending
	}
	return nil
}

func (l *preauthEntrypointsLedger) BeginBalancePreauthorizationRefund(_ context.Context, requestID string, apiKeyID int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.settlements[preauthEntrypointsKey(requestID, apiKeyID)]
	if !ok {
		return service.ErrUsageBillingRequestConflict
	}
	if rec.Status == service.BalanceSettlementPrepared || rec.Status == service.BalanceSettlementAuthorized {
		rec.Status = service.BalanceSettlementFinalizationPending
		rec.Amount = 0
		return nil
	}
	if rec.Status == service.BalanceSettlementRefunded || (rec.Status == service.BalanceSettlementFinalizationPending && rec.Amount == 0) {
		return nil
	}
	return service.ErrUsageBillingRequestConflict
}

func (l *preauthEntrypointsLedger) CompleteBalancePreauthorizationRefund(_ context.Context, requestID string, apiKeyID int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if rec, ok := l.settlements[preauthEntrypointsKey(requestID, apiKeyID)]; ok {
		rec.Status = service.BalanceSettlementRefunded
	}
	return nil
}

func (l *preauthEntrypointsLedger) LoadLiveBalanceInitializationSnapshot(_ context.Context, userID int64) (service.LiveBalanceInitializationSnapshot, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return service.LiveBalanceInitializationSnapshot{Balance: l.walletBalance[userID]}, nil
}

func (l *preauthEntrypointsLedger) Apply(_ context.Context, cmd *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.applyCalls++
	cmd.Normalize()
	k := preauthEntrypointsKey(cmd.RequestID, cmd.APIKeyID)
	if prevFP, exists := l.applied[k]; exists {
		if prevFP != "" && cmd.RequestFingerprint != "" && prevFP != cmd.RequestFingerprint {
			return nil, service.ErrUsageBillingRequestConflict
		}
		return &service.UsageBillingApplyResult{Applied: false}, nil
	}
	l.applied[k] = cmd.RequestFingerprint
	res := &service.UsageBillingApplyResult{Applied: true}
	if cmd.BalancePreauthorized {
		if rec, ok := l.settlements[k]; ok {
			rec.Amount = cmd.BalanceCost
			rec.RequestFingerprint = cmd.RequestFingerprint
			rec.Status = service.BalanceSettlementFinalizationPending
			res.BalanceFinalizationPending = true
		}
	}
	bal := l.walletBalance[cmd.UserID]
	res.NewBalance = &bal
	return res, nil
}

func (l *preauthEntrypointsLedger) AuthorizeLiveBalance(_ context.Context, userID int64, attemptID string, _ float64, hold float64) (service.LiveBalanceResult, error) {
	return l.authorizeLocked(userID, attemptID, hold), nil
}

func (l *preauthEntrypointsLedger) AuthorizeExistingLiveBalance(_ context.Context, userID int64, attemptID string, hold float64) (service.LiveBalanceResult, error) {
	return l.authorizeLocked(userID, attemptID, hold), nil
}

func (l *preauthEntrypointsLedger) AuthorizeLiveBalanceAtWatermark(_ context.Context, userID int64, attemptID string, _ float64, _ int64, hold float64) (service.LiveBalanceResult, error) {
	return l.authorizeLocked(userID, attemptID, hold), nil
}

func (l *preauthEntrypointsLedger) AuthorizeLiveBalanceAtWatermarkIfSafe(_ context.Context, userID int64, attemptID string, _ float64, _ int64, hold float64, _ bool) (service.LiveBalanceResult, error) {
	return l.authorizeLocked(userID, attemptID, hold), nil
}

func (l *preauthEntrypointsLedger) authorizeLocked(userID int64, attemptID string, hold float64) service.LiveBalanceResult {
	l.mu.Lock()
	defer l.mu.Unlock()
	if existing, ok := l.holds[attemptID]; ok {
		return service.LiveBalanceResult{
			Outcome:          service.LiveBalanceOutcomeIdempotent,
			State:            service.LiveBalanceAttemptAuthorized,
			ReservedAmount:   existing,
			AvailableBalance: l.walletBalance[userID],
		}
	}
	if l.walletBalance[userID]+1e-9 < hold {
		return service.LiveBalanceResult{
			Outcome:          service.LiveBalanceOutcomeInsufficient,
			AvailableBalance: l.walletBalance[userID],
		}
	}
	l.walletBalance[userID] -= hold
	l.holds[attemptID] = hold
	return service.LiveBalanceResult{
		Outcome:          service.LiveBalanceOutcomeApplied,
		State:            service.LiveBalanceAttemptAuthorized,
		ReservedAmount:   hold,
		AvailableBalance: l.walletBalance[userID],
	}
}

func (l *preauthEntrypointsLedger) TopUpLiveBalance(_ context.Context, userID int64, attemptID string, target float64) (service.LiveBalanceResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.topUpCalls++
	l.toppedUpIDs = append(l.toppedUpIDs, attemptID)
	current, ok := l.holds[attemptID]
	if !ok {
		return service.LiveBalanceResult{Outcome: service.LiveBalanceOutcomeNotFound}, nil
	}
	if target <= current {
		return service.LiveBalanceResult{
			Outcome:          service.LiveBalanceOutcomeIdempotent,
			State:            service.LiveBalanceAttemptAuthorized,
			ReservedAmount:   current,
			AvailableBalance: l.walletBalance[userID],
		}, nil
	}
	delta := target - current
	if l.walletBalance[userID]+1e-9 < delta {
		return service.LiveBalanceResult{
			Outcome:          service.LiveBalanceOutcomeInsufficient,
			State:            service.LiveBalanceAttemptAuthorized,
			ReservedAmount:   current,
			AvailableBalance: l.walletBalance[userID],
		}, nil
	}
	l.walletBalance[userID] -= delta
	l.holds[attemptID] = target
	return service.LiveBalanceResult{
		Outcome:          service.LiveBalanceOutcomeApplied,
		State:            service.LiveBalanceAttemptAuthorized,
		ReservedAmount:   target,
		AvailableBalance: l.walletBalance[userID],
	}, nil
}

func (l *preauthEntrypointsLedger) FinalizeLiveBalance(_ context.Context, userID int64, attemptID string, actual float64) (service.LiveBalanceResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	hold, ok := l.holds[attemptID]
	if !ok {
		return service.LiveBalanceResult{Outcome: service.LiveBalanceOutcomeIdempotent, State: service.LiveBalanceAttemptFinalized}, nil
	}
	l.walletBalance[userID] += hold - actual
	delete(l.holds, attemptID)
	l.finalizedIDs = append(l.finalizedIDs, attemptID)
	return service.LiveBalanceResult{
		Outcome:          service.LiveBalanceOutcomeApplied,
		State:            service.LiveBalanceAttemptFinalized,
		ReservedAmount:   hold,
		ActualAmount:     actual,
		AvailableBalance: l.walletBalance[userID],
	}, nil
}

func (l *preauthEntrypointsLedger) RefundLiveBalance(_ context.Context, userID int64, attemptID string) (service.LiveBalanceResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	hold, ok := l.holds[attemptID]
	if !ok {
		return service.LiveBalanceResult{Outcome: service.LiveBalanceOutcomeIdempotent, State: service.LiveBalanceAttemptRefunded}, nil
	}
	l.walletBalance[userID] += hold
	delete(l.holds, attemptID)
	l.refundedIDs = append(l.refundedIDs, attemptID)
	return service.LiveBalanceResult{
		Outcome:          service.LiveBalanceOutcomeApplied,
		State:            service.LiveBalanceAttemptRefunded,
		ReservedAmount:   hold,
		AvailableBalance: l.walletBalance[userID],
	}, nil
}

func (l *preauthEntrypointsLedger) ReadLiveBalanceAttempt(_ context.Context, userID int64, attemptID string) (service.LiveBalanceResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if hold, ok := l.holds[attemptID]; ok {
		return service.LiveBalanceResult{
			Outcome:          service.LiveBalanceOutcomeApplied,
			State:            service.LiveBalanceAttemptAuthorized,
			ReservedAmount:   hold,
			AvailableBalance: l.walletBalance[userID],
		}, nil
	}
	return service.LiveBalanceResult{Outcome: service.LiveBalanceOutcomeNotFound}, nil
}

type preauthEntrypointsBillingCache struct {
	service.BillingCache
	l *preauthEntrypointsLedger
}

func (c *preauthEntrypointsBillingCache) GetUserBalance(_ context.Context, userID int64) (float64, error) {
	return c.l.balance(userID), nil
}
func (c *preauthEntrypointsBillingCache) GetLiveBalance(_ context.Context, userID int64) (float64, bool, error) {
	return c.l.balance(userID), true, nil
}
func (c *preauthEntrypointsBillingCache) ReserveInflightBalance(_ context.Context, _ int64, _ string, _, _ float64, _ time.Duration) (bool, float64, error) {
	return true, 0, nil
}
func (c *preauthEntrypointsBillingCache) ReleaseInflightBalance(_ context.Context, _ int64, _ string) error {
	return nil
}
func (c *preauthEntrypointsBillingCache) DeductUserBalance(context.Context, int64, float64) error {
	return nil
}
func (c *preauthEntrypointsBillingCache) InvalidateUserBalance(context.Context, int64) error {
	return nil
}
func (c *preauthEntrypointsBillingCache) GetUserPlatformQuotaCache(context.Context, int64, string) (*service.UserPlatformQuotaCacheEntry, bool, error) {
	return nil, false, nil
}
func (c *preauthEntrypointsBillingCache) AuthorizeLiveBalance(ctx context.Context, userID int64, attemptID string, fallback, hold float64) (service.LiveBalanceResult, error) {
	return c.l.AuthorizeLiveBalance(ctx, userID, attemptID, fallback, hold)
}
func (c *preauthEntrypointsBillingCache) AuthorizeExistingLiveBalance(ctx context.Context, userID int64, attemptID string, hold float64) (service.LiveBalanceResult, error) {
	return c.l.AuthorizeExistingLiveBalance(ctx, userID, attemptID, hold)
}
func (c *preauthEntrypointsBillingCache) AuthorizeLiveBalanceAtWatermark(ctx context.Context, userID int64, attemptID string, fallback float64, wm int64, hold float64) (service.LiveBalanceResult, error) {
	return c.l.AuthorizeLiveBalanceAtWatermark(ctx, userID, attemptID, fallback, wm, hold)
}
func (c *preauthEntrypointsBillingCache) AuthorizeLiveBalanceAtWatermarkIfSafe(ctx context.Context, userID int64, attemptID string, fallback float64, wm int64, hold float64, allowInit bool) (service.LiveBalanceResult, error) {
	return c.l.AuthorizeLiveBalanceAtWatermarkIfSafe(ctx, userID, attemptID, fallback, wm, hold, allowInit)
}
func (c *preauthEntrypointsBillingCache) TopUpLiveBalance(ctx context.Context, userID int64, attemptID string, target float64) (service.LiveBalanceResult, error) {
	return c.l.TopUpLiveBalance(ctx, userID, attemptID, target)
}
func (c *preauthEntrypointsBillingCache) FinalizeLiveBalance(ctx context.Context, userID int64, attemptID string, actual float64) (service.LiveBalanceResult, error) {
	return c.l.FinalizeLiveBalance(ctx, userID, attemptID, actual)
}
func (c *preauthEntrypointsBillingCache) RefundLiveBalance(ctx context.Context, userID int64, attemptID string) (service.LiveBalanceResult, error) {
	return c.l.RefundLiveBalance(ctx, userID, attemptID)
}
func (c *preauthEntrypointsBillingCache) ReadLiveBalanceAttempt(ctx context.Context, userID int64, attemptID string) (service.LiveBalanceResult, error) {
	return c.l.ReadLiveBalanceAttempt(ctx, userID, attemptID)
}

type preauthEntrypointsUsageLogRepo struct {
	service.UsageLogRepository
	mu   sync.Mutex
	logs []*service.UsageLog
}

func (r *preauthEntrypointsUsageLogRepo) Create(_ context.Context, log *service.UsageLog) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *log
	r.logs = append(r.logs, &cp)
	return true, nil
}

func (r *preauthEntrypointsUsageLogRepo) snapshot() []*service.UsageLog {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*service.UsageLog, len(r.logs))
	copy(out, r.logs)
	return out
}

func newPreauthTestPricingService(t *testing.T, pricingJSON string) (*config.Config, *service.PricingService, *service.BillingService) {
	t.Helper()
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Default.RateMultiplier = 1.0
	cfg.Billing.BalancePreauthorizationEnabled = true
	cfg.Gateway.MaxBodySize = 1 << 20
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Security.URLAllowlist.AllowPrivateHosts = true
	var pricingSvc *service.PricingService
	if trimmed := strings.TrimSpace(pricingJSON); trimmed != "" && trimmed != "{}" {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "model_pricing.json"), []byte(pricingJSON), 0o644))
		cfg.Pricing.DataDir = dir
		cfg.Pricing.UpdateIntervalHours = 100000
		pricingSvc = service.NewPricingService(cfg, nil)
		require.NoError(t, pricingSvc.Initialize())
	}
	billingSvc := service.NewBillingService(cfg, pricingSvc)
	return cfg, pricingSvc, billingSvc
}

func newPreauthTestService(t *testing.T, cfg *config.Config, billingSvc *service.BillingService, ledger *preauthEntrypointsLedger) *service.BalancePreauthorizationService {
	t.Helper()
	bcs := service.NewBillingCacheService(&preauthEntrypointsBillingCache{l: ledger}, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(bcs.Stop)
	return service.NewBalancePreauthorizationService(cfg, billingSvc, bcs, ledger)
}

// --- BILL-H1 #1: DeepSeek Standalone Tools (search, fetch, asr, tts, ocr) ---

func TestBILLH1_DeepSeekStandaloneTools_PreauthorizeRejectRefundAndFinalize(t *testing.T) {
	gin.SetMode(gin.TestMode)

	searchPricePer1k := 50.0 // $0.05 per call -> exceeds $0.01 balance
	ocrPricePer1k := 50.0    // $0.05 per page -> exceeds $0.01 balance

	tools := []struct {
		name        string
		path        string
		contentType string
		body        string
		upstreamCT  string
		upstreamOut string
		invoke      func(*OpenAIGatewayHandler, *gin.Context)
		wantCostMin float64
	}{
		{
			name:        "search",
			path:        service.DeepSeekToolsPathSearch,
			contentType: "application/json",
			body:        `{"query":"sub2api preauth"}`,
			upstreamCT:  "application/json",
			upstreamOut: `{"results":[{"title":"hit","url":"https://example.com"}]}`,
			invoke:      (*OpenAIGatewayHandler).DeepSeekToolsSearch,
			wantCostMin: 0.049,
		},
		{
			name:        "fetch",
			path:        service.DeepSeekToolsPathFetch,
			contentType: "application/json",
			body:        `{"url":"https://example.com"}`,
			upstreamCT:  "application/json",
			upstreamOut: `{"content":"fetched page body"}`,
			invoke:      (*OpenAIGatewayHandler).DeepSeekToolsFetch,
			wantCostMin: 0.049,
		},
		{
			name:        "asr",
			path:        service.DeepSeekToolsPathASR,
			contentType: "multipart/form-data; boundary=dsbound",
			body:        "--dsbound\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.wav\"\r\n\r\naudio\r\n--dsbound--\r\n",
			upstreamCT:  "application/json",
			upstreamOut: `{"text":"hello","duration_ms":36000}`,
			invoke:      (*OpenAIGatewayHandler).DeepSeekToolsASR,
			wantCostMin: 0.005, // 36s/3600 * $0.78 = $0.0078; hold was 120s/3600 * $0.78 = $0.026
		},
		{
			name:        "tts",
			path:        service.DeepSeekToolsPathTTS,
			contentType: "application/json",
			body:        `{"text":"` + strings.Repeat("a", 100) + `"}`, // 100 chars * $300/1M = $0.03
			upstreamCT:  "audio/wav",
			upstreamOut: "RIFF-audio-bytes",
			invoke:      (*OpenAIGatewayHandler).DeepSeekToolsTTS,
			wantCostMin: 0.029,
		},
		{
			name:        "ocr",
			path:        service.DeepSeekToolsPathOCR,
			contentType: "multipart/form-data; boundary=dsbound",
			body:        "--dsbound\r\nContent-Disposition: form-data; name=\"file\"; filename=\"doc.png\"\r\n\r\npngdata\r\n--dsbound--\r\n",
			upstreamCT:  "application/json",
			upstreamOut: `{"id":"ocr-1","object":"ocr.result","page_count":1,"pages":[]}`,
			invoke:      (*OpenAIGatewayHandler).DeepSeekToolsOCR,
			wantCostMin: 0.049,
		},
	}

	buildDeepSeekHandler := func(t *testing.T, balance float64, upstream *httptest.Server) (*OpenAIGatewayHandler, *preauthEntrypointsLedger, *preauthEntrypointsUsageLogRepo, *service.APIKey) {
		t.Helper()
		cfg, _, billingSvc := newPreauthTestPricingService(t, `{}`)
		cfg.Gateway.MaxAccountSwitches = 1
		ledger := newPreauthEntrypointsLedger(42, balance)
		usageLogs := &preauthEntrypointsUsageLogRepo{}
		preauth := newPreauthTestService(t, cfg, billingSvc, ledger)

		groupID := int64(36)
		group := &service.Group{
			ID:               groupID,
			Platform:         service.PlatformDeepseek,
			Status:           service.StatusActive,
			Hydrated:         true,
			RateMultiplier:   1.0,
			SearchPricePer1k: &searchPricePer1k,
			ModelPricing: []service.ChannelModelPricing{
				{
					BillingMode:     service.BillingModePerRequest,
					Models:          []string{service.DeepSeekToolsLabelOCR},
					PerRequestPrice: float64Ptr(ocrPricePer1k / 1000.0),
				},
			},
		}
		account := service.Account{
			ID: 1, Platform: service.PlatformDeepseek, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Priority: 1, GroupIDs: []int64{groupID},
			Credentials: map[string]any{
				"base_url":      upstream.URL,
				"api_key":       "test-key-must-not-leak",
				"model_mapping": map[string]any{"deepseek-fixture": "deepseek-fixture"},
			},
		}
		repo := openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}
		snapshot := service.NewSchedulerSnapshotService(
			&fakeSchedulerCache{accounts: []*service.Account{&account}},
			nil, repo,
			smartRouteHandlerGroups{groups: map[int64]*service.Group{groupID: group}}, cfg,
		)
		_, err := snapshot.GetGroupByIDLite(context.Background(), groupID)
		require.NoError(t, err)

		bcs := service.NewBillingCacheService(&preauthEntrypointsBillingCache{l: ledger}, nil, nil, nil, nil, nil, cfg, nil)
		t.Cleanup(bcs.Stop)
		transport := &deepSeekNativeHandlerUpstream{client: upstream.Client()}
		gateway := service.NewOpenAIGatewayService(
			repo, usageLogs, ledger, nil, nil, nil, nil, cfg, snapshot, nil,
			billingSvc, nil, bcs, transport, &service.DeferredService{},
			nil, nil, nil, nil, nil, nil, nil,
		)
		t.Cleanup(gateway.CloseOpenAIWSPool)
		h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), bcs, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
		h.balancePreauthorizer = preauth
		apiKey := &service.APIKey{
			ID: 7, UserID: 42, GroupID: &groupID, Group: group, Status: service.StatusAPIKeyActive,
			User: &service.User{ID: 42, Status: service.StatusActive, Balance: 1000.0, Concurrency: 5},
		}
		return h, ledger, usageLogs, apiKey
	}

	makeCtx := func(path, ct, body string, key *service.APIKey) (*gin.Context, *httptest.ResponseRecorder) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", ct)
		c.Request = req.WithContext(context.WithValue(req.Context(), ctxkey.Group, key.Group))
		c.Set(string(middleware2.ContextKeyAPIKey), key)
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: key.UserID, Concurrency: 5})
		return c, rec
	}

	for _, tc := range tools {
		t.Run(tc.name+"/rejects_low_live_wallet_before_upstream", func(t *testing.T) {
			var upstreamHits atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				upstreamHits.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()

			h, ledger, _, apiKey := buildDeepSeekHandler(t, 0.01, upstream)
			c, rec := makeCtx(tc.path, tc.contentType, tc.body, apiKey)
			tc.invoke(h, c)

			require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
			require.Zero(t, upstreamHits.Load(), "low live_wallet balance must block before upstream")
			require.InDelta(t, 0.01, ledger.balance(42), 1e-9)
			require.Zero(t, ledger.activeHoldCount())
		})

		t.Run(tc.name+"/refunds_hold_on_upstream_error", func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"message":"bad tool input"}}`)
			}))
			defer upstream.Close()

			h, ledger, _, apiKey := buildDeepSeekHandler(t, 1.0, upstream)
			c, rec := makeCtx(tc.path, tc.contentType, tc.body, apiKey)
			tc.invoke(h, c)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.InDelta(t, 1.0, ledger.balance(42), 1e-9, "upstream failure must refund live_wallet hold")
			require.Zero(t, ledger.activeHoldCount())
			require.Len(t, ledger.refundedSnapshot(), 1)
		})

		t.Run(tc.name+"/finalizes_exact_cost_on_success", func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.upstreamCT)
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, tc.upstreamOut)
			}))
			defer upstream.Close()

			h, ledger, usageLogs, apiKey := buildDeepSeekHandler(t, 1.0, upstream)
			c, rec := makeCtx(tc.path, tc.contentType, tc.body, apiKey)
			tc.invoke(h, c)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Eventually(t, func() bool { return len(ledger.finalizedSnapshot()) == 1 }, 2*time.Second, 10*time.Millisecond)
			require.Zero(t, ledger.activeHoldCount())
			logs := usageLogs.snapshot()
			require.Len(t, logs, 1)
			require.GreaterOrEqual(t, logs[0].ActualCost, tc.wantCostMin)
			require.InDelta(t, 1.0-logs[0].ActualCost, ledger.balance(42), 1e-8)
		})
	}
}

// --- BILL-H1 #2: OpenAI Responses WS Per-Turn Preauthorization ---

func TestBILLH1_OpenAIWSTurnPreauth_HelpersAndIsolation(t *testing.T) {
	ctx := context.Background()
	cfg, _, billingSvc := newPreauthTestPricingService(t, `{}`)
	ledger := newPreauthEntrypointsLedger(42, 10.0)
	preauth := newPreauthTestService(t, cfg, billingSvc, ledger)

	mgr := newOpenAIWSTurnPreauth("conn-abc")
	id1 := mgr.nextRequestID(1)
	id2 := mgr.nextRequestID(1)
	id3 := mgr.nextRequestID(2)
	require.Equal(t, "ws:conn-abc:turn:1:1", id1)
	require.Equal(t, "ws:conn-abc:turn:1:2", id2, "failover retry on same turn must allocate fresh monotonic seq")
	require.Equal(t, "ws:conn-abc:turn:2:3", id3)

	g1, err := preauth.PreauthorizeFixedAmount(ctx, id1, 7, 42, "fp1", 1.0)
	require.NoError(t, err)
	mgr.set(zap.NewNop(), 1, g1)
	require.True(t, mgr.has(1))
	require.False(t, mgr.has(2))

	// When Turn 2 has no guard (e.g. PREAUTH toggled off mid-connection),
	// contextForTurn MUST mask any Turn 1 guard attached on base ctx.
	baseWithTurn1 := service.ContextWithBalancePreauthorizationGuard(ctx, g1)
	turn2Ctx := mgr.contextForTurn(2, baseWithTurn1)
	_, hasTurn2Guard := service.BalancePreauthorizationGuardFromContext(turn2Ctx)
	require.False(t, hasTurn2Guard, "Turn 2 without its own guard must not inherit Turn 1's guard")

	// Transferring Turn 1 guard makes refund(1) a no-op while leaving Turn 2 guard refundable via refundAll.
	g2, err := preauth.PreauthorizeFixedAmount(ctx, id3, 7, 42, "fp2", 2.0)
	require.NoError(t, err)
	mgr.set(zap.NewNop(), 2, g2)
	require.InDelta(t, 7.0, ledger.balance(42), 1e-9)

	workerG1, ok := g1.TransferToWorker()
	require.True(t, ok)
	mgr.refund(zap.NewNop(), 1)
	require.InDelta(t, 7.0, ledger.balance(42), 1e-9, "transferred Turn 1 guard must not be refunded by handler")
	require.NoError(t, workerG1.Finalize(ctx, 0.25, "fp1"))
	require.InDelta(t, 7.75, ledger.balance(42), 1e-9)

	mgr.refundAll(zap.NewNop())
	require.InDelta(t, 9.75, ledger.balance(42), 1e-9, "untransferred Turn 2 guard must be refunded on connection close")
	require.Zero(t, ledger.activeHoldCount())
}

func TestBILLH1_OpenAIResponsesWS_PerTurnPreauthRejectRefundAndFinalize(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const pricingJSON = `{
		"gpt-5.1": {
			"litellm_provider": "openai",
			"mode": "chat",
			"input_cost_per_token": 0.0001,
			"output_cost_per_token": 0.0002
		}
	}`

	runWSPreauth := func(t *testing.T, initialBalance float64, drainAfterTurn1 bool) (upstreamTurns int32, logs []*service.UsageLog, ledger *preauthEntrypointsLedger, closeStatus coderws.StatusCode, closeReason string) {
		t.Helper()
		var turns atomic.Int32
		upstreamDone := make(chan struct{})
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer close(upstreamDone)
			conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
			if err != nil {
				return
			}
			defer func() { _ = conn.CloseNow() }()
			for turn := 1; turn <= 2; turn++ {
				readCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
				_, _, err := conn.Read(readCtx)
				cancel()
				if err != nil {
					return
				}
				turns.Add(1)
				resp := fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_preauth_turn_%d","model":"gpt-5.1","usage":{"input_tokens":10,"output_tokens":5}}}`, turn)
				writeCtx, wcancel := context.WithTimeout(r.Context(), 3*time.Second)
				err = conn.Write(writeCtx, coderws.MessageText, []byte(resp))
				wcancel()
				if err != nil {
					return
				}
			}
		}))
		defer upstream.Close()

		cfg, _, billingSvc := newPreauthTestPricingService(t, pricingJSON)
		cfg.Gateway.OpenAIWS.Enabled = true
		cfg.Gateway.OpenAIWS.APIKeyEnabled = true
		cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
		cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
		cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
		cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
		cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

		groupID := int64(4209)
		userID := int64(1709)
		group := &service.Group{ID: groupID, Name: "ws-preauth", Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true, RateMultiplier: 1.0}
		ledger = newPreauthEntrypointsLedger(userID, initialBalance)
		preauth := newPreauthTestService(t, cfg, billingSvc, ledger)
		usageLogs := &preauthEntrypointsUsageLogRepo{}

		accountRepo := &openAIWSUsageHandlerAccountRepoStub{account: service.Account{
			ID: 9909, Name: "openai-ws-preauth", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 2,
			Credentials: map[string]any{
				"api_key":       "test-key-must-not-leak",
				"base_url":      upstream.URL,
				"model_mapping": map[string]any{"gpt-5.1": "gpt-5.1"},
			},
			Extra: map[string]any{
				"openai_apikey_responses_websockets_v2_enabled": true,
				"openai_apikey_responses_websockets_v2_mode":    service.OpenAIWSIngressModePassthrough,
			},
		}}
		bcs := service.NewBillingCacheService(&preauthEntrypointsBillingCache{l: ledger}, nil, nil, nil, nil, nil, cfg, nil)
		defer bcs.Stop()
		gateway := service.NewOpenAIGatewayService(
			accountRepo, usageLogs, ledger, nil, nil, nil, nil, cfg, nil, nil,
			billingSvc, nil, bcs, nil, &service.DeferredService{},
			nil, nil, nil, nil, nil, nil, nil,
		)
		defer gateway.CloseOpenAIWSPool()
		concurrency := &concurrencyCacheMock{
			acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
			acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
		}
		h := &OpenAIGatewayHandler{
			gatewayService:      gateway,
			billingCacheService: bcs,
			apiKeyService:       &service.APIKeyService{},
			concurrencyHelper:   NewConcurrencyHelper(service.NewConcurrencyService(concurrency), SSEPingFormatNone, time.Second),
		}
		h.balancePreauthorizer = preauth
		apiKey := &service.APIKey{
			ID: 1809, UserID: userID, GroupID: &groupID, Group: group, Status: service.StatusAPIKeyActive,
			User: &service.User{ID: userID, Status: service.StatusActive, Balance: 1000.0},
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

		turn1Frame := []byte(`{"type":"response.create","model":"gpt-5.1","input":"turn one","max_output_tokens":100}`)
		writeCtx, wcancel := context.WithTimeout(context.Background(), 3*time.Second)
		require.NoError(t, client.Write(writeCtx, coderws.MessageText, turn1Frame))
		wcancel()

		readCtx, rcancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, msg1, err1 := client.Read(readCtx)
		rcancel()
		if err1 != nil {
			var cerr coderws.CloseError
			if errors.As(err1, &cerr) {
				closeStatus, closeReason = cerr.Code, cerr.Reason
			}
			return turns.Load(), usageLogs.snapshot(), ledger, closeStatus, closeReason
		}
		require.Contains(t, string(msg1), "resp_preauth_turn_1")
		require.Eventually(t, func() bool { return len(ledger.finalizedSnapshot()) >= 1 }, 2*time.Second, 10*time.Millisecond)

		if drainAfterTurn1 {
			// Exhaust live_wallet right before Turn 2!
			ledger.setBalance(userID, 0.00001)
		}

		turn2Frame := []byte(`{"type":"response.create","model":"gpt-5.1","input":"turn two","max_output_tokens":100}`)
		writeCtx2, wcancel2 := context.WithTimeout(context.Background(), 3*time.Second)
		require.NoError(t, client.Write(writeCtx2, coderws.MessageText, turn2Frame))
		wcancel2()

		readCtx2, rcancel2 := context.WithTimeout(context.Background(), 3*time.Second)
		_, msg2, err2 := client.Read(readCtx2)
		rcancel2()
		if err2 != nil {
			var cerr coderws.CloseError
			if errors.As(err2, &cerr) {
				closeStatus, closeReason = cerr.Code, cerr.Reason
			}
		} else {
			require.Contains(t, string(msg2), "resp_preauth_turn_2")
			require.Eventually(t, func() bool { return len(ledger.finalizedSnapshot()) >= 2 }, 2*time.Second, 10*time.Millisecond)
			_ = client.Close(coderws.StatusNormalClosure, "done")
		}
		select {
		case <-upstreamDone:
		case <-time.After(time.Second):
		}
		return turns.Load(), usageLogs.snapshot(), ledger, closeStatus, closeReason
	}

	t.Run("turn1_rejected_on_insufficient_live_wallet_before_upstream", func(t *testing.T) {
		turns, logs, ledger, status, reason := runWSPreauth(t, 0.00001, false)
		require.Equal(t, int32(0), turns, "Turn 1 must be blocked before hitting upstream")
		require.Empty(t, logs)
		require.Equal(t, coderws.StatusPolicyViolation, status)
		require.Equal(t, "insufficient balance", reason)
		require.Zero(t, ledger.activeHoldCount())
	})

	t.Run("turn1_finalizes_and_turn2_rejected_when_live_wallet_exhausted", func(t *testing.T) {
		turns, logs, ledger, status, reason := runWSPreauth(t, 5.0, true)
		require.Equal(t, int32(1), turns, "Turn 2 must be blocked before reaching upstream")
		require.Len(t, logs, 1)
		require.True(t, strings.HasPrefix(logs[0].RequestID, "ws:") && strings.Contains(logs[0].RequestID, ":turn:1:"))
		require.NotNil(t, logs[0].ResponseRequestID)
		require.Equal(t, "resp_preauth_turn_1", *logs[0].ResponseRequestID)
		require.Equal(t, coderws.StatusPolicyViolation, status)
		require.Equal(t, "insufficient balance", reason)
		require.Zero(t, ledger.activeHoldCount())
	})

	t.Run("multi_turn_success_uses_distinct_turn_request_ids_and_finalizes_both", func(t *testing.T) {
		turns, logs, ledger, _, _ := runWSPreauth(t, 5.0, false)
		require.Equal(t, int32(2), turns)
		require.Len(t, logs, 2)
		require.NotEqual(t, logs[0].RequestID, logs[1].RequestID)
		require.Contains(t, logs[0].RequestID, ":turn:1:")
		require.Contains(t, logs[1].RequestID, ":turn:2:")
		require.InDelta(t, 5.0-logs[0].ActualCost-logs[1].ActualCost, ledger.balance(1709), 1e-8)
		require.Zero(t, ledger.activeHoldCount())
	})
}

// --- BILL-H1 #3: Grok Voice HTTP (TTS/STT/custom-voices) & Grok Realtime WS ---

func TestBILLH1_GrokVoiceHTTPAndRealtimeWS_PreauthTopUpAbortAndRefund(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ttsPrice := 10_000.0 // 10 chars => $0.10 (exceeds $0.01 balance)
	sttPrice := 3.6      // 60s/3600 * $3.6 = $0.06 (exceeds $0.01 balance)
	rtPrice := 0.05      // $0.05/min

	buildGrokHandler := func(t *testing.T, balance float64, upstreamURL string, client *http.Client) (*OpenAIGatewayHandler, *preauthEntrypointsLedger, *preauthEntrypointsUsageLogRepo, *service.APIKey) {
		t.Helper()
		cfg, _, billingSvc := newPreauthTestPricingService(t, `{}`)
		ledger := newPreauthEntrypointsLedger(42, balance)
		usageLogs := &preauthEntrypointsUsageLogRepo{}
		preauth := newPreauthTestService(t, cfg, billingSvc, ledger)

		groupID := int64(77)
		group := &service.Group{
			ID:                           groupID,
			Platform:                     service.PlatformGrok,
			Status:                       service.StatusActive,
			Hydrated:                     true,
			RateMultiplier:               1.0,
			AudioTTSPricePerMillionChars: &ttsPrice,
			AudioSTTPricePerHour:         &sttPrice,
			AudioRealtimePricePerMin:     &rtPrice,
		}
		account := service.Account{
			ID: 11, Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Priority: 1, GroupIDs: []int64{groupID},
			Credentials: map[string]any{"base_url": upstreamURL, "api_key": "test-key-must-not-leak"},
		}
		repo := openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}
		snapshot := service.NewSchedulerSnapshotService(
			&fakeSchedulerCache{accounts: []*service.Account{&account}},
			nil, repo,
			smartRouteHandlerGroups{groups: map[int64]*service.Group{groupID: group}}, cfg,
		)
		_, err := snapshot.GetGroupByIDLite(context.Background(), groupID)
		require.NoError(t, err)

		bcs := service.NewBillingCacheService(&preauthEntrypointsBillingCache{l: ledger}, nil, nil, nil, nil, nil, cfg, nil)
		t.Cleanup(bcs.Stop)
		transport := &deepSeekNativeHandlerUpstream{client: client}
		gateway := service.NewOpenAIGatewayService(
			repo, usageLogs, ledger, nil, nil, nil, nil, cfg, snapshot, nil,
			billingSvc, nil, bcs, transport, &service.DeferredService{},
			nil, nil, nil, nil, nil, nil, nil,
		)
		t.Cleanup(gateway.CloseOpenAIWSPool)
		h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), bcs, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
		h.balancePreauthorizer = preauth
		apiKey := &service.APIKey{
			ID: 7, UserID: 42, GroupID: &groupID, Group: group, Status: service.StatusAPIKeyActive,
			User: &service.User{ID: 42, Status: service.StatusActive, Balance: 1000.0, Concurrency: 5},
		}
		return h, ledger, usageLogs, apiKey
	}

	t.Run("voice_tts_and_stt_reject_refund_finalize_and_skip_custom_voices", func(t *testing.T) {
		var hits atomic.Int32
		var failUpstream atomic.Bool
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			if failUpstream.Load() {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"message":"invalid audio"}}`)
				return
			}
			switch {
			case strings.Contains(r.URL.Path, "tts"):
				w.Header().Set("Content-Type", "audio/mpeg")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, "mp3-bytes")
			case strings.Contains(r.URL.Path, "stt"):
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, `{"text":"spoken","duration":30}`)
			default:
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, `{"voices":[]}`)
			}
		}))
		defer upstream.Close()

		callVoice := func(h *OpenAIGatewayHandler, key *service.APIKey, endpoint, body string) *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			req := httptest.NewRequest(http.MethodPost, "/v1/voice/"+endpoint, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			c.Request = req.WithContext(context.WithValue(req.Context(), ctxkey.Group, key.Group))
			c.Set(string(middleware2.ContextKeyAPIKey), key)
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: key.UserID, Concurrency: 5})
			h.GrokVoice(c, endpoint)
			return rec
		}

		// 1. $0.01 balance rejects TTS & STT before upstream.
		hLow, ledgerLow, _, keyLow := buildGrokHandler(t, 0.01, upstream.URL, upstream.Client())
		recTTS := callVoice(hLow, keyLow, "tts", `{"text":"0123456789"}`)
		require.Equal(t, http.StatusTooManyRequests, recTTS.Code, recTTS.Body.String())
		recSTT := callVoice(hLow, keyLow, "stt", `{"audio":"abcd","duration_seconds":60}`)
		require.Equal(t, http.StatusTooManyRequests, recSTT.Code, recSTT.Body.String())
		require.Zero(t, hits.Load(), "low balance TTS/STT must be blocked before upstream")
		require.InDelta(t, 0.01, ledgerLow.balance(42), 1e-9)

		// 2. custom-voices skips preauth even with $0.01 balance.
		recCustom := callVoice(hLow, keyLow, "custom-voices", `{}`)
		require.Equal(t, http.StatusOK, recCustom.Code, recCustom.Body.String())
		require.Equal(t, int32(1), hits.Load())
		require.Zero(t, ledgerLow.activeHoldCount())

		// 3. Upstream failure refunds hold.
		failUpstream.Store(true)
		hRefund, ledgerRefund, _, keyRefund := buildGrokHandler(t, 1.0, upstream.URL, upstream.Client())
		recErr := callVoice(hRefund, keyRefund, "tts", `{"text":"0123456789"}`)
		require.Equal(t, http.StatusBadRequest, recErr.Code)
		require.InDelta(t, 1.0, ledgerRefund.balance(42), 1e-9)
		require.Len(t, ledgerRefund.refundedSnapshot(), 1)

		// 4. Upstream 200 finalizes exact audio cost.
		failUpstream.Store(false)
		hOK, ledgerOK, usageOK, keyOK := buildGrokHandler(t, 1.0, upstream.URL, upstream.Client())
		recOK := callVoice(hOK, keyOK, "tts", `{"text":"0123456789"}`)
		require.Equal(t, http.StatusOK, recOK.Code, recOK.Body.String())
		require.Eventually(t, func() bool { return len(ledgerOK.finalizedSnapshot()) == 1 }, 2*time.Second, 10*time.Millisecond)
		logs := usageOK.snapshot()
		require.Len(t, logs, 1)
		require.InDelta(t, 0.10, logs[0].ActualCost, 1e-8)
		require.InDelta(t, 0.90, ledgerOK.balance(42), 1e-8)
		require.Zero(t, ledgerOK.activeHoldCount())
	})

	t.Run("realtime_ws_rejects_low_balance_before_upgrade", func(t *testing.T) {
		var dials atomic.Int32
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			dials.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		defer upstream.Close()

		h, ledger, _, apiKey := buildGrokHandler(t, 0.01, upstream.URL, upstream.Client())
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: apiKey.UserID, Concurrency: 5})
			c.Next()
		})
		router.GET("/v1/realtime", h.GrokRealtime)
		srv := httptest.NewServer(router)
		defer srv.Close()

		dialCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, resp, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/realtime?model=grok-voice-latest", nil)
		cancel()
		require.Error(t, err)
		require.NotNil(t, resp)
		require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
		require.Zero(t, dials.Load(), "low balance realtime WS must be rejected with HTTP 403 before dialing upstream")
		require.InDelta(t, 0.01, ledger.balance(42), 1e-9)
	})

	t.Run("realtime_ws_periodic_topup_aborts_on_exhaustion_and_bills_elapsed", func(t *testing.T) {
		origInterval := grokRealtimePreauthTopUpInterval
		origNow := grokRealtimeNow
		t.Cleanup(func() {
			grokRealtimePreauthTopUpInterval = origInterval
			grokRealtimeNow = origNow
		})

		baseTime := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
		var clockOffsetSec atomic.Int64
		grokRealtimeNow = func() time.Time {
			return baseTime.Add(time.Duration(clockOffsetSec.Load()) * time.Second)
		}
		grokRealtimePreauthTopUpInterval = 15 * time.Millisecond

		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
			if err != nil {
				return
			}
			defer func() { _ = conn.CloseNow() }()
			// Emit one audio delta so billableObserved=true, then stay open until cancelled.
			_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"type":"response.output_audio.delta","delta":"AAAA"}`))
			for {
				if _, _, err := conn.Read(r.Context()); err != nil {
					return
				}
			}
		}))
		defer upstream.Close()

		// $0.06 covers minute 1 ($0.05) but NOT minute 2 ($0.10 total)!
		h, ledger, usageLogs, apiKey := buildGrokHandler(t, 0.06, upstream.URL, upstream.Client())
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: apiKey.UserID, Concurrency: 5})
			c.Next()
		})
		router.GET("/v1/realtime", h.GrokRealtime)
		srv := httptest.NewServer(router)
		defer srv.Close()

		dialCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		client, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/realtime?model=grok-voice-latest", nil)
		cancel()
		require.NoError(t, err)
		defer func() { _ = client.CloseNow() }()

		// Read the audio delta frame first so billable audio is observed.
		readCtx1, rcancel1 := context.WithTimeout(context.Background(), 3*time.Second)
		_, msg, err := client.Read(readCtx1)
		rcancel1()
		require.NoError(t, err)
		require.Contains(t, string(msg), "response.output_audio.delta")

		// Advance clock to 65s (> 1 minute) -> periodic top-up requests $0.10 (> $0.06 wallet) -> aborts!
		clockOffsetSec.Store(65)

		readCtx2, rcancel2 := context.WithTimeout(context.Background(), 3*time.Second)
		_, _, err = client.Read(readCtx2)
		rcancel2()
		require.Error(t, err)
		var cerr coderws.CloseError
		require.ErrorAs(t, err, &cerr)
		require.Equal(t, coderws.StatusPolicyViolation, cerr.Code)
		require.Equal(t, "insufficient balance", cerr.Reason)

		require.Eventually(t, func() bool { return len(ledger.finalizedSnapshot()) == 1 }, 2*time.Second, 10*time.Millisecond)
		logs := usageLogs.snapshot()
		require.Len(t, logs, 1)
		require.True(t, strings.HasPrefix(logs[0].RequestID, "grok_realtime:"))
		require.InDelta(t, 0.05*(65.0/60.0), logs[0].ActualCost, 1e-6)
		require.Zero(t, ledger.activeHoldCount())
	})
}

// --- BILL-H1 #4: Grok Search (/v1/web_search and /v1/x_search) ---

type preauthStaticUpstream struct {
	service.HTTPUpstream
	hits   atomic.Int32
	status int
	body   string
}

func (u *preauthStaticUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.hits.Add(1)
	return &http.Response{
		StatusCode: u.status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(u.body)),
	}, nil
}

func TestBILLH1_GrokSearch_PreauthorizeRejectRefundAndFinalize(t *testing.T) {
	gin.SetMode(gin.TestMode)

	searchPricePer1k := 50.0 // $0.05 per call (exceeds $0.01 balance)

	for _, isXSearch := range []bool{false, true} {
		label := "web_search"
		if isXSearch {
			label = "x_search"
		}

		buildSearchHandler := func(t *testing.T, balance float64, upstream *preauthStaticUpstream) (*GatewayHandler, *preauthEntrypointsLedger, *preauthEntrypointsUsageLogRepo, *service.APIKey) {
			t.Helper()
			cfg, _, billingSvc := newPreauthTestPricingService(t, `{}`)
			ledger := newPreauthEntrypointsLedger(42, balance)
			usageLogs := &preauthEntrypointsUsageLogRepo{}
			preauth := newPreauthTestService(t, cfg, billingSvc, ledger)

			groupID := int64(88)
			group := &service.Group{
				ID:               groupID,
				Platform:         service.PlatformGrok,
				Status:           service.StatusActive,
				Hydrated:         true,
				RateMultiplier:   1.0,
				SearchPricePer1k: &searchPricePer1k,
			}
			account := &service.Account{
				ID: 12, Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey,
				Status: service.StatusActive, Schedulable: true, Priority: 1, Concurrency: 5,
				Credentials:   map[string]any{"base_url": "https://api.x.ai", "api_key": "test-key-must-not-leak"},
				AccountGroups: []service.AccountGroup{{AccountID: 12, GroupID: groupID}},
			}
			snapshot := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{account}}, nil, nil, nil, nil)
			bcs := service.NewBillingCacheService(&preauthEntrypointsBillingCache{l: ledger}, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(bcs.Stop)
			gw := service.NewGatewayService(
				nil, &fakeGroupRepo{group: group}, usageLogs, ledger, nil, nil, nil, nil, cfg, snapshot, nil,
				billingSvc, nil, bcs, nil, upstream, &service.DeferredService{},
				nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
			)
			h := &GatewayHandler{
				cfg:                  cfg,
				gatewayService:       gw,
				billingCacheService:  bcs,
				apiKeyService:        &service.APIKeyService{},
				concurrencyHelper:    NewConcurrencyHelper(service.NewConcurrencyService(&fakeConcurrencyCache{}), SSEPingFormatNone, 0),
				balancePreauthorizer: preauth,
			}
			apiKey := &service.APIKey{
				ID: 7, UserID: 42, GroupID: &groupID, Group: group, Status: service.StatusAPIKeyActive,
				User: &service.User{ID: 42, Status: service.StatusActive, Balance: 1000.0, Concurrency: 5},
			}
			return h, ledger, usageLogs, apiKey
		}

		callSearch := func(h *GatewayHandler, key *service.APIKey) *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			req := httptest.NewRequest(http.MethodPost, "/v1/"+label, strings.NewReader(`{"query":"latest news"}`))
			req.Header.Set("Content-Type", "application/json")
			c.Request = req.WithContext(context.WithValue(req.Context(), ctxkey.Group, key.Group))
			c.Set(string(middleware2.ContextKeyAPIKey), key)
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: key.UserID, Concurrency: 5})
			if isXSearch {
				c.Set("grok_x_search_endpoint", true)
			}
			h.WebSearch(c)
			return rec
		}

		t.Run(label+"/rejects_low_live_wallet_before_upstream", func(t *testing.T) {
			upstream := &preauthStaticUpstream{status: http.StatusOK, body: `{}`}
			h, ledger, _, key := buildSearchHandler(t, 0.01, upstream)
			rec := callSearch(h, key)
			require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
			require.Zero(t, upstream.hits.Load())
			require.InDelta(t, 0.01, ledger.balance(42), 1e-9)
			require.Zero(t, ledger.activeHoldCount())
		})

		t.Run(label+"/refunds_hold_on_upstream_error", func(t *testing.T) {
			upstream := &preauthStaticUpstream{status: http.StatusBadRequest, body: `{"error":{"message":"upstream bad request"}}`}
			h, ledger, _, key := buildSearchHandler(t, 1.0, upstream)
			rec := callSearch(h, key)
			require.Equal(t, http.StatusBadGateway, rec.Code, rec.Body.String())
			require.InDelta(t, 1.0, ledger.balance(42), 1e-9)
			require.Len(t, ledger.refundedSnapshot(), 1)
			require.Zero(t, ledger.activeHoldCount())
		})

		t.Run(label+"/finalizes_exact_search_cost_on_success", func(t *testing.T) {
			upstream := &preauthStaticUpstream{
				status: http.StatusOK,
				body:   `{"output":[{"type":"message","content":[{"type":"output_text","text":"[{\"title\":\"Example\",\"url\":\"https://example.com\",\"snippet\":\"hi\"}]"}]}]}`,
			}
			h, ledger, usageLogs, key := buildSearchHandler(t, 1.0, upstream)
			rec := callSearch(h, key)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Eventually(t, func() bool { return len(ledger.finalizedSnapshot()) == 1 }, 2*time.Second, 10*time.Millisecond)
			logs := usageLogs.snapshot()
			require.Len(t, logs, 1)
			require.True(t, strings.HasPrefix(logs[0].RequestID, label+":"), "got %q", logs[0].RequestID)
			require.InDelta(t, 0.05, logs[0].ActualCost, 1e-8)
			require.InDelta(t, 0.95, ledger.balance(42), 1e-8)
			require.Zero(t, ledger.activeHoldCount())
		})
	}
}

// --- BILL-H1 #5: POST /v1/systemone (including multi-group key fixed-route guard) ---

func TestBILLH1_SystemOne_PreauthorizeRejectAndRefund(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const pricingJSON = `{
		"jev-latest": {
			"litellm_provider": "typesafe",
			"mode": "chat",
			"input_cost_per_token": 0.0001,
			"output_cost_per_token": 0.0002
		}
	}`

	buildSystemOne := func(t *testing.T, balance float64, multiGroup bool) (*GatewayHandler, *preauthEntrypointsLedger, *service.APIKey) {
		t.Helper()
		cfg, _, billingSvc := newPreauthTestPricingService(t, pricingJSON)
		ledger := newPreauthEntrypointsLedger(42, balance)
		preauth := newPreauthTestService(t, cfg, billingSvc, ledger)

		groupID := int64(9)
		group := &service.Group{ID: groupID, Platform: service.PlatformTypeSafe, Status: service.StatusActive, Hydrated: true, RateMultiplier: 1.0}
		snapshot := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: nil}, nil, nil, nil, nil)
		bcs := service.NewBillingCacheService(&preauthEntrypointsBillingCache{l: ledger}, nil, nil, nil, nil, nil, cfg, nil)
		t.Cleanup(bcs.Stop)
		gw := service.NewGatewayService(
			nil, &fakeGroupRepo{group: group}, &preauthEntrypointsUsageLogRepo{}, ledger, nil, nil, nil, nil, cfg, snapshot, nil,
			billingSvc, nil, bcs, nil, nil, &service.DeferredService{},
			nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		)
		h := &GatewayHandler{
			cfg:                  cfg,
			gatewayService:       gw,
			billingCacheService:  bcs,
			concurrencyHelper:    NewConcurrencyHelper(service.NewConcurrencyService(&fakeConcurrencyCache{}), SSEPingFormatNone, 0),
			balancePreauthorizer: preauth,
		}
		key := &service.APIKey{
			ID: 7, UserID: 42, GroupID: &groupID, Group: group, Status: service.StatusAPIKeyActive,
			User: &service.User{ID: 42, Status: service.StatusActive, Balance: 1000.0, Concurrency: 5},
		}
		if multiGroup {
			// SystemOne uses fixed primary group scheduling (never bindSelectedKeyRoute),
			// so multi-group keys MUST still be preauthorized eagerly via withFixedKeyRouteForPreauthorization.
			key.RouteGroupIDs = []int64{groupID, 99}
		}
		return h, ledger, key
	}

	for _, multiGroup := range []bool{false, true} {
		name := "single_group"
		if multiGroup {
			name = "multi_group_fixed_route"
		}
		t.Run(name+"/rejects_low_live_wallet_before_scheduling", func(t *testing.T) {
			h, ledger, key := buildSystemOne(t, 0.00001, multiGroup)
			c, rec := newSystemOneHandlerContext(validSystemOneHandlerBody)
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.Group, key.Group))
			c.Set(string(middleware2.ContextKeyAPIKey), key)
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: key.UserID, Concurrency: 5})

			h.SystemOne(c)
			require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
			require.Zero(t, ledger.activeHoldCount())
		})

		t.Run(name+"/refunds_hold_when_no_account_available", func(t *testing.T) {
			h, ledger, key := buildSystemOne(t, 10.0, multiGroup)
			c, rec := newSystemOneHandlerContext(validSystemOneHandlerBody)
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.Group, key.Group))
			c.Set(string(middleware2.ContextKeyAPIKey), key)
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: key.UserID, Concurrency: 5})

			h.SystemOne(c)
			require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
			require.InDelta(t, 10.0, ledger.balance(42), 1e-9, "failed scheduling must refund live_wallet hold")
			require.Len(t, ledger.refundedSnapshot(), 1)
			require.Zero(t, ledger.activeHoldCount())
		})
	}
}

// --- BILL-H2: Gemini CachedContent Handler Create/TopUpFailure/Patch ---

func TestBILLH2_GeminiCachedContentsHandler_PreauthCreateTopUpFailureAndPatch(t *testing.T) {
	newPreauthCachedEnv := func(t *testing.T, pgBalance, liveWalletBalance float64) (*cachedBillingEnv, *preauthEntrypointsLedger) {
		t.Helper()
		env := newCachedBillingEnv(t, pgBalance, 900)
		// Rebuild gatewayService with BalancePreauthorizationEnabled=true and our live_wallet ledger.
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "model_pricing.json"), []byte(fmt.Sprintf(`{"gemini-2.5-pro":{
			"litellm_provider":"gemini","mode":"chat","input_cost_per_token":%g,"output_cost_per_token":0.00001,
			"cache_storage_cost_per_token_per_hour":%g}}`, cachedBillingInputPrice, cachedBillingStoragePrice)), 0o644))
		cfg := &config.Config{RunMode: config.RunModeStandard}
		cfg.Pricing.DataDir = dir
		cfg.Pricing.UpdateIntervalHours = 100000
		cfg.Default.RateMultiplier = 1.0
		cfg.Billing.BalancePreauthorizationEnabled = true
		pricing := service.NewPricingService(cfg, nil)
		require.NoError(t, pricing.Initialize())
		billing := service.NewBillingService(cfg, pricing)

		ledger := newPreauthEntrypointsLedger(env.apiKey.UserID, liveWalletBalance)
		preauth := newPreauthTestService(t, cfg, billing, ledger)

		userRepo := &cachedBillingUserRepo{balance: pgBalance}
		snapshot := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{env.accounts[900]}}, nil, nil, nil, nil)
		gw := service.NewGatewayService(
			nil, &fakeGroupRepo{group: env.group}, env.usage, ledger, userRepo, nil, nil, nil, cfg, snapshot, nil,
			billing, nil, &service.BillingCacheService{}, nil, nil, &service.DeferredService{},
			nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		)
		env.h.gatewayService = gw
		env.h.balancePreauthorizer = preauth
		env.h.geminiCachedContentService.SetBillingGateway(gw)
		return env, ledger
	}

	t.Run("create_rejected_when_live_wallet_exhausted_even_if_pg_balance_high", func(t *testing.T) {
		// PG balance is $500, but Redis live_wallet only has $0.00001 (e.g. text holds took the rest).
		env, ledger := newPreauthCachedEnv(t, 500.0, 0.00001)
		env.upstream.createBody = fmt.Sprintf(`{"name":"cachedContents/up1","expireTime":%q,"usageMetadata":{"totalTokenCount":1000000}}`, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339Nano))

		rec := env.create()
		require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
		require.Zero(t, env.upstream.methodCalls(http.MethodPost), "must not create upstream cache when live_wallet rejects preauth")
		require.InDelta(t, 0.00001, ledger.balance(env.apiKey.UserID), 1e-9)
		require.Zero(t, ledger.activeHoldCount())
	})

	t.Run("create_upstream_failure_refunds_live_wallet", func(t *testing.T) {
		env, ledger := newPreauthCachedEnv(t, 500.0, 10.0)
		env.upstream.failures = map[string]cachedBillingUpstreamFailure{
			http.MethodPost: {status: http.StatusBadRequest, body: `{"error":{"message":"invalid contents"}}`},
		}

		rec := env.create()
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		require.InDelta(t, 10.0, ledger.balance(env.apiKey.UserID), 1e-9)
		require.Len(t, ledger.refundedSnapshot(), 1)
		require.Zero(t, ledger.activeHoldCount())
	})

	t.Run("create_topup_failure_deletes_orphan_and_bills_input_only", func(t *testing.T) {
		// Small text body estimates ~1 token (~$0.0001 hold), so preauth succeeds against $2.00 live_wallet.
		// Upstream reports 1_000_000 tokens over 24h => input $1.25 + storage $108.00 = $109.25 -> TopUpFixedAmount FAILS!
		env, ledger := newPreauthCachedEnv(t, 500.0, 2.0)
		env.upstream.createBody = fmt.Sprintf(`{"name":"cachedContents/up-orphan","expireTime":%q,"usageMetadata":{"totalTokenCount":%d}}`,
			time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339Nano), cachedBillingTokens)

		rec := env.create()
		require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
		require.Equal(t, 1, env.upstream.methodCalls(http.MethodDelete), "orphan upstream cache must be deleted on top-up failure")
		require.Empty(t, env.repo.records, "failed cache must not be persisted locally")

		// Input-only tokens ($1.25) MUST be billed via the guard, leaving $0.75 in live_wallet!
		totals := env.waitForUsage(t, 1)
		require.Len(t, totals, 1)
		require.InDelta(t, 1.25, totals[0], 1e-6)
		require.InDelta(t, 0.75, ledger.balance(env.apiKey.UserID), 1e-6)
		require.Zero(t, ledger.activeHoldCount())
	})

	t.Run("create_success_preauthorizes_and_finalizes_exact_cost", func(t *testing.T) {
		env, ledger := newPreauthCachedEnv(t, 500.0, 200.0)
		env.upstream.createBody = fmt.Sprintf(`{"name":"cachedContents/up-ok","expireTime":%q,"usageMetadata":{"totalTokenCount":%d}}`,
			time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339Nano), cachedBillingTokens)

		rec := env.create()
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		totals := env.waitForUsage(t, 1)
		require.Len(t, totals, 1)
		wantCost := 1.25 + 4.5*24.0 // $1.25 input + $108.00 storage = $109.25
		require.InDelta(t, wantCost, totals[0], 1e-4)
		require.InDelta(t, 200.0-totals[0], ledger.balance(env.apiKey.UserID), 1e-4)
		require.Zero(t, ledger.activeHoldCount())
	})

	t.Run("patch_rejects_low_live_wallet_before_upstream_and_finalizes_on_success", func(t *testing.T) {
		// Extension of 23h on 1M tokens costs ~$103.50.
		envLow, ledgerLow := newPreauthCachedEnv(t, 500.0, 1.0)
		envLow.seed(time.Now().Add(time.Hour))
		recLow := envLow.patch()
		require.Equal(t, http.StatusTooManyRequests, recLow.Code, recLow.Body.String())
		require.Zero(t, envLow.upstream.methodCalls(http.MethodPatch), "patch must reject low live_wallet before PatchUpstream")
		require.InDelta(t, 1.0, ledgerLow.balance(envLow.apiKey.UserID), 1e-9)

		// Upstream error refunds hold.
		envErr, ledgerErr := newPreauthCachedEnv(t, 500.0, 200.0)
		envErr.seed(time.Now().Add(time.Hour))
		envErr.upstream.failures = map[string]cachedBillingUpstreamFailure{
			http.MethodPatch: {status: http.StatusBadRequest, body: `{"error":{"message":"invalid ttl"}}`},
		}
		recErr := envErr.patch()
		require.Equal(t, http.StatusBadRequest, recErr.Code, recErr.Body.String())
		require.InDelta(t, 200.0, ledgerErr.balance(envErr.apiKey.UserID), 1e-9)
		require.Len(t, ledgerErr.refundedSnapshot(), 1)

		// Success finalizes exact extension cost.
		envOK, ledgerOK := newPreauthCachedEnv(t, 500.0, 200.0)
		envOK.seed(time.Now().Add(time.Hour))
		recOK := envOK.patch()
		require.Equal(t, http.StatusOK, recOK.Code, recOK.Body.String())
		totals := envOK.waitForUsage(t, 1)
		require.Len(t, totals, 1)
		require.Greater(t, totals[0], 90.0)
		require.InDelta(t, 200.0-totals[0], ledgerOK.balance(envOK.apiKey.UserID), 1e-4)
		require.Zero(t, ledgerOK.activeHoldCount())
	})
}

// --- BILL-H1: bindSelectedKeyRoute FixedAmount deferral + failover top-up ---

func TestBILLH1_BindSelectedKeyRoute_FixedAmountDeferAndFailoverTopUp(t *testing.T) {
	c, primary, backup := routeBindingFixture()
	cfg, _, billingSvc := newPreauthTestPricingService(t, `{}`)
	ledger := newPreauthEntrypointsLedger(42, 0.07) // enough for group A ($0.05), NOT group B ($0.10)
	preauth := newPreauthTestService(t, cfg, billingSvc, ledger)
	gateway := &routeBindingGatewayStub{}

	estimate := func(_ context.Context, key *service.APIKey) (float64, error) {
		return 0.05 * key.Group.RateMultiplier, nil
	}

	// Multi-group key MUST defer fixed-amount preauthorization until route selection.
	guard, err := preauthorizeFixedAmountGatewayRequest(c.Request.Context(), preauth, primary, nil, "web_search:multi-1", []byte("q"), 0.05)
	require.NoError(t, err)
	require.Nil(t, guard)
	require.InDelta(t, 0.07, ledger.balance(42), 1e-9)

	// Selecting primary (1x => $0.05) acquires the hold under FixedRequestID.
	err = bindSelectedKeyRoute(c, gateway, nil, preauth, keyRouteBinding{
		Previous: primary, Selected: primary, Guard: &guard,
		Body: []byte("q"), Model: "grok-web-search",
		FixedRequestID: "web_search:multi-1", FixedAmount: estimate,
	})
	require.NoError(t, err)
	require.NotNil(t, guard)
	require.Equal(t, "web_search:multi-1", guard.RequestID())
	require.InDelta(t, 0.02, ledger.balance(42), 1e-9)

	// Failover to backup (2x => $0.10) attempts TopUpFixedAmount($0.10) and fails cleanly
	// because only $0.02 remains in live_wallet; primary stays published.
	err = bindSelectedKeyRoute(c, gateway, nil, preauth, keyRouteBinding{
		Previous: primary, Selected: backup, Guard: &guard,
		Body: []byte("q"), Model: "grok-web-search",
		FixedRequestID: "web_search:multi-1", FixedAmount: estimate,
	})
	require.ErrorIs(t, err, service.ErrBalanceWithholdingFailed)
	bound, _ := middleware2.GetAPIKeyFromContext(c)
	require.Same(t, primary, bound)

	// Top up live_wallet and retry failover -> succeeds under the SAME guard!
	ledger.setBalance(42, 0.50)
	err = bindSelectedKeyRoute(c, gateway, nil, preauth, keyRouteBinding{
		Previous: primary, Selected: backup, Guard: &guard,
		Body: []byte("q"), Model: "grok-web-search",
		FixedRequestID: "web_search:multi-1", FixedAmount: estimate,
	})
	require.NoError(t, err)
	require.InDelta(t, 0.10, guard.HoldAmount(), 1e-9)
	require.InDelta(t, 0.45, ledger.balance(42), 1e-9)

	// Cleanup refund restores the full $0.10 hold.
	require.NoError(t, guard.Refund(context.Background()))
	require.InDelta(t, 0.55, ledger.balance(42), 1e-9)
	require.Zero(t, ledger.activeHoldCount())
}

func float64Ptr(v float64) *float64 { return &v }

var _ = json.Valid
