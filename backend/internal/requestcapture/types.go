// Package requestcapture records opt-in diagnostic traffic without changing it.
package requestcapture

import (
	"context"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/transportdiag"
)

const (
	MaxTasks           = 16
	MaxSessions        = 256
	BufferLimit  int64 = 64 << 20
	RecordLimit  int64 = 128 << 20
	ChunkSize          = 32 << 10
	PreviewLimit       = 256 << 10
)

var (
	ErrDisabled   = errors.New("request capture is disabled")
	ErrNotFound   = errors.New("capture not found on this instance")
	ErrFinalizing = errors.New("capture task must be stopped and finalized before exporting")
	ErrCapacity   = errors.New("request capture capacity reached")
)

type Config struct {
	Enabled       bool
	QuotaMiB      int64
	RetentionDays int
}

func (c Config) Validate() error {
	if c.QuotaMiB < 1 || c.QuotaMiB > (1<<63-1)/(1<<20) {
		return errors.New("capture quota must be a positive integer MiB within int64 range")
	}
	if c.RetentionDays < 1 || c.RetentionDays > 30 {
		return errors.New("capture retention must be between 1 and 30 days")
	}
	return nil
}

type Task struct {
	ID         string     `json:"id"`
	InstanceID string     `json:"instance_id"`
	TargetType string     `json:"target_type"`
	TargetID   int64      `json:"target_id"`
	TargetName string     `json:"target_name"`
	SaveMedia  bool       `json:"save_media"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	EndedAt    *time.Time `json:"ended_at,omitempty"`
	Status     string     `json:"status"`
	Reason     string     `json:"reason,omitempty"`
	Requests   int64      `json:"requests"`
	Partial    int64      `json:"partial"`
	Skipped    int64      `json:"skipped"`
	Bytes      int64      `json:"bytes"`
}

type CreateTask struct {
	TargetType      string `json:"target_type"`
	TargetID        int64  `json:"target_id"`
	DurationMinutes int    `json:"duration_minutes"`
	SaveMedia       bool   `json:"save_media"`
}

type Meta struct {
	RequestID       string `json:"request_id"`
	ClientRequestID string `json:"client_request_id,omitempty"`
	UserID          int64  `json:"user_id"`
	GroupID         int64  `json:"group_id"`
	RoutedGroupID   int64  `json:"routed_group_id,omitempty"`
	Method          string `json:"method"`
	Path            string `json:"path"`
	Model           string `json:"model,omitempty"`
	Protocol        string `json:"protocol"`
}

type Attempt struct {
	Phase             string              `json:"phase,omitempty"`
	HandshakeMS       *float64            `json:"handshake_ms,omitempty"`
	FirstEventMS      *float64            `json:"first_event_ms,omitempty"`
	Route             transportdiag.Route `json:"route"`
	Number            int                 `json:"number"`
	AccountID         int64               `json:"account_id"`
	StartedAt         time.Time           `json:"started_at"`
	Status            int                 `json:"status,omitempty"`
	UpstreamRequestID string              `json:"upstream_request_id,omitempty"`
	Error             string              `json:"error,omitempty"`
	ErrorStage        string              `json:"error_stage,omitempty"`
	ReadError         string              `json:"read_error,omitempty"`
	ResponseTerminal  string              `json:"response_terminal,omitempty"`
	LocalClose        bool                `json:"local_close,omitempty"`
}

type Part struct {
	URL         string            `json:"url,omitempty"`
	Name        string            `json:"name"`
	Stage       string            `json:"stage"`
	Attempt     int               `json:"attempt"`
	Turn        int               `json:"turn"`
	ContentType string            `json:"content_type"`
	Headers     map[string]string `json:"headers,omitempty"`
	Bytes       int64             `json:"bytes"`
	Omitted     string            `json:"omitted,omitempty"`
}

type Record struct {
	ID         string `json:"id"`
	TaskID     string `json:"task_id"`
	InstanceID string `json:"instance_id"`
	Turn       int    `json:"turn,omitempty"`
	Meta
	CreatedAt     time.Time        `json:"created_at"`
	FinishedAt    *time.Time       `json:"finished_at,omitempty"`
	Status        int              `json:"status"`
	IsError       bool             `json:"is_error"`
	Partial       bool             `json:"partial"`
	Reason        string           `json:"reason,omitempty"`
	Bytes         int64            `json:"bytes"`
	Attempts      []Attempt        `json:"attempts"`
	Parts         []Part           `json:"parts"`
	ClientOutcome string           `json:"client_outcome,omitempty"`
	ErrorCode     string           `json:"error_code,omitempty"`
	Usage         map[string]int64 `json:"usage,omitempty"`
}

// Store accepts metadata only. Payload bytes must never enter this interface.
type Store interface {
	SaveTask(context.Context, *Task) error
	Task(context.Context, string, string) (*Task, error)
	Tasks(context.Context, string, int, int) ([]Task, error)
	SaveRecord(context.Context, *Record) error
	Records(context.Context, string, string, bool, int, int) ([]Record, error)
	Record(context.Context, string, string) (*Record, error)
	DeleteRecord(context.Context, string, string) error
	DeleteTask(context.Context, string, string) error
}
