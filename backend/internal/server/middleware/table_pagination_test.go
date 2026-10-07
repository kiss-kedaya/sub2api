package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type tablePaginationSettingsStub struct{ calls int }

func (s *tablePaginationSettingsStub) GetTablePaginationLimits(context.Context) (int, int) {
	s.calls++
	return 20, 50
}

func TestTablePaginationCapsPanelRequestsLazily(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := &tablePaginationSettingsStub{}
	r := gin.New()
	r.Use(TablePagination(settings))
	r.GET("/profile", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.GET("/list", func(c *gin.Context) {
		page, size := response.ParsePagination(c)
		response.Paginated(c, []int{}, 0, page, size)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/profile", nil))
	require.Zero(t, settings.calls)
	for _, query := range []string{"page_size=1000", "limit=1000"} {
		w = httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/list?"+query, nil))
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), `"page_size":50`)
	}
	require.Equal(t, 2, settings.calls)
}
