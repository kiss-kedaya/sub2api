package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// GroupRateAuditRow is one admin group-update audit entry, already narrowed to
// the fields the rate history needs. It never carries request bodies to the
// caller.
type GroupRateAuditRow struct {
	CreatedAt   time.Time
	ActorUserID *int64
	ActorEmail  string
	RequestBody string
}

// groupRateAuditReader is implemented by the audit log repository. Using an
// optional interface keeps the audit log port unchanged for existing stubs.
type groupRateAuditReader interface {
	ListGroupRateUpdateAudits(ctx context.Context, groupID int64, since time.Time, limit int) ([]GroupRateAuditRow, error)
}

// GroupRateConfigChange is a real configuration change reconstructed from the
// admin audit trail: the admin's PUT /admin/groups/:id body contains the full
// group state, so consecutive entries give before/after multipliers.
type GroupRateConfigChange struct {
	GroupID      int64     `json:"group_id"`
	ChangedAt    time.Time `json:"changed_at"`
	PreviousRate *float64  `json:"previous_rate_multiplier,omitempty"`
	CurrentRate  float64   `json:"current_rate_multiplier"`
	Direction    string    `json:"direction,omitempty"`
	ActorUserID  *int64    `json:"actor_user_id,omitempty"`
	ActorEmail   string    `json:"actor_email,omitempty"`
	Source       string    `json:"source"` // always "audit_log"
}

// GroupRateHistory returns configuration-level multiplier changes for one group
// within the window, newest first.
//
// This is the honest counterpart to the user-facing observed-change endpoint:
// timestamps here are the actual audit entry times of the admin edit, and the
// values are the submitted rate_multiplier, not a request observation. The very
// first known value only establishes a baseline and is not reported as a change.
func (s *AuditLogService) GroupRateHistory(ctx context.Context, groupID int64, since time.Time, limit int) ([]GroupRateConfigChange, error) {
	if s == nil || s.repo == nil || groupID <= 0 {
		return []GroupRateConfigChange{}, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	reader, ok := s.repo.(groupRateAuditReader)
	if !ok {
		return []GroupRateConfigChange{}, nil
	}
	rows, err := reader.ListGroupRateUpdateAudits(ctx, groupID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("list group rate audits: %w", err)
	}
	return buildGroupRateConfigChanges(groupID, rows), nil
}

func buildGroupRateConfigChanges(groupID int64, rows []GroupRateAuditRow) []GroupRateConfigChange {
	changes := make([]GroupRateConfigChange, 0, len(rows))
	var previous *float64
	for _, row := range rows {
		current, ok := parseGroupRateFromAuditBody(row.RequestBody)
		if !ok {
			continue
		}
		if previous == nil {
			value := current
			previous = &value
			continue
		}
		if math.Abs(current-*previous) < 1e-9 {
			continue
		}
		direction := "down"
		if current > *previous {
			direction = "up"
		}
		previousValue := *previous
		changes = append(changes, GroupRateConfigChange{
			GroupID:      groupID,
			ChangedAt:    row.CreatedAt.UTC(),
			PreviousRate: &previousValue,
			CurrentRate:  current,
			Direction:    direction,
			ActorUserID:  row.ActorUserID,
			ActorEmail:   row.ActorEmail,
			Source:       "audit_log",
		})
		value := current
		previous = &value
	}
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].ChangedAt.After(changes[j].ChangedAt) })
	return changes
}

func parseGroupRateFromAuditBody(body string) (float64, bool) {
	body = strings.TrimSpace(body)
	if body == "" {
		return 0, false
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return 0, false
	}
	raw, exists := payload["rate_multiplier"]
	if !exists || raw == nil {
		return 0, false
	}
	value, ok := raw.(float64)
	if !ok || math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return 0, false
	}
	return value, true
}
