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
