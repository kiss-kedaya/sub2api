package dto

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func marshalGroupJSON(t *testing.T, value any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func TestApplyGroupSignalsZeroLeavesPayloadUnchanged(t *testing.T) {
	group := GroupFromService(&service.Group{ID: 3, Name: "g", RateMultiplier: 0.08})
	ApplyGroupSignals(group, service.GroupSignals{})

	payload := marshalGroupJSON(t, group)
	for _, key := range []string{"recent_rate_change", "is_new", "launched_at", "launch_rate"} {
		require.NotContains(t, payload, key)
	}
}

func TestApplyGroupSignalsRateChange(t *testing.T) {
	changedAt := time.Date(2026, 11, 5, 9, 30, 0, 0, time.FixedZone("UTC+8", 8*3600))
	group := GroupFromService(&service.Group{ID: 3, Name: "g", RateMultiplier: 0.08})
	ApplyGroupSignals(group, service.GroupSignals{RecentRateChange: &service.GroupRecentRateChange{
		Direction:    service.GroupRateUp,
		Delta:        0.02,
		PreviousRate: 0.06,
		ChangedAt:    changedAt,
	}})

	payload := marshalGroupJSON(t, group)
	change, ok := payload["recent_rate_change"].(map[string]any)
	require.True(t, ok, "recent_rate_change must be an object")
	require.Equal(t, "up", change["direction"])
	require.InDelta(t, 0.02, change["delta"], 1e-9)
	require.InDelta(t, 0.06, change["previous_rate"], 1e-9)
	require.Equal(t, "2026-11-05T01:30:00Z", change["changed_at"], "timestamps are sent in UTC")
	require.NotContains(t, payload, "is_new")
	require.NotContains(t, payload, "launch_rate")
}

func TestApplyGroupSignalsNewGroup(t *testing.T) {
	launchedAt := time.Date(2026, 11, 5, 3, 0, 0, 0, time.UTC)
	rate := 0.06
	group := GroupFromService(&service.Group{ID: 3, Name: "g", RateMultiplier: 0.08})
	ApplyGroupSignals(group, service.GroupSignals{IsNew: true, LaunchedAt: launchedAt, LaunchRate: &rate})

	payload := marshalGroupJSON(t, group)
	require.Equal(t, true, payload["is_new"])
	require.Equal(t, "2026-11-05T03:00:00Z", payload["launched_at"])
	require.InDelta(t, 0.06, payload["launch_rate"], 1e-9)
	require.NotContains(t, payload, "recent_rate_change")

	// The mapper copies the rate, so later changes to the source do not leak in.
	rate = 9
	require.InDelta(t, 0.06, *group.LaunchRate, 1e-9)
}

func TestApplyGroupSignalsNewGroupWithoutLaunchRate(t *testing.T) {
	group := GroupFromService(&service.Group{ID: 3, Name: "g"})
	ApplyGroupSignals(group, service.GroupSignals{IsNew: true, LaunchedAt: time.Date(2026, 11, 5, 3, 0, 0, 0, time.UTC)})

	payload := marshalGroupJSON(t, group)
	require.Equal(t, true, payload["is_new"])
	require.Contains(t, payload, "launched_at")
	require.NotContains(t, payload, "launch_rate", "a withheld launch rate must not be sent as 0")
}

func TestApplyGroupSignalsNilTargetIsSafe(t *testing.T) {
	require.NotPanics(t, func() { ApplyGroupSignals(nil, service.GroupSignals{IsNew: true}) })
}

func TestAdminGroupPayloadNeverCarriesSignals(t *testing.T) {
	payload := marshalGroupJSON(t, GroupFromServiceAdmin(&service.Group{ID: 3, Name: "g", RateMultiplier: 0.08}))
	for _, key := range []string{"recent_rate_change", "is_new", "launched_at", "launch_rate"} {
		require.NotContains(t, payload, key)
	}
}
