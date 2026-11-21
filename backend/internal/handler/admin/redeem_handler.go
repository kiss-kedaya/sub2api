package admin

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// RedeemHandler handles admin redeem code management
type RedeemHandler struct {
	adminService  service.AdminService
	redeemService *service.RedeemService
}

// NewRedeemHandler creates a new admin redeem handler
func NewRedeemHandler(adminService service.AdminService, redeemService *service.RedeemService) *RedeemHandler {
	return &RedeemHandler{
		adminService:  adminService,
		redeemService: redeemService,
	}
}

// GenerateRedeemCodesRequest represents generate redeem codes request
type GenerateRedeemCodesRequest struct {
	Count         int        `json:"count" binding:"required,min=1,max=1000"`
	Type          string     `json:"type" binding:"required,oneof=balance concurrency subscription invitation"`
	Value         float64    `json:"value"`
	GroupID       *int64     `json:"group_id"`      // 订阅类型必填
	ValidityDays  int        `json:"validity_days"` // 订阅类型使用，正数增加/负数退款扣减
	ExpiresAt     *time.Time `json:"expires_at"`
	ExpiresInDays *int       `json:"expires_in_days" binding:"omitempty,min=1,max=3650"`
}

const redeemGenerationStoredResponseLimit = 512 * 1024

// CreateAndRedeemCodeRequest represents creating a fixed code and redeeming it for a target user.
// Type 为 omitempty 而非 required 是为了向后兼容旧版调用方（不传 type 时默认 balance）。
type CreateAndRedeemCodeRequest struct {
	Code          string     `json:"code" binding:"required,min=3,max=128"`
	Type          string     `json:"type" binding:"omitempty,oneof=balance concurrency subscription invitation"` // 不传时默认 balance（向后兼容）
	Value         float64    `json:"value" binding:"required"`
	UserID        int64      `json:"user_id" binding:"required,gt=0"`
	GroupID       *int64     `json:"group_id"`      // subscription 类型必填
	ValidityDays  int        `json:"validity_days"` // subscription 类型：正数增加，负数退款扣减
	Notes         string     `json:"notes"`
	ExpiresAt     *time.Time `json:"expires_at"`
	ExpiresInDays *int       `json:"expires_in_days" binding:"omitempty,min=1,max=3650"`
}

func resolveRedeemCodeExpiresAt(expiresAt *time.Time, expiresInDays *int) (*time.Time, error) {
	if expiresAt != nil && expiresInDays != nil {
		return nil, infraerrors.BadRequest("REDEEM_CODE_EXPIRY_CONFLICT", "expires_at and expires_in_days cannot both be set")
	}

	now := time.Now().UTC()
	if expiresInDays != nil {
		if *expiresInDays <= 0 {
			return nil, infraerrors.BadRequest("REDEEM_CODE_EXPIRES_IN_DAYS_INVALID", "expires_in_days must be greater than zero")
		}
		expires := now.AddDate(0, 0, *expiresInDays)
		return &expires, nil
	}
	if expiresAt == nil {
		return nil, nil
	}

	expires := expiresAt.UTC()
	if !expires.After(now) {
		return nil, infraerrors.BadRequest("REDEEM_CODE_EXPIRES_AT_INVALID", "expires_at must be in the future")
	}
	return &expires, nil
}

// List handles listing all redeem codes with pagination
// GET /api/v1/admin/redeem-codes
func (h *RedeemHandler) List(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	codeType := c.Query("type")
	status := c.Query("status")
	search := c.Query("search")
	sortBy := c.DefaultQuery("sort_by", "id")
	sortOrder := c.DefaultQuery("sort_order", "desc")
	// 标准化和验证 search 参数
	search = strings.TrimSpace(search)
	if len(search) > 100 {
		search = search[:100]
	}

	codes, total, err := h.adminService.ListRedeemCodes(c.Request.Context(), page, pageSize, codeType, status, search, sortBy, sortOrder)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	out := make([]dto.AdminRedeemCode, 0, len(codes))
	for i := range codes {
		out = append(out, *dto.RedeemCodeFromServiceAdmin(&codes[i]))
	}
	response.Paginated(c, out, total, page, pageSize)
}

// GetByID handles getting a redeem code by ID
// GET /api/v1/admin/redeem-codes/:id
func (h *RedeemHandler) GetByID(c *gin.Context) {
	codeID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid redeem code ID")
		return
	}

	code, err := h.adminService.GetRedeemCode(c.Request.Context(), codeID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.RedeemCodeFromServiceAdmin(code))
}

// Generate handles generating new redeem codes
// POST /api/v1/admin/redeem-codes/generate
//
// 明文只在这个响应的「首次执行」里出现一次。幂等重放（同一 Idempotency-Key
// 重试）只回掩码，绝不回放明文——重试日志/网关缓存里出现过的完整卡密等于二次
// 查看。与 API Key 创建（#464）保持同一套语义。
func (h *RedeemHandler) Generate(c *gin.Context) {
	var req GenerateRedeemCodesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	expiresAt, err := resolveRedeemCodeExpiresAt(req.ExpiresAt, req.ExpiresInDays)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	c.Header("Cache-Control", "no-store")

	// first 保存本次真实生成的明文列表；幂等 journal 里只写掩码形态。
	var first []dto.GeneratedRedeemCode
	result, err := executeAdminIdempotentWithResponseOptions(
		c,
		"admin.redeem_codes.generate",
		req,
		service.DefaultWriteIdempotencyTTL(),
		redeemGenerationStoredResponseLimit,
		true,
		func(ctx context.Context) (any, error) {
			codes, execErr := h.adminService.GenerateRedeemCodes(ctx, &service.GenerateRedeemCodesInput{
				Count:        req.Count,
				Type:         req.Type,
				Value:        req.Value,
				GroupID:      req.GroupID,
				ValidityDays: req.ValidityDays,
				ExpiresAt:    expiresAt,
			})
			if execErr != nil {
				return nil, execErr
			}

			first = make([]dto.GeneratedRedeemCode, 0, len(codes))
			masked := make([]dto.AdminRedeemCode, 0, len(codes))
			for i := range codes {
				if secret := dto.GeneratedRedeemCodeFromService(&codes[i]); secret != nil {
					first = append(first, *secret)
				}
				masked = append(masked, *dto.RedeemCodeFromServiceAdmin(&codes[i]))
			}
			// 只有掩码形态进 journal，重放时拿不回明文。
			return masked, nil
		})
	if err != nil {
		if retryAfter := service.RetryAfterSecondsFromError(err); retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		response.ErrorFrom(c, err)
		return
	}

	if result != nil && result.Replayed {
		c.Header("X-Idempotency-Replayed", "true")
		body, marshalErr := json.Marshal(result.Data)
		if marshalErr != nil {
			response.ErrorFrom(c, service.ErrIdempotencyStoreUnavail)
			return
		}
		var replay []dto.AdminRedeemCode
		if unmarshalErr := json.Unmarshal(body, &replay); unmarshalErr != nil {
			response.ErrorFrom(c, service.ErrIdempotencyStoreUnavail)
			return
		}
		response.Success(c, dto.SanitizeAdminRedeemCodes(replay))
		return
	}

	response.Success(c, first)
}

// CreateAndRedeem creates a fixed redeem code and redeems it for a target user in one step.
// POST /api/v1/admin/redeem-codes/create-and-redeem
//
// 卡密由管理员自己提供，所以「只能看一次」的自然含义是：库里不留、后续读接口
// 不回显。幂等重放同样只回掩码（明文是入参，管理员本来就有）。
func (h *RedeemHandler) CreateAndRedeem(c *gin.Context) {
	if h.redeemService == nil {
		response.InternalError(c, "redeem service not configured")
		return
	}

	var req CreateAndRedeemCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	req.Code = service.NormalizeRedeemCode(req.Code)
	// 向后兼容：旧版调用方（如 Sub2ApiPay）不传 type 字段，默认当作 balance 充值处理。
	// 请勿删除此默认值逻辑，否则会导致旧版调用方 400 报错。
	if req.Type == "" {
		req.Type = "balance"
	}

	if req.Type == "subscription" {
		if req.GroupID == nil {
			response.BadRequest(c, "group_id is required for subscription type")
			return
		}
		if req.ValidityDays == 0 {
			response.BadRequest(c, "validity_days must not be zero for subscription type")
			return
		}
	}

	expiresAt, err := resolveRedeemCodeExpiresAt(req.ExpiresAt, req.ExpiresInDays)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	c.Header("Cache-Control", "no-store")

	result, err := executeAdminIdempotentWithResponseOptions(
		c,
		"admin.redeem_codes.create_and_redeem",
		req,
		service.DefaultWriteIdempotencyTTL(),
		0,
		false,
		func(ctx context.Context) (any, error) {
			// 过渡期兼容：历史行可能只有明文列，所以 GetByCode 仍然是
			// 「哈希优先 + 明文回退」，这里不需要区分。
			existing, err := h.redeemService.GetByCode(ctx, req.Code)
			if err == nil {
				return h.resolveCreateAndRedeemExisting(ctx, existing, req.UserID, req.Code)
			}
			if !errors.Is(err, service.ErrRedeemCodeNotFound) {
				return nil, err
			}

			createErr := h.redeemService.CreateCode(ctx, &service.RedeemCode{
				Code:         req.Code,
				Type:         req.Type,
				Value:        req.Value,
				Status:       service.StatusUnused,
				Notes:        req.Notes,
				GroupID:      req.GroupID,
				ValidityDays: req.ValidityDays,
				ExpiresAt:    expiresAt,
			})
			if createErr != nil {
				// Unique code race: if code now exists, use idempotent semantics by used_by.
				existingAfterCreateErr, getErr := h.redeemService.GetByCode(ctx, req.Code)
				if getErr == nil {
					return h.resolveCreateAndRedeemExisting(ctx, existingAfterCreateErr, req.UserID, req.Code)
				}
				return nil, createErr
			}

			redeemed, redeemErr := h.redeemService.RedeemForAdminFulfillment(ctx, req.UserID, req.Code)
			if redeemErr != nil {
				return nil, redeemErr
			}
			return gin.H{"redeem_code": dto.RedeemCodeFromServiceAdmin(redeemed)}, nil
		})
	if err != nil {
		if retryAfter := service.RetryAfterSecondsFromError(err); retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		response.ErrorFrom(c, err)
		return
	}

	if result != nil && result.Replayed {
		c.Header("X-Idempotency-Replayed", "true")
		body, marshalErr := json.Marshal(result.Data)
		if marshalErr != nil {
			response.ErrorFrom(c, service.ErrIdempotencyStoreUnavail)
			return
		}
		var replay struct {
			RedeemCode *dto.AdminRedeemCode `json:"redeem_code"`
		}
		if unmarshalErr := json.Unmarshal(body, &replay); unmarshalErr != nil {
			response.ErrorFrom(c, service.ErrIdempotencyStoreUnavail)
			return
		}
		response.Success(c, gin.H{"redeem_code": dto.SanitizeAdminRedeemCode(replay.RedeemCode)})
		return
	}

	response.Success(c, result.Data)
}

// resolveCreateAndRedeemExisting 处理「卡密已存在」的幂等语义。
//
// plainCode 必须由调用方传入：迁移 328 之后从库里读回来的 existing.Code 是空的，
// 用 existing.Code 去兑换会 404，也会把明文重新带进流程。
func (h *RedeemHandler) resolveCreateAndRedeemExisting(ctx context.Context, existing *service.RedeemCode, userID int64, plainCode string) (any, error) {
	if existing == nil {
		return nil, infraerrors.Conflict("REDEEM_CODE_CONFLICT", "redeem code conflict")
	}

	// If previous run created the code but crashed before redeem, redeem it now.
	if existing.IsExpired() {
		return nil, service.ErrRedeemCodeExpired
	}
	if existing.CanUse() {
		redeemed, err := h.redeemService.RedeemForAdminFulfillment(ctx, userID, plainCode)
		if err == nil {
			return gin.H{"redeem_code": dto.RedeemCodeFromServiceAdmin(redeemed)}, nil
		}
		if !errors.Is(err, service.ErrRedeemCodeUsed) {
			return nil, err
		}
		latest, getErr := h.redeemService.GetByCode(ctx, plainCode)
		if getErr == nil {
			existing = latest
		}
	}

	if existing.UsedBy != nil && *existing.UsedBy == userID {
		return gin.H{"redeem_code": dto.RedeemCodeFromServiceAdmin(existing)}, nil
	}

	return nil, infraerrors.Conflict("REDEEM_CODE_CONFLICT", "redeem code already used by another user")
}

// Delete handles deleting a redeem code
// DELETE /api/v1/admin/redeem-codes/:id
func (h *RedeemHandler) Delete(c *gin.Context) {
	codeID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid redeem code ID")
		return
	}

	err = h.adminService.DeleteRedeemCode(c.Request.Context(), codeID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"message": "Redeem code deleted successfully"})
}

// BatchDelete handles batch deleting redeem codes
// POST /api/v1/admin/redeem-codes/batch-delete
func (h *RedeemHandler) BatchDelete(c *gin.Context) {
	var req struct {
		IDs []int64 `json:"ids" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	deleted, err := h.adminService.BatchDeleteRedeemCodes(c.Request.Context(), req.IDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{
		"deleted": deleted,
		"message": "Redeem codes deleted successfully",
	})
}

// BatchUpdate handles batch updating redeem codes
// POST /api/v1/admin/redeem-codes/batch-update
func (h *RedeemHandler) BatchUpdate(c *gin.Context) {
	if h.redeemService == nil {
		response.InternalError(c, "redeem service not configured")
		return
	}

	var req dto.BatchUpdateRedeemCodesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	result, err := h.redeemService.BatchUpdate(c.Request.Context(), &service.RedeemCodeBatchUpdateInput{
		IDs:    req.IDs,
		Fields: redeemBatchUpdateFieldsFromDTO(req.Fields),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{
		"updated": result.Updated,
		"message": "Redeem codes updated successfully",
	})
}

func redeemBatchUpdateFieldsFromDTO(in dto.BatchUpdateRedeemCodeFields) service.RedeemCodeBatchUpdateFields {
	out := service.RedeemCodeBatchUpdateFields{
		Status: in.Status,
		Notes:  in.Notes,
		Type:   in.Type,
		Value:  in.Value,
	}
	if in.ExpiresAt.Set {
		out.ExpiresAt = service.NullableTimeUpdate{Set: true, Value: in.ExpiresAt.Value}
	}
	if in.GroupID.Set {
		out.GroupID = service.NullableInt64Update{Set: true, Value: in.GroupID.Value}
	}
	return out
}

// Expire handles expiring a redeem code
// POST /api/v1/admin/redeem-codes/:id/expire
func (h *RedeemHandler) Expire(c *gin.Context) {
	codeID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid redeem code ID")
		return
	}

	code, err := h.adminService.ExpireRedeemCode(c.Request.Context(), codeID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.RedeemCodeFromServiceAdmin(code))
}

// GetStats handles getting redeem code statistics
// GET /api/v1/admin/redeem-codes/stats
func (h *RedeemHandler) GetStats(c *gin.Context) {
	// There is no aggregate query behind this route. Do not expose mock zeroes as
	// financial statistics; add a repository aggregate before enabling it.
	response.Error(c, http.StatusNotImplemented, "redeem code statistics are not implemented")
}

// Export handles exporting redeem codes to CSV
// GET /api/v1/admin/redeem-codes/export
//
// 导出不再包含明文卡密：卡密一旦落盘成 CSV，任何一个拿到文件的环节都能直接兑换，
// 「只返回一次」的边界会被导出整体绕过。因此这里只导出掩码 + 前缀 + 元数据，
// 用于对账/审计（谁在什么时候用了哪一类卡、值多少、是否过期）。
// 需要完整卡密的场景只剩「生成回执」这一条路径。
func (h *RedeemHandler) Export(c *gin.Context) {
	codeType := c.Query("type")
	status := c.Query("status")
	search := strings.TrimSpace(c.Query("search"))
	sortBy := c.DefaultQuery("sort_by", "id")
	sortOrder := c.DefaultQuery("sort_order", "desc")
	if len(search) > 100 {
		search = search[:100]
	}

	const exportLimit = 10000
	codes, total, err := h.adminService.ListRedeemCodes(c.Request.Context(), 1, exportLimit, codeType, status, search, sortBy, sortOrder)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if total > exportLimit {
		response.BadRequest(c, fmt.Sprintf("export matches %d redeem codes; narrow the filters to %d or fewer", total, exportLimit))
		return
	}

	// Create CSV buffer
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)

	c.Header("Cache-Control", "no-store")

	// Write header. code_mask = 前 4 位 + ****；code_prefix = 落库的前 8 位。
	// 两列都不是凭据，无法用于兑换。
	if err := writer.Write([]string{"id", "code_mask", "code_prefix", "type", "value", "status", "used_by", "used_by_email", "used_at", "expires_at", "created_at"}); err != nil {
		response.InternalError(c, "Failed to export redeem codes: "+err.Error())
		return
	}

	// Write data rows
	for _, code := range codes {
		usedBy := ""
		if code.UsedBy != nil {
			usedBy = fmt.Sprintf("%d", *code.UsedBy)
		}
		usedByEmail := ""
		if code.User != nil {
			usedByEmail = code.User.Email
		}
		usedAt := ""
		if code.UsedAt != nil {
			usedAt = code.UsedAt.Format("2006-01-02 15:04:05")
		}
		expiresAt := ""
		if code.ExpiresAt != nil {
			expiresAt = code.ExpiresAt.Format("2006-01-02 15:04:05")
		}
		codePrefix := ""
		if !code.SyntheticCode {
			codePrefix = code.DisplayPrefix()
		}
		if err := writer.Write([]string{
			fmt.Sprintf("%d", code.ID),
			code.DisplayCode(),
			codePrefix,
			code.Type,
			fmt.Sprintf("%.2f", code.Value),
			code.Status,
			usedBy,
			usedByEmail,
			usedAt,
			expiresAt,
			code.CreatedAt.Format("2006-01-02 15:04:05"),
		}); err != nil {
			response.InternalError(c, "Failed to export redeem codes: "+err.Error())
			return
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		response.InternalError(c, "Failed to export redeem codes: "+err.Error())
		return
	}

	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", "attachment; filename=redeem_codes.csv")
	c.Data(200, "text/csv", buf.Bytes())
}
