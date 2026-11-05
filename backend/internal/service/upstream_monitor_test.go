package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBuildUpstreamSampleWritesFirstSnapshot(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	writes := buildUpstreamSampleWrites([]UpstreamProbeSnapshot{
		{AccountID: 7, BalanceText: "12.5", Unit: "USD", RateText: "0.8"},
	}, map[int64]UpstreamMonitorState{}, now)

	require.Len(t, writes, 1)
	require.True(t, writes[0].HasBalance)
	require.Equal(t, 12.5, writes[0].Balance)
	require.Equal(t, "USD", writes[0].Unit)
	require.NotNil(t, writes[0].SampledAt)
	require.True(t, writes[0].HasRate)
	require.False(t, writes[0].RateChanged, "the first rate has no previous value to report")
}

func TestBuildUpstreamSampleWritesSkipsUnchangedWithinWindow(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	lastSample := now.Add(-5 * time.Minute)
	balance := 12.5
	rate := 0.8

	writes := buildUpstreamSampleWrites([]UpstreamProbeSnapshot{
		{AccountID: 7, BalanceText: "12.5", Unit: "USD", RateText: "0.8"},
	}, map[int64]UpstreamMonitorState{
		7: {LastBalance: &balance, LastUnit: "USD", LastRate: &rate, LastSampleAt: &lastSample},
	}, now)

	require.Empty(t, writes)
}

func TestBuildUpstreamSampleWritesRefreshesAfterWindow(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	lastSample := now.Add(-31 * time.Minute)
	balance := 12.5
	rate := 0.8

	writes := buildUpstreamSampleWrites([]UpstreamProbeSnapshot{
		{AccountID: 7, BalanceText: "12.5", Unit: "USD", RateText: "0.8"},
	}, map[int64]UpstreamMonitorState{
		7: {LastBalance: &balance, LastUnit: "USD", LastRate: &rate, LastSampleAt: &lastSample},
	}, now)

	require.Len(t, writes, 1)
	require.NotNil(t, writes[0].SampledAt)
	require.False(t, writes[0].RateChanged)
}

func TestBuildUpstreamSampleWritesDetectsBalanceAndCurrencyChanges(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	lastSample := now.Add(-5 * time.Minute)
	balance := 12.5
	rate := 0.8

	changed := buildUpstreamSampleWrites([]UpstreamProbeSnapshot{
		{AccountID: 7, BalanceText: "9.25", Unit: "USD", RateText: "0.8"},
	}, map[int64]UpstreamMonitorState{
		7: {LastBalance: &balance, LastUnit: "USD", LastRate: &rate, LastSampleAt: &lastSample},
	}, now)
	require.Len(t, changed, 1)
	require.NotNil(t, changed[0].SampledAt)

	currency := buildUpstreamSampleWrites([]UpstreamProbeSnapshot{
		{AccountID: 7, BalanceText: "12.5", Unit: "CNY", RateText: "0.8"},
	}, map[int64]UpstreamMonitorState{
		7: {LastBalance: &balance, LastUnit: "USD", LastRate: &rate, LastSampleAt: &lastSample},
	}, now)
	require.Len(t, currency, 1)
	require.NotNil(t, currency[0].SampledAt)
}

func TestBuildUpstreamSampleWritesDetectsRateChange(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	lastSample := now.Add(-5 * time.Minute)
	balance := 12.5
	rate := 0.8

	writes := buildUpstreamSampleWrites([]UpstreamProbeSnapshot{
		{AccountID: 7, BalanceText: "12.5", Unit: "USD", RateText: "0.65"},
	}, map[int64]UpstreamMonitorState{
		7: {LastBalance: &balance, LastUnit: "USD", LastRate: &rate, LastSampleAt: &lastSample},
	}, now)

	require.Len(t, writes, 1)
	require.True(t, writes[0].RateChanged)
	require.NotNil(t, writes[0].PreviousRate)
	require.Equal(t, 0.8, *writes[0].PreviousRate)
	require.Equal(t, 0.65, writes[0].Rate)
	require.Nil(t, writes[0].SampledAt, "only the rate changed")
}

func TestBuildUpstreamSampleWritesIgnoresInvalidNumbers(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	writes := buildUpstreamSampleWrites([]UpstreamProbeSnapshot{
		{AccountID: 1, BalanceText: "n/a", Unit: "USD"},
		{AccountID: 2, RateText: "NaN"},
	}, map[int64]UpstreamMonitorState{}, now)
	require.Empty(t, writes)
}

func TestFinalizeMarksExpiredBalanceStale(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	freshUntil := now.Add(-time.Minute)
	account := UpstreamMonitorAccount{BalanceStatus: UpstreamBalanceStatusOK, BalanceFreshTo: &freshUntil}
	account.finalize(now)
	require.Equal(t, UpstreamBalanceStatusStale, account.BalanceStatus)

	empty := UpstreamMonitorAccount{}
	empty.finalize(now)
	require.Equal(t, UpstreamBalanceStatusUnknown, empty.BalanceStatus)
}

func TestDownsampleUpstreamPointsKeepsEndpoints(t *testing.T) {
	base := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	points := make([]UpstreamMonitorPoint, 0, 1000)
	for i := 0; i < 1000; i++ {
		points = append(points, UpstreamMonitorPoint{Balance: float64(i), SampledAt: base.Add(time.Duration(i) * time.Minute)})
	}
	out := downsampleUpstreamPoints(points, 240)
	require.LessOrEqual(t, len(out), 241)
	require.Equal(t, points[0], out[0])
	require.Equal(t, points[len(points)-1], out[len(out)-1])
}

func TestNormalizeUpstreamMonitorFilter(t *testing.T) {
	filter := normalizeUpstreamMonitorFilter(UpstreamMonitorFilter{BalanceStatus: "bogus", Sort: "drop table", Page: 0, PageSize: 9999})
	require.Empty(t, filter.BalanceStatus)
	require.Equal(t, "name", filter.Sort)
	require.Equal(t, 1, filter.Page)
	require.Equal(t, 200, filter.PageSize)
}

func TestBuildUpstreamSampleWritesSpendOnlyOnBalanceDrop(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	lastSample := now.Add(-10 * time.Minute)
	previous := 10.0

	drop := buildUpstreamSampleWrites([]UpstreamProbeSnapshot{
		{AccountID: 1, BalanceText: "7.25", Unit: "USD"},
	}, map[int64]UpstreamMonitorState{
		1: {LastBalance: &previous, LastUnit: "USD", LastSampleAt: &lastSample},
	}, now)
	require.Len(t, drop, 1)
	require.Equal(t, 2.75, drop[0].Spend, "a balance drop is spend")

	topUp := buildUpstreamSampleWrites([]UpstreamProbeSnapshot{
		{AccountID: 2, BalanceText: "12", Unit: "USD"},
	}, map[int64]UpstreamMonitorState{
		2: {LastBalance: &previous, LastUnit: "USD", LastSampleAt: &lastSample},
	}, now)
	require.Len(t, topUp, 1)
	require.Zero(t, topUp[0].Spend, "a top-up is not spend")

	otherUnit := buildUpstreamSampleWrites([]UpstreamProbeSnapshot{
		{AccountID: 3, BalanceText: "3", Unit: "CNY"},
	}, map[int64]UpstreamMonitorState{
		3: {LastBalance: &previous, LastUnit: "USD", LastSampleAt: &lastSample},
	}, now)
	require.Len(t, otherUnit, 1)
	require.Zero(t, otherUnit[0].Spend, "a currency change cannot be converted into spend")
}

func TestBuildUpstreamAlertsLowBalanceRules(t *testing.T) {
	sampledAt := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	writes := []UpstreamSampleWrite{
		{AccountID: 1, HasBalance: true, Balance: 4.5, Unit: "USD", SampledAt: &sampledAt},
		{AccountID: 2, HasBalance: true, Balance: 0, Unit: "USD", SampledAt: &sampledAt},
		{AccountID: 3, HasBalance: true, Balance: 1, Unit: "USD", SampledAt: &sampledAt},
		{AccountID: 4, HasBalance: true, Balance: 1, Unit: "CNY", SampledAt: &sampledAt},
		{AccountID: 5, HasBalance: true, Balance: 1, Unit: "USD", SampledAt: &sampledAt},
	}
	rules := map[int64]UpstreamAlertRule{
		1: {AccountID: 1, Threshold: 5, Unit: "USD", Enabled: true},
		2: {AccountID: 2, Threshold: 5, Unit: "USD", Enabled: true},
		3: {AccountID: 3, Threshold: 5, Unit: "USD", Enabled: false},
		4: {AccountID: 4, Threshold: 5, Unit: "USD", Enabled: true},
	}

	alerts := buildUpstreamAlerts(writes, rules)
	require.Len(t, alerts, 2)
	require.Equal(t, "balance_low", alerts[0].Kind)
	require.Equal(t, int64(1), alerts[0].AccountID)
	require.Equal(t, "warning", alerts[0].Level)
	require.NotNil(t, alerts[0].Threshold)
	require.Equal(t, "balance_depleted", alerts[1].Kind)
	require.Equal(t, int64(2), alerts[1].AccountID)
	require.Equal(t, "critical", alerts[1].Level)
}

func TestBuildUpstreamAlertsRateJumpThreshold(t *testing.T) {
	sampledAt := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	previous := 0.5
	writes := []UpstreamSampleWrite{
		{AccountID: 1, HasRate: true, Rate: 0.75, PreviousRate: &previous, RateChanged: true, SampledAt: &sampledAt},
	}

	alerts := buildUpstreamAlerts(writes, map[int64]UpstreamAlertRule{
		1: {AccountID: 1, RateMinPct: 10, Enabled: true},
	})
	require.Len(t, alerts, 1)
	require.Equal(t, "rate_jump", alerts[0].Kind)
	require.InDelta(t, 50, UpstreamRateChangePct(previous, 0.75), 0.001)

	strict := buildUpstreamAlerts(writes, map[int64]UpstreamAlertRule{
		1: {AccountID: 1, RateMinPct: 60, Enabled: true},
	})
	require.Empty(t, strict, "a move below the configured percentage stays quiet")
}

func TestUpstreamBeijingDayUsesFixedOffset(t *testing.T) {
	utc := time.Date(2026, 11, 5, 17, 30, 0, 0, time.UTC)
	require.Equal(t, "2026-11-06", UpstreamBeijingDay(utc))
	begin := beginOfUpstreamBeijingDay(utc)
	require.Equal(t, 0, begin.Hour())
	require.Equal(t, 6, begin.Day())
}
