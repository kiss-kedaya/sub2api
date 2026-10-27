// Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later.
package repository

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Opt-in, isolated pg_temp schema. No production migrations or data are touched.
func TestTrafficPostgresIdempotencyIsolationAndCalendarBuckets(t *testing.T) {
	dsn := os.Getenv("TRAFFIC_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TRAFFIC_TEST_POSTGRES_DSN not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	_, err = db.ExecContext(ctx, "SET search_path TO pg_temp")
	require.NoError(t, err)
	migration, err := os.ReadFile("../../migrations/272_user_traffic.sql")
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err = db.ExecContext(ctx, strings.ReplaceAll(string(migration), "CREATE TABLE IF NOT EXISTS", "CREATE TEMP TABLE IF NOT EXISTS"))
		require.NoError(t, err)
	}
	repo := newUsageLogRepositoryWithSQL(nil, db)
	_, err = db.ExecContext(ctx, `CREATE TEMP TABLE api_keys(id BIGINT PRIMARY KEY,user_id BIGINT,name TEXT,deleted_at TIMESTAMPTZ,key TEXT);
 INSERT INTO api_keys VALUES (2,1,'Current key',NULL,'never-return-this-secret'),(3,1,'Old key',NOW(),'deleted-secret'),(10,9,'Other key',NULL,'other-secret')`)
	require.NoError(t, err)
	choices, err := repo.TrafficKeyOptions(ctx, 1, "")
	require.NoError(t, err)
	require.Equal(t, []service.TrafficKeyOption{{ID: 2, Name: "Current key"}, {ID: 3, Name: "Old key", Deleted: true}}, choices)
	choices, err = repo.TrafficKeyOptions(ctx, 1, "old")
	require.NoError(t, err)
	require.Equal(t, []service.TrafficKeyOption{{ID: 3, Name: "Old key", Deleted: true}}, choices)
	choices, err = repo.TrafficKeyOptions(ctx, 1, "#3")
	require.NoError(t, err)
	require.Equal(t, []service.TrafficKeyOption{{ID: 3, Name: "Old key", Deleted: true}}, choices)
	choices, err = repo.TrafficKeyOptions(ctx, 1, "#10")
	require.NoError(t, err)
	require.Empty(t, choices)
	choices, err = repo.TrafficKeyOptions(ctx, 1, "Other")
	require.NoError(t, err)
	require.Empty(t, choices)
	choices, err = repo.TrafficKeyOptions(ctx, 0, "Other")
	require.NoError(t, err)
	require.Equal(t, []service.TrafficKeyOption{{ID: 10, Name: "Other key"}}, choices)
	loc, err := time.LoadLocation("Asia/Kathmandu")
	require.NoError(t, err)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, loc)
	e := service.TrafficEvent{EventID: uuid.NewString(), UserID: 1, APIKeyID: 2, GroupID: 3, RequestID: "same", Endpoint: "/v1/messages", Method: "POST", StartedAt: start.Add(5 * time.Minute), FinishedAt: start.Add(6 * time.Minute), UploadBytes: 100, DownloadBytes: 200, StatusCode: 200, Outcome: "completed", RequestComplete: true, MeasurementVersion: 1}
	require.NoError(t, repo.RecordTraffic(ctx, e))
	require.NoError(t, repo.RecordTraffic(ctx, e))
	// Opposite sides of a non-whole-hour midnight must land on different days.
	e2 := e
	e2.EventID = uuid.NewString()
	e2.StartedAt = start.AddDate(0, 0, 1).Add(-5 * time.Minute)
	e2.FinishedAt = e2.StartedAt.Add(time.Minute)
	require.NoError(t, repo.RecordTraffic(ctx, e2))
	e3 := e
	e3.EventID = uuid.NewString()
	e3.StartedAt = start.AddDate(0, 0, 1).Add(5 * time.Minute)
	e3.FinishedAt = e3.StartedAt.Add(time.Minute)
	require.NoError(t, repo.RecordTraffic(ctx, e3))
	other := e
	other.EventID = uuid.NewString()
	other.UserID = 9
	other.APIKeyID = 10
	require.NoError(t, repo.RecordTraffic(ctx, other))
	f := service.TrafficFilter{UserID: 1, Start: start, End: start.AddDate(0, 1, 0), Period: "month", Granularity: "day", Page: 1, PageSize: 1, Location: loc}
	report, err := repo.Traffic(ctx, f)
	require.NoError(t, err)
	require.EqualValues(t, 3, report.Requests)
	require.EqualValues(t, 300, report.UploadBytes)
	require.EqualValues(t, 600, report.DownloadBytes)
	require.EqualValues(t, 900, report.TotalBytes)
	require.Len(t, report.Trend, 31)
	require.EqualValues(t, 2, report.Trend[0].Requests)
	require.EqualValues(t, 1, report.Trend[1].Requests)
	require.Len(t, report.Items, 1)
	require.True(t, report.HasMore)
	require.NotNil(t, report.RecordedSince)
	f.Page = 2
	page, err := repo.Traffic(ctx, f)
	require.NoError(t, err)
	require.NotEqual(t, report.Items[0].EventID, page.Items[0].EventID)
	f.APIKeyID = 10
	report, err = repo.Traffic(ctx, f)
	require.NoError(t, err)
	require.Zero(t, report.Requests)
	require.Empty(t, report.Items)
	require.Nil(t, report.RecordedSince)
	// Event and rollup updates must roll back together if the latter fails.
	_, err = db.ExecContext(ctx, `ALTER TABLE user_traffic_hourly ADD CONSTRAINT reject_fixture CHECK (user_id <> 99)`)
	require.NoError(t, err)
	broken := e
	broken.EventID = uuid.NewString()
	broken.UserID = 99
	require.Error(t, repo.RecordTraffic(ctx, broken))
	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_traffic_events WHERE event_id=$1`, broken.EventID).Scan(&count))
	require.Zero(t, count)
	// DST's repeated hour remains two distinct buckets and is counted once each.
	ny, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	f = service.TrafficFilter{UserID: 1, Start: time.Date(2026, 11, 1, 0, 0, 0, 0, ny), End: time.Date(2026, 11, 2, 0, 0, 0, 0, ny), Period: "day", Granularity: "hour", Page: 1, PageSize: 20, Location: ny}
	for _, offset := range []time.Duration{90 * time.Minute, 150 * time.Minute} {
		item := e
		item.EventID = uuid.NewString()
		item.StartedAt = f.Start.Add(offset)
		item.FinishedAt = item.StartedAt.Add(time.Minute)
		require.NoError(t, repo.RecordTraffic(ctx, item))
	}
	report, err = repo.Traffic(ctx, f)
	require.NoError(t, err)
	require.Len(t, report.Trend, 25)
	require.EqualValues(t, 2, report.Requests)
	require.NotEqual(t, report.Trend[1].Bucket, report.Trend[2].Bucket)

	// A skipped civil day must neither hang bucket construction nor leave a
	// hole in the byte totals on the two adjacent existing dates.
	apia, err := time.LoadLocation("Pacific/Apia")
	require.NoError(t, err)
	f = service.TrafficFilter{UserID: 11, Start: time.Date(2011, 12, 1, 0, 0, 0, 0, apia), End: time.Date(2012, 1, 1, 0, 0, 0, 0, apia), Period: "month", Granularity: "day", Page: 1, PageSize: 20, Location: apia}
	for _, day := range []int{29, 31} {
		item := e
		item.EventID = uuid.NewString()
		item.UserID = 11
		item.StartedAt = time.Date(2011, 12, day, 12, 0, 0, 0, apia)
		item.FinishedAt = item.StartedAt.Add(time.Minute)
		require.NoError(t, repo.RecordTraffic(ctx, item))
	}
	report, err = repo.Traffic(ctx, f)
	require.NoError(t, err)
	require.Len(t, report.Trend, 30)
	require.EqualValues(t, 2, report.Requests)
	require.EqualValues(t, 600, report.TotalBytes)
	require.Equal(t, "2011-12-29", report.Trend[28].Bucket)
	require.Equal(t, "2011-12-31", report.Trend[29].Bucket)
	require.EqualValues(t, 1, report.Trend[28].Requests)
	require.EqualValues(t, 1, report.Trend[29].Requests)
}

// A falling/idle/rebounding series must remain independent in every view.
func TestTrafficPostgresTrendBucketsAreIndependent(t *testing.T) {
	dsn := os.Getenv("TRAFFIC_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TRAFFIC_TEST_POSTGRES_DSN not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	_, err = db.ExecContext(ctx, "SET search_path TO pg_temp")
	require.NoError(t, err)
	migration, err := os.ReadFile("../../migrations/272_user_traffic.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, strings.ReplaceAll(string(migration), "CREATE TABLE IF NOT EXISTS", "CREATE TEMP TABLE IF NOT EXISTS"))
	require.NoError(t, err)
	repo := newUsageLogRepositoryWithSQL(nil, db)
	loc, err := time.LoadLocation("Asia/Kathmandu")
	require.NoError(t, err)
	for i, period := range []string{"day", "week", "month", "year"} {
		t.Run(period, func(t *testing.T) {
			start := time.Date(2026, 10, 26, 0, 0, 0, 0, loc)
			end, granularity := start.AddDate(0, 0, 1), "hour"
			switch period {
			case "week":
				end, granularity = start.AddDate(0, 0, 7), "day"
			case "month":
				start = time.Date(2026, 10, 1, 0, 0, 0, 0, loc)
				end, granularity = start.AddDate(0, 1, 0), "day"
			case "year":
				start = time.Date(2026, 1, 1, 0, 0, 0, 0, loc)
				end, granularity = start.AddDate(1, 0, 0), "month"
			}
			filter := service.TrafficFilter{UserID: int64(20 + i), Start: start, End: end, Period: period, Granularity: granularity, Page: 1, PageSize: 20, Location: loc}
			buckets := service.TrafficBuckets(filter)
			amounts := []int64{900, 100, 0, 400}
			for index, amount := range amounts {
				if amount == 0 {
					continue
				}
				// Exercise raw boundary fragments and full UTC-hour rollups together.
				for _, offset := range []time.Duration{2 * time.Minute, 50 * time.Minute} {
					at := buckets[index].Start.Add(offset)
					event := service.TrafficEvent{EventID: uuid.NewString(), UserID: filter.UserID, APIKeyID: filter.UserID, Endpoint: "/v1/messages", Method: "POST", StartedAt: at, FinishedAt: at, UploadBytes: amount / 2, DownloadBytes: amount, StatusCode: 200, Outcome: "completed", RequestComplete: true, MeasurementVersion: 1}
					require.NoError(t, repo.RecordTraffic(ctx, event))
				}
			}
			report, err := repo.Traffic(ctx, filter)
			require.NoError(t, err)
			require.Len(t, report.Trend, len(buckets))
			var uploadSum, downloadSum int64
			for index, point := range report.Trend {
				expected := int64(0)
				if index < len(amounts) {
					expected = amounts[index]
				}
				require.Equal(t, buckets[index].Label, point.Bucket)
				require.Equal(t, expected, point.UploadBytes, "bucket %s", point.Bucket)
				require.Equal(t, expected*2, point.DownloadBytes, "bucket %s", point.Bucket)
				require.Equal(t, expected*3, point.TotalBytes, "bucket %s", point.Bucket)
				uploadSum += point.UploadBytes
				downloadSum += point.DownloadBytes
			}
			require.EqualValues(t, 1400, report.UploadBytes)
			require.EqualValues(t, 2800, report.DownloadBytes)
			require.EqualValues(t, 4200, report.TotalBytes)
			require.EqualValues(t, 6, report.Requests)
			require.Equal(t, uploadSum, report.UploadBytes)
			require.Equal(t, downloadSum, report.DownloadBytes)
		})
	}
}

func trafficOptimizationDatabase(t testing.TB) (*usageLogRepository, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("TRAFFIC_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TRAFFIC_TEST_POSTGRES_DSN not set")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	_, err = db.Exec("SET search_path TO pg_temp")
	require.NoError(t, err)
	migration, err := os.ReadFile("../../migrations/272_user_traffic.sql")
	require.NoError(t, err)
	_, err = db.Exec(strings.ReplaceAll(string(migration), "CREATE TABLE IF NOT EXISTS", "CREATE TEMP TABLE IF NOT EXISTS"))
	require.NoError(t, err)
	indexes, err := os.ReadFile("../../migrations/308_user_traffic_cursor_indexes_notx.sql")
	require.NoError(t, err)
	for range 2 {
		// Temporary tables do not need concurrent index creation.
		_, err = db.Exec(strings.ReplaceAll(string(indexes), "CONCURRENTLY ", ""))
		require.NoError(t, err)
	}
	return newUsageLogRepositoryWithSQL(nil, db), db
}

func TestTrafficPostgresCursorSurvivesNewAndTiedEvents(t *testing.T) {
	repo, db := trafficOptimizationDatabase(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 26, 12, 0, 0, 123456000, time.UTC)
	event := func(id string, user int64, started time.Time) service.TrafficEvent {
		return service.TrafficEvent{EventID: id, UserID: user, APIKeyID: user, StartedAt: started, FinishedAt: started, MeasurementVersion: 1, UploadBytes: 10, DownloadBytes: 20}
	}
	ids := []string{"00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-000000000003"}
	for _, id := range ids {
		require.NoError(t, repo.RecordTraffic(ctx, event(id, 1, at)))
	}
	f := service.TrafficFilter{UserID: 1, Start: at.Add(-time.Hour), End: at.Add(time.Hour), Period: "day", Granularity: "hour", Location: time.UTC, Page: 1, PageSize: 2, View: "details"}
	first, err := repo.Traffic(ctx, f)
	require.NoError(t, err)
	require.Equal(t, ids[2], first.Items[0].EventID)
	require.Equal(t, ids[1], first.Items[1].EventID)
	require.NotEmpty(t, first.NextCursor)
	require.Empty(t, first.Trend)
	require.Nil(t, first.RecordedSince)
	require.NoError(t, repo.RecordTraffic(ctx, event(uuid.NewString(), 1, at.Add(time.Minute))))
	require.NoError(t, repo.RecordTraffic(ctx, event(uuid.NewString(), 2, at.Add(-time.Minute))))
	f.Cursor, err = service.ParseTrafficCursor(first.NextCursor)
	require.NoError(t, err)
	second, err := repo.Traffic(ctx, f)
	require.NoError(t, err)
	require.Len(t, second.Items, 1)
	require.Equal(t, ids[0], second.Items[0].EventID)
	require.False(t, second.HasMore)
	require.Empty(t, second.NextCursor)
	// Details must remain available without the expensive rollup relation.
	_, err = db.Exec("DROP TABLE user_traffic_hourly")
	require.NoError(t, err)
	_, err = repo.Traffic(ctx, f)
	require.NoError(t, err)
	// Cursor position never grants access to another user's records.
	f.APIKeyID = 2
	foreign, err := repo.Traffic(ctx, f)
	require.NoError(t, err)
	require.Empty(t, foreign.Items)
}

func TestTrafficPostgresBatchAtomicityAndSummary(t *testing.T) {
	repo, db := trafficOptimizationDatabase(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	events := make([]service.TrafficEvent, 32)
	for i := range events {
		events[i] = service.TrafficEvent{EventID: uuid.NewString(), UserID: 1, APIKeyID: 2, StartedAt: at, FinishedAt: at, MeasurementVersion: 1, UploadBytes: 10, DownloadBytes: 20}
	}
	require.NoError(t, repo.RecordTrafficBatch(ctx, events))
	// Both duplicates within one batch and replay after an unknown commit are safe.
	require.NoError(t, repo.RecordTrafficBatch(ctx, append(events, events[0])))
	f := service.TrafficFilter{UserID: 1, Start: at, End: at.Add(time.Hour), Period: "day", Granularity: "hour", Location: time.UTC, Page: 1, PageSize: 1, View: "summary"}
	summary, err := repo.Traffic(ctx, f)
	require.NoError(t, err)
	require.EqualValues(t, 32, summary.Requests)
	require.EqualValues(t, 960, summary.TotalBytes)
	require.Empty(t, summary.Items)
	require.False(t, summary.HasMore)
	require.NotNil(t, summary.RecordedSince)
	// A single bad row must roll back every new event and rollup in the batch.
	_, err = db.Exec("ALTER TABLE user_traffic_hourly ADD CONSTRAINT reject_fixture CHECK (user_id <> 99)")
	require.NoError(t, err)
	good, bad := events[0], events[1]
	good.EventID, bad.EventID = uuid.NewString(), uuid.NewString()
	bad.UserID = 99
	require.Error(t, repo.RecordTrafficBatch(ctx, []service.TrafficEvent{good, bad}))
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM user_traffic_events WHERE event_id IN ($1,$2)", good.EventID, bad.EventID).Scan(&count))
	require.Zero(t, count)
	summary, err = repo.Traffic(ctx, f)
	require.NoError(t, err)
	require.EqualValues(t, 32, summary.Requests)
	// Batches may span users, keys, groups and hours; each dimension stays separate.
	other := make([]service.TrafficEvent, 3)
	for i := range other {
		other[i] = events[0]
		other[i].EventID = uuid.NewString()
		other[i].UserID = 2
		other[i].APIKeyID = int64(10 + i)
		other[i].GroupID = int64(i)
		other[i].StartedAt = at.Add(time.Duration(i) * time.Hour)
		other[i].FinishedAt = other[i].StartedAt
	}
	require.NoError(t, repo.RecordTrafficBatch(ctx, other))
	for _, e := range other {
		var requests, upload, download int64
		require.NoError(t, db.QueryRow(`SELECT requests,upload_bytes,download_bytes FROM user_traffic_hourly WHERE user_id=$1 AND api_key_id=$2 AND group_id=$3 AND bucket_start=$4`, e.UserID, e.APIKeyID, e.GroupID, e.StartedAt).Scan(&requests, &upload, &download))
		require.EqualValues(t, 1, requests)
		require.EqualValues(t, 10, upload)
		require.EqualValues(t, 20, download)
	}

}

func BenchmarkTrafficPostgresWrite(b *testing.B) {
	for _, batch := range []bool{false, true} {
		name := "single"
		if batch {
			name = "batch32"
		}
		b.Run(name, func(b *testing.B) {
			repo, _ := trafficOptimizationDatabase(b)
			ctx := context.Background()
			at := time.Now().UTC()
			b.ResetTimer()
			for range b.N {
				events := make([]service.TrafficEvent, 32)
				for i := range events {
					events[i] = service.TrafficEvent{EventID: uuid.NewString(), UserID: 1, APIKeyID: 2, StartedAt: at, FinishedAt: at, MeasurementVersion: 1, UploadBytes: 10, DownloadBytes: 20}
				}
				if batch {
					require.NoError(b, repo.RecordTrafficBatch(ctx, events))
				} else {
					for _, e := range events {
						require.NoError(b, repo.RecordTraffic(ctx, e))
					}
				}
			}
			b.ReportMetric(float64(b.N*32)/b.Elapsed().Seconds(), "events/s")
		})
	}
}

// Includes queue handoff, per-event durable spool, batch SQL, acknowledgements
// and a simulated outage. It never contacts an upstream provider.
func BenchmarkTrafficPostgresPipeline(b *testing.B) {
	for _, outage := range []bool{false, true} {
		name := "healthy"
		if outage {
			name = "recover_256"
		}
		b.Run(name, func(b *testing.B) {
			repo, _ := trafficOptimizationDatabase(b)
			ctx := context.Background()
			var failed atomic.Bool
			single := func(ctx context.Context, e service.TrafficEvent) error {
				if failed.Load() {
					return errors.New("simulated outage")
				}
				return repo.RecordTraffic(ctx, e)
			}
			batch := func(ctx context.Context, events []service.TrafficEvent) error {
				if failed.Load() {
					return errors.New("simulated outage")
				}
				return repo.RecordTrafficBatch(ctx, events)
			}
			dir := b.TempDir()
			d, err := service.NewTrafficDeliveryBatchedResolved(func() string { return dir }, single, batch)
			require.NoError(b, err)
			defer d.Stop()
			delivered := int64(0)
			wait := func(predicate func(service.TrafficDeliveryHealth) bool) {
				deadline := time.Now().Add(30 * time.Second)
				for time.Now().Before(deadline) {
					h, _ := d.Health(ctx)
					if h.DroppedEvents > 0 {
						b.Fatalf("lost events: %+v", h)
					}
					if predicate(h) {
						return
					}
					time.Sleep(time.Millisecond)
				}
				b.Fatal("traffic pipeline did not drain within 30s")
			}
			wait(func(h service.TrafficDeliveryHealth) bool { return h.StorageAvailable })
			b.ResetTimer()
			for range b.N {
				failed.Store(outage)
				at := time.Now().UTC()
				for range 256 {
					d.RecordTraffic(service.TrafficEvent{UserID: 1, APIKeyID: 2, StartedAt: at, FinishedAt: at, MeasurementVersion: 1, UploadBytes: 10, DownloadBytes: 20})
				}
				if outage {
					wait(func(h service.TrafficDeliveryHealth) bool { return h.PendingEvents == 256 })
					failed.Store(false)
				}
				delivered += 256
				wait(func(h service.TrafficDeliveryHealth) bool {
					return h.DeliveredEvents >= delivered && h.PendingEvents == 0
				})
			}
			b.ReportMetric(float64(b.N*256)/b.Elapsed().Seconds(), "events/s")
		})
	}
}
