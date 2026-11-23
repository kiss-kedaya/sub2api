package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// writeProtocolMismatchError writes a 400 PROTOCOL_MISMATCH body for a request
// whose inbound protocol family cannot be served by the resolved group platform
// because the group has cross-family conversion disabled.
//
// It is used at ingress (before account selection) so the caller gets a stable,
// actionable 400 with path/group_platform/hint instead of the generic retryable
// 503 "no available accounts" that appears when the scheduler filters out every
// account for a protocol the group can never serve.
func writeProtocolMismatchError(c *gin.Context, path, groupPlatform string) {
	service.WriteProtocolMismatchError(c, path, groupPlatform)
}

// protocolMismatchApplies reports whether the ingress protocol-family guard
// should run for this request. It is intentionally narrow: only an Anthropic
// platform group (including a composite route resolved to Anthropic) receiving
// an OpenAI-family endpoint is the cross-protocol bridge this guard protects.
// Other platforms keep their existing handlers/errors so type-safe, Gemini and
// OpenAI groups are unaffected.
func protocolMismatchApplies(c *gin.Context, apiKey *service.APIKey) bool {
	if service.CrossProtocolConversionAllowed(apiKey) {
		return false
	}
	return effectiveAPIKeyPlatform(c, apiKey) == service.PlatformAnthropic
}
