package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestTicketAuditOmitsContactAndFreeText(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &auditCaptureRepository{}
	svc := service.NewAuditLogService(repo, nil)
	svc.Start()
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(string(ContextKeyUser), AuthSubject{UserID: 7}); c.Next() })
	r.Use(gin.HandlerFunc(NewAuditLogMiddleware(svc)))
	for _, path := range []string{"/api/v1/tickets", "/api/v1/tickets/:id/replies", "/api/v1/admin/tickets/:id/replies"} {
		r.POST(path, func(c *gin.Context) {
			body, err := io.ReadAll(c.Request.Body)
			require.NoError(t, err)
			require.Contains(t, string(body), "private-contact")
			c.Status(200)
		})
	}
	for _, path := range []string{"/api/v1/tickets", "/api/v1/tickets/10/replies", "/api/v1/admin/tickets/10/replies"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"contact":"private-contact","content":"private-content"}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code)
	}
	svc.Stop()
	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.logs, 3)
	for _, entry := range repo.logs {
		require.NotContains(t, entry.RequestBody, "private-contact")
		require.NotContains(t, entry.RequestBody, "private-content")
	}
}
