package service

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
)

// ContextShouldReturnRequestedModel reads the effective billing group's policy.
// Key-route selection updates ctxkey.Group before forwarding; no database lookup
// or upstream-account model policy is needed on this client-facing path.
func ContextShouldReturnRequestedModel(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if c.Request != nil {
		if group, ok := c.Request.Context().Value(ctxkey.Group).(*Group); ok && group != nil {
			return group.ReturnRequestedModel
		}
	}
	// This is the API key installed by authentication middleware. Keep service
	// independent of that middleware package to avoid an import cycle.
	if value, ok := c.Get("api_key"); ok {
		if key, ok := value.(*APIKey); ok && key != nil && key.Group != nil {
			return key.Group.ReturnRequestedModel
		}
	}
	return false
}
