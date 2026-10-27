package admin

import (
	"context"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *UsageHandler) RecordConnectionTrace(record service.ConnectionTrace) {
	h.usageService.RecordConnectionTrace(record)
}

// Connections is administrator-only. It reads diagnostic traces, not billing
// totals, and never triggers a retry or a request to an upstream model.
func (h *UsageHandler) Connections(c *gin.Context) {
	f := service.ConnectionTraceFilter{Page: 1, PageSize: 20, State: strings.TrimSpace(c.Query("state")), Model: strings.TrimSpace(c.Query("model")), RequestID: strings.TrimSpace(c.Query("request_id"))}
	f.Host = strings.TrimSpace(c.Query("host"))
	f.Source = strings.TrimSpace(c.Query("source"))
	if len(f.Host) > 255 {
		response.BadRequest(c, "Host exceeds length limit")
		return
	}
	switch f.Source {
	case "", "all", "application", "edge", "nginx", "caddy":
	default:
		response.BadRequest(c, "Invalid source")
		return
	}
	if f.State == "" {
		f.State = "all"
	}
	switch f.State {
	case "edge_failed", "edge_completed", "all", "abnormal", "completed", "client_disconnected", "downstream_write_failed", "failed", "incomplete", "canceled", "unknown":
	default:
		response.BadRequest(c, "Invalid connection state")
		return
	}
	for _, p := range []struct {
		name   string
		target *int64
	}{{"user_id", &f.UserID}, {"api_key_id", &f.APIKeyID}, {"account_id", &f.AccountID}, {"group_id", &f.GroupID}} {
		if raw := c.Query(p.name); raw != "" {
			v, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || v <= 0 {
				response.BadRequest(c, "Invalid "+p.name)
				return
			}
			*p.target = v
		}
	}
	for _, p := range []struct {
		name   string
		target *int
		max    int
	}{{"page", &f.Page, 500}, {"page_size", &f.PageSize, 50}} {
		if raw := c.Query(p.name); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil || v < 1 || v > p.max {
				response.BadRequest(c, "Invalid "+p.name)
				return
			}
			*p.target = v
		}
	}
	if len(f.Model) > 256 || len(f.RequestID) > 128 {
		response.BadRequest(c, "Filter exceeds length limit")
		return
	}
	if raw := strings.TrimSpace(c.Query("client_ip")); raw != "" {
		ip, err := netip.ParseAddr(raw)
		if err != nil {
			response.BadRequest(c, "Invalid client_ip")
			return
		}
		f.ClientIP = ip.Unmap().String()
	}
	f.End = time.Now()
	f.Start = f.End.Add(-24 * time.Hour)
	for _, p := range []struct {
		name   string
		target *time.Time
	}{{"start_time", &f.Start}, {"end_time", &f.End}} {
		if raw := c.Query(p.name); raw != "" {
			v, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				response.BadRequest(c, "Invalid "+p.name)
				return
			}
			*p.target = v
		}
	}
	if !f.End.After(f.Start) || f.End.Sub(f.Start) > 31*24*time.Hour {
		response.BadRequest(c, "Connection diagnostic range must be positive and no longer than 31 days")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	result, err := h.usageService.ListConnectionTraces(ctx, f)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *UsageHandler) StopOperationalDiagnostics() {
	if h.trafficDelivery != nil {
		h.trafficDelivery.Stop()
	}
	h.usageService.StopConnectionDiagnostics()
}
