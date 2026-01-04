// Package pagination provides types and helpers for paginated responses.
package pagination

// PaginationParams 分页参数
type PaginationParams struct {
	Page     int
	PageSize int
}

// PaginationResult 分页结果
type PaginationResult struct {
	Total    int64
	Page     int
	PageSize int
	Pages    int
}

// DefaultPagination 默认分页参数
func DefaultPagination() PaginationParams {
	return PaginationParams{
		Page:     1,
		PageSize: 20,
	}
}

// Offset 计算偏移量
func (p PaginationParams) Offset() int {
	if p.Page < 1 {
		p.Page = 1
	}
	return (p.Page - 1) * p.PageSize
}

// Limit 获取限制数
func (p PaginationParams) Limit() int {
	if p.PageSize < 1 {
		return 20
	}
	if p.PageSize > 100 {
		return 100
	}
	return p.PageSize
}
