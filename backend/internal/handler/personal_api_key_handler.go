package handler

import (
	"errors"
	"io"
	"net/http"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

const createPersonalAPIKeyBodyMaxBytes = 8 << 10

type createPersonalAPIKeyRequest struct {
	Verification *apiKeyVerificationRequest `json:"verification"`
}

type createPersonalAPIKeyResponse struct {
	Key         string                            `json:"key"`
	Regenerated bool                              `json:"regenerated"`
	Status      *service.UserPersonalAPIKeyStatus `json:"status"`
}

// SetPersonalAPIKeyService 注入用户个人接口密钥服务（wire 装配使用）。
func (h *APIKeyHandler) SetPersonalAPIKeyService(svc *service.UserPersonalAPIKeyService) {
	if h == nil {
		return
	}
	h.personalAPIKeyService = svc
}

func (h *APIKeyHandler) requirePersonalAPIKeyService(c *gin.Context) *service.UserPersonalAPIKeyService {
	if h == nil || h.personalAPIKeyService == nil {
		response.ErrorFrom(c, service.ErrUserPersonalAPIKeyUnavailable)
		return nil
	}
	return h.personalAPIKeyService
}

// GetPersonalAPIKey 返回当前用户的个人接口密钥状态（仅脱敏前缀与元信息，永不返回明文）。
// GET /api/v1/user/personal-api-key
func (h *APIKeyHandler) GetPersonalAPIKey(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	svc := h.requirePersonalAPIKeyService(c)
	if svc == nil {
		return
	}
	status, err := svc.GetStatus(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, status)
}

// CreateOrRegeneratePersonalAPIKey 为当前用户生成或重置个人接口密钥。
// POST /api/v1/user/personal-api-key
//
// 安全约束：
//   - 路由本身仅接受交互式 JWT（uak_ 请求已在认证中间件层被 403 拦截）；
//   - 账号存在任意可用校验因子（Passkey / TOTP / 登录密码）时强制完成三选一身份校验，
//     不受「创建 API Key 跳过验证」开关影响；
//   - 明文密钥仅在本次响应中返回一次，库内仅落不可逆 SHA-256 摘要；
//   - 审计只记录脱敏前缀、校验因子与是否重置，请求体整体不入库。
func (h *APIKeyHandler) CreateOrRegeneratePersonalAPIKey(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	svc := h.requirePersonalAPIKeyService(c)
	if svc == nil {
		return
	}

	var req createPersonalAPIKeyRequest
	if c.Request != nil && c.Request.Body != nil && c.Request.Body != http.NoBody {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, createPersonalAPIKeyBodyMaxBytes)
		if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
			setVerificationAudit(c, nil, auditVerificationRejected, "INVALID_REQUEST")
			response.BadRequest(c, "Invalid request")
			return
		}
	}

	verifySvc := h.requireVerificationService(c)
	if verifySvc == nil {
		return
	}
	verifyResult, err := verifySvc.VerifyStrict(c.Request.Context(), subject.UserID, req.Verification.toService())
	if err != nil {
		setVerificationAudit(c, nil, auditVerificationRejected, infraerrors.Reason(err))
		respondVerificationError(c, err)
		return
	}
	if verifyResult != nil && verifyResult.Factor != "" {
		setVerificationAudit(c, verifyResult, auditVerificationVerified, "")
	} else {
		setVerificationAudit(c, nil, auditVerificationSkipped, "")
	}

	plaintext, status, regenerated, err := svc.CreateOrRegenerate(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	if regenerated {
		middleware2.SetAuditAction(c, service.AuditActionUserPersonalAPIKeyRegenerate)
	} else {
		middleware2.SetAuditAction(c, service.AuditActionUserPersonalAPIKeyCreate)
	}
	prefix := ""
	if status != nil {
		prefix = status.KeyPrefix
	}
	middleware2.SetAuditExtra(c, map[string]any{
		"personal_api_key_prefix":      prefix,
		"personal_api_key_regenerated": regenerated,
	})

	c.Header("Cache-Control", "no-store")
	response.Success(c, createPersonalAPIKeyResponse{
		Key:         plaintext,
		Regenerated: regenerated,
		Status:      status,
	})
}

// DeletePersonalAPIKey 立即吊销并删除当前用户的个人接口密钥。
// DELETE /api/v1/user/personal-api-key
func (h *APIKeyHandler) DeletePersonalAPIKey(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	svc := h.requirePersonalAPIKeyService(c)
	if svc == nil {
		return
	}
	deleted, err := svc.Delete(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	middleware2.SetAuditAction(c, service.AuditActionUserPersonalAPIKeyDelete)
	response.Success(c, gin.H{
		"deleted": deleted,
		"message": "Personal API key deleted successfully",
	})
}
