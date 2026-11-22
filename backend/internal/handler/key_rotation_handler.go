package handler

import (
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// KeyRotationHandler exposes the key-rotation gate status to the console and the
// explicit "I know the risk" acknowledgement that temporarily unblocks recharge
// for users who refuse to rotate keys or add an IP allowlist.
// The status endpoint itself is never gated (it is what the user needs to see
// why they are blocked).
type KeyRotationHandler struct {
	gate     *service.KeyRotationGateService
	riskAcks *service.KeyRotationRiskAckService
	audit    *service.AuditLogService
}

// NewKeyRotationHandler creates the handler.
func NewKeyRotationHandler(
	gate *service.KeyRotationGateService,
	riskAcks *service.KeyRotationRiskAckService,
	audit *service.AuditLogService,
) *KeyRotationHandler {
	return &KeyRotationHandler{gate: gate, riskAcks: riskAcks, audit: audit}
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

type acknowledgeKeyRotationRiskRequest struct {
	AcknowledgeRisk bool `json:"acknowledge_risk"`
}

type acknowledgeKeyRotationRiskResponse struct {
	ExpiresAt time.Time `json:"expires_at"`
	Hours     int       `json:"hours"`
}

// AcknowledgeRisk POST /api/v1/user/security/key-rotation-risk-ack
//
// Records one explicit, expiring risk acknowledgement (acknowledge_risk must be
// true) so the user can recharge while still keeping possibly-leaked keys. The
// row and the audit entry (action KEY_ROTATION_RISK_ACK) both carry expires_at
// and the cutoff snapshot, so the skip is fully traceable.
func (h *KeyRotationHandler) AcknowledgeRisk(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	var req acknowledgeKeyRotationRiskRequest
	if err := c.ShouldBindJSON(&req); err != nil || !req.AcknowledgeRisk {
		response.BadRequest(c, "acknowledge_risk must be true")
		return
	}
	if h.riskAcks == nil {
		response.ErrorFrom(c, service.ErrKeyRotationRiskAckDisabled)
		return
	}

	_, cutoff := h.gate.Settings()
	ack, err := h.riskAcks.Acknowledge(
		c.Request.Context(),
		subject.UserID,
		cutoff,
		middleware2.SecurityClientIP(c),
		c.Request.UserAgent(),
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	h.recordRiskAckAudit(c, subject.UserID, ack)
	// 本请求已经单独写入语义化审计，避免通用审计中间件再记一条重复行。
	middleware2.SkipAudit(c)

	response.Success(c, acknowledgeKeyRotationRiskResponse{
		ExpiresAt: ack.ExpiresAt.UTC(),
		Hours:     int(ack.ExpiresAt.Sub(ack.AcknowledgedAt).Hours()),
	})
}

// recordRiskAckAudit 写入一条显式风险确认审计：谁、何时、从哪个 IP、有效期多长、
// 以及确认所对应的 gate cutoff。
func (h *KeyRotationHandler) recordRiskAckAudit(c *gin.Context, userID int64, ack *service.KeyRotationRiskAck) {
	if h.audit == nil || ack == nil {
		return
	}
	uid := userID
	authMethod := c.GetString("auth_method")
	if authMethod == "" {
		authMethod = service.AuditAuthMethodJWT
	}
	fullPath := c.FullPath()
	if fullPath == "" {
		fullPath = c.Request.URL.Path
	}
	clientIP := middleware2.SecurityClientIP(c)
	entry := &service.AuditLog{
		CreatedAt:   time.Now().UTC(),
		Action:      service.AuditActionKeyRotationRiskAck,
		Method:      c.Request.Method,
		Path:        fullPath,
		ClientIP:    clientIP,
		UserAgent:   c.Request.UserAgent(),
		StatusCode:  http.StatusOK,
		ActorUserID: &uid,
		AuthMethod:  authMethod,
		Extra: map[string]any{
			"user_id":     uid,
			"ip":          clientIP,
			"expires_at":  ack.ExpiresAt.UTC().Format(time.RFC3339),
			"cutoff":      ack.CutoffSnapshot.UTC().Format(time.RFC3339),
			"ack_id":      ack.ID,
			"auth_method": authMethod,
		},
	}
	if requestID, ok := c.Request.Context().Value(ctxkey.RequestID).(string); ok {
		entry.RequestID = requestID
	}
	h.audit.Record(entry)
}
