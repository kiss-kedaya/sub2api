package service

import (
	"context"
	"fmt"
)

func (s *APIKeyService) APIKeyQueuePolicy() APIKeyQueuePolicy {
	if s == nil || s.concurrencyService == nil {
		return APIKeyQueuePolicy{}
	}
	return s.concurrencyService.APIKeyQueuePolicy()
}
func (s *APIKeyService) GetAPIKeyQueueStatsBatch(ctx context.Context, ids []int64) (map[int64]APIKeyQueueCounts, error) {
	if s == nil || s.concurrencyService == nil {
		return nil, fmt.Errorf("API key queue statistics unavailable")
	}
	return s.concurrencyService.GetAPIKeyQueueStatsBatch(ctx, ids)
}
