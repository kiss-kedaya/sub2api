//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type recordedRateChange struct {
	oldRate float64
	newRate float64
}

func readRateHistory(t *testing.T, q sqlQueryer, groupID int64) []recordedRateChange {
	t.Helper()
	rows, err := q.QueryContext(context.Background(),
		"SELECT old_rate, new_rate FROM group_rate_history WHERE group_id = $1 ORDER BY id", groupID)
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()

	var out []recordedRateChange
	for rows.Next() {
		var row recordedRateChange
		require.NoError(t, rows.Scan(&row.oldRate, &row.newRate))
		out = append(out, row)
	}
	require.NoError(t, rows.Err())
	return out
}

func TestGroupRateHistoryTriggerRecordsEveryWriterInTheSameTransaction(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newGroupRepositoryWithSQL(client, tx)

	created := &service.Group{Name: "rate-history-writers", Platform: service.PlatformOpenAI,
		Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 1}
	require.NoError(t, repo.Create(ctx, created))
	other := mustCreateGroup(t, client, &service.Group{Name: "rate-history-writers-other", RateMultiplier: 2})
	require.Empty(t, readRateHistory(t, tx, created.ID), "creating a group is not a rate change")

	// Admin save path: groupRepository.Update writes every column, including the
	// rate, on each save. An unchanged rate must stay silent.
	loaded, err := repo.GetByIDLite(ctx, created.ID)
	require.NoError(t, err)
	require.NoError(t, repo.Update(ctx, loaded))
	require.Empty(t, readRateHistory(t, tx, created.ID), "saving the same rate records nothing")

	loaded.RateMultiplier = 0.5
	require.NoError(t, repo.Update(ctx, loaded))
	require.Equal(t, []recordedRateChange{{1, 0.5}}, readRateHistory(t, tx, created.ID))

	// Single-row ent update.
	_, err = client.Group.UpdateOneID(created.ID).SetRateMultiplier(0.75).Save(ctx)
	require.NoError(t, err)
	require.Equal(t, []recordedRateChange{{1, 0.5}, {0.5, 0.75}}, readRateHistory(t, tx, created.ID))

	// Bulk update touches several groups with one statement.
	affected, err := client.Group.Update().Where(group.IDIn(created.ID, other.ID)).SetRateMultiplier(3).Save(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, affected)
	require.Equal(t, []recordedRateChange{{1, 0.5}, {0.5, 0.75}, {0.75, 3}}, readRateHistory(t, tx, created.ID))
	require.Equal(t, []recordedRateChange{{2, 3}}, readRateHistory(t, tx, other.ID))

	// Plain SQL, as an operator would run it by hand.
	_, err = tx.ExecContext(ctx, "UPDATE groups SET rate_multiplier = $1 WHERE id = $2", 4, created.ID)
	require.NoError(t, err)
	require.Equal(t, []recordedRateChange{{1, 0.5}, {0.5, 0.75}, {0.75, 3}, {3, 4}}, readRateHistory(t, tx, created.ID))

	// Setting the same value again, in any form, adds nothing.
	_, err = tx.ExecContext(ctx, "UPDATE groups SET rate_multiplier = rate_multiplier WHERE id = $1", created.ID)
	require.NoError(t, err)
	_, err = client.Group.UpdateOneID(created.ID).SetRateMultiplier(4).Save(ctx)
	require.NoError(t, err)
	require.Len(t, readRateHistory(t, tx, created.ID), 4)
}

func TestGroupRateHistoryTriggerIgnoresEditsThatDoNotChangeTheStoredRate(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	g := mustCreateGroup(t, client, &service.Group{Name: "rate-history-noise", RateMultiplier: 0.08})

	_, err := client.Group.UpdateOneID(g.ID).SetName("rate-history-noise-renamed").SetDescription("edited").Save(ctx)
	require.NoError(t, err)
	require.Empty(t, readRateHistory(t, tx, g.ID), "unrelated columns are not rate changes")

	// The column keeps four decimals, so a smaller difference is not a change.
	_, err = client.Group.UpdateOneID(g.ID).SetRateMultiplier(0.08004).Save(ctx)
	require.NoError(t, err)
	require.Empty(t, readRateHistory(t, tx, g.ID), "sub-precision difference rounds to the stored value")

	// Peak and per-media multipliers are different knobs and stay out of the history.
	_, err = client.Group.UpdateOneID(g.ID).SetPeakRateMultiplier(2).SetImageRateMultiplier(3).Save(ctx)
	require.NoError(t, err)
	require.Empty(t, readRateHistory(t, tx, g.ID))

	// Soft deleting only sets deleted_at.
	_, err = tx.ExecContext(ctx, "UPDATE groups SET deleted_at = NOW() WHERE id = $1", g.ID)
	require.NoError(t, err)
	require.Empty(t, readRateHistory(t, tx, g.ID))
}

func TestGroupRateHistoryRollsBackWithTheEdit(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	g := mustCreateGroup(t, client, &service.Group{Name: "rate-history-rollback", RateMultiplier: 1})

	_, err := tx.ExecContext(ctx, "SAVEPOINT before_rate_edit")
	require.NoError(t, err)
	_, err = client.Group.UpdateOneID(g.ID).SetRateMultiplier(2).Save(ctx)
	require.NoError(t, err)
	require.Len(t, readRateHistory(t, tx, g.ID), 1, "the row is visible inside the editing transaction")

	_, err = tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT before_rate_edit")
	require.NoError(t, err)
	require.Empty(t, readRateHistory(t, tx, g.ID), "a rolled back edit leaves no history behind")

	var rate float64
	require.NoError(t, scanSingleRow(ctx, tx, "SELECT rate_multiplier FROM groups WHERE id = $1", []any{g.ID}, &rate))
	require.InDelta(t, 1, rate, 1e-9)
}

func TestListGroupRateChangesSinceReadsAnOrderedWindow(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newGroupRepositoryWithSQL(client, tx)
	a := mustCreateGroup(t, client, &service.Group{Name: "rate-history-window-a", RateMultiplier: 1})
	b := mustCreateGroup(t, client, &service.Group{Name: "rate-history-window-b", RateMultiplier: 1})
	c := mustCreateGroup(t, client, &service.Group{Name: "rate-history-window-c", RateMultiplier: 1})

	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	insert := func(groupID int64, oldRate, newRate float64, at time.Time) {
		_, err := tx.ExecContext(ctx,
			"INSERT INTO group_rate_history (group_id, old_rate, new_rate, changed_at) VALUES ($1, $2, $3, $4)",
			groupID, oldRate, newRate, at)
		require.NoError(t, err)
	}
	insert(b.ID, 1, 2, base.Add(3*time.Hour))
	insert(a.ID, 2, 3, base.Add(2*time.Hour))
	insert(a.ID, 1, 2, base.Add(1*time.Hour))
	insert(a.ID, 0.5, 1, base) // before the cutoff
	insert(c.ID, 1, 9, base.Add(5*time.Hour))

	got, err := repo.ListGroupRateChangesSince(ctx, []int64{a.ID, b.ID}, base.Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, got, 3, "group c is not requested and the oldest row of a is before the cutoff")
	require.Equal(t, a.ID, got[0].GroupID)
	require.Equal(t, base.Add(time.Hour).UTC(), got[0].ChangedAt.UTC(), "the cutoff itself is included")
	require.Equal(t, a.ID, got[1].GroupID)
	require.InDelta(t, 2, got[1].OldRate, 1e-9)
	require.InDelta(t, 3, got[1].NewRate, 1e-9)
	require.Equal(t, base.Add(2*time.Hour).UTC(), got[1].ChangedAt.UTC(), "rows of one group come oldest first")
	require.Equal(t, b.ID, got[2].GroupID, "groups are returned in id order")

	none, err := repo.ListGroupRateChangesSince(ctx, []int64{a.ID}, base.Add(24*time.Hour))
	require.NoError(t, err)
	require.Empty(t, none)
}

// The whole chain against the real trigger: an admin edit, the history it
// leaves, and the hints the service derives from it.
func TestGroupSignalsFollowRealRateEdits(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newGroupRepositoryWithSQL(client, tx)
	svc := service.NewAPIKeyService(nil, nil, repo, nil, nil, nil, nil)

	established := mustCreateGroup(t, client, &service.Group{Name: "signals-established", RateMultiplier: 0.06})
	fresh := mustCreateGroup(t, client, &service.Group{Name: "signals-fresh", RateMultiplier: 0.05})
	quiet := mustCreateGroup(t, client, &service.Group{Name: "signals-quiet", RateMultiplier: 1})
	_, err := tx.ExecContext(ctx, "UPDATE groups SET created_at = NOW() - INTERVAL '30 days' WHERE id = ANY($1)",
		pq.Array([]int64{established.ID, quiet.ID}))
	require.NoError(t, err)

	for _, edit := range []struct {
		id   int64
		rate float64
	}{{established.ID, 0.08}, {fresh.ID, 0.07}} {
		g, getErr := repo.GetByIDLite(ctx, edit.id)
		require.NoError(t, getErr)
		g.RateMultiplier = edit.rate
		require.NoError(t, repo.Update(ctx, g))
	}

	groups := make([]service.Group, 0, 3)
	for _, id := range []int64{established.ID, fresh.ID, quiet.ID} {
		g, getErr := repo.GetByIDLite(ctx, id)
		require.NoError(t, getErr)
		groups = append(groups, *g)
	}
	signals := svc.GetGroupSignals(ctx, 1, groups, time.Now())

	require.NotContains(t, signals, quiet.ID, "an old group without edits shows nothing")

	up := signals[established.ID]
	require.NotNil(t, up.RecentRateChange)
	require.Equal(t, service.GroupRateUp, up.RecentRateChange.Direction)
	require.InDelta(t, 0.02, up.RecentRateChange.Delta, 1e-9)
	require.InDelta(t, 0.06, up.RecentRateChange.PreviousRate, 1e-9)
	require.False(t, up.IsNew)

	newAndChanged := signals[fresh.ID]
	require.True(t, newAndChanged.IsNew)
	require.NotNil(t, newAndChanged.LaunchRate)
	require.InDelta(t, 0.05, *newAndChanged.LaunchRate, 1e-9, "launch rate is the rate before the first edit")
	require.NotNil(t, newAndChanged.RecentRateChange)
	require.InDelta(t, 0.05, newAndChanged.RecentRateChange.PreviousRate, 1e-9)
}
