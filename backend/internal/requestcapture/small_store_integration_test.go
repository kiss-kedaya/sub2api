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
	migration, err := migrations.FS.ReadFile("319_small_request_retention.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	store := &SmallStore{DB: db}
	require.True(t, store.Ready(ctx), "migration 319 creates all retention tables")
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
		cutoff := smallCutoff(now)
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
	t.Run("ClearRejectsOldQueuedCopies", func(t *testing.T) {
		e := smallTestExchange()
		p, reason := prepareSmallExchange(e, nil)
		require.Empty(t, reason)
		require.NoError(t, store.Clear(ctx))
		require.NoError(t, store.Save(ctx, p))
		for _, table := range []string{"small_request_records", "small_request_responses", "small_request_blobs", "small_request_skips"} {
			var n int
			require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n))
			require.Zero(t, n)
		}
	})
}
