package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newSimpleModeGroupRouter(svc *stubAdminService) *gin.Engine {
	h := NewGroupHandlerWithConfig(svc, nil, nil, &config.Config{RunMode: config.RunModeSimple})
	r := gin.New()
	r.GET("/groups", h.List)
	r.GET("/groups/all", h.GetAll)
	r.GET("/groups/:id", h.GetByID)
	r.GET("/groups/:id/models-list-candidates", h.GetModelsListCandidates)
	r.POST("/groups", h.Create)
	r.PUT("/groups/:id", h.Update)
	r.POST("/groups/:id/duplicate", h.Duplicate)
	r.GET("/groups/:id/composite-routes", h.ListCompositeRoutes)
	r.POST("/groups/:id/composite-routes", h.CreateCompositeRoute)
	r.PUT("/groups/:id/rate-multipliers", h.BatchSetGroupRateMultipliers)
	r.DELETE("/groups/:id/rate-multipliers", h.ClearGroupRateMultipliers)
	r.PUT("/groups/:id/rpm-overrides", h.BatchSetGroupRPMOverrides)
	return r
}

func TestGroupHandlerSimpleModeSanitizesCommercialFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStubAdminService()
	r := newSimpleModeGroupRouter(svc)

	create := `{"name":"simple","description":"basic grouping","platform":"anthropic","rate_multiplier":7,"is_exclusive":true,"subscription_type":"subscription","daily_limit_usd":10,"long_context_pricing_enabled":true,"model_pricing":[{"model":"claude","input_price":1}],"allow_image_generation":true,"allow_batch_image_generation":true,"video_price_720p":2,"web_search_price_per_call":3,"audio_realtime_price_per_min":4,"rpm_limit":99}`
	req := httptest.NewRequest(http.MethodPost, "/groups", bytes.NewBufferString(create))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	require.Equal(t, http.StatusOK, res.Code)
	require.Len(t, svc.createdGroups, 1)
	created := svc.createdGroups[0]
	require.Equal(t, "basic grouping", created.Description)
	require.Equal(t, 1.0, created.RateMultiplier)
	require.False(t, created.IsExclusive)
	require.Equal(t, service.SubscriptionTypeStandard, created.SubscriptionType)
	require.Nil(t, created.DailyLimitUSD)
	require.False(t, created.AllowImageGeneration)
	require.False(t, created.LongContextPricingEnabled)
	require.Empty(t, created.ModelPricing)
	require.False(t, created.AllowBatchImageGeneration)
	require.Nil(t, created.VideoPrice720P)
	require.Nil(t, created.WebSearchPricePerCall)
	require.Nil(t, created.AudioRealtimePricePerMin)
	require.Zero(t, created.RPMLimit)

	update := `{"name":"renamed","description":"still basic","rate_multiplier":9,"is_exclusive":true,"subscription_type":"subscription","daily_limit_usd":12,"long_context_pricing_enabled":true,"model_pricing":[{"model":"claude","input_price":1}],"allow_image_generation":true,"allow_batch_image_generation":true,"video_price_720p":2,"web_search_price_per_call":3,"audio_realtime_price_per_min":4,"status":"inactive","rpm_limit":123}`
	req = httptest.NewRequest(http.MethodPut, "/groups/2", bytes.NewBufferString(update))
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	require.Equal(t, http.StatusOK, res.Code)
	require.Len(t, svc.updatedGroups, 1)
	updated := svc.updatedGroups[0]
	require.NotNil(t, updated.Description)
	require.Equal(t, "still basic", *updated.Description)
	require.Nil(t, updated.RateMultiplier)
	require.Nil(t, updated.IsExclusive)
	require.Empty(t, updated.SubscriptionType)
	require.Nil(t, updated.DailyLimitUSD)
	require.Nil(t, updated.AllowImageGeneration)
	require.Nil(t, updated.LongContextPricingEnabled)
	require.Nil(t, updated.ModelPricing)
	require.Nil(t, updated.AllowBatchImageGeneration)
	require.Nil(t, updated.VideoPrice720P)
	require.Nil(t, updated.WebSearchPricePerCall)
	require.Nil(t, updated.AudioRealtimePricePerMin)
	require.Empty(t, updated.Status)
	require.Nil(t, updated.RPMLimit)
}

func float64PtrForSimpleModeTest(value float64) *float64 { return &value }

func TestGroupHandlerSimpleModeResponseUsesFieldAllowlist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStubAdminService()
	svc.groups = []service.Group{{
		ID: 1, Name: "basic", Description: "allowed", Platform: service.PlatformAnthropic,
		Status: service.StatusActive, RateMultiplier: 9, RPMLimit: 42,
		LongContextPricingEnabled: true,
		ModelPricing:              []service.ChannelModelPricing{{Models: []string{"claude"}}},
		AllowBatchImageGeneration: true, VideoPrice720P: float64PtrForSimpleModeTest(2),
		WebSearchPricePerCall: float64PtrForSimpleModeTest(3), AudioRealtimePricePerMin: float64PtrForSimpleModeTest(4),
		ModelRouting: map[string][]int64{"claude": {2}},
	}}
	r := newSimpleModeGroupRouter(svc)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/groups", nil))
	require.Equal(t, http.StatusOK, res.Code)

	var payload struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Items, 1)
	item := payload.Data.Items[0]
	require.Equal(t, "basic", item["name"])
	require.Equal(t, "allowed", item["description"])
	require.Equal(t, "anthropic", item["platform"])
	for _, forbidden := range []string{
		"rate_multiplier", "rpm_limit", "long_context_pricing_enabled", "model_pricing",
		"allow_batch_image_generation", "video_price_720p", "web_search_price_per_call",
		"audio_realtime_price_per_min", "model_routing",
	} {
		_, exposed := item[forbidden]
		require.Falsef(t, exposed, "simple mode exposed %s", forbidden)
	}
}

func TestGroupHandlerSimpleModeAllReadAndWriteResponsesUseFieldAllowlist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStubAdminService()
	svc.groups = []service.Group{{
		ID: 1, Name: "basic", Description: "allowed", Platform: service.PlatformAnthropic,
		Status: service.StatusActive, RateMultiplier: 9, RPMLimit: 42,
		LongContextPricingEnabled: true,
		ModelPricing:              []service.ChannelModelPricing{{Models: []string{"claude"}}},
	}}
	r := newSimpleModeGroupRouter(svc)

	assertNoAdvancedFields := func(t *testing.T, item map[string]any) {
		t.Helper()
		for _, forbidden := range []string{
			"rate_multiplier", "rpm_limit", "long_context_pricing_enabled", "model_pricing",
			"allow_batch_image_generation", "video_price_720p", "web_search_price_per_call",
			"audio_realtime_price_per_min", "model_routing",
		} {
			_, exposed := item[forbidden]
			require.Falsef(t, exposed, "simple mode exposed %s", forbidden)
		}
	}

	for _, tt := range []struct {
		name   string
		method string
		path   string
		body   string
		list   bool
	}{
		{name: "get all", method: http.MethodGet, path: "/groups/all", list: true},
		{name: "get by id", method: http.MethodGet, path: "/groups/1"},
		{name: "create", method: http.MethodPost, path: "/groups", body: `{"name":"created","platform":"anthropic","model_pricing":[{"models":["claude"]}]}`},
		{name: "update", method: http.MethodPut, path: "/groups/1", body: `{"name":"updated","model_pricing":[{"models":["claude"]}]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(res, req)
			require.Equal(t, http.StatusOK, res.Code)

			var payload struct {
				Data json.RawMessage `json:"data"`
			}
			require.NoError(t, json.Unmarshal(res.Body.Bytes(), &payload))
			if tt.list {
				var items []map[string]any
				require.NoError(t, json.Unmarshal(payload.Data, &items))
				require.Len(t, items, 1)
				assertNoAdvancedFields(t, items[0])
				return
			}
			var item map[string]any
			require.NoError(t, json.Unmarshal(payload.Data, &item))
			assertNoAdvancedFields(t, item)
		})
	}
}

func TestGroupHandlerSimpleModeBlocksAdvancedOperations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/groups/1/duplicate", ""},
		{http.MethodGet, "/groups/1/models-list-candidates", ""},
		{http.MethodGet, "/groups/1/composite-routes", ""},
		{http.MethodPost, "/groups/1/composite-routes", `{"public_model":"x","target_platform":"openai"}`},
		{http.MethodPut, "/groups/1/rate-multipliers", `{"entries":[]}`},
		{http.MethodDelete, "/groups/1/rate-multipliers", ""},
		{http.MethodPut, "/groups/1/rpm-overrides", `{"entries":[]}`},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			svc := newStubAdminService()
			r := newSimpleModeGroupRouter(svc)
			res := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(res, req)
			require.Equal(t, http.StatusForbidden, res.Code)
			require.Zero(t, svc.advancedGroupOperationCalls)
		})
	}
}
