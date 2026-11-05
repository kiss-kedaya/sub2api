//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var rateSignalNow = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func rateSignalGroup(id int64, rate float64, age time.Duration) *Group {
	return &Group{ID: id, RateMultiplier: rate, CreatedAt: rateSignalNow.Add(-age)}
}

func rateSignalRow(id, groupID int64, oldRate, newRate float64, ago time.Duration) GroupRateChange {
	return GroupRateChange{ID: id, GroupID: groupID, OldRate: oldRate, NewRate: newRate, ChangedAt: rateSignalNow.Add(-ago)}
}

func TestComputeGroupSignalsRateChange(t *testing.T) {
	const old = 30 * 24 * time.Hour

	t.Run("no history and an old group shows nothing", func(t *testing.T) {
		got := ComputeGroupSignals(rateSignalGroup(1, 0.08, old), nil, GroupSignalOptions{}, rateSignalNow)
		require.True(t, got.IsZero())
		require.Nil(t, got.LaunchRate)
	})

	t.Run("rate up reports size, previous rate and time", func(t *testing.T) {
		rows := []GroupRateChange{rateSignalRow(1, 1, 0.06, 0.08, 3*time.Hour)}
		got := ComputeGroupSignals(rateSignalGroup(1, 0.08, old), rows, GroupSignalOptions{}, rateSignalNow)
		require.NotNil(t, got.RecentRateChange)
		require.Equal(t, GroupRateUp, got.RecentRateChange.Direction)
		require.InDelta(t, 0.02, got.RecentRateChange.Delta, 1e-9)
		require.InDelta(t, 0.06, got.RecentRateChange.PreviousRate, 1e-9)
		require.Equal(t, rateSignalNow.Add(-3*time.Hour), got.RecentRateChange.ChangedAt)
		require.False(t, got.IsNew)
	})

	t.Run("rate down reports a positive delta", func(t *testing.T) {
		rows := []GroupRateChange{rateSignalRow(1, 1, 0.08, 0.05, time.Hour)}
		got := ComputeGroupSignals(rateSignalGroup(1, 0.05, old), rows, GroupSignalOptions{}, rateSignalNow)
		require.NotNil(t, got.RecentRateChange)
		require.Equal(t, GroupRateDown, got.RecentRateChange.Direction)
		require.InDelta(t, 0.03, got.RecentRateChange.Delta, 1e-9)
		require.InDelta(t, 0.08, got.RecentRateChange.PreviousRate, 1e-9)
	})

	t.Run("48 hour boundary is exclusive", func(t *testing.T) {
		group := rateSignalGroup(1, 0.08, old)
		at := func(ago time.Duration) GroupSignals {
			return ComputeGroupSignals(group, []GroupRateChange{rateSignalRow(1, 1, 0.06, 0.08, ago)}, GroupSignalOptions{}, rateSignalNow)
		}
		require.NotNil(t, at(GroupRateChangeWindow-time.Second).RecentRateChange)
		require.Nil(t, at(GroupRateChangeWindow).RecentRateChange)
		require.Nil(t, at(GroupRateChangeWindow+time.Second).RecentRateChange)
	})

	t.Run("only the latest change counts and a reversal still reports the last move", func(t *testing.T) {
		rows := []GroupRateChange{
			rateSignalRow(1, 1, 0.08, 0.10, 30*time.Hour),
			rateSignalRow(2, 1, 0.10, 0.08, 2*time.Hour),
		}
		got := ComputeGroupSignals(rateSignalGroup(1, 0.08, old), rows, GroupSignalOptions{}, rateSignalNow)
		require.NotNil(t, got.RecentRateChange)
		require.Equal(t, GroupRateDown, got.RecentRateChange.Direction)
		require.InDelta(t, 0.02, got.RecentRateChange.Delta, 1e-9)
		require.InDelta(t, 0.10, got.RecentRateChange.PreviousRate, 1e-9)
	})

	t.Run("rows are ordered by time then id regardless of input order", func(t *testing.T) {
		rows := []GroupRateChange{
			rateSignalRow(3, 1, 0.09, 0.07, time.Hour),
			rateSignalRow(1, 1, 0.05, 0.08, 5*time.Hour),
			rateSignalRow(2, 1, 0.08, 0.09, 3*time.Hour),
		}
		got := ComputeGroupSignals(rateSignalGroup(1, 0.07, old), rows, GroupSignalOptions{}, rateSignalNow)
		require.NotNil(t, got.RecentRateChange)
		require.InDelta(t, 0.09, got.RecentRateChange.PreviousRate, 1e-9)
	})

	t.Run("same timestamp falls back to id order", func(t *testing.T) {
		rows := []GroupRateChange{
			rateSignalRow(8, 1, 0.09, 0.07, time.Hour),
			rateSignalRow(7, 1, 0.05, 0.09, time.Hour),
		}
		got := ComputeGroupSignals(rateSignalGroup(1, 0.07, old), rows, GroupSignalOptions{}, rateSignalNow)
		require.NotNil(t, got.RecentRateChange)
		require.InDelta(t, 0.09, got.RecentRateChange.PreviousRate, 1e-9)
	})

	t.Run("a stale row that no longer matches the current rate claims nothing", func(t *testing.T) {
		rows := []GroupRateChange{rateSignalRow(1, 1, 0.06, 0.08, time.Hour)}
		got := ComputeGroupSignals(rateSignalGroup(1, 0.09, old), rows, GroupSignalOptions{}, rateSignalNow)
		require.Nil(t, got.RecentRateChange)
	})

	t.Run("float noise on the current rate still matches", func(t *testing.T) {
		rows := []GroupRateChange{rateSignalRow(1, 1, 0.06, 0.08, time.Hour)}
		got := ComputeGroupSignals(rateSignalGroup(1, 0.08+1e-12, old), rows, GroupSignalOptions{}, rateSignalNow)
		require.NotNil(t, got.RecentRateChange)
	})

	t.Run("a change that rounds to zero is not reported", func(t *testing.T) {
		rows := []GroupRateChange{rateSignalRow(1, 1, 0.08004, 0.08, time.Hour)}
		got := ComputeGroupSignals(rateSignalGroup(1, 0.08, old), rows, GroupSignalOptions{}, rateSignalNow)
		require.Nil(t, got.RecentRateChange)
	})

	t.Run("delta is rounded to the stored precision", func(t *testing.T) {
		rows := []GroupRateChange{rateSignalRow(1, 1, 0.07, 0.10, time.Hour)}
		got := ComputeGroupSignals(rateSignalGroup(1, 0.10, old), rows, GroupSignalOptions{}, rateSignalNow)
		require.NotNil(t, got.RecentRateChange)
		require.Equal(t, 0.03, got.RecentRateChange.Delta)
	})

	t.Run("a change stamped slightly in the future still counts", func(t *testing.T) {
		rows := []GroupRateChange{{ID: 1, GroupID: 1, OldRate: 0.06, NewRate: 0.08, ChangedAt: rateSignalNow.Add(2 * time.Minute)}}
		got := ComputeGroupSignals(rateSignalGroup(1, 0.08, old), rows, GroupSignalOptions{}, rateSignalNow)
		require.NotNil(t, got.RecentRateChange)
	})

	t.Run("rows of other groups are ignored", func(t *testing.T) {
		rows := []GroupRateChange{rateSignalRow(1, 2, 0.06, 0.08, time.Hour)}
		got := ComputeGroupSignals(rateSignalGroup(1, 0.08, old), rows, GroupSignalOptions{}, rateSignalNow)
		require.True(t, got.IsZero())
	})

	t.Run("a nil group yields nothing", func(t *testing.T) {
		require.True(t, ComputeGroupSignals(nil, nil, GroupSignalOptions{}, rateSignalNow).IsZero())
	})
}

func TestComputeGroupSignalsNewGroup(t *testing.T) {
	t.Run("24 hour boundary is exclusive", func(t *testing.T) {
		at := func(age time.Duration) GroupSignals {
			return ComputeGroupSignals(rateSignalGroup(1, 0.08, age), nil, GroupSignalOptions{}, rateSignalNow)
		}
		require.True(t, at(GroupNewWindow-time.Second).IsNew)
		require.False(t, at(GroupNewWindow).IsNew)
		require.False(t, at(GroupNewWindow+time.Second).IsNew)
		require.True(t, at(0).IsNew)
	})

	t.Run("a group without a creation time is never new", func(t *testing.T) {
		got := ComputeGroupSignals(&Group{ID: 1, RateMultiplier: 0.08}, nil, GroupSignalOptions{}, rateSignalNow)
		require.False(t, got.IsNew)
	})

	t.Run("launch rate is the current rate when it never changed", func(t *testing.T) {
		group := rateSignalGroup(1, 0.08, 5*time.Hour)
		got := ComputeGroupSignals(group, nil, GroupSignalOptions{}, rateSignalNow)
		require.True(t, got.IsNew)
		require.Equal(t, group.CreatedAt, got.LaunchedAt)
		require.NotNil(t, got.LaunchRate)
		require.InDelta(t, 0.08, *got.LaunchRate, 1e-9)
		require.Nil(t, got.RecentRateChange)
	})

	t.Run("launch rate is the oldest recorded old rate once it changed", func(t *testing.T) {
		rows := []GroupRateChange{
			rateSignalRow(2, 1, 0.08, 0.10, time.Hour),
			rateSignalRow(1, 1, 0.06, 0.08, 4*time.Hour),
		}
		got := ComputeGroupSignals(rateSignalGroup(1, 0.10, 5*time.Hour), rows, GroupSignalOptions{}, rateSignalNow)
		require.True(t, got.IsNew)
		require.NotNil(t, got.LaunchRate)
		require.InDelta(t, 0.06, *got.LaunchRate, 1e-9)
		require.NotNil(t, got.RecentRateChange, "a new group can also carry a rate change")
		require.Equal(t, GroupRateUp, got.RecentRateChange.Direction)
		require.InDelta(t, 0.08, got.RecentRateChange.PreviousRate, 1e-9)
	})

	t.Run("a personal rate keeps the new marker but withholds every base rate", func(t *testing.T) {
		rows := []GroupRateChange{rateSignalRow(1, 1, 0.06, 0.08, time.Hour)}
		group := rateSignalGroup(1, 0.08, 5*time.Hour)
		got := ComputeGroupSignals(group, rows, GroupSignalOptions{PersonalRate: true}, rateSignalNow)
		require.True(t, got.IsNew)
		require.Equal(t, group.CreatedAt, got.LaunchedAt)
		require.Nil(t, got.LaunchRate)
		require.Nil(t, got.RecentRateChange)
	})

	t.Run("unavailable history keeps the new marker but withholds rate hints", func(t *testing.T) {
		rows := []GroupRateChange{rateSignalRow(1, 1, 0.06, 0.08, time.Hour)}
		got := ComputeGroupSignals(rateSignalGroup(1, 0.08, 5*time.Hour), rows, GroupSignalOptions{HistoryUnavailable: true}, rateSignalNow)
		require.True(t, got.IsNew)
		require.Nil(t, got.LaunchRate)
		require.Nil(t, got.RecentRateChange)
	})

	t.Run("an old group with unavailable history shows nothing", func(t *testing.T) {
		rows := []GroupRateChange{rateSignalRow(1, 1, 0.06, 0.08, time.Hour)}
		got := ComputeGroupSignals(rateSignalGroup(1, 0.08, 30*24*time.Hour), rows, GroupSignalOptions{HistoryUnavailable: true}, rateSignalNow)
		require.True(t, got.IsZero())
	})
}

func TestGroupSignalsIsZero(t *testing.T) {
	require.True(t, GroupSignals{}.IsZero())
	require.False(t, GroupSignals{IsNew: true}.IsZero())
	require.False(t, GroupSignals{RecentRateChange: &GroupRecentRateChange{}}.IsZero())
}

type rateSignalGroupRepo struct {
	GroupRepository
	rows     []GroupRateChange
	err      error
	calls    int
	gotIDs   []int64
	gotSince time.Time
}

func (r *rateSignalGroupRepo) ListGroupRateChangesSince(_ context.Context, ids []int64, since time.Time) ([]GroupRateChange, error) {
	r.calls++
	r.gotIDs = append([]int64(nil), ids...)
	r.gotSince = since
	return r.rows, r.err
}

// rateSignalPlainGroupRepo does not implement GroupRateHistoryReader.
type rateSignalPlainGroupRepo struct{ GroupRepository }

type rateSignalUserRates struct {
	UserGroupRateRepository
	rates map[int64]float64
	err   error
}

func (r *rateSignalUserRates) GetByUserID(context.Context, int64) (map[int64]float64, error) {
	return r.rates, r.err
}

func TestGetGroupSignals(t *testing.T) {
	const old = 30 * 24 * time.Hour
	groups := []Group{
		*rateSignalGroup(1, 0.08, old),
		*rateSignalGroup(2, 0.10, old),
		*rateSignalGroup(3, 0.05, 2*time.Hour),
		*rateSignalGroup(4, 1, old),
	}
	rows := []GroupRateChange{
		rateSignalRow(1, 1, 0.06, 0.08, time.Hour),
		rateSignalRow(2, 2, 0.12, 0.10, 2*time.Hour),
	}

	t.Run("no groups skips every lookup", func(t *testing.T) {
		repo := &rateSignalGroupRepo{}
		svc := &APIKeyService{groupRepo: repo}
		require.Empty(t, svc.GetGroupSignals(context.Background(), 7, nil, rateSignalNow))
		require.Zero(t, repo.calls)
	})

	t.Run("queries the 48 hour window for all listed groups and keeps only groups with a hint", func(t *testing.T) {
		repo := &rateSignalGroupRepo{rows: rows}
		svc := &APIKeyService{groupRepo: repo}
		got := svc.GetGroupSignals(context.Background(), 7, groups, rateSignalNow)

		require.Equal(t, 1, repo.calls)
		require.Equal(t, []int64{1, 2, 3, 4}, repo.gotIDs)
		require.Equal(t, rateSignalNow.Add(-GroupRateChangeWindow), repo.gotSince)

		require.Len(t, got, 3)
		require.Equal(t, GroupRateUp, got[1].RecentRateChange.Direction)
		require.Equal(t, GroupRateDown, got[2].RecentRateChange.Direction)
		require.True(t, got[3].IsNew)
		require.NotContains(t, got, int64(4))
	})

	t.Run("a personal rate hides the base rate change of that group only", func(t *testing.T) {
		repo := &rateSignalGroupRepo{rows: rows}
		svc := &APIKeyService{
			groupRepo:         repo,
			userGroupRateRepo: &rateSignalUserRates{rates: map[int64]float64{1: 0.05}},
		}
		got := svc.GetGroupSignals(context.Background(), 7, groups, rateSignalNow)
		require.NotContains(t, got, int64(1))
		require.NotNil(t, got[2].RecentRateChange)
	})

	t.Run("a personal rate on a new group keeps the marker without a launch rate", func(t *testing.T) {
		svc := &APIKeyService{
			groupRepo:         &rateSignalGroupRepo{},
			userGroupRateRepo: &rateSignalUserRates{rates: map[int64]float64{3: 0.02}},
		}
		got := svc.GetGroupSignals(context.Background(), 7, groups, rateSignalNow)
		require.True(t, got[3].IsNew)
		require.Nil(t, got[3].LaunchRate)
	})

	t.Run("failing to read personal rates fails closed on base rate hints", func(t *testing.T) {
		svc := &APIKeyService{
			groupRepo:         &rateSignalGroupRepo{rows: rows},
			userGroupRateRepo: &rateSignalUserRates{err: errors.New("rates unavailable")},
		}
		got := svc.GetGroupSignals(context.Background(), 7, groups, rateSignalNow)
		require.NotContains(t, got, int64(1))
		require.NotContains(t, got, int64(2))
		require.True(t, got[3].IsNew)
		require.Nil(t, got[3].LaunchRate)
	})

	t.Run("failing to read history withholds rate hints but keeps the new marker", func(t *testing.T) {
		svc := &APIKeyService{groupRepo: &rateSignalGroupRepo{err: errors.New("history unavailable")}}
		got := svc.GetGroupSignals(context.Background(), 7, groups, rateSignalNow)
		require.Len(t, got, 1)
		require.True(t, got[3].IsNew)
		require.Nil(t, got[3].LaunchRate)
	})

	t.Run("a repository without history support behaves like unavailable history", func(t *testing.T) {
		svc := &APIKeyService{groupRepo: &rateSignalPlainGroupRepo{}}
		got := svc.GetGroupSignals(context.Background(), 7, groups, rateSignalNow)
		require.Len(t, got, 1)
		require.True(t, got[3].IsNew)
		require.Nil(t, got[3].LaunchRate)
	})

	t.Run("a missing personal rate repository means no overrides", func(t *testing.T) {
		svc := &APIKeyService{groupRepo: &rateSignalGroupRepo{rows: rows}}
		got := svc.GetGroupSignals(context.Background(), 7, groups, rateSignalNow)
		require.NotNil(t, got[1].RecentRateChange)
	})
}
