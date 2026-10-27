// Adapted from ranxi2001/sub2api @ 9620d315, LGPL-3.0-or-later.
package handler

import (
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const apiKeyQueueStatsMaxIDs = 100

type apiKeyQueuePolicyDTO struct {
	MaxWaiting     int `json:"max_waiting"`
	TimeoutSeconds int `json:"timeout_seconds"`
}

type apiKeyQueueItemDTO struct {
	ID                 int64 `json:"id"`
	CurrentConcurrency int   `json:"current_concurrency"`
	CurrentWaiting     int   `json:"current_waiting"`
}

type apiKeyQueueStatsResponse struct {
	QueuePolicy apiKeyQueuePolicyDTO `json:"queue_policy"`
	Items       []apiKeyQueueItemDTO `json:"items"`
}

// parseAPIKeyQueueStatsIDs 校验正整数、去重并限制数量，顺序保持请求顺序。
func parseAPIKeyQueueStatsIDs(raw string) ([]int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []int64{}, nil
	}
	seen := make(map[int64]struct{})
	ids := make([]int64, 0, 8)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id <= 0 {
			return nil, errors.New("ids must be positive integers")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		if len(ids) >= apiKeyQueueStatsMaxIDs {
			return nil, errors.New("too many ids")
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}

// GetConcurrencyQueue 只读返回本进程的 Key 队列策略与指定 Key 的并发/等待统计。
// GET /api/v1/keys/concurrency?ids=1,2
func (h *APIKeyHandler) GetConcurrencyQueue(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	ids, err := parseAPIKeyQueueStatsIDs(c.Query("ids"))
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	policy := h.apiKeyService.APIKeyQueuePolicy()
	out := apiKeyQueueStatsResponse{
		QueuePolicy: apiKeyQueuePolicyDTO{
			MaxWaiting:     policy.MaxWaiting,
			TimeoutSeconds: int(policy.Timeout / time.Second),
		},
		Items: []apiKeyQueueItemDTO{},
	}
	if len(ids) == 0 {
		response.Success(c, out)
		return
	}

	owned, err := h.apiKeyService.VerifyOwnership(c.Request.Context(), subject.UserID, ids)
	if err != nil {
		response.InternalError(c, "Failed to verify API key ownership")
		return
	}
	ownedSet := make(map[int64]struct{}, len(owned))
	for _, id := range owned {
		ownedSet[id] = struct{}{}
	}
	for _, id := range ids {
		if _, ok := ownedSet[id]; !ok {
			// Uniform error; never disclose whether the key exists for another user.
			response.Error(c, http.StatusNotFound, "API key not found")
			return
		}
	}

	stats, err := h.apiKeyService.GetAPIKeyQueueStatsBatch(c.Request.Context(), ids)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "API key queue statistics unavailable")
		return
	}
	for _, id := range ids {
		counts := stats[id]
		out.Items = append(out.Items, apiKeyQueueItemDTO{
			ID:                 id,
			CurrentConcurrency: counts.Active,
			CurrentWaiting:     counts.Waiting,
		})
	}
	response.Success(c, out)
}
