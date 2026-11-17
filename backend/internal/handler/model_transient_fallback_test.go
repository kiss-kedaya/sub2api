//go:build unit

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelTransientFallbackSingleAccountReturnsUpstreamStatus(t *testing.T) {
	group := &service.Group{ID: 10, Platform: service.PlatformAnthropic, Status: service.StatusActive, Hydrated: true}
	account := &service.Account{ID: 1, Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{10}}
	cfg := &config.Config{}
	snapshot := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{account}}, nil, nil, nil, cfg)
	rateLimit := service.NewRateLimitService(nil, nil, cfg, nil, nil)
	gateway := service.NewGatewayService(nil, nil, nil, nil, nil, nil, nil, nil, cfg,
		snapshot, nil, nil, rateLimit, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	ctx := gateway.PrepareSchedulerRequestContext(context.WithValue(context.Background(), ctxkey.Group, group))
	// Use the real response/error policy to enter the long model cooldown.
	for range 3 {
		rateLimit.HandleUpstreamError(ctx, account, http.StatusBadGateway, nil, nil, "model-a")
	}
	require.False(t, rateLimit.ModelTransientAllowed(account.ID, "model-a"))

	selection, err := gateway.SelectAccountWithLoadAwareness(ctx, &group.ID, "", "model-a", nil, "", 0)
	require.NoError(t, err)
	require.Equal(t, account.ID, selection.Account.ID)
	state := NewFillFailoverState(1, false)
	upstream := &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: []byte(`{"error":{"message":"upstream unavailable"}}`)}
	require.Equal(t, FailoverContinue, state.HandleFailoverError(ctx, gateway, account.ID, account.Platform, 0, upstream))
	_, err = gateway.SelectAccountWithLoadAwareness(ctx, &group.ID, "", "model-a", state.FailedAccountIDs, "", 0)
	require.ErrorIs(t, err, service.ErrNoAvailableAccounts)
	require.Equal(t, FailoverExhausted, state.HandleSelectionExhausted(ctx))

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(ctx)
	(&GatewayHandler{}).handleFailoverExhausted(c, state.LastFailoverErr, account.Platform, false)
	require.Equal(t, http.StatusBadGateway, recorder.Code, "keep the upstream status, never replace it with routing 503")
}
