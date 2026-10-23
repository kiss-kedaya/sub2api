package dto

import (
	"fmt"
	"time"
)

// ObserverUsageLog is an explicit output contract. It cannot inherit future
// additions to User, APIKey, Account, Group or the full administrator DTO.
// Identifiers remain useful for anonymous correlation; credentials, personal
// profiles, raw headers, IPs and client/upstream correlation values never leave.
type ObserverUsageLog struct {
	ID                        int64             `json:"id"`
	UserID                    int64             `json:"user_id"`
	APIKeyID                  int64             `json:"api_key_id"`
	AccountID                 int64             `json:"account_id"`
	Model                     string            `json:"model"`
	GroupID                   *int64            `json:"group_id"`
	ServiceTier               *string           `json:"service_tier,omitempty"`
	ReasoningEffort           *string           `json:"reasoning_effort,omitempty"`
	InputTokens               int               `json:"input_tokens"`
	OutputTokens              int               `json:"output_tokens"`
	CacheCreationTokens       int               `json:"cache_creation_tokens"`
	CacheReadTokens           int               `json:"cache_read_tokens"`
	CacheCreation5mTokens     int               `json:"cache_creation_5m_tokens"`
	CacheCreation1hTokens     int               `json:"cache_creation_1h_tokens"`
	InputCost                 float64           `json:"input_cost"`
	OutputCost                float64           `json:"output_cost"`
	CacheCreationCost         float64           `json:"cache_creation_cost"`
	CacheReadCost             float64           `json:"cache_read_cost"`
	TotalCost                 float64           `json:"total_cost"`
	ActualCost                float64           `json:"actual_cost"`
	RateMultiplier            float64           `json:"rate_multiplier"`
	AccountRateMultiplier     *float64          `json:"account_rate_multiplier"`
	AccountStatsCost          *float64          `json:"account_stats_cost,omitempty"`
	LongContextBillingApplied bool              `json:"long_context_billing_applied"`
	BillingType               int8              `json:"billing_type"`
	RequestType               string            `json:"request_type"`
	Stream                    bool              `json:"stream"`
	OpenAIWSMode              bool              `json:"openai_ws_mode"`
	NativeCompactionV2        bool              `json:"native_compaction_v2"`
	DurationMs                *int              `json:"duration_ms"`
	FirstTokenMs              *int              `json:"first_token_ms"`
	ImageCount                int               `json:"image_count"`
	ImageSize                 *string           `json:"image_size"`
	ImageInputSize            *string           `json:"image_input_size"`
	ImageOutputSize           *string           `json:"image_output_size"`
	ImageInputTokens          int               `json:"image_input_tokens"`
	ImageInputCost            float64           `json:"image_input_cost"`
	ImageOutputTokens         int               `json:"image_output_tokens"`
	ImageOutputCost           float64           `json:"image_output_cost"`
	ImageSizeSource           *string           `json:"image_size_source"`
	ImageSizeBreakdown        map[string]int    `json:"image_size_breakdown"`
	MediaType                 *string           `json:"media_type"`
	CacheTTLOverridden        bool              `json:"cache_ttl_overridden"`
	BillingMode               *string           `json:"billing_mode,omitempty"`
	BillingTier               *string           `json:"billing_tier,omitempty"`
	Inflight                  bool              `json:"inflight,omitempty"`
	CreatedAt                 time.Time         `json:"created_at"`
	User                      ObserverUsageUser `json:"user"`
	APIKey                    AccountSummary    `json:"api_key"`
	Account                   AccountSummary    `json:"account"`
	Group                     *AccountSummary   `json:"group,omitempty"`
}

type ObserverUsageUser struct {
	ID       int64  `json:"id"`
	Email    string `json:"email"`
	Username string `json:"username"`
}

func UsageLogForObserver(l AdminUsageLog) ObserverUsageLog {
	out := ObserverUsageLog{
		ID: l.ID, UserID: l.UserID, APIKeyID: l.APIKeyID, AccountID: l.AccountID, Model: l.Model, GroupID: l.GroupID,
		ServiceTier: l.ServiceTier, ReasoningEffort: l.ReasoningEffort,
		InputTokens: l.InputTokens, OutputTokens: l.OutputTokens, CacheCreationTokens: l.CacheCreationTokens, CacheReadTokens: l.CacheReadTokens,
		CacheCreation5mTokens: l.CacheCreation5mTokens, CacheCreation1hTokens: l.CacheCreation1hTokens,
		InputCost: l.InputCost, OutputCost: l.OutputCost, CacheCreationCost: l.CacheCreationCost, CacheReadCost: l.CacheReadCost,
		TotalCost: l.TotalCost, ActualCost: l.ActualCost, RateMultiplier: l.RateMultiplier,
		AccountRateMultiplier: l.AccountRateMultiplier, AccountStatsCost: l.AccountStatsCost, LongContextBillingApplied: l.LongContextBillingApplied,
		BillingType: l.BillingType, RequestType: l.RequestType, Stream: l.Stream, OpenAIWSMode: l.OpenAIWSMode, NativeCompactionV2: l.NativeCompactionV2,
		DurationMs: l.DurationMs, FirstTokenMs: l.FirstTokenMs,
		ImageCount: l.ImageCount, ImageSize: l.ImageSize, ImageInputSize: l.ImageInputSize, ImageOutputSize: l.ImageOutputSize,
		ImageInputTokens: l.ImageInputTokens, ImageInputCost: l.ImageInputCost, ImageOutputTokens: l.ImageOutputTokens, ImageOutputCost: l.ImageOutputCost,
		ImageSizeSource: l.ImageSizeSource, ImageSizeBreakdown: l.ImageSizeBreakdown, MediaType: l.MediaType,
		CacheTTLOverridden: l.CacheTTLOverridden, BillingMode: l.BillingMode, BillingTier: l.BillingTier, Inflight: l.Inflight, CreatedAt: l.CreatedAt,
		User:    ObserverUsageUser{ID: l.UserID, Email: fmt.Sprintf("user#%d", l.UserID), Username: fmt.Sprintf("user#%d", l.UserID)},
		APIKey:  AccountSummary{ID: l.APIKeyID, Name: fmt.Sprintf("key#%d", l.APIKeyID)},
		Account: AccountSummary{ID: l.AccountID, Name: fmt.Sprintf("upstream#%d", l.AccountID)},
	}
	if l.GroupID != nil {
		out.Group = &AccountSummary{ID: *l.GroupID, Name: fmt.Sprintf("group#%d", *l.GroupID)}
	}
	return out
}
