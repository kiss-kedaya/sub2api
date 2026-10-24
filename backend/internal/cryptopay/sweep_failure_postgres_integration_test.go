//go:build integration

package cryptopay

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func isolatedSweepAlertPostgres(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("CRYPTO_SWEEP_TEST_DSN")
	if dsn == "" {
		t.Skip("set CRYPTO_SWEEP_TEST_DSN for the disposable integration cluster")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Port() != "15439" ||
		u.User == nil || u.User.Username() != "crypto_review" || u.Path != "/postgres" ||
		u.Query().Get("sslmode") != "disable" || len(u.Query()) != 1 {
		t.Fatal("integration DSN must name the disposable 127.0.0.1:15439 crypto_review cluster")
	}
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := fmt.Sprintf("crypto_alert_it_%d", time.Now().UnixNano())
	quoted := pq.QuoteIdentifier(schema)
	_, err = admin.ExecContext(t.Context(), "CREATE SCHEMA "+quoted)
	require.NoError(t, err)
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	db.SetMaxOpenConns(5)
	t.Cleanup(func() {
		_ = db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := admin.ExecContext(ctx, "DROP SCHEMA "+quoted+" CASCADE")
		if err != nil {
			t.Error(err)
		}
		_ = admin.Close()
	})
	_, err = db.ExecContext(t.Context(), `CREATE TABLE settings (
		id BIGSERIAL PRIMARY KEY, key VARCHAR(100) NOT NULL UNIQUE,
		value TEXT NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`)
	require.NoError(t, err)
	return db
}

type sweepConcurrentPush struct {
	calls atomic.Int64
	fail  atomic.Bool
}

func (push *sweepConcurrentPush) PushToAdmins(context.Context, string, string, string) error {
	push.calls.Add(1)
	if push.fail.Load() {
		return errors.New("simulated Bark unavailable")
	}
	return nil
}

func TestPostgresSweepFailureOutboxRetainsDistinctTasksAndSerializesDelivery(t *testing.T) {
	db := isolatedSweepAlertPostgres(t)
	_, err := db.ExecContext(t.Context(), `INSERT INTO settings (key,value) VALUES ($1,$2)`, "site_title", "ordinary non-JSON setting")
	require.NoError(t, err)
	svc := &Service{db: db}
	push := &sweepConcurrentPush{}
	svc.SetPushNotifier(push)
	other := &Service{db: db}
	other.SetPushNotifier(push)
	now := time.Now()
	events := []sweepFailureEvent{
		{key: sweepAlertPrefix + alertDigest("job:1"), fingerprint: "same cause", body: "task #1 failed"},
		{key: sweepAlertPrefix + alertDigest("job:2"), fingerprint: "same cause", body: "task #2 failed"},
	}
	require.NoError(t, svc.queueSweepFailureAlerts(t.Context(), events, now))
	push.fail.Store(true)
	require.Error(t, svc.deliverSweepFailureAlerts(t.Context()))
	require.Equal(t, int64(2), push.calls.Load())
	var pending int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT count(*) FROM settings WHERE key >= $1 AND key < $2 AND CASE WHEN key >= $1 AND key < $2 THEN value::jsonb->>'pending'='true' ELSE false END`, sweepAlertPrefix, "crypto.sweep.alert.v1/").Scan(&pending))
	require.Equal(t, 2, pending, "delivery failure must not acknowledge either task")
	require.NoError(t, svc.deliverSweepFailureAlerts(t.Context()))
	require.Equal(t, int64(2), push.calls.Load(), "retry backoff must survive a new delivery pass")
	_, err = db.ExecContext(t.Context(), `UPDATE settings SET value=jsonb_set(value::jsonb,'{next_attempt_at}','0'::jsonb)::text WHERE key >= $1 AND key < $2`, sweepAlertPrefix, "crypto.sweep.alert.v1/")
	require.NoError(t, err)
	push.fail.Store(false)
	var wg sync.WaitGroup
	errorsCh := make(chan error, 2)
	for _, instance := range []*Service{svc, other} {
		wg.Add(1)
		go func(current *Service) {
			defer wg.Done()
			errorsCh <- current.deliverSweepFailureAlerts(t.Context())
		}(instance)
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		require.NoError(t, err)
	}
	require.Equal(t, int64(4), push.calls.Load(), "two app instances must deliver each distinct task only once")
	require.NoError(t, svc.queueSweepFailureAlerts(t.Context(), events, time.Now()))
	require.NoError(t, svc.deliverSweepFailureAlerts(t.Context()))
	require.Equal(t, int64(4), push.calls.Load(), "successful delivery cooldown must survive new service calls")
	event := sweepFailureEvent{key: sweepAlertPrefix + alertDigest("job:3"), fingerprint: "same cause", body: "task #3 failed"}
	require.NoError(t, svc.queueSweepFailureAlerts(t.Context(), []sweepFailureEvent{event}, time.Now()))
	require.NoError(t, svc.deliverSweepFailureAlerts(t.Context()))
	require.Equal(t, int64(5), push.calls.Load(), "another task must not inherit a previous task's cooldown")
}

func TestPostgresSweepFailureBatchIsAtomicAndPreservesCooldownAndRetryLease(t *testing.T) {
	db := isolatedSweepAlertPostgres(t)
	svc := &Service{db: db}
	now := time.Now()
	event := sweepFailureEvent{key: sweepAlertPrefix + alertDigest("job:1"), fingerprint: "cause", body: "fixed metadata"}
	invalid := event
	invalid.key = strings.Repeat("x", 101)
	require.Error(t, svc.queueSweepFailureAlerts(t.Context(), []sweepFailureEvent{event, invalid}, now))
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT count(*) FROM settings`).Scan(&count))
	require.Zero(t, count, "a failed batch must not silently preserve only its first tasks")
	for _, test := range []struct {
		name string
		old  sweepFailureAlert
		want bool
	}{
		{name: "recent success", old: sweepFailureAlert{Version: 1, Fingerprint: "cause", LastSentAt: now.Unix()}},
		{name: "expired cooldown", old: sweepFailureAlert{Version: 1, Fingerprint: "cause", LastSentAt: now.Add(-sweepAlertCooldown).Unix()}, want: true},
		{name: "pending retry lease", old: sweepFailureAlert{Version: 1, Fingerprint: "cause", Pending: true, NextAttemptAt: now.Add(time.Minute).Unix(), DeliveryToken: "existing claim"}, want: true},
		{name: "changed failure", old: sweepFailureAlert{Version: 1, Fingerprint: "different cause", LastSentAt: now.Unix()}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := db.ExecContext(t.Context(), `INSERT INTO settings (key,value) VALUES ($1,$2) ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value`, event.key, encodeSweepAlert(t, test.old))
			require.NoError(t, err)
			require.NoError(t, svc.queueSweepFailureAlerts(t.Context(), []sweepFailureEvent{event}, now))
			var raw string
			require.NoError(t, db.QueryRowContext(t.Context(), `SELECT value FROM settings WHERE key=$1`, event.key).Scan(&raw))
			var state sweepFailureAlert
			require.NoError(t, json.Unmarshal([]byte(raw), &state))
			require.Equal(t, test.want, state.Pending)
			require.Equal(t, test.old.LastSentAt, state.LastSentAt)
			if test.old.Pending {
				require.Equal(t, test.old.NextAttemptAt, state.NextAttemptAt)
				require.Equal(t, test.old.DeliveryToken, state.DeliveryToken, "repeated observation must not steal a live delivery lease")
			}
		})
	}
}

type blockedSweepPush struct {
	started chan struct{}
	release chan struct{}
}

func (push blockedSweepPush) PushToAdmins(ctx context.Context, _, _, _ string) error {
	close(push.started)
	select {
	case <-push.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestPostgresSweepFailureSendDoesNotHoldRowLockOrClearANewerFailure(t *testing.T) {
	db := isolatedSweepAlertPostgres(t)
	svc := &Service{db: db}
	push := blockedSweepPush{started: make(chan struct{}), release: make(chan struct{})}
	svc.SetPushNotifier(push)
	event := sweepFailureEvent{key: sweepAlertPrefix + alertDigest("job:1"), fingerprint: "old cause", body: "old metadata"}
	require.NoError(t, svc.queueSweepFailureAlerts(t.Context(), []sweepFailureEvent{event}, time.Now()))
	finished := make(chan error, 1)
	go func() { finished <- svc.deliverSweepFailureAlert(t.Context(), event.key, time.Now()) }()
	select {
	case <-push.started:
	case <-time.After(time.Second):
		t.Fatal("delivery did not begin")
	}
	// A network-delayed push must not lock out another instance observing an
	// updated failure for this task. The old sender cannot acknowledge it.
	event.fingerprint, event.body = "new cause", "new metadata"
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	err := svc.queueSweepFailureAlerts(ctx, []sweepFailureEvent{event}, time.Now())
	cancel()
	close(push.release)
	require.NoError(t, err, "a Bark request held the settings row locked")
	require.NoError(t, <-finished)
	var raw string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT value FROM settings WHERE key=$1`, event.key).Scan(&raw))
	var state sweepFailureAlert
	require.NoError(t, json.Unmarshal([]byte(raw), &state))
	require.True(t, state.Pending)
	require.Equal(t, "new cause", state.Fingerprint)
	require.Equal(t, "new metadata", state.Body)
}
