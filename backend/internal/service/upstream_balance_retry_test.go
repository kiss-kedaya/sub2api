package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fastUpstreamBalanceRetryBackoff shrinks the production 1s/3s/9s back-off so
// the retry tests finish in milliseconds without changing the real cadence.
func fastUpstreamBalanceRetryBackoff(t *testing.T) {
	t.Helper()
	base, max := upstreamBalanceRetryBaseDelay, upstreamBalanceRetryMaxDelay
	upstreamBalanceRetryBaseDelay, upstreamBalanceRetryMaxDelay = time.Millisecond, 3*time.Millisecond
	t.Cleanup(func() {
		upstreamBalanceRetryBaseDelay, upstreamBalanceRetryMaxDelay = base, max
	})
}

func TestUpstreamBalanceRetryDelayIsExponentialAndCapped(t *testing.T) {
	require.Equal(t, time.Second, upstreamBalanceRetryDelay(0))
	require.Equal(t, time.Second, upstreamBalanceRetryDelay(1))
	require.Equal(t, 3*time.Second, upstreamBalanceRetryDelay(2))
	require.Equal(t, 9*time.Second, upstreamBalanceRetryDelay(3))
	require.Equal(t, 9*time.Second, upstreamBalanceRetryDelay(4))
}

func TestUpstreamBalanceRetryAllowedOnlyInBackgroundRounds(t *testing.T) {
	require.False(t, upstreamBalanceRetryAllowed(context.Background()))
	require.True(t, upstreamBalanceRetryAllowed(withUpstreamBalanceRetry(context.Background())))
}

// A stub reader that fails twice and succeeds on the third read: the round must
// end on the successful read instead of recording a failure.
func failingThenSucceedingBalanceReader(calls *int) func() upstreamBillingProbeHTTPResult {
	return func() upstreamBillingProbeHTTPResult {
		*calls++
		if *calls < 3 {
			return upstreamBillingProbeHTTPResult{statusCode: http.StatusInternalServerError}
		}
		return upstreamBillingProbeHTTPResult{
			statusCode: http.StatusOK,
			body:       []byte(`{"isValid":true,"remaining":12.5,"unit":"USD"}`),
		}
	}
}

func TestUpstreamBalanceReadRetriesUntilSuccess(t *testing.T) {
	fastUpstreamBalanceRetryBackoff(t)
	calls := 0
	fetch := failingThenSucceedingBalanceReader(&calls)

	result, data, ok := upstreamBalanceRead(context.Background(), upstreamBalanceRetryAttempts, fetch, parseUpstreamBalanceResponse)

	require.True(t, ok)
	require.Equal(t, 3, calls)
	require.Equal(t, http.StatusOK, result.statusCode)
	require.Equal(t, 12.5, data["remaining"])
}

// A manual probe runs on the admin request path: one read, no sleeping.
func TestUpstreamBalanceReadManualProbeDoesNotRetry(t *testing.T) {
	calls := 0
	fetch := failingThenSucceedingBalanceReader(&calls)

	_, _, ok := upstreamBalanceRead(context.Background(), 1, fetch, parseUpstreamBalanceResponse)

	require.False(t, ok)
	require.Equal(t, 1, calls)
}

func TestUpstreamBalanceReadStopsOnRejectedKey(t *testing.T) {
	fastUpstreamBalanceRetryBackoff(t)
	calls := 0
	fetch := func() upstreamBillingProbeHTTPResult {
		calls++
		return upstreamBillingProbeHTTPResult{statusCode: http.StatusForbidden}
	}

	_, _, ok := upstreamBalanceRead(context.Background(), upstreamBalanceRetryAttempts, fetch, parseUpstreamBalanceResponse)

	require.False(t, ok)
	require.Equal(t, 1, calls, "a rejected key must not be retried in the same round")
}

func TestUpstreamBalanceReadStopsWhenRoundIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	fetch := func() upstreamBillingProbeHTTPResult {
		calls++
		// Cancel while the first read is in flight: the back-off must not sleep
		// behind a stopped service.
		cancel()
		return upstreamBillingProbeHTTPResult{statusCode: http.StatusInternalServerError}
	}

	_, _, ok := upstreamBalanceRead(ctx, upstreamBalanceRetryAttempts, fetch, parseUpstreamBalanceResponse)

	require.False(t, ok)
	require.Equal(t, 1, calls)
}

func TestUpstreamBalanceRetryPendingSnapshotKeepsLastGoodValue(t *testing.T) {
	receivedAt := time.Date(2026, time.November, 12, 1, 0, 0, 0, time.UTC)
	freshUntil := receivedAt.Add(20 * time.Minute)
	previous := &UpstreamBalanceSnapshot{
		Status:     UpstreamBillingProbeStatusOK,
		Data:       map[string]any{"remaining": 5.0},
		ReceivedAt: &receivedAt,
		FreshUntil: &freshUntil,
		Source:     UpstreamBalanceSourceSub2APIUsage,
	}
	now := receivedAt.Add(time.Minute)

	snapshot := upstreamBalanceRetryPendingSnapshot(previous, now, http.StatusBadGateway, "http_error")

	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.Equal(t, previous.Data, snapshot.Data)
	require.Equal(t, previous.ReceivedAt, snapshot.ReceivedAt)
	require.Equal(t, previous.FreshUntil, snapshot.FreshUntil)
	require.Equal(t, UpstreamBalanceSourceSub2APIUsage, snapshot.Source)
	require.Equal(t, 1, snapshot.RetryCount)
	require.Equal(t, "http_error", snapshot.LastError)
	require.Equal(t, http.StatusBadGateway, snapshot.HTTPStatus)
}

func TestUpstreamBalanceRetryPendingSnapshotWithoutEarlierValue(t *testing.T) {
	now := time.Date(2026, time.November, 12, 1, 0, 0, 0, time.UTC)

	snapshot := upstreamBalanceRetryPendingSnapshot(nil, now, 0, "request_failed")

	// "pending" is not "failed": the account list shows a read in progress.
	require.Equal(t, UpstreamBillingProbeStatusPending, snapshot.Status)
	require.Nil(t, snapshot.Data)
	require.Equal(t, "request_failed", snapshot.LastError)
}

func TestUpstreamBalanceRetryNextDelayFollowsRetryCount(t *testing.T) {
	require.Equal(t, upstreamBalanceRetryDelay(1), upstreamBalanceRetryNextDelay(nil, false))
	require.Equal(t, upstreamBalanceRetryDelay(2), upstreamBalanceRetryNextDelay(&UpstreamBalanceSnapshot{RetryCount: 1}, false))
	require.Equal(t, upstreamBalanceRetryMaxDelay, upstreamBalanceRetryNextDelay(&UpstreamBalanceSnapshot{RetryCount: 9}, false))
	require.Equal(t, upstreamBalanceRejectedKeyDelay, upstreamBalanceRetryNextDelay(&UpstreamBalanceSnapshot{RetryCount: 9}, true))
}

const upstreamBalanceRetryBillingBody = `{"object":"sub2api.key_billing","schema_version":1,"billing_scope":"token",` +
	`"group_rate_multiplier":0.5,"resolved_rate_multiplier":0.5,"peak_rate_enabled":false,` +
	`"effective_rate_multiplier":0.5,"observed_at":"2026-11-12T01:00:00Z"}`

const upstreamBalanceRetryUsageBody = `{"isValid":true,"planName":"钱包余额","remaining":42,"unit":"USD"}`

// upstreamBalanceRetryExtra mirrors a snapshot read back from accounts.extra:
// the upstream was recognised as Sub2API and shows the given earlier balance.
func upstreamBalanceRetryExtra(withPreviousBalance bool) map[string]any {
	probe := map[string]any{
		"status":          "ok",
		"upstream_kind":   UpstreamKindSub2API,
		"last_attempt_at": "2026-11-12T01:00:00Z",
	}
	if withPreviousBalance {
		probe["balance"] = map[string]any{
			"status":          "ok",
			"data":            map[string]any{"is_valid": true, "remaining": 5.0},
			"received_at":     "2026-11-12T01:00:00Z",
			"fresh_until":     "2026-11-12T01:20:00Z",
			"last_attempt_at": "2026-11-12T01:00:00Z",
		}
	}
	return map[string]any{UpstreamBillingProbeExtraKey: probe}
}

func runUpstreamBalanceRetryProbe(
	t *testing.T,
	account *Account,
	upstream *httpUpstreamRecorder,
	now time.Time,
	backgroundRound bool,
) *UpstreamBillingProbeSnapshot {
	t.Helper()
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
	svc.SetAccountOpsService(func() *AccountOpsService {
		ops := NewAccountOpsService(nil, nil, nil)
		cfg := defaultAccountOpsConfig()
		cfg.Enabled = true
		cfg.BalanceThresholds = []AccountOpsBalanceRule{{AccountID: account.ID, Enabled: true, Unit: "USD", Threshold: 5}}
		ops.config.Store(cfg)
		return ops
	}())
	svc.now = func() time.Time { return now }
	ctx := context.Background()
	if backgroundRound {
		ctx = withUpstreamBalanceRetry(ctx)
	}
	snapshot, err := svc.probeAccountWithMode(ctx, account.ID, 30, false)
	require.NoError(t, err)
	require.NotNil(t, snapshot)
	return snapshot
}

// The stub reader fails the first two balance reads and succeeds on the third:
// the round must end with the balance written back and no failure recorded.
func TestUpstreamBalanceProbeRetriesIdentifiedSub2APIUntilSuccess(t *testing.T) {
	fastUpstreamBalanceRetryBackoff(t)
	now := time.Date(2026, time.November, 12, 1, 30, 0, 0, time.UTC)
	account := newUpstreamBalanceProbeTestAccount(701, upstreamBalanceRetryExtra(true))
	upstream := &httpUpstreamRecorder{resp: upstreamBalanceProbeJSONResponse(http.StatusInternalServerError, `{}`), responses: []*http.Response{
		upstreamBalanceProbeJSONResponse(http.StatusOK, upstreamBalanceRetryBillingBody),
		upstreamBalanceProbeJSONResponse(http.StatusInternalServerError, `{}`),
		upstreamBalanceProbeJSONResponse(http.StatusBadGateway, `{}`),
		upstreamBalanceProbeJSONResponse(http.StatusOK, upstreamBalanceRetryUsageBody),
	}}

	snapshot := runUpstreamBalanceRetryProbe(t, account, upstream, now, true)

	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.Equal(t, UpstreamKindSub2API, snapshot.UpstreamKind)
	require.NotNil(t, snapshot.Balance)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Balance.Status)
	require.Equal(t, 42.0, snapshot.Balance.Data["remaining"])
	require.Zero(t, snapshot.Balance.RetryCount)
	require.Equal(t, 4, len(upstream.requests), "rate read + three balance attempts")
}

// When nothing was ever read the snapshot stays "pending" and the next probe is
// scheduled soon, so the failure never reaches the account list.
func TestUpstreamBalanceProbeHidesFailureForIdentifiedSub2API(t *testing.T) {
	fastUpstreamBalanceRetryBackoff(t)
	now := time.Date(2026, time.November, 12, 1, 30, 0, 0, time.UTC)
	account := newUpstreamBalanceProbeTestAccount(702, upstreamBalanceRetryExtra(false))
	upstream := &httpUpstreamRecorder{resp: upstreamBalanceProbeJSONResponse(http.StatusInternalServerError, `{}`), responses: []*http.Response{
		upstreamBalanceProbeJSONResponse(http.StatusOK, upstreamBalanceRetryBillingBody),
	}}

	snapshot := runUpstreamBalanceRetryProbe(t, account, upstream, now, true)

	require.NotEqual(t, UpstreamBillingProbeStatusFailed, snapshot.Balance.Status)
	require.Equal(t, UpstreamBillingProbeStatusPending, snapshot.Balance.Status)
	require.Equal(t, UpstreamKindSub2API, snapshot.UpstreamKind, "the kind must survive so the next round retries the same endpoint")
	require.True(t, snapshot.NextProbeAt.After(now))
	require.False(t, snapshot.NextProbeAt.After(now.Add(10*time.Second)))
}

// A recognised upstream with an earlier successful read keeps showing that
// value while the retries run.
func TestUpstreamBalanceProbeKeepsLastValueForIdentifiedSub2API(t *testing.T) {
	fastUpstreamBalanceRetryBackoff(t)
	now := time.Date(2026, time.November, 12, 1, 30, 0, 0, time.UTC)
	account := newUpstreamBalanceProbeTestAccount(703, upstreamBalanceRetryExtra(true))
	upstream := &httpUpstreamRecorder{resp: upstreamBalanceProbeJSONResponse(http.StatusInternalServerError, `{}`), responses: []*http.Response{
		upstreamBalanceProbeJSONResponse(http.StatusOK, upstreamBalanceRetryBillingBody),
	}}

	snapshot := runUpstreamBalanceRetryProbe(t, account, upstream, now, true)

	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Balance.Status)
	require.Equal(t, 5.0, snapshot.Balance.Data["remaining"])
	// One retry round was spent; the next round keeps going (1s, 3s, 9s).
	require.Equal(t, 1, snapshot.Balance.RetryCount)
	require.Equal(t, "http_error", snapshot.Balance.LastError)
}

// A manual probe must not sleep on retries: it performs one balance read and
// leaves the retry to the scheduler.
func TestUpstreamBalanceProbeManualProbeDoesNotSleepOnRetry(t *testing.T) {
	now := time.Date(2026, time.November, 12, 1, 30, 0, 0, time.UTC)
	account := newUpstreamBalanceProbeTestAccount(704, upstreamBalanceRetryExtra(false))
	upstream := &httpUpstreamRecorder{resp: upstreamBalanceProbeJSONResponse(http.StatusInternalServerError, `{}`), responses: []*http.Response{
		upstreamBalanceProbeJSONResponse(http.StatusOK, upstreamBalanceRetryBillingBody),
	}}

	snapshot := runUpstreamBalanceRetryProbe(t, account, upstream, now, false)

	require.Equal(t, UpstreamBillingProbeStatusPending, snapshot.Balance.Status)
	require.Equal(t, 2, len(upstream.requests), "rate read + exactly one balance read")
}

// An upstream that was NOT identified keeps the original behaviour and may be
// reported as unsupported/failed.
func TestUpstreamBalanceProbeUnrecognisedKeepsFailurePath(t *testing.T) {
	now := time.Date(2026, time.November, 12, 1, 30, 0, 0, time.UTC)
	account := newUpstreamBalanceProbeTestAccount(705, nil)
	// Nothing answers, so the probe never identifies the upstream as Sub2API /
	// New API and the original unsupported verdict stands.
	upstream := &httpUpstreamRecorder{resp: upstreamBalanceProbeJSONResponse(http.StatusNotFound, `{}`)}

	snapshot := runUpstreamBalanceRetryProbe(t, account, upstream, now, true)

	require.Equal(t, UpstreamKindCustom, snapshot.UpstreamKind)
	require.Equal(t, UpstreamBillingProbeStatusUnsupported, snapshot.Balance.Status)
	require.Equal(t, "unsupported", snapshot.Balance.LastError)
	require.Zero(t, snapshot.Balance.RetryCount)
}
