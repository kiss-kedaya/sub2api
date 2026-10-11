package admin

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"testing"
)

type requestIDOpsRepo struct {
	service.OpsRepository
	got string
}

func (r *requestIDOpsRepo) ListErrorLogs(_ context.Context, f *service.OpsErrorLogFilter) (*service.OpsErrorLogList, error) {
	r.got = f.RequestID
	return &service.OpsErrorLogList{Page: 1, PageSize: 20}, nil
}
func TestOpsErrorListsForwardRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, name := range []string{"all", "request", "upstream"} {
		t.Run(name, func(t *testing.T) {
			repo := &requestIDOpsRepo{}
			svc := service.NewOpsService(repo, nil, &config.Config{Ops: config.OpsConfig{Enabled: true}}, nil, nil, nil, nil, nil, nil, nil, nil)
			h := NewOpsHandler(svc)
			handlers := map[string]gin.HandlerFunc{"all": h.GetErrorLogs, "request": h.ListRequestErrors, "upstream": h.ListUpstreamErrors}
			r := gin.New()
			r.GET("/errors", handlers[name])
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/errors?request_id=%3Dclient%3Auuid", nil))
			if w.Code != 200 || repo.got != "=client:uuid" {
				t.Fatalf("status %d filter %q", w.Code, repo.got)
			}
		})
	}
}
