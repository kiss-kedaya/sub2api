//go:build unit

package handler

import (
	"context"
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

func TestGrokImageQualityFailoverPreservesQualityAndFinalError(t *testing.T) {
	const rejection = `{"error":{"code":"bad_response_status_code","message":"This model only supports the following quality value(s): low, medium, auto.","param":"","type":"bad_response_status_code"}}`
	for _, scenario := range []string{"second succeeds", "all reject", "bad prompt"} {
		t.Run(scenario, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			group := &service.Group{ID: 24, Platform: service.PlatformGrok, Status: service.StatusActive, Hydrated: true, AllowImageGeneration: true, RateMultiplier: 1}
			accounts := []service.Account{
				{ID: 29131, Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{24}, Priority: 0, Credentials: map[string]any{"api_key": "fixture-only"}},
				{ID: 29132, Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{24}, Priority: 1, Credentials: map[string]any{"api_key": "fixture-only"}},
			}
			repo := openAIImagesFailoverAccountRepo{accounts: accounts}
			var ids []int64
			upstream := &grokMediaSlotUpstream{call: func(req *http.Request, id int64) (*http.Response, error) {
				ids = append(ids, id)
				body, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.Equal(t, "high", gjson.GetBytes(body, "quality").String(), "never downgrade the requested quality")
				require.Equal(t, "grok-imagine-image-2.0", gjson.GetBytes(body, "model").String())
				status, payload := 400, rejection
				if scenario == "second succeeds" && id == 29132 {
					status = 200
					payload = `{"data":[{"url":"https://images.invalid/result.png"}]}`
				}
				if scenario == "bad prompt" {
					payload = `{"error":{"message":"prompt is required","type":"invalid_request_error"}}`
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(payload))}, nil
			}}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			usage := smartRouteHandlerUsage{logs: make(chan *service.UsageLog, 4)}
			concurrency := service.NewConcurrencyService(nil)
			gateway := service.NewOpenAIGatewayService(repo, usage, smartRouteHandlerBilling{}, nil, nil, nil, nil, cfg, nil, concurrency, service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
			h := NewOpenAIGatewayHandler(gateway, concurrency, billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(10))
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"grok-imagine-image-2.0","quality":"high","prompt":"test"}`)).WithContext(ctx)
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 20, UserID: 10, User: &service.User{ID: 10, Status: service.StatusActive}, GroupID: &group.ID, Group: group})
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 10})
			h.GrokImages(c)
			if scenario == "bad prompt" {
				require.Equal(t, []int64{29131}, ids)
				require.Equal(t, 400, w.Code)
			} else {
				require.Equal(t, []int64{29131, 29132}, ids, "each candidate is attempted once, including when all reject")
				if scenario == "second succeeds" {
					require.Equal(t, 200, w.Code, w.Body.String())
					require.Contains(t, w.Body.String(), "result.png")
					select {
					case log := <-usage.logs:
						require.Equal(t, int64(29132), log.AccountID)
					case <-time.After(3 * time.Second):
						t.Fatal("successful attempt usage not recorded")
					}
				} else {
					require.Equal(t, 400, w.Code, w.Body.String())
					require.Equal(t, "invalid_request_error", gjson.GetBytes(w.Body.Bytes(), "error.type").String())
					require.Contains(t, w.Body.String(), "low, medium, auto.")
				}
			}
			select {
			case <-usage.logs:
				t.Fatal("failed attempt must not be charged")
			default:
			}
		})
	}
}
