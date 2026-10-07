// Package response provides standardized HTTP response helpers.
package response

import (
	"log"
	"math"
	"net/http"
	"strconv"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
	"github.com/gin-gonic/gin"
)

// Response 标准API响应格式
type Response struct {
	Code     int               `json:"code"`
	Message  string            `json:"message"`
	Reason   string            `json:"reason,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Data     any               `json:"data,omitempty"`
}

// PaginatedData 分页数据格式（匹配前端期望）
type PaginatedData struct {
	Items    any   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Pages    int   `json:"pages"`
}

// Success 返回成功响应
func Success(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Response{
		Code:    0,
		Message: "success",
		Data:    data,
	})
}

// Created 返回创建成功响应
func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, Response{
		Code:    0,
		Message: "success",
		Data:    data,
	})
}

// Accepted 返回异步接受响应 (HTTP 202)
func Accepted(c *gin.Context, data any) {
	c.JSON(http.StatusAccepted, Response{
		Code:    0,
		Message: "accepted",
		Data:    data,
	})
}

// Error 返回错误响应
func Error(c *gin.Context, statusCode int, message string) {
	c.JSON(statusCode, Response{
		Code:     statusCode,
		Message:  message,
		Reason:   "",
		Metadata: nil,
	})
}

// ErrorWithDetails returns an error response compatible with the existing envelope while
// optionally providing structured error fields (reason/metadata).
func ErrorWithDetails(c *gin.Context, statusCode int, message, reason string, metadata map[string]string) {
	c.JSON(statusCode, Response{
		Code:     statusCode,
		Message:  message,
		Reason:   reason,
		Metadata: metadata,
	})
}

// ErrorFrom converts an ApplicationError (or any error) into the envelope-compatible error response.
// It returns true if an error was written.
func ErrorFrom(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}

	statusCode, status := infraerrors.ToHTTP(err)

	// Log internal errors with full details for debugging
	if statusCode >= 500 && c.Request != nil {
		log.Printf("[ERROR] %s %s\n  Error: %s", c.Request.Method, c.Request.URL.Path, logredact.RedactText(err.Error()))
	}

	ErrorWithDetails(c, statusCode, status.Message, status.Reason, status.Metadata)
	return true
}

// BadRequest 返回400错误
func BadRequest(c *gin.Context, message string) {
	Error(c, http.StatusBadRequest, message)
}

// Unauthorized 返回401错误
func Unauthorized(c *gin.Context, message string) {
	Error(c, http.StatusUnauthorized, message)
}

// Forbidden 返回403错误
func Forbidden(c *gin.Context, message string) {
	Error(c, http.StatusForbidden, message)
}

// NotFound 返回404错误
func NotFound(c *gin.Context, message string) {
	Error(c, http.StatusNotFound, message)
}

// InternalError 返回500错误
func InternalError(c *gin.Context, message string) {
	Error(c, http.StatusInternalServerError, message)
}

// Paginated 返回分页数据
func Paginated(c *gin.Context, items any, total int64, page, pageSize int) {
	pages := int(math.Ceil(float64(total) / float64(pageSize)))
	if pages < 1 {
		pages = 1
	}

	Success(c, PaginatedData{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Pages:    pages,
	})
}

// PaginationResult 分页结果（与pagination.PaginationResult兼容）
type PaginationResult struct {
	Total    int64
	Page     int
	PageSize int
	Pages    int
}

// PaginatedWithResult 使用PaginationResult返回分页数据
func PaginatedWithResult(c *gin.Context, items any, pagination *PaginationResult) {
	if pagination == nil {
		Success(c, PaginatedData{
			Items:    items,
			Total:    0,
			Page:     1,
			PageSize: 20,
			Pages:    1,
		})
		return
	}

	Success(c, PaginatedData{
		Items:    items,
		Total:    pagination.Total,
		Page:     pagination.Page,
		PageSize: pagination.PageSize,
		Pages:    pagination.Pages,
	})
}

// ParsePagination 解析分页参数
func ParsePagination(c *gin.Context) (page, pageSize int) {
	page = 1
	pageSize, _ = paginationLimits(c)

	if p := c.Query("page"); p != "" {
		if val, err := parseInt(p); err == nil && val > 0 {
			page = val
		}
	}

	// 支持 page_size 和 limit 两种参数名
	if ps := c.Query("page_size"); ps != "" {
		if val, err := parseInt(ps); err == nil && val > 0 && val <= 1000 {
			pageSize = val
		}
	} else if l := c.Query("limit"); l != "" {
		if val, err := parseInt(l); err == nil && val > 0 && val <= 1000 {
			pageSize = val
		}
	}

	pageSize = ClampPageSize(c, pageSize)
	// Pagination repositories calculate (page-1)*pageSize. Reject values that
	// would overflow int before they reach an offset calculation.
	if page > 1 && page-1 > math.MaxInt/pageSize {
		page = 1
	}
	return page, pageSize
}

const paginationLimitsKey = "response.pagination_limits"
const paginationLimitsLoaderKey = "response.pagination_limits_loader"

type paginationLimitValues struct {
	defaultSize int
	maxSize     int
}

// SetPaginationLimitsLoader defers settings reads until a handler needs pagination.
func SetPaginationLimitsLoader(c *gin.Context, loader func() (int, int)) {
	c.Set(paginationLimitsLoaderKey, loader)
}

func paginationLimits(c *gin.Context) (int, int) {
	if value, exists := c.Get(paginationLimitsKey); exists {
		if limits, ok := value.(paginationLimitValues); ok {
			return limits.defaultSize, limits.maxSize
		}
	}
	defaultSize, maxSize := 20, 1000
	if value, exists := c.Get(paginationLimitsLoaderKey); exists {
		if loader, ok := value.(func() (int, int)); ok && loader != nil {
			defaultSize, maxSize = loader()
		}
	}
	if maxSize < 1 || maxSize > 1000 {
		maxSize = 1000
	}
	if defaultSize < 1 {
		defaultSize = 20
	}
	defaultSize = min(defaultSize, maxSize)
	c.Set(paginationLimitsKey, paginationLimitValues{defaultSize, maxSize})
	return defaultSize, maxSize
}

// ClampPageSize also covers handlers with stricter or cursor-based parsers.
func ClampPageSize(c *gin.Context, pageSize int) int {
	defaultSize, maxSize := paginationLimits(c)
	if pageSize < 1 {
		return defaultSize
	}
	return min(pageSize, maxSize)
}

// parseInt accepts digits only. strconv.Atoi would also take a leading sign or
// surrounding whitespace, which these query parameters must not allow.
func parseInt(s string) (int, error) {
	for _, char := range s {
		if char < '0' || char > '9' {
			return 0, strconv.ErrSyntax
		}
	}
	return strconv.Atoi(s)
}
