package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 分组改价（rate_multiplier / 各模态单价 / 利润控制阈值）与 search_provider_api_key
// 写入属资金与凭据敏感写操作：必须与路由级 step-up 使用同一套错误码语义。
type groupStepUpHarness struct {
	stepUpEnabled bool
	granted       bool
	adminAPIKey   bool
}

func setupGroupStepUpRouter(t *testing.T, h groupStepUpHarness) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	svc := newStubAdminService()
	actorRepo := &totpDemotionActorRepo{actor: &service.User{
		ID: 41, Role: service.RoleAdmin, Status: service.StatusActive, TotpEnabled: true,
	}}
	cache := &totpDemotionGrantCache{}
	if h.granted {
		cache.grantUserID = 41
		cache.grantSession = "admin-session"
	}
	totpSvc := service.NewTotpService(actorRepo, nil, cache, nil, nil, nil)
	userSvc := service.NewUserService(actorRepo, nil, nil, nil)
	enabled := "false"
	if h.stepUpEnabled {
		enabled = "true"
	}
	settings := service.NewSettingService(&settingHandlerRepoStub{values: map[string]string{
		service.SettingKeyStepUpEnabled: enabled,
	}}, nil)

	groupHandler := NewGroupHandler(svc, nil, nil)
	groupHandler.SetStepUpDependencies(totpSvc, userSvc, settings)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 41})
		c.Set(middleware.ContextKeySessionID, "admin-session")
		if h.adminAPIKey {
			c.Set("auth_method", service.AuditAuthMethodAdminAPIKey)
		} else {
			c.Set("auth_method", service.AuditAuthMethodJWT)
		}
		c.Next()
	})
	router.POST("/api/v1/admin/groups", groupHandler.Create)
	router.PUT("/api/v1/admin/groups/:id", groupHandler.Update)
	return router
}

func doGroupRequest(t *testing.T, router *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	return rec
}

// 改价必须要求 step-up；完成后放行。
func TestGroupPricingChangeRequiresStepUp(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			router := setupGroupStepUpRouter(t, groupStepUpHarness{stepUpEnabled: true})
			path := "/api/v1/admin/groups"
			if method == http.MethodPut {
				path += "/1"
			}
			rec := doGroupRequest(t, router, method, path, `{"name":"fixture","platform":"anthropic","rate_multiplier":1.5}`)
			require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), "STEP_UP_REQUIRED")
		})
	}

	// 完成 step-up 后放行。
	router := setupGroupStepUpRouter(t, groupStepUpHarness{stepUpEnabled: true, granted: true})
	rec := doGroupRequest(t, router, http.MethodPut, "/api/v1/admin/groups/1", `{"name":"fixture","platform":"anthropic","rate_multiplier":1.5}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// 只改名称/描述不触发门控：前端整表单回传默认定价不应打断日常运营。
func TestGroupNonPricingEditSkipsStepUp(t *testing.T) {
	router := setupGroupStepUpRouter(t, groupStepUpHarness{stepUpEnabled: true})
	rec := doGroupRequest(t, router, http.MethodPost, "/api/v1/admin/groups", `{"name":"fixture","platform":"anthropic","rate_multiplier":1}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = doGroupRequest(t, router, http.MethodPut, "/api/v1/admin/groups/1", `{"name":"renamed","description":"daily edit","rate_multiplier":1}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// 写入只写不读的 search_provider_api_key 等同写入上游凭据：要求 step-up。
func TestGroupSearchProviderCredentialRequiresStepUp(t *testing.T) {
	router := setupGroupStepUpRouter(t, groupStepUpHarness{stepUpEnabled: true})
	rec := doGroupRequest(t, router, http.MethodPut, "/api/v1/admin/groups/1", `{"name":"fixture","search_provider_api_key":"sk-search-secret"}`)
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "STEP_UP_REQUIRED")
}

// 机器凭据无法完成 TOTP：改价必须返回 STEP_UP_ADMIN_API_KEY_FORBIDDEN。
func TestGroupPricingChangeRejectsAdminAPIKey(t *testing.T) {
	router := setupGroupStepUpRouter(t, groupStepUpHarness{stepUpEnabled: true, granted: true, adminAPIKey: true})
	rec := doGroupRequest(t, router, http.MethodPut, "/api/v1/admin/groups/1", `{"name":"fixture","rate_multiplier":2}`)
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "STEP_UP_ADMIN_API_KEY_FORBIDDEN")
}

// step_up 开关关闭时保持门控引入前的行为（改价不再要求 2FA）。
func TestGroupPricingChangeStepUpDisabledKeepsLegacyBehavior(t *testing.T) {
	router := setupGroupStepUpRouter(t, groupStepUpHarness{stepUpEnabled: false})
	rec := doGroupRequest(t, router, http.MethodPut, "/api/v1/admin/groups/1", `{"name":"fixture","rate_multiplier":2}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// DeepSeek 工具定价字段（fetch_price_per_1k / tool_rate_independent / tool_rate_multiplier）改动须触发 step-up；
// 传默认值不误拦截。
func TestGroupToolPricingChangeRequiresStepUp(t *testing.T) {
	for _, body := range []string{
		`{"name":"ds","platform":"deepseek","rate_multiplier":1,"fetch_price_per_1k":3.5}`,
		`{"name":"ds","platform":"deepseek","rate_multiplier":1,"tool_rate_independent":true}`,
		`{"name":"ds","platform":"deepseek","rate_multiplier":1,"tool_rate_multiplier":0.5}`,
	} {
		t.Run(body, func(t *testing.T) {
			router := setupGroupStepUpRouter(t, groupStepUpHarness{stepUpEnabled: true})
			rec := doGroupRequest(t, router, http.MethodPost, "/api/v1/admin/groups", body)
			require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), "STEP_UP_REQUIRED")
		})
	}

	router := setupGroupStepUpRouter(t, groupStepUpHarness{stepUpEnabled: true})
	rec := doGroupRequest(t, router, http.MethodPost, "/api/v1/admin/groups", `{"name":"ds","platform":"deepseek","rate_multiplier":1,"tool_rate_independent":false,"tool_rate_multiplier":1}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// 非 DeepSeek 分组（如 Grok）PUT 传入 null 对共享价格字段保持“不修改”语义，不误触发 step-up；
// DeepSeek 分组传入 null 则清除价格，必须触发 step-up。
func TestUpdateGroupRequestTouchesPricing_PlatformNullSemantics(t *testing.T) {
	price := 5.0
	grok := &service.Group{
		ID:                           1,
		Platform:                     service.PlatformGrok,
		SearchPricePer1k:             &price,
		AudioTTSPricePerMillionChars: &price,
		AudioSTTPricePerHour:         &price,
	}
	reqNull := &UpdateGroupRequest{
		SearchPricePer1k:             optionalLimitField{set: true, value: nil},
		AudioTtsPricePerMillionChars: optionalLimitField{set: true, value: nil},
		AudioSttPricePerHour:         optionalLimitField{set: true, value: nil},
	}
	require.False(t, updateGroupRequestTouchesPricing(grok, reqNull), "Grok null shared price means unchanged")

	clear := -1.0
	reqClear := &UpdateGroupRequest{
		SearchPricePer1k: optionalLimitField{set: true, value: &clear},
	}
	require.True(t, updateGroupRequestTouchesPricing(grok, reqClear), "Grok -1 sentinel clears price and must trigger step-up")

	ds := &service.Group{
		ID:               2,
		Platform:         service.PlatformDeepseek,
		SearchPricePer1k: &price,
	}
	require.True(t, updateGroupRequestTouchesPricing(ds, reqNull), "DeepSeek null clears search_price_per_1k and must trigger step-up")
}
