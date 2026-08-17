package admin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGroupHandlerSimpleModeSanitizesCommercialFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStubAdminService()
	h := NewGroupHandlerWithConfig(svc, nil, nil, &config.Config{RunMode: config.RunModeSimple})
	r := gin.New()
	r.POST("/groups", h.Create)
	r.PUT("/groups/:id", h.Update)

	create := `{"name":"simple","platform":"anthropic","rate_multiplier":7,"is_exclusive":true,"subscription_type":"subscription","daily_limit_usd":10,"allow_image_generation":true,"rpm_limit":99}`
	req := httptest.NewRequest(http.MethodPost, "/groups", bytes.NewBufferString(create))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	require.Equal(t, http.StatusOK, res.Code)
	require.Len(t, svc.createdGroups, 1)
	created := svc.createdGroups[0]
	require.Equal(t, 1.0, created.RateMultiplier)
	require.False(t, created.IsExclusive)
	require.Equal(t, service.SubscriptionTypeStandard, created.SubscriptionType)
	require.Nil(t, created.DailyLimitUSD)
	require.False(t, created.AllowImageGeneration)
	require.Zero(t, created.RPMLimit)

	update := `{"name":"renamed","rate_multiplier":9,"is_exclusive":true,"subscription_type":"subscription","daily_limit_usd":12,"allow_image_generation":true,"status":"inactive","rpm_limit":123}`
	req = httptest.NewRequest(http.MethodPut, "/groups/2", bytes.NewBufferString(update))
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	require.Equal(t, http.StatusOK, res.Code)
	require.Len(t, svc.updatedGroups, 1)
	updated := svc.updatedGroups[0]
	require.NotNil(t, updated.RateMultiplier)
	require.Equal(t, 1.0, *updated.RateMultiplier)
	require.NotNil(t, updated.IsExclusive)
	require.False(t, *updated.IsExclusive)
	require.Equal(t, service.SubscriptionTypeStandard, updated.SubscriptionType)
	require.Nil(t, updated.DailyLimitUSD)
	require.NotNil(t, updated.AllowImageGeneration)
	require.False(t, *updated.AllowImageGeneration)
	require.Empty(t, updated.Status)
	require.NotNil(t, updated.RPMLimit)
	require.Zero(t, *updated.RPMLimit)
}
