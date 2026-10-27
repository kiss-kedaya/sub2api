package service

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func isGeminiCachedContentNotFound(status int, body []byte) bool {
	lower := strings.ToLower(string(body))
	switch status {
	case http.StatusForbidden:
		return strings.Contains(lower, "cachedcontent not found")
	case http.StatusBadRequest:
		return strings.Contains(lower, "invalid resource state for cache content")
	case http.StatusNotFound:
		return strings.Contains(lower, "cached content") && strings.Contains(lower, "not found")
	}
	return false
}

// Cached content errors belong to the request, not to the upstream credential.
// Reuse the existing non-stream request-scoped ops record rather than marking an
// entire shared account unhealthy for an expired or unauthorized resource ID.
func MarkOpsRequestScopedError(c *gin.Context, errType string) {
	MarkOpsStreamErrorValue(c, OpsStreamError{
		ErrType: errType, Message: GeminiCachedContentNotFoundMessage,
		IntendedStatus: http.StatusForbidden, RequestScoped: true, NonStream: true,
	})
}

func IsOpsRequestScopedError(c *gin.Context) bool {
	if c == nil {
		return false
	}
	value, ok := c.Get(OpsStreamErrorKey)
	if !ok {
		return false
	}
	record, ok := value.(OpsStreamError)
	return ok && record.RequestScoped
}

func (s *GeminiMessagesCompatService) writeGeminiCachedContentNotFound(c *gin.Context, account *Account, resp *http.Response, body []byte, requestID string, isOAuth bool) error {
	body = unwrapIfNeeded(isOAuth, body)
	message := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	setOpsUpstreamError(c, resp.StatusCode, message, "")
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform: account.Platform, AccountID: account.ID, AccountName: account.Name,
		UpstreamStatusCode: resp.StatusCode, UpstreamRequestID: requestID,
		Kind: "http_error", Message: message,
	})
	MarkOpsRequestScopedError(c, "permission_error")
	MarkResponseCommitted(c)
	c.Data(http.StatusForbidden, "application/json", []byte(GeminiCachedContentNotFoundResponse))
	return fmt.Errorf("gemini cached content unavailable: %d", resp.StatusCode)
}
