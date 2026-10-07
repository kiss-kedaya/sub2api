package service

import (
	"context"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const tablePaginationCacheTTL = 5 * time.Second

type cachedTablePaginationLimits struct {
	defaultSize int
	maxSize     int
	expiresAt   time.Time
}

// ValidateTablePreferences validates writes; reading legacy settings stays tolerant.
func ValidateTablePreferences(defaultSize int, options []int) error {
	if defaultSize < 5 || defaultSize > 1000 {
		return infraerrors.BadRequest("INVALID_TABLE_DEFAULT_PAGE_SIZE", "table_default_page_size must be an integer between 5 and 1000")
	}
	if len(options) == 0 {
		return infraerrors.BadRequest("INVALID_TABLE_PAGE_SIZE_OPTIONS", "table_page_size_options must contain integers between 5 and 1000")
	}
	for _, size := range options {
		if size < 5 || size > 1000 {
			return infraerrors.BadRequest("INVALID_TABLE_PAGE_SIZE_OPTIONS", "table_page_size_options must contain integers between 5 and 1000")
		}
	}
	return nil
}

func tablePaginationLimits(defaultSize int, options []int) (int, int) {
	defaultSize, options = normalizeTablePreferences(defaultSize, options)
	maxSize := options[len(options)-1]
	return min(defaultSize, maxSize), maxSize
}

// GetTablePaginationLimits shares a short cache across panel requests. Other
// replicas observe changes within the TTL; a failed refresh keeps the last cap.
func (s *SettingService) GetTablePaginationLimits(ctx context.Context) (int, int) {
	if s == nil || s.settingRepo == nil {
		return 20, 1000
	}
	if cached := s.tablePaginationCache.Load(); cached != nil && time.Now().Before(cached.expiresAt) {
		return cached.defaultSize, cached.maxSize
	}
	s.tablePaginationMu.Lock()
	defer s.tablePaginationMu.Unlock()
	prior := s.tablePaginationCache.Load()
	if prior != nil && time.Now().Before(prior.expiresAt) {
		return prior.defaultSize, prior.maxSize
	}
	if ctx == nil {
		ctx = context.Background()
	}
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	values, err := s.settingRepo.GetMultiple(dbCtx, []string{SettingKeyTableDefaultPageSize, SettingKeyTablePageSizeOptions})
	defaultSize, options := parseTablePreferences(values[SettingKeyTableDefaultPageSize], values[SettingKeyTablePageSizeOptions])
	defaultSize, maxSize := tablePaginationLimits(defaultSize, options)
	if err != nil {
		defaultSize, maxSize = 5, 5
		if prior != nil {
			defaultSize, maxSize = prior.defaultSize, prior.maxSize
		}
	}
	s.tablePaginationCache.Store(&cachedTablePaginationLimits{defaultSize, maxSize, time.Now().Add(tablePaginationCacheTTL)})
	return defaultSize, maxSize
}

func (s *SettingService) publishTablePaginationLimits(defaultSize int, options []int) {
	defaultSize, maxSize := tablePaginationLimits(defaultSize, options)
	// Serialize publication with refresh so an in-flight old read cannot win.
	s.tablePaginationMu.Lock()
	defer s.tablePaginationMu.Unlock()
	s.tablePaginationCache.Store(&cachedTablePaginationLimits{defaultSize, maxSize, time.Now().Add(tablePaginationCacheTTL)})
}
