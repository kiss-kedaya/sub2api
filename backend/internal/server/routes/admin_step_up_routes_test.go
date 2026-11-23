//go:build unit

package routes

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const stepUpProbeHeader = "X-Test-Step-Up-Grant"

// stepUpRouteSettingsRepo 是只读的设置仓储桩，满足 step-up / 合规守卫的最小读取。
type stepUpRouteSettingsRepo struct {
	values map[string]string
}

func (r *stepUpRouteSettingsRepo) Get(_ context.Context, key string) (*service.Setting, error) {
	value, ok := r.values[key]
	if !ok {
		return nil, service.ErrSettingNotFound
	}
	return &service.Setting{Key: key, Value: value}, nil
}

func (r *stepUpRouteSettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", service.ErrSettingNotFound
}

func (r *stepUpRouteSettingsRepo) Set(context.Context, string, string) error { return nil }
func (r *stepUpRouteSettingsRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}
func (r *stepUpRouteSettingsRepo) SetMultiple(context.Context, map[string]string) error { return nil }
func (r *stepUpRouteSettingsRepo) GetAll(context.Context) (map[string]string, error) {
	return r.values, nil
}
func (r *stepUpRouteSettingsRepo) Delete(context.Context, string) error { return nil }

func stepUpRouteSettings(values map[string]string) *service.SettingService {
	return service.NewSettingService(&stepUpRouteSettingsRepo{values: values}, nil)
}

// stepUpProbe 作为 stepUpAuth 的替身：带 grant 头时 204 并中止（证明中间件已执行），
// 否则 403 STEP_UP_REQUIRED（证明请求在进入业务处理器前被拦住）。
func stepUpProbe() middleware.StepUpAuthMiddleware {
	return middleware.StepUpAuthMiddleware(func(c *gin.Context) {
		if c.GetHeader(stepUpProbeHeader) == "1" {
			c.Status(http.StatusNoContent)
			c.Abort()
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"reason": "STEP_UP_REQUIRED"})
	})
}

func adminRoutesTestAuth(authMode string) middleware.AdminAuthMiddleware {
	return middleware.AdminAuthMiddleware(func(c *gin.Context) {
		c.Set("auth_method", authMode)
		// 观察员身份（非 1 号管理员）跳过行政合规确认，测试只关注 step-up 装配。
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
		c.Next()
	})
}

func buildAdminRouteProbeRouter(t *testing.T, stepUp middleware.StepUpAuthMiddleware, authMode string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	RegisterAdminRoutes(
		r.Group("/api/v1"),
		&handler.Handlers{
			Crypto: &handler.CryptoHandler{},
			Admin: &handler.AdminHandlers{
				// 上游渠道/请求抓取接口按 handler 是否装配决定是否注册，探针需要非 nil 占位。
				UpstreamChannel: &adminhandler.UpstreamChannelHandler{},
				RequestCapture:  &adminhandler.RequestCaptureHandler{},
			},
		},
		adminRoutesTestAuth(authMode),
		middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }),
		stepUp,
		stepUpRouteSettings(map[string]string{}),
		nil,
	)
	return r
}

func buildPaymentProbeRouter(t *testing.T, stepUp middleware.StepUpAuthMiddleware, authMode string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	RegisterPaymentRoutes(
		r.Group("/api/v1"),
		&handler.PaymentHandler{},
		&handler.PaymentWebhookHandler{},
		&adminhandler.PaymentHandler{},
		middleware.JWTAuthMiddleware(func(c *gin.Context) { c.Next() }),
		adminRoutesTestAuth(authMode),
		middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }),
		stepUp,
		nil,
		stepUpRouteSettings(map[string]string{}),
		nil,
	)
	return r
}

type stepUpRouteCase struct {
	method string
	path   string
	router string
}

func sensitiveStepUpRoutes() []stepUpRouteCase {
	return []stepUpRouteCase{
		// 系统设置（含支付/邮件/注册等敏感配置）
		{http.MethodPut, "/api/v1/admin/settings", "admin"},
		// 删号、建号、改余额
		{http.MethodDelete, "/api/v1/admin/users/1", "admin"},
		{http.MethodPost, "/api/v1/admin/users", "admin"},
		{http.MethodPost, "/api/v1/admin/users/1/balance", "admin"},
		// 给账号绑定第三方登录身份（会新增一条登录路径）
		{http.MethodPost, "/api/v1/admin/users/1/auth-identities", "admin"},
		// 查看用户/分组的 API Key 列表（key 前缀、掩码与绑定关系）
		{http.MethodGet, "/api/v1/admin/users/1/api-keys", "admin"},
		{http.MethodGet, "/api/v1/admin/groups/1/api-keys", "admin"},
		// 改 Key 分组会改它的可用分组与路由授权：要求 step-up，机器凭据一律 403。
		{http.MethodPut, "/api/v1/admin/api-keys/1", "admin"},
		// 管理面出站探测入口：S3/图床"测试连接"、风控"测试 Key"会以服务端身份
		// 向管理员填写的任意地址发请求（SSRF 探测原语），与改配置同级。
		{http.MethodPost, "/api/v1/admin/backups/s3-config/test", "admin"},
		{http.MethodPost, "/api/v1/admin/backups/image-storage/test", "admin"},
		{http.MethodPost, "/api/v1/admin/risk-control/api-keys/test", "admin"},
		// 上游失败 dump 的正文含客户端请求体与上游响应体
		{http.MethodGet, "/api/v1/admin/debug-dumps/1", "admin"},
		// 卡密（兑换码）：生成/固定码直发/改值/作废/删除都会产出或改变可兑价值
		{http.MethodPost, "/api/v1/admin/redeem-codes/generate", "admin"},
		{http.MethodPost, "/api/v1/admin/redeem-codes/create-and-redeem", "admin"},
		{http.MethodDelete, "/api/v1/admin/redeem-codes/1", "admin"},
		{http.MethodPost, "/api/v1/admin/redeem-codes/batch-delete", "admin"},
		{http.MethodPost, "/api/v1/admin/redeem-codes/batch-update", "admin"},
		{http.MethodPost, "/api/v1/admin/redeem-codes/1/expire", "admin"},
		// 批量导出一次带走最多 1 万条业务凭据元数据（CSV 已是掩码，但仍属导出类）
		{http.MethodGet, "/api/v1/admin/redeem-codes/export", "admin"},
		// 优惠码携带赠送余额
		{http.MethodPost, "/api/v1/admin/promo-codes", "admin"},
		{http.MethodPut, "/api/v1/admin/promo-codes/1", "admin"},
		{http.MethodDelete, "/api/v1/admin/promo-codes/1", "admin"},
		// 上游渠道凭据（渠道配置与上游子密钥）
		{http.MethodPost, "/api/v1/admin/upstream-monitor/channels", "admin"},
		{http.MethodPut, "/api/v1/admin/upstream-monitor/channels/1", "admin"},
		{http.MethodDelete, "/api/v1/admin/upstream-monitor/channels/1", "admin"},
		{http.MethodPost, "/api/v1/admin/upstream-monitor/channels/1/api-keys", "admin"},
		{http.MethodPut, "/api/v1/admin/upstream-monitor/channels/1/api-keys/1", "admin"},
		{http.MethodDelete, "/api/v1/admin/upstream-monitor/channels/1/api-keys/1", "admin"},
		// 上游账户充值/兑换/订阅采购消耗或新增真实额度
		{http.MethodPost, "/api/v1/admin/upstream-monitor/channels/1/recharge", "admin"},
		{http.MethodPost, "/api/v1/admin/upstream-monitor/channels/1/redeem", "admin"},
		{http.MethodPost, "/api/v1/admin/upstream-monitor/channels/1/subscriptions", "admin"},
		// 订阅权益的分配/延期/重置额度/撤销/恢复等同资金操作
		{http.MethodPost, "/api/v1/admin/subscriptions/assign", "admin"},
		{http.MethodPost, "/api/v1/admin/subscriptions/bulk-assign", "admin"},
		{http.MethodPost, "/api/v1/admin/subscriptions/1/extend", "admin"},
		{http.MethodPost, "/api/v1/admin/subscriptions/1/reset-quota", "admin"},
		{http.MethodPost, "/api/v1/admin/subscriptions/1/revoke", "admin"},
		{http.MethodPost, "/api/v1/admin/subscriptions/1/restore", "admin"},
		{http.MethodDelete, "/api/v1/admin/subscriptions/1", "admin"},
		// 分组专属倍率 = 用户实付价格：批量改价 / 清空属资金操作
		{http.MethodPut, "/api/v1/admin/groups/1/rate-multipliers", "admin"},
		{http.MethodDelete, "/api/v1/admin/groups/1/rate-multipliers", "admin"},
		// 批量权限/配额变更与批量停用：一次影响成批用户
		{http.MethodPost, "/api/v1/admin/users/batch-concurrency", "admin"},
		{http.MethodPost, "/api/v1/admin/users/batch-limits", "admin"},
		{http.MethodPost, "/api/v1/admin/users/batch-disable", "admin"},
		{http.MethodPost, "/api/v1/admin/users/1/replace-group", "admin"},
		{http.MethodPut, "/api/v1/admin/users/1/platform-quotas", "admin"},
		{http.MethodPost, "/api/v1/admin/users/1/platform-quotas/reset", "admin"},
		// 部署 / 回滚 / 重启属主机生命周期动作
		{http.MethodPost, "/api/v1/admin/system/update", "admin"},
		{http.MethodPost, "/api/v1/admin/system/rollback", "admin"},
		{http.MethodPost, "/api/v1/admin/system/restart", "admin"},
		// 支付渠道配置
		{http.MethodPut, "/api/v1/admin/payment/config", "payment"},
		{http.MethodPost, "/api/v1/admin/payment/providers", "payment"},
		{http.MethodPut, "/api/v1/admin/payment/providers/1", "payment"},
		{http.MethodDelete, "/api/v1/admin/payment/providers/1", "payment"},
		// 退款与退款确认
		{http.MethodPost, "/api/v1/admin/payment/orders/1/refund", "payment"},
		{http.MethodPost, "/api/v1/admin/payment/orders/1/refund/query", "payment"},
		// 敏感资源的只读接口同样是敏感读：卡密（读模型已掩码，见下方只读列表）、
		// 代理明文导出、上游渠道/账号（上游凭证）、系统设置（渠道密钥）等。
		// 「只读所以不用 2FA」的口径已废弃，这些路由仍要求 step-up（机器凭据一律 403）。
		// 注意：代理列表/详情/统计的密码已在 #497 改掩码，属于下方的只读豁免列表。
		{http.MethodGet, "/api/v1/admin/proxies/data", "admin"},
		{http.MethodPost, "/api/v1/admin/proxies", "admin"},
		{http.MethodPut, "/api/v1/admin/proxies/1", "admin"},
		{http.MethodDelete, "/api/v1/admin/proxies/1", "admin"},
		// 渠道读模型已掩码（DTO 不带 password/token，API Key 列表清空 Key），
		// 只有写操作与 reveal POST 要求 step-up；读接口见下方只读列表。
		{http.MethodPost, "/api/v1/admin/upstream-monitor/channels/1/api-keys/1/reveal", "admin"},
		{http.MethodGet, "/api/v1/admin/accounts/1/credential", "admin"},
		{http.MethodGet, "/api/v1/admin/backups/s3-config", "admin"},
		{http.MethodGet, "/api/v1/admin/backups/1/download-url", "admin"},
		{http.MethodGet, "/api/v1/admin/debug-dumps/1", "admin"},
	}
}

// readOnlyStepUpRoutes 是明确无敏感数据的只读查询：挂 step-up 会误伤日常运营，
// 必须保证它们不经过门控。这里只允许 secret_free 家族；任何返回卡密/凭证/
// 密码/密钥的只读接口都必须放进上方的 sensitive 列表。
func readOnlyStepUpRoutes() []stepUpRouteCase {
	return []stepUpRouteCase{
		{http.MethodGet, "/api/v1/admin/dashboard/stats", "admin"},
		{http.MethodGet, "/api/v1/admin/users", "admin"},
		{http.MethodGet, "/api/v1/admin/groups", "admin"},
		{http.MethodGet, "/api/v1/admin/usage", "admin"},
		{http.MethodGet, "/api/v1/admin/ops/dashboard/overview", "admin"},
		{http.MethodGet, "/api/v1/admin/announcements", "admin"},
		{http.MethodGet, "/api/v1/admin/subscriptions", "admin"},
		{http.MethodGet, "/api/v1/admin/subscriptions/1", "admin"},
		{http.MethodGet, "/api/v1/admin/subscriptions/1/progress", "admin"},
		// 分组倍率列表只是读取当前配置（不是改价），不得被门控误伤。
		{http.MethodGet, "/api/v1/admin/groups/1/rate-multipliers", "admin"},
		{http.MethodGet, "/api/v1/admin/tickets", "admin"},
		{http.MethodGet, "/api/v1/admin/channel-monitors", "admin"},
		{http.MethodGet, "/api/v1/admin/audit-logs", "admin"},
		// 资源列表已明确脱敏（不返回秘密），不要求 step-up；写操作仍要求。
		{http.MethodGet, "/api/v1/admin/accounts", "admin"},
		{http.MethodGet, "/api/v1/admin/accounts/1", "admin"},
		// 页面加载自动调用的批量只读聚合（POST 形式）：绝不能弹 TOTP。
		{http.MethodPost, "/api/v1/admin/accounts/today-stats/batch", "admin"},
		{http.MethodPost, "/api/v1/admin/accounts/usage/batch", "admin"},
		// 账号列表滚动时自动拉取的自定义用量（只回用量数字，无上游凭证）。
		{http.MethodPost, "/api/v1/admin/accounts/1/custom-usage-query", "admin"},
		{http.MethodPost, "/api/v1/admin/accounts/custom-usage-batch", "admin"},
		// 上游渠道列表/详情/密钥列表/余额：读模型已掩码，页面加载不应弹码。
		{http.MethodGet, "/api/v1/admin/upstream-monitor/channels", "admin"},
		{http.MethodGet, "/api/v1/admin/upstream-monitor/channels/1", "admin"},
		{http.MethodGet, "/api/v1/admin/upstream-monitor/channels/1/api-keys", "admin"},
		{http.MethodGet, "/api/v1/admin/upstream-monitor/channels/1/balance", "admin"},
		// 代理列表/详情/统计/关联账号：密码已掩码，页面下拉与列表加载不应弹码。
		{http.MethodGet, "/api/v1/admin/proxies", "admin"},
		{http.MethodGet, "/api/v1/admin/proxies/all", "admin"},
		{http.MethodGet, "/api/v1/admin/proxies/1", "admin"},
		{http.MethodGet, "/api/v1/admin/proxies/1/stats", "admin"},
		{http.MethodGet, "/api/v1/admin/proxies/1/accounts", "admin"},
		{http.MethodGet, "/api/v1/admin/settings", "admin"},
		{http.MethodGet, "/api/v1/admin/crypto/config", "admin"},
		{http.MethodGet, "/api/v1/admin/payment/orders", "payment"},
		{http.MethodGet, "/api/v1/admin/plugins", "admin"},
		{http.MethodGet, "/api/v1/admin/request-captures", "admin"},
		{http.MethodGet, "/api/v1/admin/debug-dumps", "admin"},
		// 卡密读模型已硬化为掩码：列表/详情/统计只读且不再要求 TOTP，
		// 但机器凭据仍被 middleware.ForbidMachineCredentials 拒绝。
		{http.MethodGet, "/api/v1/admin/redeem-codes", "admin"},
		{http.MethodGet, "/api/v1/admin/redeem-codes/1", "admin"},
		{http.MethodGet, "/api/v1/admin/redeem-codes/stats", "admin"},
	}
}

// 每个敏感路由都必须挂上 step-up：无授权被拦，有授权才进入业务层。
func TestSensitiveAdminRoutesAreStepUpGated(t *testing.T) {
	adminRouter := buildAdminRouteProbeRouter(t, stepUpProbe(), service.AuditAuthMethodJWT)
	paymentRouter := buildPaymentProbeRouter(t, stepUpProbe(), service.AuditAuthMethodJWT)
	for _, tc := range sensitiveStepUpRoutes() {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			router := adminRouter
			if tc.router == "payment" {
				router = paymentRouter
			}
			// 未完成 step-up：403。
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), "STEP_UP_REQUIRED")

			// 完成 step-up：放行（probe 返回 204，证明中间件确实执行过）。
			w = httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set(stepUpProbeHeader, "1")
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
		})
	}
}

// stepUpRecordingProbe 记录被挂上 step-up 的请求路径，供只读接口回归用。
func stepUpRecordingProbe(recorded *[]string) middleware.StepUpAuthMiddleware {
	return middleware.StepUpAuthMiddleware(func(c *gin.Context) {
		*recorded = append(*recorded, c.Request.URL.Path)
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"reason": "STEP_UP_REQUIRED"})
	})
}

// 只读列表/查询不得被 step-up 误伤：门控中间件不能被触发。
// 业务 handler 用空桩（未挂门控时会在处理阶段 panic），这里用静默 recovery 吸收，
// 断言点只看门控是否执行过，不依赖业务 handler 的返回。
func TestReadOnlyAdminRoutesAreNotStepUpGated(t *testing.T) {
	var gated []string
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, _ any) {
		c.AbortWithStatus(http.StatusInternalServerError)
	}))
	RegisterAdminRoutes(
		r.Group("/api/v1"),
		&handler.Handlers{Admin: &handler.AdminHandlers{
			UpstreamChannel: &adminhandler.UpstreamChannelHandler{},
		}},
		adminRoutesTestAuth(service.AuditAuthMethodJWT),
		middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }),
		stepUpRecordingProbe(&gated),
		stepUpRouteSettings(map[string]string{}),
		nil,
	)
	for _, tc := range readOnlyStepUpRoutes() {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			gated = nil
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			require.NotContains(t, gated, tc.path, "read-only route must not run the step-up gate")
			require.NotContains(t, w.Body.String(), "STEP_UP_REQUIRED")
		})
	}
}

// 机器凭据（管理员 API Key）与其它 step-up 路由语义一致：即使有 grant 也 403。
func TestGatedAdminRoutesRejectAdminAPIKeyMachineCredential(t *testing.T) {
	settings := stepUpRouteSettings(map[string]string{service.SettingKeyStepUpEnabled: "true"})
	realStepUp := middleware.NewStepUpAuthMiddleware(nil, nil, settings)
	adminRouter := buildAdminRouteProbeRouter(t, realStepUp, service.AuditAuthMethodAdminAPIKey)
	paymentRouter := buildPaymentProbeRouter(t, realStepUp, service.AuditAuthMethodAdminAPIKey)
	for _, tc := range sensitiveStepUpRoutes() {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			router := adminRouter
			if tc.router == "payment" {
				router = paymentRouter
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), "STEP_UP_ADMIN_API_KEY_FORBIDDEN")
		})
	}
}

// step_up 开关关闭时保持门控引入前的既有行为（不误伤现有部署）。
func TestSensitiveAdminRoutesStepUpDisabledKeepsLegacyBehavior(t *testing.T) {
	settings := stepUpRouteSettings(map[string]string{service.SettingKeyStepUpEnabled: "false"})
	realStepUp := middleware.NewStepUpAuthMiddleware(nil, nil, settings)
	// 用真实 step-up + 一个 204 的业务桩，验证关闭时 API Key 也能通过。
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("auth_method", service.AuditAuthMethodAdminAPIKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
		c.Next()
	})
	r.PUT("/api/v1/admin/settings", gin.HandlerFunc(realStepUp), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", nil))
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
}

// readOnlyMachineCredentialForbiddenRoutes 是「只读、但机器凭据不许成批读走」的
// 管理面接口（#489 审计 B-2：卡密列表/详情/统计）。它们不需要 step-up（人只会话
// 直接读），但必须拒绝旧版全权 x-api-key 与 adm_sc_ 受限密钥。
func readOnlyMachineCredentialForbiddenRoutes() []stepUpRouteCase {
	return []stepUpRouteCase{
		{http.MethodGet, "/api/v1/admin/redeem-codes", "admin"},
		{http.MethodGet, "/api/v1/admin/redeem-codes/stats", "admin"},
		{http.MethodGet, "/api/v1/admin/redeem-codes/1", "admin"},
	}
}

// 机器凭据不得访问卡密只读接口：读操作不强迫人工 TOTP，那就必须要求人只会话。
func TestReadOnlyRedeemRoutesRejectAdminAPIKeyMachineCredential(t *testing.T) {
	settings := stepUpRouteSettings(map[string]string{})
	realStepUp := middleware.NewStepUpAuthMiddleware(nil, nil, settings)
	router := buildAdminRouteProbeRouter(t, realStepUp, service.AuditAuthMethodAdminAPIKey)
	for _, tc := range readOnlyMachineCredentialForbiddenRoutes() {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), middleware.MachineCredentialForbiddenCode)
		})
	}
}

// 卡密只读接口（列表/详情/统计）在 2026-11 2FA 触发矩阵复核后归为
// secret_free：读模型只回掩码，人只会话直接读、不再弹 TOTP；机器凭据仍被
// ForbidMachineCredentials 拒绝（见 TestReadOnlyRedeemRoutesRejectAdminAPIKeyMachineCredential）。
// 批量导出 GET /redeem-codes/export 仍在上面的 sensitiveStepUpRoutes 里。

// stepUpConsistencyUserRepo 是只实现 GetByID 的用户仓储桩（其余方法由嵌入的
// service.UserRepository 接口提供，本测试不会调用）。
type stepUpConsistencyUserRepo struct {
	service.UserRepository
	user *service.User
}

func (r *stepUpConsistencyUserRepo) GetByID(context.Context, int64) (*service.User, error) {
	return r.user, nil
}

// GetUserAvatar 供 UserService.GetByID 的头像补全路径使用；门控测试不关心头像。
func (r *stepUpConsistencyUserRepo) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

// stepUpConsistencyGrantCache 是可控的 step-up 授权缓存桩。
type stepUpConsistencyGrantCache struct {
	service.TotpCache
	granted   bool
	remaining time.Duration
}

func (c *stepUpConsistencyGrantCache) HasStepUpGrant(context.Context, int64, string, service.Factor) (bool, error) {
	return c.granted, nil
}

func (c *stepUpConsistencyGrantCache) StepUpGrantRemaining(context.Context, int64, string, service.Factor) (time.Duration, bool, error) {
	if !c.granted {
		return 0, false, nil
	}
	if c.remaining > 0 {
		return c.remaining, true, nil
	}
	return 4 * time.Minute, true, nil
}

// realStepUpWithGrant 装配真实 step-up 中间件（真人 JWT、已启用 TOTP，授权与否可控）。
func realStepUpWithGrant(t *testing.T, granted bool) middleware.StepUpAuthMiddleware {
	t.Helper()
	repo := &stepUpConsistencyUserRepo{user: &service.User{ID: 9, TotpEnabled: true, Role: service.RoleAdmin, Status: service.StatusActive}}
	totpService := service.NewTotpService(repo, nil, &stepUpConsistencyGrantCache{granted: granted}, nil, nil, nil)
	userService := service.NewUserService(repo, nil, nil, nil)
	settings := stepUpRouteSettings(map[string]string{service.SettingKeyStepUpEnabled: "true"})
	return middleware.NewStepUpAuthMiddleware(totpService, userService, settings)
}

type stepUpErrorBody struct {
	Code string `json:"code"`
}

// 前端按 JSON 里的 code 弹 TOTP 对话框：所有敏感路由必须返回同一可读错误码，
// 而不是各自拼 message 文本。这里对 sensitiveStepUpRoutes() 全量断言精确 code。
func TestSensitiveAdminRoutesReturnStepUpRequiredCode(t *testing.T) {
	adminRouter := buildAdminRouteProbeRouter(t, realStepUpWithGrant(t, false), service.AuditAuthMethodJWT)
	paymentRouter := buildPaymentProbeRouter(t, realStepUpWithGrant(t, false), service.AuditAuthMethodJWT)
	for _, tc := range sensitiveStepUpRoutes() {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			router := adminRouter
			if tc.router == "payment" {
				router = paymentRouter
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			var body stepUpErrorBody
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), w.Body.String())
			require.Equal(t, "STEP_UP_REQUIRED", body.Code, w.Body.String())
			// 没有授权时不得出现「已跳过 2FA」的误导性响应头。
			require.Empty(t, w.Header().Get(middleware.StepUpSkippedHeader))
			require.Empty(t, w.Header().Get(middleware.StepUpRemainingHeader))
		})
	}
}

// 机器凭据（旧版全权 x-api-key / 受限 adm_sc_ 密钥）无法完成 TOTP：
// 敏感路由必须返回 STEP_UP_ADMIN_API_KEY_FORBIDDEN，而不是 STEP_UP_REQUIRED。
func TestSensitiveAdminRoutesReturnAdminAPIKeyForbiddenCode(t *testing.T) {
	adminRouter := buildAdminRouteProbeRouter(t, realStepUpWithGrant(t, true), service.AuditAuthMethodAdminAPIKey)
	paymentRouter := buildPaymentProbeRouter(t, realStepUpWithGrant(t, true), service.AuditAuthMethodAdminAPIKey)
	for _, tc := range sensitiveStepUpRoutes() {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			router := adminRouter
			if tc.router == "payment" {
				router = paymentRouter
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			var body stepUpErrorBody
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), w.Body.String())
			require.Equal(t, "STEP_UP_ADMIN_API_KEY_FORBIDDEN", body.Code, w.Body.String())
		})
	}
}

// 完成 step-up 后敏感路由必须放行到业务层，并且响应必须显式回传
// 「本次跳过了 2FA + 剩余免授权秒数」——不允许静默跳过。
func TestSensitiveAdminRoutesAllowedAfterStepUpGrant(t *testing.T) {
	adminRouter := buildAdminRouteProbeRouter(t, realStepUpWithGrant(t, true), service.AuditAuthMethodJWT)
	paymentRouter := buildPaymentProbeRouter(t, realStepUpWithGrant(t, true), service.AuditAuthMethodJWT)
	for _, tc := range sensitiveStepUpRoutes() {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			router := adminRouter
			if tc.router == "payment" {
				router = paymentRouter
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			require.NotEqual(t, http.StatusForbidden, w.Code, w.Body.String())
			require.NotContains(t, w.Body.String(), "STEP_UP_REQUIRED")
			require.Equal(t, "1", w.Header().Get(middleware.StepUpSkippedHeader),
				"跳过 2FA 必须显式回传 %s", middleware.StepUpSkippedHeader)
			require.Equal(t, "240", w.Header().Get(middleware.StepUpRemainingHeader),
				"必须回传真实剩余秒数（桩里是 4 分钟）")
		})
	}
}
