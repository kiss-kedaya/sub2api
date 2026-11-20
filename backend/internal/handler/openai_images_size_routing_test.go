//go:build unit

package handler

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// openAIImagesSizeRoutingAccountRepo 在 failover 基座上补充分组可用账号查询，
// 供 OpenAIImageSizeTierUnsupported 判定“分组内没有该档位账号”。
type openAIImagesSizeRoutingAccountRepo struct {
	openAIImagesFailoverAccountRepo
}

func (r openAIImagesSizeRoutingAccountRepo) ListModelAvailabilityCandidates(_ context.Context, _ *int64, platforms []string, _ bool) ([]service.Account, error) {
	var out []service.Account
	for _, platform := range platforms {
		out = append(out, r.accountsForPlatform(platform)...)
	}
	return out, nil
}

func newOpenAIImagesSizeRoutingTestHandler(t *testing.T, accounts []service.Account, upstream service.HTTPUpstream) (*OpenAIGatewayHandler, *service.BillingCacheService) {
	t.Helper()
	cfg := &config.Config{RunMode: config.RunModeSimple}
	accountRepo := openAIImagesSizeRoutingAccountRepo{openAIImagesFailoverAccountRepo{accounts: accounts}}
	gatewayService := service.NewOpenAIGatewayService(
		accountRepo,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		cfg,
		nil,
		nil,
		nil,
		nil,
		nil,
		upstream,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	billingService := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingService.Stop)
	handler := NewOpenAIGatewayHandler(
		gatewayService,
		service.NewConcurrencyService(nil),
		billingService,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil,
		nil,
		nil,
		nil,
		cfg,
	)
	handler.maxAccountSwitches = 10
	return handler, billingService
}

func serveOpenAIImagesSizeRoutingRequest(t *testing.T, handler *OpenAIGatewayHandler, groupID int64, body string) (*httptest.ResponseRecorder, *gin.Context) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID:      99,
		GroupID: &groupID,
		Group:   &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true, AllowImageGeneration: true},
		User:    &service.User{ID: 100},
	})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 100, Concurrency: 0})
	handler.Images(c)
	return rec, c
}

func newOpenAIImagesSizeRoutingAccount(id int64, tiers ...any) service.Account {
	return service.Account{
		ID:          id,
		Name:        fmt.Sprintf("image-size-account-%d", id),
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeOAuth,
		Status:      service.StatusActive,
		Schedulable: true,
		Priority:    int(id),
		Credentials: map[string]any{"access_token": fmt.Sprintf("token-%d", id)},
		Extra:       map[string]any{service.AccountExtraOpenAIImageSizeTiers: tiers},
	}
}

func TestOpenAIGatewayHandlerImages_SizeTierMissingFromGroupReturns400(t *testing.T) {
	upstream := &openAIImagesFailoverHTTPUpstream{}
	handler, _ := newOpenAIImagesSizeRoutingTestHandler(t, []service.Account{
		newOpenAIImagesSizeRoutingAccount(1, "1K", "2K"),
	}, upstream)

	rec, c := serveOpenAIImagesSizeRoutingRequest(t, handler, 3140, `{"model":"gpt-image-1","prompt":"draw a cat","size":"3840x2160"}`)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid_request_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Contains(t, gjson.GetBytes(rec.Body.Bytes(), "error.message").String(), "4K")
	require.Empty(t, upstream.calls(), "a request no account may serve must not reach an upstream")
	require.Equal(t, service.OpsClientBusinessLimitedReasonLocalModelConfiguration, service.OpsClientBusinessLimitedReason(c))
}

func TestOpenAIGatewayHandlerImages_SizeTierRoutesToAllowedAccounts(t *testing.T) {
	upstream := &openAIImagesFailoverHTTPUpstream{}
	handler, _ := newOpenAIImagesSizeRoutingTestHandler(t, []service.Account{
		newOpenAIImagesSizeRoutingAccount(1, "1K", "2K"),
		newOpenAIImagesSizeRoutingAccount(2, "4K"),
		newOpenAIImagesSizeRoutingAccount(3, "4K"),
	}, upstream)

	rec, _ := serveOpenAIImagesSizeRoutingRequest(t, handler, 3141, `{"model":"gpt-image-1","prompt":"draw a cat","size":"3840x2160"}`)

	// 上游在此基座上返回 SSE server_error，请求已到达允许 4K 的账号即说明分流生效。
	calls := upstream.calls()
	require.NotEmpty(t, calls)
	for _, accountID := range calls {
		require.NotEqual(t, int64(1), accountID, "the 1K/2K-only account must not serve a 4K request")
	}
	require.NotEqual(t, http.StatusBadRequest, rec.Code)
}
