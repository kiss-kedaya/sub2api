//go:build unit

package handler

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type smartRouteHandlerSnapshot struct{ fakeSchedulerCache }

func (s *smartRouteHandlerSnapshot) GetSnapshot(_ context.Context, bucket service.SchedulerBucket) ([]*service.Account, bool, error) {
	accounts := make([]*service.Account, 0)
	for _, account := range s.accounts {
		for _, groupID := range account.GroupIDs {
			if groupID == bucket.GroupID && account.Platform == bucket.Platform {
				accounts = append(accounts, account)
			}
		}
	}
	return accounts, true, nil
}

type smartRouteHandlerGroups struct {
	service.GroupRepository
	groups map[int64]*service.Group
}

func (r smartRouteHandlerGroups) GetByIDLite(_ context.Context, id int64) (*service.Group, error) {
	return r.groups[id], nil
}

type smartRouteHandlerUsage struct {
	service.UsageLogRepository
	logs chan *service.UsageLog
}

func (r smartRouteHandlerUsage) Create(_ context.Context, usage *service.UsageLog) (bool, error) {
	r.logs <- usage
	return true, nil
}

type smartRouteHandlerBilling struct{ service.UsageBillingRepository }

func (smartRouteHandlerBilling) Apply(context.Context, *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	return &service.UsageBillingApplyResult{Applied: true}, nil
}

type smartRouteHandlerUpstream struct {
	service.HTTPUpstream
	groupID int64
	tier    string
	model   string
	effort  string
	calls   int
}

func (u *smartRouteHandlerUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls++
	group, ok := req.Context().Value(ctxkey.Group).(*service.Group)
	if !ok {
		return nil, errors.New("forwarded request is missing its group")
	}
	u.groupID = group.ID
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.tier, u.model = gjson.GetBytes(body, "service_tier").String(), gjson.GetBytes(body, "model").String()
	u.effort = gjson.GetBytes(body, "reasoning.effort").String()
	response := `{"id":"resp_route_test","object":"response","status":"completed","model":"gpt-5.1","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":1000,"output_tokens":10,"total_tokens":1010,"input_tokens_details":{"cached_tokens":600}}}`
	contentType := "application/json"
	if gjson.GetBytes(body, "stream").Bool() {
		contentType = "text/event-stream"
		response = "event: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":1,\"response\":" + response + "}\n\n"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response))}, nil
}

func TestSmartRouteHandlerFallbackUpdatesForwardingAndUsage(t *testing.T) {
	for _, endpoint := range []string{"responses", "chat/completions", "messages"} {
		t.Run(endpoint, func(t *testing.T) {
			primary := &service.Group{ID: 9, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true, RateMultiplier: 1, ForceOpenAIFast: true, FreeOpenAIFast: true}
			backup := &service.Group{ID: 17, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true, RateMultiplier: 2}
			if endpoint == "messages" {
				primary.MaxReasoningEffort, backup.MaxReasoningEffort = "low", "medium"
			}
			for _, group := range []*service.Group{primary, backup} {
				group.AllowMessagesDispatch = true
				group.ModelAllowlist = service.GroupModelsListConfig{Enabled: true, Models: []string{"gpt-5.1"}}
			}
			account := service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{backup.ID}, Credentials: map[string]any{
				"api_key": "synthetic-route-test", "base_url": "https://upstream.invalid/v1", "api_protocol": service.APIProtocolResponses,
				"model_mapping": map[string]any{"gpt-5.1": "gpt-5.1"},
			}}
			cfg := &config.Config{RunMode: config.RunModeStandard}
			cfg.Default.RateMultiplier = 1
			accountRepo := openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}
			snapshot := service.NewSchedulerSnapshotService(&smartRouteHandlerSnapshot{fakeSchedulerCache{accounts: []*service.Account{&account}}}, nil, accountRepo,
				smartRouteHandlerGroups{groups: map[int64]*service.Group{9: primary, 17: backup}}, cfg)
			for _, id := range []int64{9, 17} {
				_, err := snapshot.GetGroupByIDLite(context.Background(), id)
				require.NoError(t, err)
			}
			// Only eligibility storage is bypassed; routing, forwarding and usage
			// settlement below are the production handlers and services.
			billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, &config.Config{RunMode: config.RunModeSimple}, nil)
			t.Cleanup(billingCache.Stop)
			usage := smartRouteHandlerUsage{logs: make(chan *service.UsageLog, 1)}
			upstream := &smartRouteHandlerUpstream{}
			gateway := service.NewOpenAIGatewayService(accountRepo, usage, smartRouteHandlerBilling{}, nil, nil, nil, nil, cfg, snapshot, nil,
				service.NewBillingService(cfg, nil), nil, billingCache, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
			h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billingCache, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
			key := &service.APIKey{ID: 7, UserID: 42, User: &service.User{ID: 42, Status: service.StatusActive}, GroupID: &primary.ID, Group: primary, RouteGroupIDs: []int64{9, 17}}
			body := `{"model":"gpt-5.1","stream":false,"input":"hello"}`
			if endpoint != "responses" {
				body = `{"model":"gpt-5.1","stream":false,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`
			}
			if endpoint == "messages" {
				body = `{"model":"gpt-5.1","stream":false,"max_tokens":64,"output_config":{"effort":"high"},"messages":[{"role":"user","content":"hello"}]}`
			}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			ctx := context.WithValue(context.Background(), ctxkey.Group, primary)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, bytes.NewBufferString(body)).WithContext(ctx)
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(string(middleware.ContextKeyAPIKey), key)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42, Concurrency: 0})
			switch endpoint {
			case "responses":
				h.Responses(c)
			case "chat/completions":
				h.ChatCompletions(c)
			case "messages":
				h.Messages(c)
			}
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), "hello")
			require.Equal(t, 1, upstream.calls)
			require.Equal(t, backup.ID, upstream.groupID)
			require.Empty(t, upstream.tier, "primary's forced Fast must not leak into fallback")
			require.Equal(t, "gpt-5.1", upstream.model)
			if endpoint == "messages" {
				require.Equal(t, "medium", upstream.effort, "the fallback's reasoning policy must replace the primary policy")
			}
			select {
			case recorded := <-usage.logs:
				require.Equal(t, backup.ID, *recorded.GroupID)
				require.Equal(t, 2.0, recorded.RateMultiplier)
				require.Equal(t, 600, recorded.CacheReadTokens)
				require.Equal(t, 400, recorded.InputTokens)
			case <-time.After(3 * time.Second):
				t.Fatal("usage record was not submitted")
			}
		})
	}
}
