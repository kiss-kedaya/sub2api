package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrChannelOperationsConflict = infraerrors.Conflict("CHANNEL_OPERATIONS_CONFLICT", "配置已改变，请刷新后重新确认")
var ErrChannelOperationsValidation = infraerrors.BadRequest("CHANNEL_OPERATIONS_VALIDATION", "渠道运维参数无效，或账号不属于该渠道分组")

// Operations classification deliberately does not participate in gateway routing.
type ChannelOperationsQuery struct {
	Search         string
	Page, PageSize int
	UpstreamSearch string
	UpstreamPage   int
	Membership     string
	UpstreamID     int64
}
type ChannelOperationUpstream struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Members int64  `json:"members"`
}
type ChannelOperationAccount struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Platform    string `json:"platform"`
	Status      string `json:"status"`
	Schedulable bool   `json:"schedulable"`
	Concurrency int    `json:"concurrency"`
	UpstreamID  int64  `json:"upstream_id"`
	Eligible    bool   `json:"eligible"`
}
type ChannelOperationsCatalog struct {
	Revision      int64                      `json:"revision"`
	Upstreams     []ChannelOperationUpstream `json:"upstreams"`
	UpstreamTotal int64                      `json:"upstream_total"`
	Accounts      []ChannelOperationAccount  `json:"accounts"`
	Total         int64                      `json:"total"`
}
type ChannelOperationsMutation struct {
	Action             string `json:"action"`
	Revision           *int64 `json:"revision"`
	UpstreamID         int64  `json:"upstream_id"`
	AccountID          int64  `json:"account_id"`
	ExpectedUpstreamID int64  `json:"expected_upstream_id"`
	Name               string `json:"name"`
	Confirm            bool   `json:"confirm"`
}
type ChannelOperationsFilter struct {
	ChannelID, UpstreamID, AccountID, GroupID int64
	From, Until                               time.Time
	Model, ModelScope, Dimension              string
	Page, PageSize                            int
	QualityBefore                             int64
}
type ChannelOperationsSection struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
	// Stable generic errors only: DB errors may contain sensitive details.
	Error     string    `json:"error,omitempty"`
	QueriedAt time.Time `json:"queried_at"`
}
type ChannelOperationsScope struct {
	ChannelID  int64  `json:"channel_id"`
	UpstreamID int64  `json:"upstream_id"`
	AccountID  int64  `json:"account_id"`
	GroupID    int64  `json:"group_id"`
	Model      string `json:"model"`
	ModelScope string `json:"model_scope"`
	Dimension  string `json:"dimension"`
}
type ChannelOperationsSnapshot struct {
	Scope      ChannelOperationsScope   `json:"scope"`
	Usage      ChannelOperationsSection `json:"usage"`
	Quality    ChannelOperationsSection `json:"quality"`
	Errors     ChannelOperationsSection `json:"errors"`
	Capacity   ChannelOperationsSection `json:"capacity"`
	From       time.Time                `json:"from"`
	Until      time.Time                `json:"until"`
	ModelScope string                   `json:"model_scope"`
}
type ChannelOperationsRepository interface {
	ChannelOperationsCatalog(context.Context, int64, ChannelOperationsQuery) (*ChannelOperationsCatalog, error)
	MutateChannelOperations(context.Context, int64, ChannelOperationsMutation) (int64, error)
	ReadChannelOperations(context.Context, ChannelOperationsFilter) *ChannelOperationsSnapshot
}

func (s *ChannelService) operationsRepository() (ChannelOperationsRepository, error) {
	repo, ok := s.repo.(ChannelOperationsRepository)
	if !ok {
		return nil, infraerrors.ServiceUnavailable("CHANNEL_OPERATIONS_UNAVAILABLE", "渠道运维数据源不可用")
	}
	return repo, nil
}
func (s *ChannelService) OperationsCatalog(ctx context.Context, id int64, q ChannelOperationsQuery) (*ChannelOperationsCatalog, error) {
	if id <= 0 || q.Page < 1 || q.Page > 1000000 || q.UpstreamPage < 1 || q.UpstreamPage > 1000000 || q.PageSize < 1 || q.PageSize > 100 || len(q.Search) > 200 || len(q.UpstreamSearch) > 200 || q.UpstreamID < 0 {
		return nil, ErrChannelOperationsValidation
	}
	if q.Membership != "all" && q.Membership != "assigned" && q.Membership != "unassigned" {
		return nil, ErrChannelOperationsValidation
	}
	repo, err := s.operationsRepository()
	if err != nil {
		return nil, err
	}
	return repo.ChannelOperationsCatalog(ctx, id, q)
}
func ValidateChannelOperationsMutation(m *ChannelOperationsMutation) error {
	if m.Revision == nil || *m.Revision < 0 || m.UpstreamID < 0 || m.AccountID < 0 || m.ExpectedUpstreamID < 0 {
		return ErrChannelOperationsValidation
	}
	m.Name = strings.TrimSpace(m.Name)
	switch m.Action {
	case "create_upstream", "update_upstream":
		if m.Name == "" || utf8.RuneCountInString(m.Name) > 100 || strings.IndexFunc(m.Name, unicode.IsControl) >= 0 || (m.Action == "update_upstream" && m.UpstreamID == 0) {
			return ErrChannelOperationsValidation
		}
	case "delete_upstream":
		if m.UpstreamID == 0 || !m.Confirm {
			return ErrChannelOperationsValidation
		}
	case "assign":
		if m.AccountID == 0 || m.UpstreamID == 0 || (m.ExpectedUpstreamID != 0 && !m.Confirm) {
			return ErrChannelOperationsValidation
		}
	case "detach":
		if m.AccountID == 0 || m.ExpectedUpstreamID == 0 || !m.Confirm {
			return ErrChannelOperationsValidation
		}
	default:
		return ErrChannelOperationsValidation
	}
	return nil
}
func (s *ChannelService) MutateOperations(ctx context.Context, id int64, m ChannelOperationsMutation) (int64, error) {
	if id <= 0 {
		return 0, ErrChannelOperationsValidation
	}
	if err := ValidateChannelOperationsMutation(&m); err != nil {
		return 0, err
	}
	repo, err := s.operationsRepository()
	if err != nil {
		return 0, err
	}
	return repo.MutateChannelOperations(ctx, id, m)
}
func ValidateChannelOperationsFilter(f ChannelOperationsFilter) error {
	if f.ChannelID < 0 || f.AccountID < 0 || f.UpstreamID < -1 || f.GroupID < 0 || f.QualityBefore < 0 || f.From.IsZero() || !f.Until.After(f.From) || f.Until.Sub(f.From) > 31*24*time.Hour || f.Until.After(time.Now().Add(time.Minute)) || len(f.Model) > 200 || f.Page < 1 || f.Page > 1000000 || f.PageSize < 1 || f.PageSize > 100 {
		return ErrChannelOperationsValidation
	}
	if f.Dimension != "channel" && f.Dimension != "upstream" && f.Dimension != "account" {
		return ErrChannelOperationsValidation
	}
	if f.ChannelID == 0 && (f.UpstreamID != 0 || (f.Dimension != "channel" && (f.Dimension != "account" || f.AccountID <= 0))) {
		return ErrChannelOperationsValidation
	}
	if f.ModelScope != "public" && f.ModelScope != "recorded" && f.ModelScope != "upstream" {
		return ErrChannelOperationsValidation
	}
	return nil
}
func (s *ChannelService) OperationsSnapshot(ctx context.Context, f ChannelOperationsFilter) (*ChannelOperationsSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if err := ValidateChannelOperationsFilter(f); err != nil {
		return nil, err
	}
	repo, err := s.operationsRepository()
	if err != nil {
		return nil, err
	}
	if f.ChannelID > 0 {
		if _, err = s.repo.GetByID(ctx, f.ChannelID); err != nil {
			return nil, err
		}
	}
	return repo.ReadChannelOperations(ctx, f), nil
}
