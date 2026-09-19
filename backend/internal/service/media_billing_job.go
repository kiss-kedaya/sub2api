package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const (
	MediaBillingKindGrokVideo = "grok_video"
	MediaBillingKindImage     = "image_usage"
	MediaBillingCreating      = "creating"
	MediaBillingSubmitted     = "submitted"
	MediaBillingReady         = "ready"
	MediaBillingBilled        = "billed"
	MediaBillingDone          = "done"
	MediaBillingFailed        = "failed"
)

var ErrMediaBillingJobNotFound = errors.New("media billing job not found")

// MediaBillingIntent contains no credentials, request body, or media URLs.
// Both the charge and log are frozen before any result becomes public.
type MediaBillingIntent struct {
	Command       UsageBillingCommand `json:"command"`
	Usage         UsageLog            `json:"usage"`
	QuotaPlatform string              `json:"quota_platform,omitempty"`
	RawAmounts    [5]float64          `json:"raw_amounts"`
}

type MediaBillingJob struct {
	ID             string              `json:"id"`
	Kind           string              `json:"kind"`
	UserID         int64               `json:"user_id"`
	APIKeyID       int64               `json:"api_key_id"`
	AccountID      int64               `json:"account_id"`
	GroupID        int64               `json:"group_id"`
	SubscriptionID *int64              `json:"subscription_id,omitempty"`
	UpstreamTaskID string              `json:"upstream_task_id,omitempty"`
	Status         string              `json:"status"`
	ReservedAmount float64             `json:"reserved_amount"`
	Snapshot       json.RawMessage     `json:"snapshot,omitempty"`
	Intent         *MediaBillingIntent `json:"intent,omitempty"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
	AvailableAt    time.Time           `json:"available_at"`
	Attempts       int                 `json:"attempts"`
}

type MediaBillingJobRepository interface {
	CreateMediaBillingJob(context.Context, *MediaBillingJob) (*MediaBillingJob, error)
	SubmitMediaBillingJob(context.Context, string, string, json.RawMessage) error
	GetMediaBillingJob(context.Context, int64, int64, string) (*MediaBillingJob, error)
	GetMediaBillingJobByID(context.Context, string) (*MediaBillingJob, error)
	PrepareMediaBillingIntent(context.Context, string, *MediaBillingIntent) (*MediaBillingIntent, error)
	ApplyMediaBillingJob(context.Context, string) (*UsageBillingApplyResult, error)
	FailMediaBillingJob(context.Context, string) (*UsageBillingApplyResult, error)
	CompleteMediaBillingJob(context.Context, string) error
	ListMediaBillingJobs(context.Context, int) ([]*MediaBillingJob, error)
	RetryMediaBillingJob(context.Context, string, time.Duration) error
}
