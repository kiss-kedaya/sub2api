package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// stubAuditLogRepo 只实现审计日志 handler/service 测试需要的行为。
// 其余端口方法返回零值，避免为了一个只读断言去搭完整仓储。
type stubAuditLogRepo struct {
	logs      []*service.AuditLog
	listCalls int
	truncated bool
	inserted  []*service.AuditLog
}

func (r *stubAuditLogRepo) BatchInsert(context.Context, []*service.AuditLog) (int64, error) {
	return 0, nil
}

func (r *stubAuditLogRepo) Insert(_ context.Context, log *service.AuditLog) error {
	r.inserted = append(r.inserted, log)
	return nil
}

func (r *stubAuditLogRepo) List(_ context.Context, filter *service.AuditLogFilter) (*service.AuditLogList, error) {
	r.listCalls++
	page, pageSize := 1, 20
	if filter != nil {
		if filter.Page > 0 {
			page = filter.Page
		}
		if filter.PageSize > 0 {
			pageSize = filter.PageSize
		}
	}
	return &service.AuditLogList{Logs: r.logs, Total: len(r.logs), Page: page, PageSize: pageSize}, nil
}

func (r *stubAuditLogRepo) GetByID(context.Context, int64) (*service.AuditLog, error) {
	return nil, service.ErrAuditLogNotFound
}

func (r *stubAuditLogRepo) Count(context.Context) (int64, error) {
	return int64(len(r.logs)), nil
}

func (r *stubAuditLogRepo) TruncateAll(context.Context) error {
	r.truncated = true
	r.logs = nil
	return nil
}

func (r *stubAuditLogRepo) DeleteBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func newAuditLogTestRouter(handler *AuditLogHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/audit-logs", handler.List)
	router.POST("/audit-logs/clear", handler.Clear)
	return router
}

// 后台管理端不得清理审计日志：带合法 TOTP 也必须固定 403，
// 且请求绝不能触达 ClearAll（Count/TruncateAll/Insert 都不应被调用）。
func TestAuditLogClearAlwaysForbidden(t *testing.T) {
	repo := &stubAuditLogRepo{logs: []*service.AuditLog{{ID: 1}}}
	handler := NewAuditLogHandler(service.NewAuditLogService(repo, nil))
	router := newAuditLogTestRouter(handler)

	cases := []struct {
		name string
		body string
	}{
		{"valid totp body", `{"totp_code":"123456"}`},
		{"no body", ``},
		{"arbitrary body", `{"totp_code":"","anything":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/audit-logs/clear", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			// 即便凭证看起来是管理员会话，也不能放开清理能力。
			req.Header.Set("Authorization", "Bearer admin-session")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			require.Equal(t, http.StatusForbidden, w.Code)
			var resp struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
				Reason  string `json:"reason"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			require.Equal(t, http.StatusForbidden, resp.Code)
			require.Equal(t, "AUDIT_LOG_CLEAR_DISABLED", resp.Reason)
			require.Contains(t, strings.ToLower(resp.Message), "database administrator")
			require.Contains(t, strings.ToLower(resp.Message), "database")
		})
	}

	require.False(t, repo.truncated, "HTTP clear must never reach TruncateAll")
	require.Empty(t, repo.inserted, "HTTP clear must not write a clear-trace record")
}

// 审计日志本身仍然可读：禁用的只有清空，列表/详情链路不受影响。
func TestAuditLogListRemainsReadable(t *testing.T) {
	repo := &stubAuditLogRepo{logs: []*service.AuditLog{{
		ID:         7,
		CreatedAt:  time.Now().UTC(),
		ActorEmail: "admin@example.com",
		Action:     "admin.account.update",
		Method:     http.MethodPut,
		Path:       "/api/v1/admin/accounts/1",
		StatusCode: http.StatusOK,
	}}}
	handler := NewAuditLogHandler(service.NewAuditLogService(repo, nil))
	router := newAuditLogTestRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/audit-logs?page=1&page_size=20", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Items    []map[string]any `json:"items"`
			Total    int              `json:"total"`
			Page     int              `json:"page"`
			PageSize int              `json:"page_size"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Len(t, resp.Data.Items, 1)
	require.Equal(t, 1, resp.Data.Total)
	require.Equal(t, "admin.account.update", resp.Data.Items[0]["action"])
	require.Equal(t, 1, repo.listCalls)
}

// 清空函数仍保留给内部/运维 CLI：不经 HTTP，直接调用服务方法依旧工作，
// 并同步写入留痕记录。
func TestAuditLogServiceClearAllStillAvailableInternally(t *testing.T) {
	repo := &stubAuditLogRepo{logs: []*service.AuditLog{{ID: 1}, {ID: 2}, {ID: 3}}}
	svc := service.NewAuditLogService(repo, nil)

	trace := &service.AuditLog{ActorEmail: "dba@example.com", Action: ""}
	deleted, err := svc.ClearAll(context.Background(), trace)
	require.NoError(t, err)
	require.Equal(t, int64(3), deleted)
	require.True(t, repo.truncated)
	require.Empty(t, repo.logs)
	require.Len(t, repo.inserted, 1)

	traceRecord := repo.inserted[0]
	require.Equal(t, service.AuditActionAuditLogClear, traceRecord.Action)
	require.Equal(t, int64(3), traceRecord.Extra["deleted_rows"])
	require.Equal(t, "dba@example.com", traceRecord.ActorEmail)
}
