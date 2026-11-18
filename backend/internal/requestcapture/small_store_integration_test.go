//go:build integration

package requestcapture

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Same disposable PostgreSQL/testcontainers convention as repository's CI suite;
// no external DSN is accepted, so this cannot accidentally target a live store.
func TestSmallRetentionPostgresStore(t *testing.T) {
	probe, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err := exec.CommandContext(probe, "docker", "info").Run()
	cancel()
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("Docker required in CI: %v", err)
		}
		t.Skip("Docker unavailable; PostgreSQL integration runs in CI")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	image := os.Getenv("SUB2API_TEST_POSTGRES_IMAGE")
	if image == "" {
		image = "postgres:18.1-alpine3.23"
	}
	container, err := tcpostgres.Run(ctx, image, tcpostgres.WithDatabase("small_retention_fixture"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("postgres"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	defer func() { require.NoError(t, container.Terminate(context.Background())) }()
	dsn, err := container.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	store := &SmallStore{DB: db}
	for _, filename := range []string{"319_small_request_retention.sql", "320_small_request_retention_occurrences.sql"} {
		if filename == "320_small_request_retention_occurrences.sql" {
			require.False(t, store.Ready(ctx), "capture must remain off until the occurrence migration exists")
		}
		migration, err := migrations.FS.ReadFile(filename)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, string(migration))
		require.NoError(t, err)
	}
	require.True(t, store.Ready(ctx))
	// Hold all eight sessions at once before returning them to the pool.
	connections := make([]*sql.Conn, 8)
	for i := range connections {
		connections[i], err = db.Conn(ctx)
		require.NoError(t, err)
	}
	for _, connection := range connections {
		require.NoError(t, connection.Close())
	}

	checkRefs := func(t *testing.T) {
		t.Helper()
		var mismatches int
		require.NoError(t, db.QueryRowContext(ctx, `WITH refs AS (
		 SELECT bucket,user_id,request_headers_hash AS hash FROM small_request_records
		 UNION ALL SELECT bucket,user_id,request_body_hash FROM small_request_records
		 UNION ALL SELECT s.bucket,r.user_id,s.headers_hash FROM small_request_responses s JOIN small_request_records r ON r.id=s.record_id
		 UNION ALL SELECT s.bucket,r.user_id,s.body_hash FROM small_request_responses s JOIN small_request_records r ON r.id=s.record_id
		), expected AS (SELECT bucket,user_id,hash,count(*) AS n FROM refs GROUP BY bucket,user_id,hash)
		SELECT count(*) FROM small_request_blobs b FULL JOIN expected e USING(bucket,user_id,hash) WHERE b.ref_count IS DISTINCT FROM e.n`).Scan(&mismatches))
		require.Zero(t, mismatches, "every blob is referenced exactly once per retained slot")
	}
	t.Run("EightConnectionsConcurrentFingerprint", func(t *testing.T) {
		e := smallTestExchange()
		p, reason := prepareSmallExchange(e, nil)
		require.Empty(t, reason)
		start := make(chan struct{})
		errors := make(chan error, 8)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for n := 0; n < 8; n++ {
					if err := store.Save(ctx, p); err != nil {
						errors <- err
						return
					}
				}
			}()
		}
		close(start)
		wg.Wait()
		close(errors)
		for err := range errors {
			require.NoError(t, err)
		}
		var records, repeats, samples, responseRepeats int
		require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*),sum(repeat_count) FROM small_request_records").Scan(&records, &repeats))
		require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*),sum(repeat_count) FROM small_request_responses").Scan(&samples, &responseRepeats))
		require.Equal(t, 1, records)
		require.Equal(t, 64, repeats)
		require.Equal(t, 1, samples)
		require.Equal(t, 64, responseRepeats)
		checkRefs(t)
	})
	t.Run("ResponseSamplesEvictAndCountOmissions", func(t *testing.T) {
		require.NoError(t, store.Clear(ctx))
		base := time.Now().UTC().Truncate(time.Second).Add(time.Second)
		for i := 0; i < 7; i++ {
			e := smallTestExchange()
			e.StartedAt = base.Add(time.Duration(i) * time.Millisecond)
			e.ResponseBody = []byte(fmt.Sprintf(`{"output":"sample-%d"}`, i))
			p, reason := prepareSmallExchange(e, nil)
			require.Empty(t, reason)
			copies := 1
			if i == 1 {
				copies = 3
			}
			for n := 0; n < copies; n++ {
				require.NoError(t, store.Save(ctx, p))
			}
		}
		var id, repeats, omitted int64
		require.NoError(t, db.QueryRowContext(ctx, "SELECT id,repeat_count,omitted_responses FROM small_request_records").Scan(&id, &repeats, &omitted))
		require.EqualValues(t, 9, repeats)
		require.EqualValues(t, 4, omitted)
		detail, err := store.Detail(ctx, id)
		require.NoError(t, err)
		require.Len(t, detail.Responses, 5)
		require.Equal(t, `{"output":"sample-0"}`, detail.Responses[0].Body)
		checkRefs(t)
	})
	t.Run("CleanupOnlyExpiredBuckets", func(t *testing.T) {
		require.NoError(t, store.Clear(ctx))
		now := time.Now().UTC()
		cutoff := smallCutoff(now).Truncate(time.Hour)
		for i := 0; i < 3; i++ {
			e := smallTestExchange()
			e.UserID += int64(i)
			e.StartedAt = now
			p, reason := prepareSmallExchange(e, nil)
			require.Empty(t, reason)
			require.NoError(t, store.Save(ctx, p))
			bucket := cutoff.Add(time.Duration(i-1) * time.Hour)
			for _, table := range []string{"small_request_records", "small_request_blobs"} {
				_, err := db.ExecContext(ctx, "UPDATE "+table+" SET bucket=$1 WHERE user_id=$2", bucket, e.UserID)
				require.NoError(t, err)
			}
			_, err := db.ExecContext(ctx, "UPDATE small_request_responses SET bucket=$1 WHERE record_id IN (SELECT id FROM small_request_records WHERE user_id=$2)", bucket, e.UserID)
			require.NoError(t, err)
			require.NoError(t, store.AddSkipped(ctx, bucket, "usage_unknown", 1))
		}
		require.NoError(t, store.Cleanup(ctx, now))
		for _, table := range []string{"small_request_records", "small_request_responses", "small_request_blobs", "small_request_skips"} {
			var expired, boundary int
			require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FILTER(WHERE bucket<$1),count(*) FILTER(WHERE bucket=$1) FROM "+table, cutoff).Scan(&expired, &boundary))
			require.Zero(t, expired)
			require.Positive(t, boundary, table)
		}
		checkRefs(t)
	})
	t.Run("ExactWindowRetainsOnlyLiveOccurrencesWithinHour", func(t *testing.T) {
		require.NoError(t, store.Clear(ctx))
		_, err := db.ExecContext(ctx, "UPDATE small_request_state SET cleared_at='-infinity' WHERE id=1")
		require.NoError(t, err)
		cutoff := time.Now().UTC().Truncate(time.Hour).Add(-90 * time.Minute)
		for _, tc := range []struct {
			shift              time.Duration
			id, response, cost string
			input              int
		}{
			{-time.Microsecond, "expired-older", "old-only", "0.11000000", 10},
			{0, "expired-at-cutoff", "shared-sample", "0.12000000", 20},
			{time.Microsecond, "live-near-cutoff", "shared-sample", "0.13000000", 30},
			{10 * time.Minute, "live-later", "new-only", "0.14000000", 40},
		} {
			e := smallTestExchange()
			e.StartedAt, e.Usage.RequestID, e.Usage.ActualCost, e.Usage.InputTokens = cutoff.Add(tc.shift), tc.id, tc.cost, tc.input
			e.Usage.OutputTokens = tc.input * 2
			e.Usage.CacheReadTokens = tc.input + 1
			e.Usage.CacheCreationTokens = tc.input + 2
			if tc.shift == 10*time.Minute {
				e.Status = 201
			}
			e.RequestHeaders.Set("X-Request-ID", tc.id)
			e.ResponseHeaders.Set("Content-Type", "application/json")
			e.ResponseHeaders.Set("X-Request-ID", tc.id)
			e.ResponseBody = []byte(fmt.Sprintf(`{"output":%q}`, tc.response))
			p, reason := prepareSmallExchange(e, nil)
			require.Empty(t, reason)
			require.NoError(t, store.Save(ctx, p))
		}
		complete, err := store.listAt(ctx, SmallFilter{}, cutoff.Add(-2*time.Microsecond))
		require.NoError(t, err)
		require.Len(t, complete, 1)
		require.EqualValues(t, 4, complete[0].RepeatCount)
		require.NotNil(t, complete[0].Data.FirstUsage)
		items, err := store.listAt(ctx, SmallFilter{}, cutoff)
		require.NoError(t, err)
		require.Len(t, items, 1)
		r := items[0]
		require.EqualValues(t, 2, r.RepeatCount)
		require.EqualValues(t, 70, r.InputTokens)
		require.EqualValues(t, 140, r.OutputTokens)
		require.EqualValues(t, 72, r.CacheReadTokens)
		require.EqualValues(t, 74, r.CacheCreationTokens)
		require.Equal(t, 201, r.Status)
		require.Equal(t, "0.27000000", r.ActualCost)
		require.Equal(t, []string{"live-near-cutoff", "live-later"}, r.RequestIDs)
		require.True(t, r.FirstSeen.Equal(cutoff.Add(time.Microsecond)))
		require.True(t, r.ExpiresAt.Equal(cutoff.Add(24*time.Hour+10*time.Minute)))
		require.Nil(t, r.Data.FirstUsage)
		require.Empty(t, r.Data.RequestVolatileHeaders)

		items, err = store.listAt(ctx, SmallFilter{RequestID: "expired-at-cutoff"}, cutoff)
		require.NoError(t, err)
		require.Empty(t, items)
		items, err = store.listAt(ctx, SmallFilter{RequestID: "live-near-cutoff", MinRepeats: 2}, cutoff)
		require.NoError(t, err)
		require.Len(t, items, 1)
		detail, err := store.detailAt(ctx, r.ID, cutoff)
		require.NoError(t, err)
		require.NotEmpty(t, detail.RequestBody)
		require.NotContains(t, detail.Curl, "expired-")
		require.Len(t, detail.Responses, 2)
		require.JSONEq(t, `{"output":"shared-sample"}`, detail.Responses[0].Body)
		require.EqualValues(t, 1, detail.Responses[0].RepeatCount)
		require.Nil(t, detail.Responses[0].Data.Usage)
		require.Empty(t, detail.Responses[0].Headers["x-request-id"])
		require.NotNil(t, detail.Responses[1].Data.Usage)
		require.Equal(t, "live-later", detail.Responses[1].Data.Usage.RequestID)
		stats, err := store.statsAt(ctx, cutoff)
		require.NoError(t, err)
		require.EqualValues(t, 1, stats.Records)
		require.EqualValues(t, 2, stats.Requests)
		require.EqualValues(t, 1, stats.Duplicates)
		require.Equal(t, map[string]int64{"2-9": 1}, stats.RepeatDistribution)
		require.NoError(t, store.Cleanup(ctx, cutoff.Add(smallRetentionWindow)))
		var occurrences int
		require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM small_request_occurrences").Scan(&occurrences))
		require.Equal(t, 2, occurrences)
		detail, err = store.detailAt(ctx, r.ID, cutoff)
		require.NoError(t, err)
		require.EqualValues(t, 2, detail.Record.RepeatCount)
		require.Len(t, detail.Responses, 2)
		_, err = store.detailAt(ctx, r.ID, cutoff.Add(10*time.Minute))
		require.ErrorIs(t, err, sql.ErrNoRows)
		checkRefs(t)
	})
	t.Run("LegacyAndMixedWritersAtBoundary", func(t *testing.T) {
		require.NoError(t, store.Clear(ctx))
		_, err := db.ExecContext(ctx, "UPDATE small_request_state SET cleared_at='-infinity' WHERE id=1")
		require.NoError(t, err)
		cutoff := time.Now().UTC().Truncate(time.Hour).Add(-90 * time.Minute)
		storeSeen := func(id string, seen time.Time) {
			e := smallTestExchange()
			e.StartedAt, e.Usage.RequestID = seen, id
			p, reason := prepareSmallExchange(e, nil)
			require.Empty(t, reason)
			require.NoError(t, store.Save(ctx, p))
		}
		storeSeen("legacy-old", cutoff.Add(-time.Minute))
		storeSeen("legacy-new", cutoff.Add(time.Minute))
		// An older writer persisted the group and blobs but had no occurrence table.
		_, err = db.ExecContext(ctx, "DELETE FROM small_request_occurrences")
		require.NoError(t, err)
		before, err := store.listAt(ctx, SmallFilter{}, cutoff.Add(-2*time.Minute))
		require.NoError(t, err)
		require.Len(t, before, 1)
		require.EqualValues(t, 2, before[0].RepeatCount)
		boundary, err := store.listAt(ctx, SmallFilter{}, cutoff)
		require.NoError(t, err)
		require.Empty(t, boundary)
		storeSeen("timestamped", cutoff.Add(2*time.Minute))
		boundary, err = store.listAt(ctx, SmallFilter{}, cutoff)
		require.NoError(t, err)
		require.Len(t, boundary, 1)
		require.Equal(t, []string{"timestamped"}, boundary[0].RequestIDs)
		require.EqualValues(t, 1, boundary[0].RepeatCount)
		checkRefs(t)
	})
	t.Run("OldClearRemainsCompatible", func(t *testing.T) {
		_, err := db.ExecContext(ctx, "TRUNCATE small_request_responses,small_request_records,small_request_blobs,small_request_skips")
		require.NoError(t, err)
		rows, err := store.List(ctx, SmallFilter{})
		require.NoError(t, err)
		require.Empty(t, rows)
		require.NoError(t, store.Cleanup(ctx, time.Now().Add(25*time.Hour)))
		var remaining int
		require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM small_request_occurrences").Scan(&remaining))
		require.Zero(t, remaining)
	})
	t.Run("RejectsExpiredSave", func(t *testing.T) {
		e := smallTestExchange()
		e.StartedAt = time.Now().UTC().Add(-smallRetentionWindow - time.Minute)
		p, reason := prepareSmallExchange(e, nil)
		require.Empty(t, reason)
		require.ErrorIs(t, store.Save(ctx, p), errSmallRetentionExpired)
	})
	t.Run("ClearRejectsOldQueuedCopies", func(t *testing.T) {
		e := smallTestExchange()
		p, reason := prepareSmallExchange(e, nil)
		require.Empty(t, reason)
		require.NoError(t, store.Clear(ctx))
		require.NoError(t, store.Save(ctx, p))
		for _, table := range []string{"small_request_records", "small_request_responses", "small_request_blobs", "small_request_skips", "small_request_occurrences"} {
			var n int
			require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n))
			require.Zero(t, n)
		}
	})
}
