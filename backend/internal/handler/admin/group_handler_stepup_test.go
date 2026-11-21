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
