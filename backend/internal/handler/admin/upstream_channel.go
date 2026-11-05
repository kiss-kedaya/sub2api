package admin

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// UpstreamChannelHandler manages the upstream sites we resell from: stored
// credentials, sessions, announcement sync, key CRUD, recharge, redeem and
// subscriptions. Every mutating call is admin-only and audited by the existing
// admin middleware; money-spending calls additionally require confirm=true.
type UpstreamChannelHandler struct {
	svc *service.UpstreamChannelService
}

func NewUpstreamChannelHandler(svc *service.UpstreamChannelService) *UpstreamChannelHandler {
	return &UpstreamChannelHandler{svc: svc}
}

func (h *UpstreamChannelHandler) available(c *gin.Context) bool {
	if h == nil || h.svc == nil {
		response.Error(c, http.StatusServiceUnavailable, "upstream channel service unavailable")
		return false
	}
	return true
}

func upstreamChannelID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid channel id")
		return 0, false
	}
	return id, true
}

func upstreamKeyID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("key_id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid api key id")
		return 0, false
	}
	return id, true
}

type upstreamChannelRequest struct {
	Name              string   `json:"name"`
	SiteURL           string   `json:"site_url"`
	Username          string   `json:"username"`
	Password          string   `json:"password"`
	TurnstileEnabled  *bool    `json:"turnstile_enabled"`
	CaptchaProvider   string   `json:"captcha_provider"`
	CaptchaAPIKey     string   `json:"captcha_api_key"`
	CaptchaEndpoint   string   `json:"captcha_endpoint"`
	ProxyURL          string   `json:"proxy_url"`
	SyncAnnouncements *bool    `json:"sync_announcements"`
	Status            string   `json:"status"`
	Tags              []string `json:"tags"`
	Notes             string   `json:"notes"`
}

func (r upstreamChannelRequest) toInput() service.UpstreamChannelInput {
	return service.UpstreamChannelInput{
		Name: r.Name, SiteURL: r.SiteURL, Username: r.Username, Password: r.Password,
		TurnstileEnabled: r.TurnstileEnabled, CaptchaProvider: r.CaptchaProvider,
		CaptchaAPIKey: r.CaptchaAPIKey, CaptchaEndpoint: r.CaptchaEndpoint,
		ProxyURL: r.ProxyURL, SyncAnnouncements: r.SyncAnnouncements,
		Status: r.Status, Tags: r.Tags, Notes: r.Notes,
	}
}

// List GET /api/v1/admin/upstream-monitor/channels
func (h *UpstreamChannelHandler) List(c *gin.Context) {
	if !h.available(c) {
		return
	}
	page, pageSize := response.ParsePagination(c)
	items, total, err := h.svc.List(c.Request.Context(), service.UpstreamChannelFilter{
		Search: c.Query("search"), Status: c.Query("status"), Page: page, PageSize: pageSize,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	pages := 0
	if pageSize > 0 {
		pages = int((total + int64(pageSize) - 1) / int64(pageSize))
	}
	response.Success(c, response.PaginatedData{Items: items, Total: total, Page: page, PageSize: pageSize, Pages: pages})
}

// Get GET /api/v1/admin/upstream-monitor/channels/:id
func (h *UpstreamChannelHandler) Get(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := upstreamChannelID(c)
	if !ok {
		return
	}
	channel, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, channel)
}

// Create POST /api/v1/admin/upstream-monitor/channels
func (h *UpstreamChannelHandler) Create(c *gin.Context) {
	if !h.available(c) {
		return
	}
	var req upstreamChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request")
		return
	}
	channel, err := h.svc.Create(c.Request.Context(), req.toInput())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, channel)
}

// Update PUT /api/v1/admin/upstream-monitor/channels/:id
func (h *UpstreamChannelHandler) Update(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := upstreamChannelID(c)
	if !ok {
		return
	}
	var req upstreamChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request")
		return
	}
	channel, err := h.svc.Update(c.Request.Context(), id, req.toInput())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, channel)
}

// Delete DELETE /api/v1/admin/upstream-monitor/channels/:id
func (h *UpstreamChannelHandler) Delete(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := upstreamChannelID(c)
	if !ok {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": id})
}

// Login POST /api/v1/admin/upstream-monitor/channels/:id/login
func (h *UpstreamChannelHandler) Login(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := upstreamChannelID(c)
	if !ok {
		return
	}
	channel, err := h.svc.Login(c.Request.Context(), id)
	if err != nil {
		response.Error(c, http.StatusBadGateway, err.Error())
		return
	}
	response.Success(c, channel)
}

// Balance GET /api/v1/admin/upstream-monitor/channels/:id/balance
func (h *UpstreamChannelHandler) Balance(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := upstreamChannelID(c)
	if !ok {
		return
	}
	balance, err := h.svc.Balance(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"channel_id": id, "balance": balance})
}

// SyncAnnouncements POST /api/v1/admin/upstream-monitor/channels/:id/announcements/sync
func (h *UpstreamChannelHandler) SyncAnnouncements(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := upstreamChannelID(c)
	if !ok {
		return
	}
	result, err := h.svc.SyncAnnouncements(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// Announcements GET /api/v1/admin/upstream-monitor/announcements?channel_id=&page=
func (h *UpstreamChannelHandler) Announcements(c *gin.Context) {
	if !h.available(c) {
		return
	}
	page, pageSize := response.ParsePagination(c)
	var channelID int64
	if raw := strings.TrimSpace(c.Query("channel_id")); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			channelID = parsed
		}
	}
	items, total, err := h.svc.ListAnnouncements(c.Request.Context(), channelID, page, pageSize)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	pages := 0
	if pageSize > 0 {
		pages = int((total + int64(pageSize) - 1) / int64(pageSize))
	}
	response.Success(c, response.PaginatedData{Items: items, Total: total, Page: page, PageSize: pageSize, Pages: pages})
}

// APIKeys GET /api/v1/admin/upstream-monitor/channels/:id/api-keys
func (h *UpstreamChannelHandler) APIKeys(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := upstreamChannelID(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	keys, err := h.svc.APIKeys(c.Request.Context(), id, service.UpstreamAPIKeyQuery{
		Page: page, PageSize: pageSize, Search: c.Query("search"),
		Status: c.Query("status"), GroupID: c.Query("group_id"),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// The list never carries a full key value.
	for i := range keys.Items {
		keys.Items[i].Key = ""
	}
	response.Success(c, keys)
}

// APIKeyGroups GET /api/v1/admin/upstream-monitor/channels/:id/api-keys/groups
func (h *UpstreamChannelHandler) APIKeyGroups(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := upstreamChannelID(c)
	if !ok {
		return
	}
	groups, err := h.svc.APIKeyGroups(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": groups})
}

type upstreamAPIKeyCreateRequest struct {
	Name          string   `json:"name"`
	GroupID       *int64   `json:"group_id"`
	CustomKey     string   `json:"custom_key"`
	Quota         *float64 `json:"quota"`
	ExpiresInDays *int     `json:"expires_in_days"`
	RateLimit5h   *float64 `json:"rate_limit_5h"`
	RateLimit1d   *float64 `json:"rate_limit_1d"`
	RateLimit7d   *float64 `json:"rate_limit_7d"`
	IPWhitelist   []string `json:"ip_whitelist"`
	IPBlacklist   []string `json:"ip_blacklist"`
}

// CreateAPIKey POST /api/v1/admin/upstream-monitor/channels/:id/api-keys
func (h *UpstreamChannelHandler) CreateAPIKey(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := upstreamChannelID(c)
	if !ok {
		return
	}
	var req upstreamAPIKeyCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request")
		return
	}
	key, err := h.svc.CreateAPIKey(c.Request.Context(), id, service.UpstreamAPIKeyCreateRequest{
		Name: req.Name, GroupID: req.GroupID, CustomKey: req.CustomKey, Quota: req.Quota,
		ExpiresInDays: req.ExpiresInDays, RateLimit5h: req.RateLimit5h,
		RateLimit1d: req.RateLimit1d, RateLimit7d: req.RateLimit7d,
		IPWhitelist: req.IPWhitelist, IPBlacklist: req.IPBlacklist,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, key)
}

type upstreamAPIKeyUpdateRequest struct {
	Name                *string   `json:"name"`
	GroupID             *int64    `json:"group_id"`
	Status              *string   `json:"status"`
	Quota               *float64  `json:"quota"`
	ExpiresAt           *string   `json:"expires_at"`
	IPWhitelist         *[]string `json:"ip_whitelist"`
	IPBlacklist         *[]string `json:"ip_blacklist"`
	ResetQuota          *bool     `json:"reset_quota"`
	RateLimit5h         *float64  `json:"rate_limit_5h"`
	RateLimit1d         *float64  `json:"rate_limit_1d"`
	RateLimit7d         *float64  `json:"rate_limit_7d"`
	ResetRateLimitUsage *bool     `json:"reset_rate_limit_usage"`
}

// UpdateAPIKey PUT /api/v1/admin/upstream-monitor/channels/:id/api-keys/:key_id
func (h *UpstreamChannelHandler) UpdateAPIKey(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := upstreamChannelID(c)
	if !ok {
		return
	}
	keyID, ok := upstreamKeyID(c)
	if !ok {
		return
	}
	var req upstreamAPIKeyUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request")
		return
	}
	key, err := h.svc.UpdateAPIKey(c.Request.Context(), id, keyID, service.UpstreamAPIKeyUpdateRequest{
		Name: req.Name, GroupID: req.GroupID, Status: req.Status, Quota: req.Quota,
		ExpiresAt: req.ExpiresAt, IPWhitelist: req.IPWhitelist, IPBlacklist: req.IPBlacklist,
		ResetQuota: req.ResetQuota, RateLimit5h: req.RateLimit5h, RateLimit1d: req.RateLimit1d,
		RateLimit7d: req.RateLimit7d, ResetRateLimitUsage: req.ResetRateLimitUsage,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, key)
}

// DeleteAPIKey DELETE /api/v1/admin/upstream-monitor/channels/:id/api-keys/:key_id
func (h *UpstreamChannelHandler) DeleteAPIKey(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := upstreamChannelID(c)
	if !ok {
		return
	}
	keyID, ok := upstreamKeyID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteAPIKey(c.Request.Context(), id, keyID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": keyID})
}

// RevealAPIKey POST /api/v1/admin/upstream-monitor/channels/:id/api-keys/:key_id/reveal
func (h *UpstreamChannelHandler) RevealAPIKey(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := upstreamChannelID(c)
	if !ok {
		return
	}
	keyID, ok := upstreamKeyID(c)
	if !ok {
		return
	}
	value, err := h.svc.RevealAPIKey(c.Request.Context(), id, keyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, gin.H{"key": value})
}

// RechargeInfo GET /api/v1/admin/upstream-monitor/channels/:id/recharge
func (h *UpstreamChannelHandler) RechargeInfo(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := upstreamChannelID(c)
	if !ok {
		return
	}
	info, err := h.svc.RechargeInfo(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, info)
}

type upstreamRechargeRequest struct {
	Amount        float64 `json:"amount"`
	PaymentMethod string  `json:"payment_method"`
	IsMobile      bool    `json:"is_mobile"`
	Confirm       bool    `json:"confirm"`
}

// CreateRecharge POST /api/v1/admin/upstream-monitor/channels/:id/recharge
func (h *UpstreamChannelHandler) CreateRecharge(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := upstreamChannelID(c)
	if !ok {
		return
	}
	var req upstreamRechargeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request")
		return
	}
	launch, err := h.svc.CreateRecharge(c.Request.Context(), id, service.UpstreamRechargeRequest{
		Amount: req.Amount, PaymentMethod: req.PaymentMethod, IsMobile: req.IsMobile,
	}, req.Confirm)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, launch)
}

type upstreamRedeemRequest struct {
	Code    string `json:"code"`
	Confirm bool   `json:"confirm"`
}

// Redeem POST /api/v1/admin/upstream-monitor/channels/:id/redeem
func (h *UpstreamChannelHandler) Redeem(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := upstreamChannelID(c)
	if !ok {
		return
	}
	var req upstreamRedeemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request")
		return
	}
	result, err := h.svc.Redeem(c.Request.Context(), id, req.Code, req.Confirm)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// SubscriptionInfo GET /api/v1/admin/upstream-monitor/channels/:id/subscriptions
func (h *UpstreamChannelHandler) SubscriptionInfo(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := upstreamChannelID(c)
	if !ok {
		return
	}
	info, err := h.svc.SubscriptionInfo(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, info)
}

// SubscriptionUsage GET /api/v1/admin/upstream-monitor/channels/:id/subscriptions/usage
func (h *UpstreamChannelHandler) SubscriptionUsage(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := upstreamChannelID(c)
	if !ok {
		return
	}
	items, err := h.svc.SubscriptionUsage(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": items, "fetched_at": time.Now().UTC()})
}

type upstreamSubscriptionRequest struct {
	PlanID        string `json:"plan_id"`
	PaymentMethod string `json:"payment_method"`
	IsMobile      bool   `json:"is_mobile"`
	Confirm       bool   `json:"confirm"`
}

// CreateSubscription POST /api/v1/admin/upstream-monitor/channels/:id/subscriptions
func (h *UpstreamChannelHandler) CreateSubscription(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := upstreamChannelID(c)
	if !ok {
		return
	}
	var req upstreamSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request")
		return
	}
	launch, err := h.svc.CreateSubscription(c.Request.Context(), id, service.UpstreamSubscriptionRequest{
		PlanID: req.PlanID, PaymentMethod: req.PaymentMethod, IsMobile: req.IsMobile,
	}, req.Confirm)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, launch)
}
