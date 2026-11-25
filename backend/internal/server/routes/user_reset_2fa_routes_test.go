//go:build unit

package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminroutepolicy"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// 破玻璃重置 2FA 的 HTTP 层验收：无 step-up → 403；机器凭据 → 403；
// 无 reason → 400；用户不存在 → 404；用户没开 2FA → 400；成功 → 200 +
// TOTP 清空 + 审计 + 邮件。

const (
	reset2FAPath      = "/api/v1/admin/users/3249/reset-2fa"
	reset2FASessionID = "admin-session"
)

// reset2FAUserRepo 是只实现重置所需方法的用户仓储桩。
type reset2FAUserRepo struct {
	service.UserRepository
	user       *service.User
	getErr     error
	resetCalls int
}

func (r *reset2FAUserRepo) GetByID(context.Context, int64) (*service.User, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	copied := *r.user
	return &copied, nil
}

func (r *reset2FAUserRepo) ResetTotpForAdmin(context.Context, int64) error {
	r.resetCalls++
	r.user.TotpEnabled = false
	r.user.TotpSecretEncrypted = nil
	r.user.TotpEnabledAt = nil
	return nil
}

type reset2FAAuditCapture struct {
	service.AuditLogRepository
	entries []*service.AuditLog
}

type reset2FARouteFixture struct {
	router    *gin.Engine
	repo      *reset2FAUserRepo
	audit     *reset2FAAuditCapture
	totpCache service.TotpCache
}

func (c *reset2FAAuditCapture) Insert(_ context.Context, entry *service.AuditLog) error {
	copied := *entry
	c.entries = append(c.entries, &copied)
	return nil
}

// reset2FAActorRepo 给 step-up 门控提供「当前登录管理员」：门控要读操作者自己
// 绑定了哪些因子（管理员自己也得有有效 step-up 授权）。
//
// 生产装配里 TotpService.userRepo 是同一个 UserRepository：RequiredFactors
// 用操作者 ID 查同一张表，reset 服务用目标 ID 查同一张表。测试里必须保持
// 这个形状，否则会出现「门控读到的因子来自目标用户」的假绿。
type reset2FAActorRepo struct {
	service.UserRepository
	target *reset2FAUserRepo
	admin  service.User
}

func (r *reset2FAActorRepo) GetByID(ctx context.Context, id int64) (*service.User, error) {
	if id == r.admin.ID {
		copied := r.admin
		return &copied, nil
	}
	return r.target.GetByID(ctx, id)
}

// ResetTotpForAdmin 透传给目标仓储：同一个 UserRepository 同时承担
// 「读操作者因子」与「重置目标 TOTP」两种调用。
func (r *reset2FAActorRepo) ResetTotpForAdmin(ctx context.Context, userID int64) error {
	return r.target.ResetTotpForAdmin(ctx, userID)
}

// GetUserAvatar 是 UserService.GetByID 的附属读取：没有头像就不补水印。
func (r *reset2FAActorRepo) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

// buildReset2FARouter 装配真实的路由 + step-up 门控 + 审计中间件。
//
// authMode 决定认证中间件写入的 auth_method：jwt 模拟真人会话，
// admin_api_key 模拟机器凭据（step-up 门控必须拒绝）。
func buildReset2FARouter(t *testing.T, authMode string, user *service.User) *reset2FARouteFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)

	miniRedis := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: miniRedis.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	totpCacheStore := repository.NewTotpCacheStore(rdb)

	repo := &reset2FAUserRepo{user: user}
	// 操作者（管理员 1）自己也开了 TOTP：门控要求他有有效的 step-up 授权。
	// 门控与业务共用同一个仓储（与生产一致），按 ID 区分操作者与目标用户。
	actorRepo := &reset2FAActorRepo{
		target: repo,
		admin: service.User{
			ID: 1, Email: "admin@example.test", Role: service.RoleAdmin,
			Status: service.StatusActive, TotpEnabled: true,
		},
	}
	totpService := service.NewTotpService(actorRepo, nil, totpCacheStore, nil, nil, nil)
	refreshCache := repository.NewRefreshTokenCache(rdb)

	userService := service.NewUserService(actorRepo, nil, nil, nil)

	auditRepo := &reset2FAAuditCapture{}
	auditService := service.NewAuditLogService(auditRepo, nil)
	resetService := service.NewAdmin2FAResetService(service.Admin2FAResetDeps{
		UserRepo:     repo,
		Totp:         totpService,
		RefreshCache: refreshCache,
		StepUp:       totpCacheStore,
		Audit:        auditService,
	})

	userHandler := adminhandler.NewUserHandler(nil, nil, nil, nil, totpService, userService, nil)
	userHandler.SetAdmin2FAResetService(resetService)

	settings := stepUpRouteSettings(map[string]string{service.SettingKeyStepUpEnabled: "true"})
	realStepUp := middleware.NewStepUpAuthMiddleware(totpService, userService, settings)

	r := gin.New()
	r.Use(gin.Recovery())
	admin := r.Group("/api/v1/admin")
	admin.Use(func(c *gin.Context) {
		c.Set("auth_method", authMode)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		c.Set(string(middleware.ContextKeyUserRole), service.RoleAdmin)
		c.Set(middleware.ContextKeyAuthEmail, "admin@example.test")
		// 会话 ID 决定 step-up 授权键：没有它门控会退化成用户级键 u<uid>。
		c.Set(middleware.ContextKeySessionID, reset2FASessionID)
		c.Next()
	})
	admin.Use(gin.HandlerFunc(middleware.NewAuditLogMiddleware(auditService)))
	admin.POST("/users/:id/reset-2fa", gin.HandlerFunc(realStepUp), userHandler.ResetUser2FA)

	return &reset2FARouteFixture{router: r, repo: repo, audit: auditRepo, totpCache: totpCacheStore}
}

func reset2FARequest(t *testing.T, router *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(http.MethodPost, reset2FAPath, reader)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	return rec
}

func reset2FATargetUser() *service.User {
	enabledAt := time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)
	secret := "v1:top-secret"
	return &service.User{
		ID:                  3249,
		Email:               "1921089334@qq.com",
		Role:                service.RoleUser,
		Status:              service.StatusActive,
		TotpEnabled:         true,
		TotpSecretEncrypted: &secret,
		TotpEnabledAt:       &enabledAt,
	}
}

// 路由注册表必须把新路由归为 step_up_required（且不是机器凭据可调的白名单），
// 这是「管理员自己必须先过 2FA」的单一真相源。
func TestReset2FARouteIsStepUpRequiredInRegistry(t *testing.T) {
	resolved, ok := adminroutepolicy.Resolve(http.MethodPost, "/api/v1/admin/users/:id/reset-2fa")
	require.True(t, ok, "reset-2fa must be classified in adminroutepolicy")
	require.Equal(t, adminroutepolicy.ClassStepUpRequired, resolved.Class)
	require.Equal(t, adminroutepolicy.ReasonAccountSecurityReset, resolved.Reason)
	require.Equal(t, adminroutepolicy.ResourceAccountSecurity, resolved.Resource)
}

// 没有 step-up 授权 → 403 STEP_UP_REQUIRED，且不能碰数据库。
func TestReset2FAWithoutStepUpIsForbidden(t *testing.T) {
	fixture := buildReset2FARouter(t, service.AuditAuthMethodJWT, reset2FATargetUser())

	rec := reset2FARequest(t, fixture.router, `{"reason":"用户忘记动态口令"}`)
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "STEP_UP_REQUIRED")
	require.Zero(t, fixture.repo.resetCalls)
	require.Empty(t, fixture.audit.entries)
}

// 机器凭据（x-api-key）即使带着 step-up 头也必须 403。
func TestReset2FAMachineCredentialIsForbidden(t *testing.T) {
	fixture := buildReset2FARouter(t, service.AuditAuthMethodAdminAPIKey, reset2FATargetUser())

	rec := reset2FARequest(t, fixture.router, `{"reason":"用户忘记动态口令"}`)
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	// step-up 门控对机器凭据统一以这个机器码拒绝（与其它高风险管理路由一致）。
	require.Contains(t, rec.Body.String(), "STEP_UP_ADMIN_API_KEY_FORBIDDEN")
	require.Zero(t, fixture.repo.resetCalls)
	require.Empty(t, fixture.audit.entries)
}

// 拿到 step-up 授权后：无 reason → 400（且不重置）。
func TestReset2FAWithStepUpButNoReasonIsBadRequest(t *testing.T) {
	user := reset2FATargetUser()
	fixture := buildReset2FARouter(t, service.AuditAuthMethodJWT, user)
	require.NoError(t, fixture.totpCache.SetStepUpGrant(context.Background(), 1, reset2FASessionID, service.FactorTOTP, service.StepUpGrantTTL))

	rec := reset2FARequest(t, fixture.router, `{"reason":"   "}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "RESET_2FA_REASON_REQUIRED")
	require.Zero(t, fixture.repo.resetCalls)
}

// 请求体缺 reason 字段同样 400。
func TestReset2FAMissingReasonFieldIsBadRequest(t *testing.T) {
	user := reset2FATargetUser()
	fixture := buildReset2FARouter(t, service.AuditAuthMethodJWT, user)
	require.NoError(t, fixture.totpCache.SetStepUpGrant(context.Background(), 1, reset2FASessionID, service.FactorTOTP, service.StepUpGrantTTL))

	rec := reset2FARequest(t, fixture.router, `{}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Zero(t, fixture.repo.resetCalls)
}

// 目标用户不存在 → 404。
func TestReset2FAUnknownUserIsNotFound(t *testing.T) {
	user := reset2FATargetUser()
	fixture := buildReset2FARouter(t, service.AuditAuthMethodJWT, user)
	fixture.repo.getErr = service.ErrUserNotFound
	require.NoError(t, fixture.totpCache.SetStepUpGrant(context.Background(), 1, reset2FASessionID, service.FactorTOTP, service.StepUpGrantTTL))

	rec := reset2FARequest(t, fixture.router, `{"reason":"用户忘记动态口令"}`)
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	require.Zero(t, fixture.repo.resetCalls)
}

// 目标用户没开 2FA → 400「该用户未启用 2FA」。
func TestReset2FATargetWithoutTwoFactorIsBadRequest(t *testing.T) {
	user := reset2FATargetUser()
	user.TotpEnabled = false
	user.TotpSecretEncrypted = nil
	fixture := buildReset2FARouter(t, service.AuditAuthMethodJWT, user)
	require.NoError(t, fixture.totpCache.SetStepUpGrant(context.Background(), 1, reset2FASessionID, service.FactorTOTP, service.StepUpGrantTTL))

	rec := reset2FARequest(t, fixture.router, `{"reason":"用户忘记动态口令"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "RESET_2FA_TARGET_NOT_ENABLED")
	require.Zero(t, fixture.repo.resetCalls)
}

// 成功路径：200 + TOTP 清空 + 审计落库（原因/操作者/前后状态，无 secret）。
func TestReset2FASuccessReturnsResetResultAndAudits(t *testing.T) {
	user := reset2FATargetUser()
	fixture := buildReset2FARouter(t, service.AuditAuthMethodJWT, user)
	require.NoError(t, fixture.totpCache.SetStepUpGrant(context.Background(), 1, reset2FASessionID, service.FactorTOTP, service.StepUpGrantTTL))

	rec := reset2FARequest(t, fixture.router, `{"reason":"用户忘记动态口令，经工单核实"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var envelope struct {
		Code int                             `json:"code"`
		Data service.Admin2FAResetResult     `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), rec.Body.String())
	require.Equal(t, 0, envelope.Code)
	require.Equal(t, int64(3249), envelope.Data.UserID)
	require.False(t, envelope.Data.TotpEnabledAfter)
	require.True(t, envelope.Data.TotpPreviouslyEnabled)
	require.Equal(t, "用户忘记动态口令，经工单核实", envelope.Data.Reason)
	require.Equal(t, 1, fixture.repo.resetCalls)
	require.False(t, fixture.repo.user.TotpEnabled)
	require.Nil(t, fixture.repo.user.TotpSecretEncrypted)

	require.Len(t, fixture.audit.entries, 1)
	entry := fixture.audit.entries[0]
	require.Equal(t, service.AuditActionAdminUserReset2FA, entry.Action)
	require.Equal(t, "用户忘记动态口令，经工单核实", entry.Extra["reason"])
	require.Equal(t, false, entry.Extra["totp_enabled_after"])
	require.Equal(t, true, entry.Extra["totp_enabled_before"])
	require.NotContains(t, entry.RequestBody, "top-secret")
}
