//go:build unit

package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type silenceHandlerBody struct {
	prefix *strings.Reader
	closed chan struct{}
	once   sync.Once
}

func (b *silenceHandlerBody) Read(p []byte) (int, error) {
	if b.prefix.Len() > 0 {
		return b.prefix.Read(p)
	}
	<-b.closed
	return 0, errors.New("synthetic closed input")
}
func (b *silenceHandlerBody) Close() error { b.once.Do(func() { close(b.closed) }); return nil }

type silenceHandlerUpstream struct {
	service.HTTPUpstream
	wire  string
	calls int
}

func (u *silenceHandlerUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls++
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: &silenceHandlerBody{prefix: strings.NewReader(u.wire), closed: make(chan struct{})}}, nil
}

type silenceHandlerLedger struct {
	openAIWSFirstRefusalBillingRepo
	finalizations atomic.Int32
	refunds       atomic.Int32
}

func (r *silenceHandlerLedger) BeginBalancePreauthorizationFinalization(_ context.Context, _ string, _ int64, amount float64, _ string) error {
	if amount != 0 {
		return errors.New("silence must finalize at zero")
	}
	r.finalizations.Add(1)
	return nil
}
func (*silenceHandlerLedger) CompleteBalancePreauthorizationRefund(context.Context, string, int64) error {
	return nil
}
func (r *silenceHandlerLedger) RefundLiveBalance(context.Context, int64, string) (service.LiveBalanceResult, error) {
	r.refunds.Add(1)
	return service.LiveBalanceResult{Outcome: service.LiveBalanceOutcomeApplied, State: service.LiveBalanceAttemptRefunded}, nil
}
func (*silenceHandlerLedger) AuthorizeLiveBalance(context.Context, int64, string, float64, float64) (service.LiveBalanceResult, error) {
	return service.LiveBalanceResult{}, errors.New("unexpected authorize")
}
func (*silenceHandlerLedger) TopUpLiveBalance(context.Context, int64, string, float64) (service.LiveBalanceResult, error) {
	return service.LiveBalanceResult{}, errors.New("unexpected top up")
}
func (*silenceHandlerLedger) FinalizeLiveBalance(context.Context, int64, string, float64) (service.LiveBalanceResult, error) {
	return service.LiveBalanceResult{}, errors.New("unexpected charge")
}

func TestDeepSeekStreamSilenceReviewHandlersSettleOnce(t *testing.T) {
	t.Cleanup(service.SetDeepSeekStreamSilenceLimitForTest(80 * time.Millisecond))
	for _, endpoint := range []string{"embeddings", "alpha/search", "responses"} {
		streams := []bool{false}
		if endpoint == "responses" {
			streams = append(streams, true)
		}
		for _, stream := range streams {
			t.Run(fmt.Sprintf("%s/%t", endpoint, stream), func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				primary := &service.Group{ID: 4001, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true, RateMultiplier: 1, AllowMessagesDispatch: true}
				price, providerKey := 1.0, "synthetic-search"
				backup := &service.Group{ID: 4002, Platform: service.PlatformDeepseek, Status: service.StatusActive, Hydrated: true, RateMultiplier: 1, SearchPricePer1k: &price, SearchProviderAPIKey: &providerKey}
				account := service.Account{ID: 9901, Name: "synthetic", Platform: service.PlatformDeepseek, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{backup.ID}, Credentials: map[string]any{"api_key": "synthetic", "base_url": "https://example.invalid", "api_protocol": service.APIProtocolResponses}}
				cfg := &config.Config{RunMode: config.RunModeStandard}
				cfg.Default.RateMultiplier = 1
				cfg.Gateway.MaxAccountSwitches = 1
				repo := openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}
				snapshot := service.NewSchedulerSnapshotService(&smartRouteHandlerSnapshot{fakeSchedulerCache{accounts: []*service.Account{&account}}}, nil, repo, smartRouteHandlerGroups{groups: map[int64]*service.Group{primary.ID: primary, backup.ID: backup}}, cfg)
				for _, group := range []*service.Group{primary, backup} {
					_, err := snapshot.GetGroupByIDLite(context.Background(), group.ID)
					require.NoError(t, err)
				}
				billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, &config.Config{RunMode: config.RunModeSimple}, nil)
				t.Cleanup(billing.Stop)
				usage, ledger := &openAIWSFirstRefusalUsageRepo{}, &silenceHandlerLedger{}
				wire := `{"usage":{"input_tokens":1000,"prompt_tokens":1000,"output_tokens":2}}`
				if endpoint == "responses" {
					wire = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_synthetic\",\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial answer\"}\n\n"
				}
				upstream := &silenceHandlerUpstream{wire: wire}
				gateway := service.NewOpenAIGatewayService(repo, usage, ledger, nil, nil, nil, nil, cfg, snapshot, nil, service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
				t.Cleanup(gateway.StopOpenAICodexTicketHarvester)
				reports, restoreSchedule := service.InstallDeepSeekSilenceScheduleObserverForTest(gateway)
				defer restoreSchedule()
				h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
				key := &service.APIKey{ID: 55, UserID: 77, User: &service.User{ID: 77, Status: service.StatusActive, Balance: 100}, GroupID: &primary.ID, Group: primary, RouteGroupIDs: []int64{primary.ID, backup.ID}}
				large := strings.Repeat("x", 20<<20+1)
				body := fmt.Sprintf(`{"model":"deepseek-synthetic","input":"%s","commands":[{"query":"%s"}],"stream":false}`, large, large)
				if endpoint == "responses" {
					body = fmt.Sprintf(`{"model":"deepseek-synthetic","stream":%t,"input":[{"type":"message","role":"user","content":"%s"},{"type":"web_search_call","id":"ws_synthetic","status":"completed","action":{"type":"search","query":"synthetic"}}],"tools":[{"type":"web_search"}],"tool_choice":"none"}`, stream, large)
				}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				ctx := context.WithValue(context.Background(), ctxkey.Group, primary)
				ctx = service.WithOpsGinContext(ctx, c)
				guard := service.NewDeepSeekSilenceGuardForTest(ledger, ledger)
				ctx = service.ContextWithBalancePreauthorizationGuard(ctx, guard)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, strings.NewReader(body)).WithContext(ctx)
				c.Set(string(middleware.ContextKeyAPIKey), key)
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 77})
				switch endpoint {
				case "embeddings":
					h.Embeddings(c)
				case "alpha/search":
					h.AlphaSearch(c)
				default:
					h.Responses(c)
				}
				require.Equal(t, 1, upstream.calls, rec.Body.String())
				require.Equal(t, 1, strings.Count(rec.Body.String(), "Upstream did not respond in time"))
				if stream {
					require.Equal(t, 1, strings.Count(rec.Body.String(), "event: response.failed"))
				} else {
					require.Equal(t, http.StatusGatewayTimeout, rec.Code, rec.Body.String())
					require.True(t, gjson.Valid(rec.Body.String()), rec.Body.String())
					require.NotContains(t, rec.Body.String(), "event:")
				}
				require.Eventually(t, func() bool { return len(usage.snapshot()) == 1 && len(ledger.snapshot()) == 1 }, 3*time.Second, 10*time.Millisecond)
				rows, settlements := usage.snapshot(), ledger.snapshot()
				require.Len(t, rows, 1)
				require.Len(t, settlements, 1)
				require.Zero(t, rows[0].ActualCost)
				require.Zero(t, rows[0].TotalCost)
				require.Zero(t, settlements[0].BalanceCost)
				require.True(t, settlements[0].BalancePreauthorized)
				require.Equal(t, int32(1), ledger.finalizations.Load())
				require.Equal(t, int32(1), ledger.refunds.Load())
				require.NoError(t, guard.Refund(context.Background()))
				require.Equal(t, int32(1), ledger.refunds.Load(), "the transferred handler guard must not refund twice")
				require.Zero(t, reports(), "silence must not report scheduler success or failure")
				require.True(t, account.Schedulable)
				raw, exists := c.Get(service.OpsUpstreamErrorsKey)
				require.True(t, exists)
				events, ok := raw.([]*service.OpsUpstreamErrorEvent)
				require.True(t, ok)
				require.Len(t, events, 1)
				require.Equal(t, "stream_silence_limit", events[0].Kind)
			})
		}
	}
}

var _ io.Reader = (*silenceHandlerBody)(nil)
