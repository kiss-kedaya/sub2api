//go:build unit

package routes

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const stepUpMatrixProbeHeader = "X-StepUp-Matrix-Probe"

// stepUpMatrixRow 是一条管理面路由的 dump 结果。
type stepUpMatrixRow struct {
	Method   string
	Path     string // gin 路由模式，如 /api/v1/admin/users/:id
	Status   string // gated / ungated（= 是否挂路由级 step-up）
	Concrete string // 探针请求实际使用的具体路径
}

// stepUpMatrixDump 是全量管理面路由矩阵的数据来源：
// 用全部条件 handler 装配 RegisterAdminRoutes / RegisterPaymentRoutes，
// 挂一个只打标记的 step-up 探针，然后对每条已注册路由发一次请求，
// 由响应头是否带探针标记判定该路由是否挂上了路由级 step-up 门控。
func stepUpMatrixDump(t *testing.T) []stepUpMatrixRow {
	t.Helper()
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, _ any) {
		c.AbortWithStatus(http.StatusInternalServerError)
	}))
	probe := middleware.StepUpAuthMiddleware(func(c *gin.Context) {
		c.Header(stepUpMatrixProbeHeader, "1")
		c.AbortWithStatus(http.StatusForbidden)
	})
	h := &handler.Handlers{
		ModelPlaza: &handler.ModelPlazaHandler{},
		Gateway:    &handler.GatewayHandler{},
		Crypto:     &handler.CryptoHandler{},
		Admin: &handler.AdminHandlers{
			UpstreamChannel: &adminhandler.UpstreamChannelHandler{},
			IPRisk:          &adminhandler.IPRiskHandler{},
			RequestCapture:  &adminhandler.RequestCaptureHandler{},
			UpstreamMonitor: &adminhandler.UpstreamMonitorHandler{},
			PromptAudit:     &securityaudit.PromptAdminHandler{},
			AccountOps:      &adminhandler.AccountOpsHandler{},
			Mihomo:          &adminhandler.MihomoHandler{},
			Serverless:      &adminhandler.ServerlessHandler{},
			ExcelGrant:      &adminhandler.ExcelGrantHandler{},
			OpsAutomation:   &adminhandler.OpsAutomationHandler{},
			CredentialGuard: &adminhandler.CredentialGuardHandler{},
		},
	}
	RegisterAdminRoutes(
		r.Group("/api/v1"), h,
		adminRoutesTestAuth(service.AuditAuthMethodJWT),
		middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }),
		probe,
		stepUpRouteSettings(map[string]string{}),
		nil,
	)
	RegisterPaymentRoutes(
		r.Group("/api/v1"),
		&handler.PaymentHandler{}, &handler.PaymentWebhookHandler{}, &adminhandler.PaymentHandler{},
		middleware.JWTAuthMiddleware(func(c *gin.Context) { c.Next() }),
		adminRoutesTestAuth(service.AuditAuthMethodJWT),
		middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }),
		probe,
		nil, stepUpRouteSettings(map[string]string{}), nil,
	)

	type key struct{ method, path string }
	seen := map[key]bool{}
	var rows []stepUpMatrixRow
	for _, ri := range r.Routes() {
		k := key{ri.Method, ri.Path}
		if seen[k] {
			continue
		}
		seen[k] = true
		rows = append(rows, stepUpMatrixRow{Method: ri.Method, Path: ri.Path})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Path == rows[j].Path {
			return rows[i].Method < rows[j].Method
		}
		return rows[i].Path < rows[j].Path
	})

	for i := range rows {
		rows[i].Concrete = concreteProbePath(rows[i].Path)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(rows[i].Method, rows[i].Concrete, nil))
		rows[i].Status = "ungated"
		if w.Header().Get(stepUpMatrixProbeHeader) == "1" {
			rows[i].Status = "gated"
		}
	}
	return rows
}

func concreteProbePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if strings.HasPrefix(s, ":") {
			segs[i] = "1"
		} else if strings.HasPrefix(s, "*") {
			segs[i] = "x"
		}
	}
	return strings.Join(segs, "/")
}

func TestStepUpMatrixDump(t *testing.T) {
	rows := stepUpMatrixDump(t)
	gated := 0
	for _, row := range rows {
		if row.Status == "gated" {
			gated++
		}
	}
	t.Logf("TOTAL %d GATED %d", len(rows), gated)
	for _, row := range rows {
		t.Logf("%s\t%s\t%s", row.Method, row.Path, row.Status)
	}
}

// TestStepUpMatrixKeepsDocumentedRouteGates 是「路由级门控不许被静默移除」的回归闸门。
//
// 全量 dump 用来生成 docs/stepup-backend-coverage-matrix.md 的现状列；本断言只要求
// 文档里列为「已挂路由级 step-up」的敏感路由仍然是 gated。它对后续的集中注册表改造
// 保持宽容（注册表可能在组入口再挂一层门控，使 gated 集合更大），但只要有人把某条
// 敏感路由的 stepUpAuth 包装删掉，CI 立刻红。
func TestStepUpMatrixKeepsDocumentedRouteGates(t *testing.T) {
	rows := stepUpMatrixDump(t)
	gated := map[string]bool{}
	for _, row := range rows {
		if row.Status == "gated" {
			gated[row.Method+" "+row.Concrete] = true
		}
	}
	for _, tc := range sensitiveStepUpRoutes() {
		key := tc.method + " " + tc.path
		require.True(t, gated[key], "敏感路由的路由级 step-up 门控被移除了: %s", key)
	}
}
