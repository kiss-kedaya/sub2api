package service

import (
	"context"
	"time"
)

// Pending state contains no upstream credential or request body. A worker
// restart resumes GET polling; it must not submit a second POST for this audit.
type ChanshuiAuditState struct {
	ID                 string    `json:"id"`
	PollURL            string    `json:"poll_url"`
	ServiceURL         string    `json:"service_url"`
	StartedAt          time.Time `json:"started_at"`
	ExpiresAt          int64     `json:"expires_at"`
	KeyFingerprint     string    `json:"key_fingerprint"`
	Status             string    `json:"status"`
	Skipped            any       `json:"skipped,omitempty"`
	DecisionConfigHash string    `json:"decision_config_hash,omitempty"`
}

type ChanshuiAuditView struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
	ExpiresAt int64     `json:"expires_at"`
}

type ScheduledChanshuiRepository interface {
	GetChanshuiAudit(context.Context, int64) (*ChanshuiAuditState, error)
	SetChanshuiAudit(context.Context, int64, *ChanshuiAuditState) error
	ReserveChanshuiCreate(context.Context) (bool, error)
	DelayChanshuiCreate(context.Context, time.Duration) error
	DeferChanshuiPlan(context.Context, int64, time.Time) error
}
