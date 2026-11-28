// Package handler provides HTTP request handlers for the application.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// APIKeyHandler handles API key-related requests
type APIKeyHandler struct {
	apiKeyService *service.APIKeyService
	// secretService 只负责浏览器端封存密文的存取（服务器不解密）。
	// 旧测试/嵌入方可以传 nil，此时列表与详情不暴露 has_sealed_secret 之外的
	// 任何行为，也不会访问新表。
	secretService *service.APIKeySecretService
	// verificationService 是创建 / 二次查看的三选一身份校验（密码 / TOTP / Passkey）。
	// 由 ProvideAPIKeyHandler 注入；nil 时旧测试与嵌入方行为不变（不强制校验），
	// 生产装配必定非 nil。
	verificationService *service.APIKeyVerificationService
	// personalAPIKeyService 管理用户个人接口密钥（/api/v1/user/personal-api-key）。
	personalAPIKeyService *service.UserPersonalAPIKeyService
}

// SetVerificationService 注入身份校验服务（wire 装配使用）。
// 保持 NewAPIKeyHandler 的签名不变，避免大量既有测试构造点改动。
func (h *APIKeyHandler) SetVerificationService(verificationService *service.APIKeyVerificationService) {
	if h == nil {
		return
	}
	h.verificationService = verificationService
}

// SetPasswordAttemptLimiter 把密码因子的按用户失败预算交给校验服务（router 装配）。
// 它与登录 2FA 的预算是两个独立的计数桶（同一套 Redis 滑动窗口实现，key 前缀不同），
// 互不清零、互不锁死。校验服务未装配时什么都不做。
func (h *APIKeyHandler) SetPasswordAttemptLimiter(limiter service.APIKeyPasswordAttemptLimiter) {
	if h == nil || h.verificationService == nil {
		return
	}
	h.verificationService.SetPasswordAttemptLimiter(limiter)
}

// NewAPIKeyHandler creates a new APIKeyHandler
func NewAPIKeyHandler(apiKeyService *service.APIKeyService, secretService *service.APIKeySecretService) *APIKeyHandler {
	return &APIKeyHandler{
		apiKeyService: apiKeyService,
		secretService: secretService,
	}
}

// BlockDatacenterIPs appends hosting/proxy IPs from the key's recent usage
// to its IP blacklist.
// POST /api/v1/keys/:id/block-datacenter-ips
func (h *APIKeyHandler) BlockDatacenterIPs(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid API key ID")
		return
	}
	added, checked, err := h.apiKeyService.BlockDatacenterIPs(c.Request.Context(), id, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"added": added, "added_count": len(added), "checked_ip_count": checked})
}

// CreateAPIKeyRequest represents the create API key request payload
type CreateAPIKeyRequest struct {
	ConcurrencyLimit int      `json:"concurrency_limit" binding:"gte=0"`
	Name             string   `json:"name" binding:"required"`
	GroupID          *int64   `json:"group_id"` // nullable
	GroupIDs         []int64  `json:"group_ids"`
	CustomKey        *string  `json:"custom_key"`      // 可选的自定义key
	IPWhitelist      []string `json:"ip_whitelist"`    // IP 白名单
	IPBlacklist      []string `json:"ip_blacklist"`    // IP 黑名单
	Quota            *float64 `json:"quota"`           // 配额限制 (USD)
	ExpiresInDays    *int     `json:"expires_in_days"` // 过期天数

	// Rate limit fields (0 = unlimited)
	RateLimit5h *float64 `json:"rate_limit_5h"`
	RateLimit1d *float64 `json:"rate_limit_1d"`
	RateLimit7d *float64 `json:"rate_limit_7d"`

	// AnthropicCacheTTLMode 密钥级 Anthropic cache_control TTL 覆盖：
	// inherit（默认，跟随管理员全局设置）/ off / 1h / 5m。
	AnthropicCacheTTLMode        string `json:"anthropic_cache_ttl_mode"`
	DeepSeekToolInjectionEnabled *bool  `json:"deepseek_tool_injection_enabled"`
	// AutoBlockDatacenterIPs 打开后由后台定时任务把机房/代理 IP 追加进黑名单。
	AutoBlockDatacenterIPs bool `json:"auto_block_datacenter_ips"`
	// Verification 创建前的三选一身份校验（登录密码 / TOTP / Passkey）。
	// 账号存在可用因子时必填；校验失败直接 400，不允许创建 Key。
	Verification *apiKeyVerificationRequest `json:"verification"`
}

// UpdateAPIKeyRequest represents the update API key request payload
type UpdateAPIKeyRequest struct {
	ConcurrencyLimit *int      `json:"concurrency_limit" binding:"omitempty,gte=0"`
	Name             string    `json:"name"`
	GroupID          *int64    `json:"group_id"`
	GroupIDs         *[]int64  `json:"group_ids"`
	Status           string    `json:"status" binding:"omitempty,oneof=active inactive"`
	IPWhitelist      *[]string `json:"ip_whitelist"` // IP 白名单（nil 不修改，空数组清空）
	IPBlacklist      *[]string `json:"ip_blacklist"` // IP 黑名单（nil 不修改，空数组清空）
	Quota            *float64  `json:"quota"`        // 配额限制 (USD), 0=无限制
	ExpiresAt        *string   `json:"expires_at"`   // 过期时间 (ISO 8601)
	ResetQuota       *bool     `json:"reset_quota"`  // 重置已用配额

	// Rate limit fields (nil = no change, 0 = unlimited)
	RateLimit5h         *float64 `json:"rate_limit_5h"`
	RateLimit1d         *float64 `json:"rate_limit_1d"`
	RateLimit7d         *float64 `json:"rate_limit_7d"`
	ResetRateLimitUsage *bool    `json:"reset_rate_limit_usage"` // 重置限速用量

	// AnthropicCacheTTLMode 密钥级 Anthropic cache_control TTL 覆盖
	// （nil = 不修改；合法值 inherit/off/1h/5m）。
	AnthropicCacheTTLMode        *string `json:"anthropic_cache_ttl_mode"`
	DeepSeekToolInjectionEnabled *bool   `json:"deepseek_tool_injection_enabled"`
	// AutoBlockDatacenterIPs 开关（nil = 不修改）。
	AutoBlockDatacenterIPs *bool `json:"auto_block_datacenter_ips"`
	// RegenerateKey 轮换密钥：生成新的 key 值，旧值立刻失效。
	RegenerateKey bool `json:"regenerate_key"`
	// Verification 本次编辑会下发 / 变更明文时的三选一身份校验（登录密码 /
	// TOTP / Passkey）。只有 regenerate_key=true 时才必填；改名 / 配额 /
	// 白名单等不下发明文的编辑不要求校验。服务端不接受在这一路径上改写
	// 密钥值本身（不存在 custom_key 字段），避免多一条下发 / 变更明文的入口。
	Verification *apiKeyVerificationRequest `json:"verification"`
}

func validAPIKeyLimit(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }

func validateAPIKeyCreateRequest(req CreateAPIKeyRequest) error {
	if req.Quota != nil && !validAPIKeyLimit(*req.Quota) {
		return errors.New("invalid quota")
	}
	if req.RateLimit5h != nil && !validAPIKeyLimit(*req.RateLimit5h) {
		return errors.New("invalid rate_limit_5h")
	}
	if req.RateLimit1d != nil && !validAPIKeyLimit(*req.RateLimit1d) {
		return errors.New("invalid rate_limit_1d")
	}
	if req.RateLimit7d != nil && !validAPIKeyLimit(*req.RateLimit7d) {
		return errors.New("invalid rate_limit_7d")
	}
	if req.ExpiresInDays != nil && *req.ExpiresInDays <= 0 {
		return errors.New("invalid expires_in_days")
	}
	return nil
}

func validateAPIKeyUpdateRequest(req UpdateAPIKeyRequest) error {
	if req.Quota != nil && !validAPIKeyLimit(*req.Quota) {
		return errors.New("invalid quota")
	}
	if req.RateLimit5h != nil && !validAPIKeyLimit(*req.RateLimit5h) {
		return errors.New("invalid rate_limit_5h")
	}
	if req.RateLimit1d != nil && !validAPIKeyLimit(*req.RateLimit1d) {
		return errors.New("invalid rate_limit_1d")
	}
	if req.RateLimit7d != nil && !validAPIKeyLimit(*req.RateLimit7d) {
		return errors.New("invalid rate_limit_7d")
	}
	return nil
}

// List handles listing user's API keys with pagination
// GET /api/v1/api-keys
func (h *APIKeyHandler) List(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	page, pageSize := response.ParsePagination(c)
	params := pagination.PaginationParams{
		Page:      page,
		PageSize:  pageSize,
		SortBy:    c.DefaultQuery("sort_by", "created_at"),
		SortOrder: c.DefaultQuery("sort_order", "desc"),
	}

	// Parse filter parameters
	var filters service.APIKeyListFilters
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		if len(search) > 100 {
			search = search[:100]
		}
		filters.Search = search
	}
	filters.Status = c.Query("status")
	if groupIDStr := c.Query("group_id"); groupIDStr != "" {
		gid, err := strconv.ParseInt(groupIDStr, 10, 64)
		if err == nil {
			filters.GroupID = &gid
		}
	}

	keys, result, err := h.apiKeyService.List(c.Request.Context(), subject.UserID, params, filters)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// 与充值/兑换门禁同源标注「待整改」，让 /keys 列表能直接标出要处理的 Key。
	h.apiKeyService.AnnotateKeyRotations(keys)

	out := make([]dto.APIKey, 0, len(keys))
	for i := range keys {
		out = append(out, *dto.APIKeyFromService(&keys[i]))
	}
	// has_sealed_secret 只做布尔标注（批量一次查询），密文本体不出现在列表里。
	h.attachSealedSecretPresence(c.Request.Context(), subject.UserID, keys, out)
	response.Paginated(c, out, result.Total, page, pageSize)
}

// GetByID handles getting a single API key
// GET /api/v1/api-keys/:id
func (h *APIKeyHandler) GetByID(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	keyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid key ID")
		return
	}

	key, err := h.apiKeyService.GetByID(c.Request.Context(), keyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// 验证所有权
	if key.UserID != subject.UserID {
		response.NotFound(c, "API key not found")
		return
	}

	h.apiKeyService.AnnotateKeyRotation(key)
	out := dto.APIKeyFromService(key)
	h.annotateSealedSecretPresence(c.Request.Context(), subject.UserID, key.ID, out)
	response.Success(c, out)
}

// Create handles creating a new API key
// POST /api/v1/api-keys
func (h *APIKeyHandler) Create(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	var req CreateAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := validateAPIKeyCreateRequest(req); err != nil {
		response.BadRequest(c, "Invalid request: numeric limits must be finite and non-negative, and expires_in_days must be greater than zero")
		return
	}

	svcReq := service.CreateAPIKeyRequest{
		Name:          req.Name,
		GroupID:       req.GroupID,
		GroupIDs:      req.GroupIDs,
		CustomKey:     req.CustomKey,
		IPWhitelist:   req.IPWhitelist,
		IPBlacklist:   req.IPBlacklist,
		ExpiresInDays: req.ExpiresInDays,
	}
	if req.Quota != nil {
		svcReq.Quota = *req.Quota
	}
	if req.RateLimit5h != nil {
		svcReq.RateLimit5h = *req.RateLimit5h
	}
	if req.RateLimit1d != nil {
		svcReq.RateLimit1d = *req.RateLimit1d
	}
	if req.RateLimit7d != nil {
		svcReq.RateLimit7d = *req.RateLimit7d
	}
	svcReq.ConcurrencyLimit = req.ConcurrencyLimit
	svcReq.AnthropicCacheTTLMode = req.AnthropicCacheTTLMode
	svcReq.DeepSeekToolInjectionEnabled = req.DeepSeekToolInjectionEnabled
	svcReq.AutoBlockDatacenterIPs = req.AutoBlockDatacenterIPs

	c.Header("Cache-Control", "no-store")
	// 幂等指纹不含身份校验材料（密码 / 动态码 / Passkey 票据）：指纹会落库，
	// 带密码的哈希等于给拖库者一个离线爆破目标。校验材料只在本次请求内使用。
	//
	// 但指纹不含校验材料 ≠ 重放可以不校验：命中历史成功回执时（24 小时内同一个
	// Idempotency-Key），中间件会用本请求的 c.Get(ApiKeyVerificationGuardPassedKey)
	// 判断「本次请求是否真的过了校验」——没过就返回 VERIFY_REQUIRED，绝不直接
	// 复用存储响应。见 idempotency_helper.go。
	idempotencyPayload := req
	idempotencyPayload.Verification = nil
	var first *dto.APIKeySecret
	result := runUserIdempotentJSONWithReplayGate(c, "user.api_keys.create", idempotencyPayload, service.DefaultWriteIdempotencyTTL(), func() (bool, error) {
		// 命中历史成功回执时问一句：本次请求真的过过身份校验吗？
		//   - 是（guard 已在本请求内打过标记）：瞬时重放，可以复用回执；
		//   - 否：24 小时内换一个连接重放同一个 Idempotency-Key，必须重新校验，
		//     没带校验材料 → 400 VERIFY_REQUIRED，绝不返回存储响应。
		if c.GetBool(service.ApiKeyVerificationGuardPassedKey) {
			return true, nil
		}
		if _, err := h.keyVerificationGuard(c, subject.UserID, 0, req.Verification); err != nil {
			return false, err
		}
		return true, nil
	}, func(ctx context.Context) (any, error) {
		verifiedMethod, err := h.keyVerificationGuard(c, subject.UserID, 0, req.Verification)
		if err != nil {
			return nil, err
		}
		key, err := h.apiKeyService.Create(ctx, subject.UserID, svcReq)
		if err != nil {
			return nil, err
		}
		h.apiKeyService.AnnotateKeyRotation(key)
		annotateKeyShowVerification(c, verifiedMethod)
		first = dto.APIKeySecretFromService(key)
		return &first.APIKey, nil // Only this masked form goes into the retry journal.
	})
	if result == nil {
		return
	}
	if !result.Replayed && first != nil {
		response.Success(c, first)
		return
	}

	// Older journal entries can have a plaintext key field. Never forward
	// the stored object directly; the masked DTO cannot serialize that field.
	var replay dto.APIKey
	body, err := json.Marshal(result.Data)
	if err != nil || json.Unmarshal(body, &replay) != nil || replay.ID <= 0 || replay.UserID != subject.UserID {
		response.ErrorFrom(c, service.ErrIdempotencyStoreUnavail)
		return
	}
	response.Success(c, &replay)
}

// Update handles updating an API key
// PUT /api/v1/api-keys/:id
func (h *APIKeyHandler) Update(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	keyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid key ID")
		return
	}

	var req UpdateAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := validateAPIKeyUpdateRequest(req); err != nil {
		response.BadRequest(c, "Invalid request: numeric limits must be finite and non-negative")
		return
	}

	svcReq := service.UpdateAPIKeyRequest{
		IPWhitelist:         req.IPWhitelist,
		IPBlacklist:         req.IPBlacklist,
		Quota:               req.Quota,
		ResetQuota:          req.ResetQuota,
		RateLimit5h:         req.RateLimit5h,
		RateLimit1d:         req.RateLimit1d,
		RateLimit7d:         req.RateLimit7d,
		ResetRateLimitUsage: req.ResetRateLimitUsage,

		AnthropicCacheTTLMode:        req.AnthropicCacheTTLMode,
		DeepSeekToolInjectionEnabled: req.DeepSeekToolInjectionEnabled,
		AutoBlockDatacenterIPs:       req.AutoBlockDatacenterIPs,
		ConcurrencyLimit:             req.ConcurrencyLimit,
		RegenerateKey:                req.RegenerateKey,
	}
	if req.Name != "" {
		svcReq.Name = &req.Name
	}
	svcReq.GroupID = req.GroupID
	svcReq.GroupIDs = req.GroupIDs
	if req.Status != "" {
		svcReq.Status = &req.Status
	}
	// Parse expires_at if provided
	if req.ExpiresAt != nil {
		if *req.ExpiresAt == "" {
			// Empty string means clear expiration
			svcReq.ExpiresAt = nil
			svcReq.ClearExpiration = true
		} else {
			t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
			if err != nil {
				response.BadRequest(c, "Invalid expires_at format: "+err.Error())
				return
			}
			svcReq.ExpiresAt = &t
		}
	}

	// 审计：轮换（regenerate_key）会下发新明文，编辑类请求也统一记录动作名。
	// 审计只记 Key 身份与计数，明文 key 永远不写进 audit_logs。
	// 轮换成功后会改成 api_key.rotate（与批量轮换同动作名），这里先标编辑。
	middleware2.SetAuditAction(c, service.AuditActionAPIKeyUpdate)
	// 轮到「下发 / 变更明文」的编辑必须先过三选一身份校验，且校验放在真正
	// 轮换之前：没校验通过就一步都不动，旧 key 也不会被悄悄换掉。
	if keyUpdateEmitsPlaintext(req) {
		verifiedMethod, verr := h.keyVerificationGuard(c, subject.UserID, keyID, req.Verification)
		if verr != nil {
			respondVerificationError(c, verr)
			return
		}
		annotateKeyShowVerification(c, verifiedMethod)
	}
	key, err := h.apiKeyService.Update(c.Request.Context(), keyID, subject.UserID, svcReq)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// 轮换/加白名单成功后，回执必须立刻反映整改结果（needs_rotation=false），
	// 前端才能就地撤掉角标而不用等下一次列表刷新。
	h.apiKeyService.AnnotateKeyRotation(key)
	// 轮换是明文 key 的另一次（也是唯一另一次）回执：旧值立刻失效，
	// 用户必须当场拿到新值。普通编辑只回脱敏视图。
	if req.RegenerateKey {
		// 单条轮换与批量轮换共用一个审计动作名，批量请求额外带 batch=true。
		// 审计只记 Key 身份与计数，明文 key 永远不写进 audit_logs。
		middleware2.SetAuditAction(c, service.AuditActionAPIKeyRotate)
		middleware2.SetAuditExtra(c, map[string]any{
			"batch":          false,
			"rotate":         true,
			"api_key_id":     key.ID,
			"api_key_prefix": key.DisplayPrefix(),
		})
		// 轮换在同一事务里删掉了旧密文，has_sealed_secret 保持 false。
		response.Success(c, dto.APIKeySecretFromService(key))
		return
	}
	out := dto.APIKeyFromService(key)
	h.annotateSealedSecretPresence(c.Request.Context(), subject.UserID, key.ID, out)
	response.Success(c, out)
}

// BatchRotateAPIKeysRequest 是批量轮换的请求体。
type BatchRotateAPIKeysRequest struct {
	IDs []int64 `json:"ids" binding:"required"`
	// Verification 批量轮换会为每把 Key 下发新明文，因此与创建 Key 一样必须
	// 先完成三选一身份校验（登录密码 / TOTP / Passkey）。账号没有任何可用
	// 因子时放行并在审计里记 skipped_no_factor。
	Verification *apiKeyVerificationRequest `json:"verification"`
}

// BatchRotateAPIKeyItem 是批量轮换的单条结果。
//
// 成功时 key 是本次轮换产生的新明文，只在这一次响应里出现（和单条轮换的
// 回执一样）；失败时 status=failed 并带 error_code，不会阻断其他条。
type BatchRotateAPIKeyItem struct {
	ID        int64  `json:"id"`
	Status    string `json:"status"`
	Key       string `json:"key,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

// BatchRotate 批量轮换当前用户自己的 API Key。
// POST /api/v1/keys/batch-rotate
//
// 契约（与前端约定）：
//   - body: {"ids": [1,2,3]}，去重后 1..50 条；
//   - 仅本人、未删除的 Key。任一条不属于本人/不存在 → 整批 404，不轮换任何一条；
//   - 逐条轮换、逐条成败：一条失败不影响其他条；
//   - 每条成功回一次新明文 key（唯一一次），旧 key 立即失效；
//   - 审计动作 api_key.rotate，带 batch=true 与 requested/succeeded/failed 计数。
func (h *APIKeyHandler) BatchRotate(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	var req BatchRotateAPIKeysRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	c.Header("Cache-Control", "no-store")
	// 批量轮换会把每把 Key 的新明文一次性下发，是与「创建」同级的明文输出口，
	// 必须同样要求三选一身份校验：没校验通过就一条都不轮换（审计记 rejected）。
	verifiedMethod, verifyErr := h.keyVerificationGuard(c, subject.UserID, 0, req.Verification)
	if verifyErr != nil {
		respondVerificationError(c, verifyErr)
		return
	}
	results, err := h.apiKeyService.BatchRotate(c.Request.Context(), subject.UserID, req.IDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	out := make([]BatchRotateAPIKeyItem, 0, len(results))
	succeeded := 0
	for _, item := range results {
		entry := BatchRotateAPIKeyItem{ID: item.ID, Status: "failed"}
		if item.Err != nil {
			entry.ErrorCode = infraerrors.Reason(item.Err)
			if entry.ErrorCode == "" {
				entry.ErrorCode = "API_KEY_ROTATE_FAILED"
			}
		} else {
			entry.Status = "ok"
			entry.Key = item.Key
			succeeded++
		}
		out = append(out, entry)
	}
	failed := len(out) - succeeded

	middleware2.SetAuditAction(c, service.AuditActionAPIKeyRotate)
	annotateKeyShowVerification(c, verifiedMethod)
	middleware2.SetAuditExtra(c, map[string]any{
		"batch":           true,
		"rotate":          true,
		"requested_count": len(out),
		"succeeded_count": succeeded,
		"failed_count":    failed,
	})
	response.Success(c, gin.H{
		"results":         out,
		"requested_count": len(out),
		"succeeded_count": succeeded,
		"failed_count":    failed,
	})
}

// Delete handles deleting an API key
// DELETE /api/v1/api-keys/:id
func (h *APIKeyHandler) Delete(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	keyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid key ID")
		return
	}

	err = h.apiKeyService.Delete(c.Request.Context(), keyID, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"message": "API key deleted successfully"})
}

// GetAvailableGroups 获取用户可以绑定的分组列表
// GET /api/v1/groups/available
func (h *APIKeyHandler) GetAvailableGroups(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	groups, err := h.apiKeyService.GetAvailableGroups(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// Display hints (recent rate change, new group). Best effort: a failure
	// inside only withholds hints, it never fails the group list.
	signals := h.apiKeyService.GetGroupSignals(c.Request.Context(), subject.UserID, groups, time.Now())
	out := make([]dto.Group, 0, len(groups))
	for i := range groups {
		item := dto.GroupFromService(&groups[i])
		dto.ApplyGroupSignals(item, signals[groups[i].ID])
		out = append(out, *item)
	}
	response.Success(c, out)
}

// GetUserGroupRates 获取当前用户的专属分组倍率配置
// GET /api/v1/groups/rates
func (h *APIKeyHandler) GetUserGroupRates(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	rates, err := h.apiKeyService.GetUserGroupRates(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, rates)
}
