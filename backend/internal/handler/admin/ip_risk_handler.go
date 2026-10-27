package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/iprisk"
	ippkg "github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// IPRiskHandler keeps policy control separate from ordinary site settings.
// All endpoints require the primary administrator, including read-only IP data.
type IPRiskHandler struct {
	manager  *iprisk.Manager
	settings *service.SettingService
	audit    *service.AuditLogService
}

func NewIPRiskHandler(manager *iprisk.Manager, settings *service.SettingService, audit *service.AuditLogService) *IPRiskHandler {
	return &IPRiskHandler{manager: manager, settings: settings, audit: audit}
}

func (h *IPRiskHandler) ipRiskManager(c *gin.Context) *iprisk.Manager {
	if !middleware.IsPrimaryAdmin(c) {
		response.Forbidden(c, "Only the primary administrator may manage IP policies")
		return nil
	}
	if h.manager == nil {
		response.Error(c, 503, "IP risk manager unavailable")
		return nil
	}
	return h.manager
}

func (h *IPRiskHandler) GetIPRisk(c *gin.Context) {
	m := h.ipRiskManager(c)
	if m == nil {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	cfg, err := m.Config(ctx)
	if err != nil {
		response.Error(c, 503, "IP risk configuration unavailable")
		return
	}
	response.Success(c, gin.H{
		"config":                     cfg,
		"health":                     m.Health(ctx),
		"current_ip":                 ippkg.GetTrustedClientIP(c),
		"current_peer_ip":            c.RemoteIP(),
		"trusted_proxies_configured": h.settings != nil && h.settings.IPRiskTrustedProxiesConfigured(),
	})
}

func (h *IPRiskHandler) SaveIPRisk(c *gin.Context) {
	m := h.ipRiskManager(c)
	if m == nil {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 512<<10)
	var cfg iprisk.Config
	if c.ShouldBindJSON(&cfg) != nil {
		response.BadRequest(c, "Invalid IP risk configuration")
		return
	}
	cfg, err := iprisk.NormalizeConfig(cfg)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if err := m.CheckAdministrator(ctx, cfg, ippkg.GetTrustedClientIP(c), c.Request.UserAgent()); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := m.Save(ctx, cfg); err != nil {
		response.Error(c, 503, "Could not save IP risk configuration; check PostgreSQL and Redis")
		return
	}
	middleware.SetAuditAction(c, "admin.ip_risk.config.update")
	middleware.SetAuditExtra(c, map[string]any{"enabled": cfg.Enabled, "blocking_enabled": !cfg.ObserveOnly, "anonymous_api_enabled": cfg.AnonymousAPI.Enabled})
	response.Success(c, cfg)
}

func (h *IPRiskHandler) GetIPRiskEvents(c *gin.Context) {
	m := h.ipRiskManager(c)
	if m == nil {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	category := c.Query("category")
	if category != "" && category != "login" && category != "register" && category != "verification" {
		response.BadRequest(c, "Invalid authentication category")
		return
	}
	ipFilter := strings.TrimSpace(c.Query("ip"))
	if ipFilter != "" {
		if _, err := iprisk.ParseNetwork(ipFilter); err != nil {
			response.BadRequest(c, "Invalid IP filter")
			return
		}
	}
	page, pageSize := response.ParsePagination(c)
	result, err := m.EventsPage(ctx, ipFilter, c.Query("reason"), category, page, pageSize)
	if err != nil {
		response.Error(c, 503, "IP risk events unavailable")
		return
	}
	response.Success(c, result)
}

func (h *IPRiskHandler) ClearIPRiskEvents(c *gin.Context) {
	m := h.ipRiskManager(c)
	if m == nil {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	if err := m.ClearEvents(ctx); err != nil {
		response.Error(c, 503, "Could not clear IP risk events")
		return
	}
	middleware.SetAuditAction(c, "admin.ip_risk.events.clear")
	response.Success(c, gin.H{"cleared": true})
}

func (h *IPRiskHandler) GetIPRiskBans(c *gin.Context) {
	m := h.ipRiskManager(c)
	if m == nil {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	items, err := m.Bans(ctx)
	if err != nil {
		response.Error(c, 503, "IP risk bans unavailable")
		return
	}
	response.Success(c, gin.H{"items": items})
}

func (h *IPRiskHandler) UnbanIPRisk(c *gin.Context) {
	m := h.ipRiskManager(c)
	if m == nil {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var req struct {
		Network string `json:"network"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid network")
		return
	}
	if _, err := iprisk.ParseNetwork(req.Network); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	if err := m.Unban(ctx, req.Network); err != nil {
		response.Error(c, 503, "Could not remove IP risk ban")
		return
	}
	middleware.SetAuditAction(c, "admin.ip_risk.unban")
	response.Success(c, gin.H{"unbanned": true})
}

func (h *IPRiskHandler) CheckIPRisk(c *gin.Context) {
	m := h.ipRiskManager(c)
	if m == nil {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var req struct {
		IP        string `json:"ip"`
		UserAgent string `json:"user_agent"`
		Auth      bool   `json:"auth"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid IP risk check")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	result, err := m.Check(ctx, req.IP, req.UserAgent, req.Auth)
	if err != nil {
		response.BadRequest(c, "Unable to check IP; verify the IP format, configuration and Redis")
		return
	}
	middleware.SetAuditAction(c, "admin.ip_risk.check")
	response.Success(c, result)
}

// GetAnonymousAPIBans is separate from global/login IP bans: these bans apply
// only after credentials are proven invalid and never imply a permanent deny.
func (h *IPRiskHandler) GetAnonymousAPIBans(c *gin.Context) {
	m := h.ipRiskManager(c)
	if m == nil {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	items, err := m.AnonymousBans(ctx)
	if err != nil {
		response.Error(c, 503, "Anonymous API bans unavailable")
		return
	}
	response.Success(c, gin.H{"items": items})
}

func (h *IPRiskHandler) UnbanAnonymousAPI(c *gin.Context) {
	m := h.ipRiskManager(c)
	if m == nil {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var req struct {
		Network string `json:"network"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid network")
		return
	}
	prefix, err := iprisk.ParseNetwork(req.Network)
	if err != nil || prefix.Bits() != prefix.Addr().BitLen() {
		response.BadRequest(c, "An exact IP address is required")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	if err := m.UnbanAnonymous(ctx, prefix.String()); err != nil {
		response.Error(c, 503, "Could not remove anonymous API ban")
		return
	}
	middleware.SetAuditAction(c, "admin.ip_risk.anonymous.unban")
	// Keep only a target digest in audit extras, never raw credentials/body.
	digest := sha256.Sum256([]byte(prefix.String()))
	middleware.SetAuditExtra(c, map[string]any{"filter_hash": hex.EncodeToString(digest[:8])})
	response.Success(c, gin.H{"unbanned": true})
}
