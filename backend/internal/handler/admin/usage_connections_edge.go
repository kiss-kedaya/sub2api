package admin

import (
	"context"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Only a narrow, credential-free schema is accepted from administrator-owned
// edge collectors. No caller-provided user/key identity or arbitrary JSON.
type edgeConnectionEvent struct {
	ID             string    `json:"id"`
	Source         string    `json:"source"`
	Time           time.Time `json:"time"`
	RequestID      string    `json:"request_id"`
	Host           string    `json:"host"`
	Scheme         string    `json:"scheme"`
	Method         string    `json:"method"`
	Path           string    `json:"path"`
	ClientIP       string    `json:"client_ip"`
	HTTPVersion    string    `json:"http_version"`
	TLSVersion     string    `json:"tls_version"`
	Status         int       `json:"status"`
	DurationMS     float64   `json:"duration_ms"`
	Bytes          int64     `json:"bytes"`
	UpstreamStatus string    `json:"upstream_status"`
	NetworkError   string    `json:"network_error"`
}

var connectionStatusPattern = regexp.MustCompile(`^[0-9, :.-]*$`)
var connectionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]*$`)
var connectionHostPattern = regexp.MustCompile(`^[a-zA-Z0-9.\[\]:_-]{1,255}$`)

func edgeConnectionRecord(v edgeConnectionEvent, now time.Time) (service.ConnectionTrace, bool) {
	id, err := uuid.Parse(v.ID)
	if err != nil || (v.Source != "nginx" && v.Source != "caddy") || v.Time.Before(now.Add(-30*24*time.Hour)) || v.Time.After(now.Add(5*time.Minute)) {
		return service.ConnectionTrace{}, false
	}
	handshake := v.NetworkError == "tls_error" && v.Status == 0 && v.Path == "" && v.Method == ""
	if (!connectionHostPattern.MatchString(v.Host) && (!handshake || v.Host != "")) || (v.Scheme != "http" && v.Scheme != "https") || len(v.RequestID) > 128 || len(v.Path) > 4096 || len(v.Method) > 16 || len(v.HTTPVersion) > 16 || len(v.TLSVersion) > 32 || len(v.UpstreamStatus) > 64 {
		return service.ConnectionTrace{}, false
	}
	if !connectionIDPattern.MatchString(v.RequestID) || !connectionIDPattern.MatchString(v.Method) || !connectionIDPattern.MatchString(v.TLSVersion) {
		return service.ConnectionTrace{}, false
	}
	switch v.HTTPVersion {
	case "", "HTTP/1.0", "HTTP/1.1", "HTTP/2.0", "HTTP/3.0", "HTTP/2", "HTTP/3":
	default:
		return service.ConnectionTrace{}, false
	}
	if !connectionStatusPattern.MatchString(v.UpstreamStatus) {
		return service.ConnectionTrace{}, false
	}
	ip, err := netip.ParseAddr(v.ClientIP)
	if err != nil || v.Status < 0 || v.Status > 599 || v.DurationMS < 0 || v.DurationMS > 86400000 || v.Bytes < 0 {
		return service.ConnectionTrace{}, false
	}
	switch v.NetworkError {
	case "", "client_closed", "timeout", "connection_refused", "connection_reset", "tls_error", "upstream_unavailable", "other":
	default:
		return service.ConnectionTrace{}, false
	}
	if !handshake && !strings.HasPrefix(v.Path, "/") {
		return service.ConnectionTrace{}, false
	}
	record := service.ConnectionTrace{TraceID: id.String(), Source: v.Source, StartedAt: v.Time, RequestID: v.RequestID, Host: v.Host, Scheme: v.Scheme, Method: v.Method, Path: service.ConnectionPath(v.Path), ClientIP: ip.Unmap().String(), HTTPVersion: v.HTTPVersion, TLSVersion: v.TLSVersion, UpstreamStatus: v.UpstreamStatus, NetworkError: v.NetworkError}
	if handshake {
		record.Path = ""
	}
	record.Detail = requesttiming.Snapshot{Version: 1, TraceID: record.TraceID, StartedAt: record.StartedAt, TotalMS: v.DurationMS, Status: v.Status, DownstreamBytes: v.Bytes, Events: map[string]float64{}, Spans: []requesttiming.Span{}, Attempts: []requesttiming.Attempt{}}
	return record, true
}

func (h *UsageHandler) ImportEdgeConnections(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256*1024)
	var body struct {
		Events []edgeConnectionEvent `json:"events"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || len(body.Events) < 1 || len(body.Events) > 50 {
		response.BadRequest(c, "Expected 1 to 50 edge events within 256 KiB")
		return
	}
	records := make([]service.ConnectionTrace, 0, len(body.Events))
	now := time.Now()
	for _, event := range body.Events {
		record, ok := edgeConnectionRecord(event, now)
		if !ok {
			response.BadRequest(c, "Invalid edge event")
			return
		}
		records = append(records, record)
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if err := h.usageService.ImportEdgeConnections(ctx, records); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"accepted": len(records)})
}
