package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Both plain content-policy errors and legacy failover errors must settle
// before the ordinary retry/error path. Local session blocks never bill.
func (h *OpenAIGatewayHandler) handleGrokModerationRefusal(c *gin.Context, key *service.APIKey, account *service.Account,
	body []byte, model string, stream bool, result *service.OpenAIForwardResult, forwardErr error,
	submit func(*service.OpenAIForwardResult),
) bool {
	if account == nil || forwardErr == nil {
		return false
	}
	matched := service.IsGrokPlatformContentPolicyRejection(account.Platform, forwardErr)
	var failover *service.UpstreamFailoverError
	if !matched && errors.As(forwardErr, &failover) && failover.StatusCode == http.StatusForbidden {
		matched = service.IsGrokModerationRefusal(account.Platform, failover.ResponseBody)
	}
	if !matched {
		return false
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 3*time.Second)
	defer cancel()
	h.gatewayService.BlockGrokModerationRefusedRequest(ctx, c, key, body)
	result = service.PrepareGrokModerationRefusalOpenAIResult(result, model, service.GrokModerationRefusalBilledCost(key.Group))
	result.Stream = stream
	submit(result)
	// The plain-error path already wrote the upstream 403. Legacy failover
	// errors have not written a response yet; preserve the refusal status.
	if !c.Writer.Written() {
		h.errorResponse(c, http.StatusForbidden, "invalid_request_error", service.GrokModerationRefusalMessage())
	}
	return true
}

func (h *OpenAIGatewayHandler) rejectIfGrokModerationSessionBlocked(c *gin.Context, key *service.APIKey, body []byte) bool {
	if !h.grokModerationSessionBlocked(c, key, body) {
		return false
	}
	h.errorResponse(c, http.StatusForbidden, "invalid_request_error", service.GrokModerationRefusalMessage())
	return true
}

func (h *OpenAIGatewayHandler) grokModerationSessionBlocked(c *gin.Context, key *service.APIKey, body []byte) bool {
	// The hash already isolates users. Group 0 keeps a smart-route key's
	// primary/selected group changes from bypassing the conversation block.
	return key != nil && h.gatewayService.GrokModerationRefusedRequestBlocked(c.Request.Context(), c, key, body)
}
