package service

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Call only after a complete upstream response, with the same model key used
// at the failure boundary. HTTP 200 alone does not prove an SSE request succeeded.
func (s *RateLimitService) observeModelTransientSuccess(ctx context.Context, c *gin.Context, account *Account, model string, responseBody ...[]byte) {
	if s == nil || s.modelTransient == nil || s.modelTransient.entryCount.Load() == 0 || account == nil || (ctx != nil && ctx.Err() != nil) {
		return
	}
	if len(responseBody) > 0 {
		if _, failed := anthropicStreamErrorPayload(string(responseBody[0])); failed {
			return
		}
	}
	if c != nil {
		if len(c.Errors) > 0 || c.Writer.Status() >= http.StatusBadRequest || geminiClientGone(c) {
			return
		}
		if _, failed := GetOpsStreamError(c); failed || c.GetBool(upstreamFinancialErrorSentKey) {
			return
		}
	}
	s.modelTransient.ObserveSuccess(account.ID, model)
}
