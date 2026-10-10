package dto

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// UsageLogFromInflight 把还在跑的请求变成列表行。费用栏放预留金额。
func UsageLogFromInflight(row service.UsageInflightSnapshot, now time.Time) UsageLog {
	duration := int(now.Sub(row.StartedAt).Milliseconds())
	if duration < 0 {
		duration = 0
	}
	requestType := "sync"
	if row.WebSocket {
		requestType = "ws_v2"
	} else if row.Stream {
		requestType = "stream"
	}
	var groupID *int64
	if row.GroupID > 0 {
		id := row.GroupID
		groupID = &id
	}
	log := UsageLog{
		ID:             service.InflightUsageRowID(row.RequestID),
		UserID:         row.UserID,
		APIKeyID:       row.APIKeyID,
		AccountID:      row.AccountID,
		RequestID:      row.RequestID,
		Model:          row.Model,
		GroupID:        groupID,
		InputTokens:    row.InputTokens,
		OutputTokens:   row.OutputTokens,
		ActualCost:     row.ReservedAmount,
		TotalCost:      row.ReservedAmount,
		RateMultiplier: 1,
		Stream:         row.Stream,
		OpenAIWSMode:   row.WebSocket,
		RequestType:    requestType,
		DurationMs:     &duration,
		FirstTokenMs:   row.FirstTokenMs,
		Inflight:       true,
		CreatedAt:      row.StartedAt,
	}
	if row.Email != "" {
		log.User = &User{ID: row.UserID, Email: row.Email}
	}
	if row.APIKeyID > 0 || row.APIKeyName != "" {
		log.APIKey = &APIKey{ID: row.APIKeyID, Name: row.APIKeyName}
	}
	if row.ReasoningEffort != "" {
		effort := row.ReasoningEffort
		log.ReasoningEffort = &effort
	}
	if row.InboundEndpoint != "" {
		endpoint := row.InboundEndpoint
		log.InboundEndpoint = &endpoint
	}
	if row.IPAddress != "" {
		addr := row.IPAddress
		log.IPAddress = &addr
	}
	return log
}

// AdminUsageLogFromInflight 管理员看到同一条在途记录，并带上用户邮箱。
func AdminUsageLogFromInflight(row service.UsageInflightSnapshot, now time.Time) AdminUsageLog {
	log := AdminUsageLog{UsageLog: UsageLogFromInflight(row, now)}
	if row.UpstreamEndpoint != "" {
		endpoint := row.UpstreamEndpoint
		log.UpstreamEndpoint = &endpoint
	}
	return log
}
