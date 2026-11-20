package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// KeyRotationHandler exposes the key-rotation gate status to the console.
// The redeem / payment / crypto-deposit pages read it on mount so they can show
// the remediation card before the user submits anything. The endpoint itself is
// never gated (it is what the user needs to see why they are blocked).
type KeyRotationHandler struct {
	gate *service.KeyRotationGateService
}

// NewKeyRotationHandler creates the handler.
func NewKeyRotationHandler(gate *service.KeyRotationGateService) *KeyRotationHandler {
	return &KeyRotationHandler{gate: gate}
}

// GetStatus GET /api/v1/user/key-rotation-gate
func (h *KeyRotationHandler) GetStatus(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	if h.gate == nil {
		response.Success(c, &service.KeyRotationGateStatus{
			Reason:          service.KeyRotationReasonDisabled,
			RemediationPath: service.KeyRotationRemediationPath,
		})
		return
	}
	status, err := h.gate.Evaluate(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}
