package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// 三个接口对应「浏览器端封存 API Key」的后端一半：
//
//	PUT    /api/v1/user/api-keys/:id/secret-seal  存密文（幂等 upsert）
//	GET    /api/v1/user/api-keys/:id/secret-seal  取密文（前端拿密码解密）
//	DELETE /api/v1/user/api-keys/:id/secret-seal  删除密文（关闭「后续可查看」）
//
// 服务端只做存取：不生成密钥、不接触密码、不解密。密文的加解密全部发生在浏览器。

// apiKeySecretSealRequest 是 PUT 的请求体。kdf 原样保存，salt 就在里面。
//
// Password 只在服务端做 bcrypt 比对（确认封存用的确实是登录密码，否则以后
// 解密必然失败），不落库、不进日志、不进审计。AuthFactor 记录本次封存由哪个
// 因子授权（创建时的三选一），二次查看时要求同一因子。
type apiKeySecretSealRequest struct {
	Version    int             `json:"version"`
	KDF        json.RawMessage `json:"kdf"`
	Sealed     string          `json:"sealed"`
	AuthFactor string          `json:"auth_factor"`
	Password   string          `json:"password"`
}

// apiKeySecretResponse 是 GET 的响应体；只回密封材料，不回任何服务端派生数据。
type apiKeySecretResponse struct {
	Version int             `json:"version"`
	KDF     json.RawMessage `json:"kdf"`
	Sealed  string          `json:"sealed"`
}

// PutSecretSeal 保存浏览器端加密后的 API Key 密文。
// PUT /api/v1/user/api-keys/:id/secret-seal
func (h *APIKeyHandler) PutSecretSeal(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	keyID, ok := parseAPIKeySecretID(c)
	if !ok {
		return
	}
	if h.secretService == nil {
		response.InternalError(c, "API key secret storage is not configured")
		return
	}

	// 先按 Content-Length 直接拒绝超大请求；没有 Content-Length（chunked）的
	// 情况由路由上的 MaxBytesReader 兜底，解析错误会映射成 413。
	if c.Request.ContentLength > service.APIKeySecretMaxBodyBytes {
		response.Error(c, http.StatusRequestEntityTooLarge,
			buildBodyTooLargeMessage(service.APIKeySecretMaxBodyBytes))
		return
	}
	var req apiKeySecretSealRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		if maxErr, isMax := extractMaxBytesError(err); isMax {
			response.Error(c, http.StatusRequestEntityTooLarge, buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	authFactor := strings.ToLower(strings.TrimSpace(req.AuthFactor))
	if authFactor != "" && !service.APIKeyVerificationMethodValid(authFactor) {
		response.ErrorFrom(c, service.ErrAPIKeySecretInvalid)
		return
	}
	// 旧客户端没有 password / auth_factor 字段：保持兼容放行（不记录因子），
	// 新客户端两个字段必带，封存前先验证登录密码，避免「创建成功、以后解不开」。
	legacySeal := authFactor == "" && strings.TrimSpace(req.Password) == ""
	if !legacySeal {
		if h.verificationService == nil {
			response.ErrorFrom(c, service.ErrAPIKeyVerificationUnavailable)
			return
		}
		if err := h.verificationService.VerifyForSeal(c.Request.Context(), subject.UserID, req.Password); err != nil {
			setVerificationAudit(c, nil, auditVerificationRejected, "VERIFY_PASSWORD_INVALID")
			response.ErrorFrom(c, err)
			return
		}
		if authFactor == "" {
			authFactor = service.APIKeyFactorPassword
		}
		setVerificationAudit(c, &service.APIKeyVerificationResult{Factor: authFactor}, auditVerificationVerified, "")
	}

	key, err := h.secretService.Seal(c.Request.Context(), subject.UserID, keyID, service.APIKeySecretSealInput{
		Version:    req.Version,
		KDF:        req.KDF,
		Sealed:     req.Sealed,
		AuthFactor: authFactor,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	setSecretSealAudit(c, service.AuditActionAPIKeySecretSeal, key)
	// PUT 不返回密文，避免“存完顺手回显”形成第二个密文出口。
	response.Success(c, gin.H{"ok": true})
}

// GetSecretSeal 返回密文与 KDF 参数，由浏览器在用户输入登录密码后解密。
// GET /api/v1/user/api-keys/:id/secret-seal
func (h *APIKeyHandler) GetSecretSeal(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	keyID, ok := parseAPIKeySecretID(c)
	if !ok {
		return
	}
	if h.secretService == nil {
		response.InternalError(c, "API key secret storage is not configured")
		return
	}

	secret, key, err := h.secretService.Get(c.Request.Context(), subject.UserID, keyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// 二次查看必须先用同一因子完成身份校验，拿到 5 分钟一次性 grant；
	// 没有 grant / grant 过期 / 已被用过 → 403，前端重新弹校验。
	if _, ok := h.consumeRevealGrant(c, subject.UserID, keyID); !ok {
		return
	}
	setSecretSealAudit(c, service.AuditActionAPIKeySecretFetch, key)

	// 密文虽然不能直接解密，但也不该被中间层/浏览器缓存。
	c.Header("Cache-Control", "no-store")
	response.Success(c, apiKeySecretResponse{
		Version: secret.Version,
		KDF:     secret.KDF,
		Sealed:  secret.Sealed,
	})
}

// DeleteSecretSeal 删除密文；重复删除幂等返回成功。
// DELETE /api/v1/user/api-keys/:id/secret-seal
func (h *APIKeyHandler) DeleteSecretSeal(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	keyID, ok := parseAPIKeySecretID(c)
	if !ok {
		return
	}
	if h.secretService == nil {
		response.InternalError(c, "API key secret storage is not configured")
		return
	}

	key, err := h.secretService.Delete(c.Request.Context(), subject.UserID, keyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	setSecretSealAudit(c, service.AuditActionAPIKeySecretDelete, key)
	response.Success(c, gin.H{"ok": true})
}

// parseAPIKeySecretID 解析路径里的 Key ID；失败时已写好 400 响应。
func parseAPIKeySecretID(c *gin.Context) (int64, bool) {
	keyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || keyID <= 0 {
		response.BadRequest(c, "Invalid key ID")
		return 0, false
	}
	return keyID, true
}

// setSecretSealAudit 给当前审计记录补动作名与最小 extra：
// 只放 api_key_id / api_key_prefix，绝不把 sealed 或 salt 写进审计。
func setSecretSealAudit(c *gin.Context, action string, key *service.APIKey) {
	middleware2.SetAuditAction(c, action)
	if key == nil {
		return
	}
	middleware2.SetAuditExtra(c, map[string]any{
		"api_key_id":     key.ID,
		"api_key_prefix": key.DisplayPrefix(),
	})
}

// annotateSealedSecretPresence 给单把 Key 的 DTO 填 has_sealed_secret。
// 辅助查询失败时保持 false 并记一条告警：不能让「密文表查询抖动」把整个
// 详情/列表接口打挂（密文本身仍只能由 GET secret-seal 读取）。
func (h *APIKeyHandler) annotateSealedSecretPresence(ctx context.Context, userID, keyID int64, out *dto.APIKey) {
	if h == nil || h.secretService == nil || out == nil {
		return
	}
	present, err := h.secretService.Presence(ctx, userID, []int64{keyID})
	if err != nil {
		slog.Warn("api key sealed secret presence check failed", "user_id", userID, "api_key_id", keyID, "error", err)
		return
	}
	factor, ok := present[keyID]
	out.HasSealedSecret = ok
	if ok {
		out.SealAuthFactor = factor
	}
}

// attachSealedSecretPresence 给列表批量填 has_sealed_secret（一次查询，避免 N+1）。
func (h *APIKeyHandler) attachSealedSecretPresence(ctx context.Context, userID int64, keys []service.APIKey, out []dto.APIKey) {
	if h == nil || h.secretService == nil || len(keys) == 0 || len(out) == 0 {
		return
	}
	ids := make([]int64, 0, len(keys))
	for i := range keys {
		ids = append(ids, keys[i].ID)
	}
	present, err := h.secretService.Presence(ctx, userID, ids)
	if err != nil {
		slog.Warn("api key sealed secret presence check failed", "user_id", userID, "count", len(ids), "error", err)
		return
	}
	for i := range out {
		if factor, ok := present[out[i].ID]; ok {
			out[i].HasSealedSecret = true
			out[i].SealAuthFactor = factor
		}
	}
}
