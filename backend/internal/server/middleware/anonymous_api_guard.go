package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/iprisk"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Only call after a definitive credential failure, never based on response
// status/user_id. Even a banned IP must be allowed to present a valid key.
func rejectAnonymousCredentialFailure(c *gin.Context, svc *service.APIKeyService, reason string, google bool) bool {
	guard := iprisk.AnonymousGuard(c)
	blocked := false
	fallback := !guard.AnonymousEnabled(c.Request.Context())
	if guard != nil && !fallback {
		ban, err := guard.AnonymousFailure(c.Request.Context(), ip.GetTrustedClientIP(c), reason)
		fallback = err != nil
		if err == nil && ban != nil {
			blocked = true
			c.Header("Retry-After", strconv.FormatInt(max(int64(1), int64((ban.RetryAfter+time.Second-1)/time.Second)), 10))
		}
	}
	// Redis outages use the existing bounded per-instance limiter, but only on
	// a proven invalid-credential path; valid keys never enter this IP gate.
	if fallback && reason != "disabled" {
		blocked = rejectInvalidAuthAbuse(c, svc)
		if !blocked {
			recordInvalidAuthFailure(c, svc)
		}
	}

	if !blocked {
		return false
	}
	MarkIngressRejected(c, IngressRejectInvalidAuthRateLimited)
	if google {
		abortWithGoogleError(c, http.StatusTooManyRequests, "Too many invalid authentication attempts; retry later")
	} else {
		AbortWithError(c, http.StatusTooManyRequests, "INVALID_AUTH_RATE_LIMITED", "Too many invalid authentication attempts; retry later")
	}
	return true
}

func anonymousCredentialSuccess(c *gin.Context) {
	if guard := iprisk.AnonymousGuard(c); guard != nil {
		_ = guard.AnonymousSuccess(c.Request.Context(), ip.GetTrustedClientIP(c))
	}
}
