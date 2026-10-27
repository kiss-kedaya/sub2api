package iprisk

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestIPRiskEventsPagination(t *testing.T) {
	m, _, client := testManager(t, nil)
	ctx := context.Background()
	now := time.Now()
	members := make([]redis.Z, 0, 460)
	for i := 0; i < 450; i++ {
		event := Event{ID: fmt.Sprint(i), At: now.Unix(), IP: "198.51.100.1", Network: "198.51.100.1/32", Category: "login", Reason: "login_failures"}
		if i%2 == 1 {
			event.Category = "register"
		}
		raw, err := json.Marshal(event)
		require.NoError(t, err)
		members = append(members, redis.Z{Score: float64(now.UnixMicro() + int64(i)), Member: string(raw)})
	}
	// Neither malformed records nor expired events count towards the total,
	// even when their Redis score is recent.
	old, err := json.Marshal(Event{At: now.Add(-721 * time.Hour).Unix(), IP: "198.51.100.1", Category: "login", Reason: "login_failures"})
	require.NoError(t, err)
	members = append(members, redis.Z{Score: float64(now.UnixMicro()), Member: string(old)}, redis.Z{Score: float64(now.UnixMicro()), Member: "invalid-json"})
	require.NoError(t, client.ZAdd(ctx, redisPrefix+"events", members...).Err())

	first, err := m.EventsPage(ctx, "::ffff:198.51.100.1", "login_failures", "login", 1, 200)
	require.NoError(t, err)
	require.Equal(t, 225, first.Total)
	require.Equal(t, 2, first.Pages)
	require.Len(t, first.Items, 200)
	require.Equal(t, "448", first.Items[0].ID)
	require.Equal(t, "50", first.Items[199].ID)
	last, err := m.EventsPage(ctx, "198.51.100.1/32", "login_failures", "login", 2, 200)
	require.NoError(t, err)
	require.Equal(t, 225, last.Total)
	require.Len(t, last.Items, 25)
	require.Equal(t, "48", last.Items[0].ID)
	require.Equal(t, "0", last.Items[24].ID)
	for _, page := range []int{3, int(^uint(0) >> 1)} {
		result, err := m.EventsPage(ctx, "198.51.100.1", "login_failures", "login", page, 200)
		require.NoError(t, err)
		require.Equal(t, last, result)
	}
	defaults, err := m.EventsPage(ctx, "", "", "", -1, 0)
	require.NoError(t, err)
	require.Equal(t, 1, defaults.Page)
	require.Equal(t, 20, defaults.PageSize)
	require.Equal(t, 450, defaults.Total)
	large, err := m.EventsPage(ctx, "", "", "", 1, 1001)
	require.NoError(t, err)
	require.Equal(t, 1000, large.PageSize)
	require.Len(t, large.Items, 450)
	for _, filters := range [][3]string{{"203.0.113.1", "", ""}, {"", "country_blocked", ""}, {"", "", "verification"}} {
		empty, err := m.EventsPage(ctx, filters[0], filters[1], filters[2], 99, 20)
		require.NoError(t, err)
		require.Equal(t, EventPage{Items: []Event{}, Page: 1, PageSize: 20, Pages: 1}, empty)
	}
	require.NoError(t, m.ClearEvents(ctx))
	empty, err := m.EventsPage(ctx, "", "", "", 2, 20)
	require.NoError(t, err)
	require.Equal(t, EventPage{Items: []Event{}, Page: 1, PageSize: 20, Pages: 1}, empty)
}
