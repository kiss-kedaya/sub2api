package cryptopay

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
	"github.com/stretchr/testify/require"
)

func TestSweepFailureAlertsCoverFailedPendingAndManualReviewWithoutReportingNormalWaits(t *testing.T) {
	for _, test := range []struct {
		name, status, reason string
		dryRun               bool
		want                 int
	}{
		{name: "failed", status: "failed", want: 1},
		{name: "manual review without error", status: "manual_review", want: 1},
		{name: "pending rpc error", status: "pending", reason: "RPC connection failed", want: 1},
		{name: "pending confirmation", status: "pending", reason: "awaiting canonical receipt and required confirmations"},
		{name: "pending gas payer confirmation", status: "pending", reason: "awaiting gas payer transaction confirmation"},
		{name: "pending no error", status: "pending"},
		{name: "success", status: "swept"},
		{name: "dry run failure", status: "failed", dryRun: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			summary := &sweeper.Summary{DryRun: test.dryRun, Entries: []sweeper.SweepEntry{{JobID: 37, Chain: "bsc", Status: test.status, Error: test.reason}}}
			require.Len(t, sweepFailureEvents(summary, nil), test.want)
		})
	}
	require.Empty(t, sweepFailureEvents(&sweeper.Summary{DryRun: true}, errors.New("plan failed")))
	require.Len(t, sweepFailureEvents(nil, errors.New("RPC connection failed")), 1)
}

func TestSweepFailureAlertsSeparateTasksAndNeverExposeSecrets(t *testing.T) {
	summary := &sweeper.Summary{Entries: []sweeper.SweepEntry{
		{JobID: 1, Chain: "bsc", Address: "fixture-private-address", Status: "manual_review", Error: "https://node.example/fixture-secret-path?key=fixture-secret-key"},
		{JobID: 2, Chain: "bsc", Address: "fixture-private-address", Status: "pending", Error: "RPC connection failed"},
	}}
	events := sweepFailureEvents(summary, errors.New("fixture-secret-driver-message"))
	require.Len(t, events, 2)
	require.NotEqual(t, events[0].key, events[1].key, "different tasks must not overwrite an undelivered alert")
	for _, event := range events {
		require.LessOrEqual(t, len(event.key), 100, "settings key column is VARCHAR(100)")
		require.Contains(t, event.body, "BSC")
		require.NotContains(t, event.body, "fixture-")
		require.NotContains(t, event.body, "https://")
	}
	unknown := sweepFailureEvents(&sweeper.Summary{Entries: []sweeper.SweepEntry{{Chain: "fixture-secret-chain", Status: "failed"}}}, nil)
	require.NotContains(t, unknown[0].body, "fixture-")
}

type sweepAlertStateArgument struct {
	check func(sweepFailureAlert) bool
}

func (arg sweepAlertStateArgument) Match(value driver.Value) bool {
	raw, ok := value.(string)
	if !ok || strings.Contains(raw, "fixture-secret") {
		return false
	}
	var state sweepFailureAlert
	return json.Unmarshal([]byte(raw), &state) == nil && arg.check(state)
}

func encodeSweepAlert(t *testing.T, state sweepFailureAlert) string {
	t.Helper()
	encoded, err := json.Marshal(state)
	require.NoError(t, err)
	return string(encoded)
}

type sweepAlertBatchArgument struct {
	check func([]struct {
		Key   string            `json:"key"`
		Value sweepFailureAlert `json:"value"`
	}) bool
}

func (arg sweepAlertBatchArgument) Match(value driver.Value) bool {
	raw, ok := value.(string)
	if !ok {
		return false
	}
	var items []struct {
		Key   string            `json:"key"`
		Value sweepFailureAlert `json:"value"`
	}
	return json.Unmarshal([]byte(raw), &items) == nil && arg.check(items)
}

func TestSweepFailureBatchPersistsAllDistinctTasksInOneOrderedStatement(t *testing.T) {
	now := time.Unix(1800000000, 0)
	var events []sweepFailureEvent
	for index := 199; index >= 0; index-- {
		events = append(events, sweepFailureEvent{key: sweepAlertPrefix + alertDigest(fmt.Sprintf("job:%d", index)), fingerprint: "cause", body: "fixed failure metadata"})
	}
	events = append(events, events[0])
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectExec("INSERT INTO settings").WithArgs(sweepAlertBatchArgument{check: func(items []struct {
		Key   string            `json:"key"`
		Value sweepFailureAlert `json:"value"`
	}) bool {
		if len(items) != 200 {
			return false
		}
		for index, item := range items {
			if !item.Value.Pending || item.Value.Fingerprint != "cause" ||
				(index > 0 && items[index-1].Key >= item.Key) {
				return false
			}
		}
		return true
	}}, now.Add(-sweepAlertCooldown).Unix()).WillReturnResult(sqlmock.NewResult(0, 200))
	require.NoError(t, (&Service{db: db}).queueSweepFailureAlerts(t.Context(), events, now))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSweepFailurePushFailureRemainsPendingAndSuccessfulRetryAcknowledges(t *testing.T) {
	now := time.Unix(1800000000, 0)
	key := sweepAlertPrefix + alertDigest("job:8")
	state := sweepFailureAlert{Version: 1, Fingerprint: "fingerprint", Pending: true, Body: "fixed failure metadata"}
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	svc := &Service{db: db}
	push := &walletOperationPush{err: errors.New("https://bark.example/fixture-secret-device")}
	svc.SetPushNotifier(push)
	mock.ExpectQuery("UPDATE settings SET").WithArgs(key, sqlmock.AnyArg(), sqlmock.AnyArg(), now.Unix()).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(encodeSweepAlert(t, state)))
	mock.ExpectExec("UPDATE settings SET value").WithArgs(key, sweepAlertStateArgument{check: func(next sweepFailureAlert) bool {
		return next.Pending && next.NextAttemptAt == now.Add(sweepAlertRetry).Unix() && next.LastSentAt == 0
	}}, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	err = svc.deliverSweepFailureAlert(t.Context(), key, now)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "fixture-secret")
	require.Equal(t, 1, push.calls)
	state.NextAttemptAt = now.Add(sweepAlertRetry).Unix()
	now = now.Add(sweepAlertRetry)
	push.err = nil
	mock.ExpectQuery("UPDATE settings SET").WithArgs(key, sqlmock.AnyArg(), sqlmock.AnyArg(), now.Unix()).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(encodeSweepAlert(t, state)))
	mock.ExpectExec("UPDATE settings SET value").WithArgs(key, sweepAlertStateArgument{check: func(next sweepFailureAlert) bool {
		return !next.Pending && next.LastSentAt == now.Unix() && next.NextAttemptAt == 0
	}}, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, svc.deliverSweepFailureAlert(t.Context(), key, now))
	require.Equal(t, 2, push.calls)
	require.NoError(t, mock.ExpectationsWereMet())
}

type cancelCallerPush struct{ cancel context.CancelFunc }

func (p cancelCallerPush) PushToAdmins(ctx context.Context, _, _, _ string) error {
	p.cancel()
	return nil // the remote accepted the push just before the caller disconnected
}

func TestSweepFailureDeliveryAcknowledgmentSurvivesCallerCancellation(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	now := time.Unix(1800000000, 0)
	key := sweepAlertPrefix + alertDigest("job:9")
	state := sweepFailureAlert{Version: 1, Pending: true, Body: "fixed failure metadata"}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	svc := &Service{db: db}
	svc.SetPushNotifier(cancelCallerPush{cancel: cancel})
	mock.ExpectQuery("UPDATE settings SET").WithArgs(key, sqlmock.AnyArg(), sqlmock.AnyArg(), now.Unix()).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(encodeSweepAlert(t, state)))
	mock.ExpectExec("UPDATE settings SET value").WithArgs(key, sweepAlertStateArgument{check: func(next sweepFailureAlert) bool { return !next.Pending }}, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, svc.deliverSweepFailureAlert(ctx, key, now))
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.NoError(t, mock.ExpectationsWereMet())
}

type waitingSweepPush struct{ calls int }

func (push *waitingSweepPush) PushToAdmins(ctx context.Context, _, _, _ string) error {
	push.calls++
	<-ctx.Done()
	return ctx.Err()
}

func TestSweepFailureSendReservesAcknowledgmentBudgetAndDoesNotExtendOverallDeadline(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	now := time.Now()
	key := sweepAlertPrefix + alertDigest("job:deadline")
	svc := &Service{db: db}
	push := &waitingSweepPush{}
	svc.SetPushNotifier(push)
	ctx, cancel := context.WithTimeout(t.Context(), sweepAlertAckTime+100*time.Millisecond)
	defer cancel()
	mock.ExpectQuery("UPDATE settings SET").WithArgs(key, sqlmock.AnyArg(), sqlmock.AnyArg(), now.Unix()).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(encodeSweepAlert(t, sweepFailureAlert{Version: 1, Pending: true, Body: "fixed metadata"})))
	mock.ExpectExec("UPDATE settings SET value").WithArgs(key, sweepAlertStateArgument{check: func(state sweepFailureAlert) bool {
		return state.Pending && state.NextAttemptAt == now.Add(sweepAlertRetry).Unix() && state.DeliveryToken == ""
	}}, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	started := time.Now()
	require.Error(t, svc.deliverSweepFailureAlert(ctx, key, now))
	require.Less(t, time.Since(started), time.Second, "send must leave the reserved acknowledgment window, not consume the parent deadline")
	require.Equal(t, 1, push.calls)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSweepFailureDoesNotClaimOrSendWhenOnlyAcknowledgmentBudgetRemains(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	svc := &Service{db: db}
	push := &walletOperationPush{}
	svc.SetPushNotifier(push)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	require.ErrorIs(t, svc.deliverSweepFailureAlert(ctx, "unused", time.Now()), context.DeadlineExceeded)
	require.Zero(t, push.calls)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSweepFailureDryRunAndReadOnlyDoNotWriteOrPush(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	svc := &Service{db: db}
	push := &walletOperationPush{}
	svc.SetPushNotifier(push)
	svc.reportSweepFailure(t.Context(), &sweeper.Summary{DryRun: true}, errors.New("dry run failure"))
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	svc.reportSweepFailure(t.Context(), nil, errors.New("real sweep blocked by read-only mode"))
	require.Zero(t, push.calls)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRealSweepFailureQueuesAndNotifiesWithoutReplacingItsOutcome(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	svc := &Service{db: db} // disabled executor fails before any signing/RPC
	push := &walletOperationPush{}
	svc.SetPushNotifier(push)
	event := sweepFailureEvents(nil, errors.New("cryptopay is disabled"))[0]
	mock.ExpectExec("INSERT INTO settings").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT key FROM settings").WithArgs(sweepAlertPrefix, "crypto.sweep.alert.v1/", sqlmock.AnyArg(), sweepAlertBatch).
		WillReturnRows(sqlmock.NewRows([]string{"key"}).AddRow(event.key))
	mock.ExpectQuery("UPDATE settings SET").WithArgs(event.key, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(encodeSweepAlert(t, sweepFailureAlert{
			Version: 1, Fingerprint: event.fingerprint, Pending: true, Body: event.body,
		})))
	mock.ExpectExec("UPDATE settings SET value").WithArgs(event.key, sweepAlertStateArgument{check: func(state sweepFailureAlert) bool {
		return !state.Pending && state.LastSentAt > 0
	}}, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	summary, err := svc.RunSweep(t.Context(), false)
	require.Nil(t, summary)
	require.EqualError(t, err, "cryptopay is disabled")
	require.Equal(t, 1, push.calls)
	require.Contains(t, push.title, "归集失败")
	require.NoError(t, mock.ExpectationsWereMet())
}
