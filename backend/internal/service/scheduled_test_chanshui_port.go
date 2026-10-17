package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/chanshui"
)

// Chanshui types are aliased so handlers and repositories keep one stable
// contract while the implementation lives in the standalone chanshui package
// (config validation, HTTP contract, stop-condition policy, redaction).
type ChanshuiConfig = chanshui.Config
type ChanshuiStopCondition = chanshui.StopCondition
type ChanshuiStopRule = chanshui.StopRule
type ChanshuiDecision = chanshui.Decision

// ChanshuiAuditState is the persisted per-plan audit progress.
type ChanshuiAuditState = chanshui.State

const (
	// QualityProviderPelican is a legacy label kept only to read stored history
	// from runs produced before the pelican reviewer was removed.
	QualityProviderPelican  = chanshui.ProviderPelican
	QualityProviderChanshui = chanshui.ProviderChanshui
	DefaultChanshuiBaseURL  = chanshui.DefaultBaseURL
)

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

// ScheduledChanshuiAccountState applies per-plan pause ownership on the account
// row. Multiple failing plans must each pass before the account can resume.
type ScheduledChanshuiAccountState interface {
	ApplyChanshuiQualityState(context.Context, int64, int64, time.Time, bool) (bool, error)
}
