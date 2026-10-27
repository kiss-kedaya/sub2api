package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
)

// ConnectionTrace is independent of billing: zero-usage HTTP requests must
// remain diagnosable. It contains no bodies, credentials or raw transport errors.
type ConnectionTrace struct {
	ErrorLogID      int64                  `json:"error_log_id"`
	ErrorType       string                 `json:"error_type"`
	ErrorStatus     int                    `json:"error_status"`
	Source          string                 `json:"source"`
	Host            string                 `json:"host"`
	Scheme          string                 `json:"scheme"`
	Method          string                 `json:"method"`
	HTTPVersion     string                 `json:"http_version"`
	TLSVersion      string                 `json:"tls_version"`
	UpstreamStatus  string                 `json:"upstream_status"`
	NetworkError    string                 `json:"network_error"`
	TraceID         string                 `json:"trace_id"`
	RequestID       string                 `json:"request_id"`
	ClientRequestID string                 `json:"client_request_id"`
	UserID          int64                  `json:"user_id"`
	APIKeyID        int64                  `json:"api_key_id"`
	AccountID       int64                  `json:"account_id"`
	GroupID         int64                  `json:"group_id"`
	Model           string                 `json:"model"`
	Path            string                 `json:"path"`
	ClientIP        string                 `json:"client_ip"`
	UserAgent       string                 `json:"user_agent"`
	Stream          bool                   `json:"stream"`
	StartedAt       time.Time              `json:"started_at"`
	UserName        string                 `json:"user_name"`
	UserEmail       string                 `json:"user_email"`
	APIKeyName      string                 `json:"api_key_name"`
	AccountName     string                 `json:"account_name"`
	GroupName       string                 `json:"group_name"`
	State           string                 `json:"state"`
	Legacy          bool                   `json:"legacy"`
	Detail          requesttiming.Snapshot `json:"detail"`
}

type ConnectionTraceFilter struct {
	Host, Source                         string
	Start, End                           time.Time
	UserID, APIKeyID, AccountID, GroupID int64
	Model, RequestID, ClientIP, State    string
	Page, PageSize                       int
}

type ConnectionTraceList struct {
	Items         []ConnectionTrace `json:"items"`
	HasMore       bool              `json:"has_more"`
	Page          int               `json:"page"`
	PageSize      int               `json:"page_size"`
	RetentionDays int               `json:"retention_days"`
}

func (s *UsageService) RecordConnectionTrace(record ConnectionTrace) {
	if repo, ok := s.usageRepo.(interface{ RecordConnectionTrace(ConnectionTrace) }); ok {
		repo.RecordConnectionTrace(record)
	}
}

func (s *UsageService) ListConnectionTraces(ctx context.Context, filter ConnectionTraceFilter) (*ConnectionTraceList, error) {
	if repo, ok := s.usageRepo.(interface {
		ListConnectionTraces(context.Context, ConnectionTraceFilter) (*ConnectionTraceList, error)
	}); ok {
		return repo.ListConnectionTraces(ctx, filter)
	}
	return &ConnectionTraceList{Items: []ConnectionTrace{}, Page: filter.Page, PageSize: filter.PageSize, RetentionDays: 30}, nil
}

// Keep a useful URL path without query/fragment or known capability tokens.
func ConnectionPath(raw string) string {
	path := strings.SplitN(strings.SplitN(raw, "?", 2)[0], "#", 2)[0]
	// Unknown path components can be credential capabilities. Keep route words,
	// not arbitrary IDs, filenames or signed path tokens.
	allowed := map[string]bool{}
	for _, word := range strings.Fields("v1 v1beta openai antigravity backend-api codex api models messages responses compact chat completions embeddings images generations edits variations videos audio transcriptions translations speech live realtime health readyz usage sub2api billing admin connections edge") {
		allowed[word] = true
	}
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part != "" && !allowed[part] {
			parts[i] = "[redacted]"
		}
	}
	path = strings.Join(parts, "/")
	if !strings.HasPrefix(path, "/") || len(path) > 128 {
		return "/[redacted]"
	}
	return path
}

func (s *UsageService) ImportEdgeConnections(ctx context.Context, records []ConnectionTrace) error {
	if repo, ok := s.usageRepo.(interface {
		ImportEdgeConnections(context.Context, []ConnectionTrace) error
	}); ok {
		return repo.ImportEdgeConnections(ctx, records)
	}
	return fmt.Errorf("edge diagnostic storage unavailable")
}

func (s *UsageService) StopConnectionDiagnostics() {
	if repo, ok := s.usageRepo.(interface{ StopConnectionDiagnostics() }); ok {
		repo.StopConnectionDiagnostics()
	}
}
