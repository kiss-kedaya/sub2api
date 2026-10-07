package middleware

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

type tablePaginationSettings interface {
	GetTablePaginationLimits(context.Context) (int, int)
}

// TablePagination applies the shared table settings only to authenticated panel routes.
func TablePagination(settings tablePaginationSettings) gin.HandlerFunc {
	return func(c *gin.Context) {
		if settings != nil {
			response.SetPaginationLimitsLoader(c, func() (int, int) {
				return settings.GetTablePaginationLimits(c.Request.Context())
			})
		}
		c.Next()
	}
}
