//go:build unit

package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

// 端到端：真实 TotpCache（miniredis 里的真实键布局）+ 真实 TotpService + 真实 TOTP step-up 处理器 +
// 真实 step-up 中间件。同时绑定 TOTP 与 Passkey 的账号只验证其中一个因子就能过门控（老板 2026-11-26）。
// Passkey 的 WebAuthn 仪式无法在单测里跑，用 GrantStepUpForFactor 模拟 StepUpFinish 校验通过后写授权的那一步。

type anyFactorE2EUserRepo struct{ *cryptoRouteUserRepo }

func (anyFactorE2EUserRepo) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

type anyFactorE2EPasskeyRepo struct {
	service.PasskeyRepository
	bound bool
}

func (r anyFactorE2EPasskeyRepo) ListByUserID(context.Context, int64) ([]service.PasskeyCredentialRecord, error) {
	if !r.bound {
		return nil, nil
	}
	return []service.PasskeyCredentialRecord{{ID: 9, UserID: 41}}, nil
}

func TestStepUpAnyFactorEndToEndWithTheRealGrantCache(t *testing.T) {
	user := activeCryptoAdmin() // ID 41，已启用 TOTP
	harness := newCryptoRouteHarness(t, user)
	settings := service.NewSettingService(&cryptoRouteSettingsRepo{values: map[string]string{
		service.SettingKeyStepUpEnabled: "true",
	}}, nil)
	userRepo := anyFactorE2EUserRepo{&cryptoRouteUserRepo{user: user}}
	totpSvc := service.NewTotpService(userRepo, cryptoRouteEncryptor{}, repository.NewTotpCache(harness.redis), settings, nil, nil)
	totpSvc.SetPasskeyRepository(anyFactorE2EPasskeyRepo{bound: true})
	userSvc := service.NewUserService(userRepo, nil, nil, nil)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	asUser := func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: user.ID})
		c.Set(middleware.ContextKeySessionID, c.GetHeader("X-Test-Session"))
		c.Next()
	}
	router.POST("/api/v1/user/totp/step-up", asUser, handler.NewTotpHandler(totpSvc).StepUp)
	router.PUT("/api/v1/admin/settings", asUser,
		gin.HandlerFunc(middleware.NewStepUpAuthMiddleware(totpSvc, userSvc, settings)),
		func(c *gin.Context) { c.Status(http.StatusNoContent) })

	sensitive := func(session string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", nil)
		req.Header.Set("X-Test-Session", session)
		router.ServeHTTP(rec, req)
		return rec
	}
	grantKeys := func(session string) (totpKey, passkeyKey string) {
		keys := repository.TotpStepUpGrantKeys(user.ID, session) // [totp, passkey, 旧布局]
		return keys[0], keys[1]
	}

	// 1. 没有任何授权：403，挑战体明说「已绑定因子任选其一」。
	rec := sensitive("session-a")
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"code":"STEP_UP_REQUIRED"`)
	require.Contains(t, rec.Body.String(), `"step_up_mode":"any"`)
	require.Contains(t, rec.Body.String(), `"factors":"totp,passkey"`)

	// 2. 只用真实的 TOTP step-up 接口验证一个因子（会话 session-a）。
	code, err := totp.GenerateCode(cryptoRouteTotpSecret, time.Now())
	require.NoError(t, err)
	stepUpReq := httptest.NewRequest(http.MethodPost, "/api/v1/user/totp/step-up", strings.NewReader(`{"code":"`+code+`"}`))
	stepUpReq.Header.Set("Content-Type", "application/json")
	stepUpReq.Header.Set("X-Test-Session", "session-a")
	stepUpRec := httptest.NewRecorder()
	router.ServeHTTP(stepUpRec, stepUpReq)
	require.Equal(t, http.StatusOK, stepUpRec.Code, stepUpRec.Body.String())

	// Redis 里只有 TOTP 因子的授权键，没有 Passkey 的。
	totpKey, passkeyKey := grantKeys("session-a")
	require.True(t, harness.mini.Exists(totpKey))
	require.False(t, harness.mini.Exists(passkeyKey))

	// 3. 只有 TOTP 授权也放行，响应头回传是哪个因子满足的。
	rec = sensitive("session-a")
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	require.Equal(t, "1", rec.Header().Get(middleware.StepUpSkippedHeader))
	require.Equal(t, "totp", rec.Header().Get(middleware.StepUpFactorsHeader))

	// 4. 授权绑定会话：另一个会话照旧 403。
	require.Equal(t, http.StatusForbidden, sensitive("session-b").Code)

	// 5. 另一个会话里只通过 Passkey（模拟 StepUpFinish 断言通过后写授权）：同样放行，且没有 TOTP 授权。
	_, err = totpSvc.GrantStepUpForFactor(context.Background(), user.ID, "session-b", service.FactorPasskey)
	require.NoError(t, err)
	totpKeyB, passkeyKeyB := grantKeys("session-b")
	require.False(t, harness.mini.Exists(totpKeyB))
	require.True(t, harness.mini.Exists(passkeyKeyB))
	rec = sensitive("session-b")
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	require.Equal(t, "passkey", rec.Header().Get(middleware.StepUpFactorsHeader))

	// 6. 5 分钟窗口照旧：过期后两个会话都回到 403。
	harness.mini.FastForward(service.StepUpGrantTTL + time.Second)
	require.Equal(t, http.StatusForbidden, sensitive("session-a").Code)
	require.Equal(t, http.StatusForbidden, sensitive("session-b").Code)

	// 7. 解绑 Passkey 之后，残留的 Passkey 授权（5 分钟内）不算数：只剩 TOTP 一个因子，Passkey 授权不能顶替。
	_, err = totpSvc.GrantStepUpForFactor(context.Background(), user.ID, "session-c", service.FactorPasskey)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, sensitive("session-c").Code, "Passkey 仍绑定时它的授权有效")
	totpSvc.SetPasskeyRepository(anyFactorE2EPasskeyRepo{bound: false})
	rec = sensitive("session-c")
	require.Equal(t, http.StatusForbidden, rec.Code, "已解绑因子的残留授权不得放行")
	require.Contains(t, rec.Body.String(), `"factors":"totp"`)
}
