package service

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// Display windows for the group hints shown next to a group's rate on the
// channel status and API key pages. They are read-only presentation signals:
// nothing here changes how a request is priced or routed.
const (
	// GroupRateChangeWindow is how long a base rate change stays visible.
	GroupRateChangeWindow = 48 * time.Hour
	// GroupNewWindow is how long a freshly created group is flagged as new.
	GroupNewWindow = 24 * time.Hour

	// groups.rate_multiplier and group_rate_history store 4 decimals.
	groupRateEpsilon = 5e-5
	groupRateScale   = 1e4
)

// GroupRateChange is one row of group_rate_history: a base rate edit recorded
// by the database trigger in the same transaction as the edit.
type GroupRateChange struct {
	ID        int64
	GroupID   int64
	OldRate   float64
	NewRate   float64
	ChangedAt time.Time
}

// GroupRateHistoryReader is implemented by the group repository. It is an
// optional capability so existing GroupRepository stubs stay unchanged.
type GroupRateHistoryReader interface {
	// ListGroupRateChangesSince returns the recorded rate changes of the given
	// groups with changed_at >= since, ordered by group, time and id.
	ListGroupRateChangesSince(ctx context.Context, groupIDs []int64, since time.Time) ([]GroupRateChange, error)
}

// GroupRateDirection says which way the base rate moved.
type GroupRateDirection string

const (
	GroupRateUp   GroupRateDirection = "up"
	GroupRateDown GroupRateDirection = "down"
)

// GroupRecentRateChange describes the latest base rate edit of a group.
type GroupRecentRateChange struct {
	Direction GroupRateDirection
	// Delta is the absolute size of the change (never negative); Direction
	// carries the sign.
	Delta        float64
	PreviousRate float64
	ChangedAt    time.Time
}

// GroupSignals are the optional display hints of one group for one viewer.
type GroupSignals struct {
	// RecentRateChange is set when the base rate changed inside
	// GroupRateChangeWindow and the viewer is billed that base rate.
	RecentRateChange *GroupRecentRateChange
	// IsNew is true while the group is younger than GroupNewWindow.
	IsNew      bool
	LaunchedAt time.Time
	// LaunchRate is the base rate the group went live with. It is withheld
	// when the viewer pays a personal rate or the history is unavailable.
	LaunchRate *float64
}

// IsZero reports whether there is nothing to show.
func (s GroupSignals) IsZero() bool {
	return s.RecentRateChange == nil && !s.IsNew
}

// GroupSignalOptions describe what the viewer experiences, so a hint never
// claims something the viewer does not pay.
type GroupSignalOptions struct {
	// PersonalRate is true when the viewer has a fixed personal rate for the
	// group (or that could not be determined). Base rate hints are withheld:
	// the personal rate does not follow the base rate.
	PersonalRate bool
	// HistoryUnavailable is true when the rate history could not be read.
	HistoryUnavailable bool
}

func roundGroupRate(v float64) float64 {
	return math.Round(v*groupRateScale) / groupRateScale
}

// ComputeGroupSignals derives the hints of one group. history may contain rows
// of other groups; they are ignored. now is explicit so boundaries are testable.
//
// Rules:
//   - New: now - created_at < 24h. launch_rate is the oldest recorded old_rate
//     when the rate changed since, otherwise the current rate.
//   - Rate change: the latest recorded change is younger than 48h and its
//     new_rate still equals the group's current rate (otherwise the row is
//     stale or racing with a newer edit and nothing is claimed).
func ComputeGroupSignals(group *Group, history []GroupRateChange, opts GroupSignalOptions, now time.Time) GroupSignals {
	var out GroupSignals
	if group == nil {
		return out
	}
	rows := groupRateRows(group.ID, history)

	if !group.CreatedAt.IsZero() && now.Sub(group.CreatedAt) < GroupNewWindow {
		out.IsNew = true
		out.LaunchedAt = group.CreatedAt
		if !opts.PersonalRate && !opts.HistoryUnavailable {
			rate := group.RateMultiplier
			if len(rows) > 0 {
				rate = rows[0].OldRate
			}
			out.LaunchRate = &rate
		}
	}

	if opts.PersonalRate || opts.HistoryUnavailable || len(rows) == 0 {
		return out
	}
	last := rows[len(rows)-1]
	if now.Sub(last.ChangedAt) >= GroupRateChangeWindow {
		return out
	}
	if math.Abs(last.NewRate-group.RateMultiplier) > groupRateEpsilon {
		return out
	}
	delta := roundGroupRate(last.NewRate - last.OldRate)
	if delta == 0 {
		return out
	}
	direction := GroupRateUp
	if delta < 0 {
		direction = GroupRateDown
	}
	out.RecentRateChange = &GroupRecentRateChange{
		Direction:    direction,
		Delta:        math.Abs(delta),
		PreviousRate: last.OldRate,
		ChangedAt:    last.ChangedAt,
	}
	return out
}

// groupRateRows keeps the rows of one group in chronological order.
func groupRateRows(groupID int64, history []GroupRateChange) []GroupRateChange {
	rows := make([]GroupRateChange, 0, len(history))
	for _, row := range history {
		if row.GroupID == groupID {
			rows = append(rows, row)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].ChangedAt.Equal(rows[j].ChangedAt) {
			return rows[i].ChangedAt.Before(rows[j].ChangedAt)
		}
		return rows[i].ID < rows[j].ID
	})
	return rows
}

// GetGroupSignals computes the display hints of the given groups for one
// viewer. It never fails the caller: when the personal rates or the history
// cannot be read, the affected hints are withheld instead (fail closed) and the
// problem is logged. Groups without any hint are absent from the result.
func (s *APIKeyService) GetGroupSignals(ctx context.Context, userID int64, groups []Group, now time.Time) map[int64]GroupSignals {
	if len(groups) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(groups))
	for i := range groups {
		ids = append(ids, groups[i].ID)
	}

	personal, err := s.GetUserGroupRates(ctx, userID)
	personalUnknown := err != nil
	if personalUnknown {
		logger.LegacyPrintf("service.api_key", "[GroupSignals] load personal rates failed: user=%d err=%v", userID, err)
	}

	historyByGroup := make(map[int64][]GroupRateChange, len(groups))
	historyUnavailable := true
	if reader, ok := s.groupRepo.(GroupRateHistoryReader); ok {
		rows, readErr := reader.ListGroupRateChangesSince(ctx, ids, now.Add(-GroupRateChangeWindow))
		if readErr != nil {
			logger.LegacyPrintf("service.api_key", "[GroupSignals] load rate history failed: groups=%d err=%v", len(ids), readErr)
		} else {
			historyUnavailable = false
			for _, row := range rows {
				historyByGroup[row.GroupID] = append(historyByGroup[row.GroupID], row)
			}
		}
	}

	out := make(map[int64]GroupSignals, len(groups))
	for i := range groups {
		group := &groups[i]
		_, hasPersonal := personal[group.ID]
		signals := ComputeGroupSignals(group, historyByGroup[group.ID], GroupSignalOptions{
			PersonalRate:       personalUnknown || hasPersonal,
			HistoryUnavailable: historyUnavailable,
		}, now)
		if !signals.IsZero() {
			out[group.ID] = signals
		}
	}
	return out
}
