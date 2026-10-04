//go:build unit

package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type wrappedUpstreamFailoverHTTP struct {
	service.HTTPUpstream
	mu  sync.Mutex
	ids []int64
}

func (u *wrappedUpstreamFailoverHTTP) Do(_ *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.ids = append(u.ids, id)
	u.mu.Unlock()
	body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_failed_fixture\"}}\n\n" +
		"data: {\"type\":\"response.failed\",\"sequence_number\":8,\"response\":{\"id\":\"resp_failed_fixture\",\"status\":\"failed\",\"error\":{\"code\":\"upstream_error\",\"message\":\"Upstream access forbidden, please contact administrator\"}}}\n\n"
	if id == 9911 {
		body = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_healthy\"}}\n\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"healthy fallback\"}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_healthy\",\"model\":\"gpt-6-astra\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func TestOpenAIResponsesWrappedUpstreamFailureActuallyCallsFallback(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(strconv.FormatBool(passthrough), func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			groupID := int64(4206)
			accounts := []service.Account{}
			for index, id := range []int64{9910, 9911} {
				accounts = append(accounts, service.Account{ID: id, Name: "fixture", Platform: service.PlatformOpenAI,
					Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Priority: index + 1,
					GroupIDs: []int64{groupID}, Credentials: map[string]any{"api_key": "fixture-" + strconv.FormatInt(id, 10), "base_url": "https://api.example.test"},
					Extra: map[string]any{"openai_passthrough": passthrough}})
			}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			cfg.Default.RateMultiplier = 1
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Gateway.MaxAccountSwitches = 1
			repo := &openAIWSFailoverHandlerAccountRepoStub{accounts: accounts}
			upstream := &wrappedUpstreamFailoverHTTP{}
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			svc := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
				service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
			h := NewOpenAIGatewayHandler(svc, service.NewConcurrencyService(nil), billing,
				service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-6-astra","input":"hello","stream":true}`))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 1806, GroupID: &groupID,
				User:  &service.User{ID: 1706, Status: service.StatusActive},
				Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true}})
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1706, Concurrency: 0})
			h.Responses(c)
			upstream.mu.Lock()
			ids := append([]int64(nil), upstream.ids...)
			upstream.mu.Unlock()
			require.Equal(t, []int64{9910, 9911}, ids, "must make a real second upstream call, not only record a switch")
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Contains(t, recorder.Body.String(), "healthy fallback")
			require.NotContains(t, recorder.Body.String(), "resp_failed_fixture")
			require.NotContains(t, recorder.Body.String(), "response.failed")
		})
	}
}
