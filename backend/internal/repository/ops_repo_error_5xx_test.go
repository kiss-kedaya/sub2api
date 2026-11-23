package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// 5xx 分源计数：SQL 必须同时按 error_source 分组，且只统计 status_code>=500
// 的非 count_tokens 行；桶序号来自 created_at 相对起点，缺失桶由 Go 补零。
func TestCountErrorLogs5xxBySource_SplitsSourcesAndFillsBuckets(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	start := time.Date(2026, time.November, 23, 9, 30, 0, 0, time.UTC)
	end := start.Add(35 * time.Minute)

	rows := sqlmock.NewRows([]string{"bucket_idx", "upstream_5xx", "gateway_5xx"}).
		AddRow(int64(0), int64(120), int64(3)).
		AddRow(int64(6), int64(9506), int64(26))

	mock.ExpectQuery(regexp.QuoteMeta("COALESCE(error_source, '') = 'upstream_http'")).
		WithArgs(start, int64(300), end, "", int64(0)).
		WillReturnRows(rows)

	repo := &opsRepository{db: db}
	buckets, err := repo.CountErrorLogs5xxBySource(context.Background(), start, end, 300, "", nil)
	require.NoError(t, err)
	require.Len(t, buckets, 7, "35 分钟 / 5 分钟 = 7 个桶")
	require.NoError(t, mock.ExpectationsWereMet())

	require.Equal(t, start, buckets[0].BucketStart)
	require.EqualValues(t, 120, buckets[0].Upstream5xx)
	require.EqualValues(t, 3, buckets[0].Gateway5xx)

	// 中间桶补零，保证突增基线序列完整。
	for _, index := range []int{1, 2, 3, 4, 5} {
		require.Equal(t, start.Add(time.Duration(index*300)*time.Second), buckets[index].BucketStart)
		require.Zero(t, buckets[index].Upstream5xx)
		require.Zero(t, buckets[index].Gateway5xx)
	}

	require.Equal(t, start.Add(30*time.Minute), buckets[6].BucketStart)
	require.EqualValues(t, 9506, buckets[6].Upstream5xx)
	require.EqualValues(t, 26, buckets[6].Gateway5xx)
}

func TestCountErrorLogs5xxBySource_AppliesScopeFilters(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	start := time.Date(2026, time.November, 23, 9, 55, 0, 0, time.UTC)
	end := start.Add(5 * time.Minute)
	groupID := int64(42)

	mock.ExpectQuery(regexp.QuoteMeta("($4 = '' OR platform = $4)")).
		WithArgs(start, int64(300), end, "openai", groupID).
		WillReturnRows(sqlmock.NewRows([]string{"bucket_idx", "upstream_5xx", "gateway_5xx"}))

	repo := &opsRepository{db: db}
	buckets, err := repo.CountErrorLogs5xxBySource(context.Background(), start, end, 300, " openai ", &groupID)
	require.NoError(t, err)
	require.Len(t, buckets, 1)
	require.EqualValues(t, 0, buckets[0].Upstream5xx)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCountErrorLogs5xxBySource_ExcludesCountTokensAndOldStatuses(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	start := time.Date(2026, time.November, 23, 9, 55, 0, 0, time.UTC)
	end := start.Add(5 * time.Minute)

	// 查询里必须带 5xx 与 count_tokens 过滤，否则指标会把 4xx/计数请求混进来。
	mock.ExpectQuery(regexp.QuoteMeta("COALESCE(status_code, 0) >= 500")).
		WithArgs(start, int64(300), end, "", int64(0)).
		WillReturnRows(sqlmock.NewRows([]string{"bucket_idx", "upstream_5xx", "gateway_5xx"}).
			AddRow(int64(0), int64(2), int64(1)))

	repo := &opsRepository{db: db}
	_, err = repo.CountErrorLogs5xxBySource(context.Background(), start, end, 300, "", nil)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCountErrorLogs5xxBySource_RejectsInvalidWindow(t *testing.T) {
	repo := &opsRepository{}
	now := time.Now().UTC()

	_, err := repo.CountErrorLogs5xxBySource(context.Background(), now, now, 300, "", nil)
	require.Error(t, err, "空窗口必须报错而不是返回 0")

	_, err = repo.CountErrorLogs5xxBySource(context.Background(), now.Add(-time.Minute), now, 0, "", nil)
	require.Error(t, err, "非法桶宽必须报错")
}

func TestCountErrorLogs5xxBySource_PropagatesQueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	start := time.Date(2026, time.November, 23, 9, 55, 0, 0, time.UTC)
	end := start.Add(5 * time.Minute)
	mock.ExpectQuery(regexp.QuoteMeta("FROM ops_error_logs")).
		WithArgs(start, int64(300), end, "", int64(0)).
		WillReturnError(context.DeadlineExceeded)

	repo := &opsRepository{db: db}
	_, err = repo.CountErrorLogs5xxBySource(context.Background(), start, end, 300, "", nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// 查询谓词必须与 338 迁移的部分索引谓词一致，否则部分索引不会被使用。
func TestCountErrorLogs5xxBySource_MatchesPartialIndexPredicate(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	start := time.Date(2026, time.November, 23, 9, 55, 0, 0, time.UTC)
	end := start.Add(5 * time.Minute)

	mock.ExpectQuery(".*").WithArgs(start, int64(300), end, "", int64(0)).
		WillReturnRows(sqlmock.NewRows([]string{"bucket_idx", "upstream_5xx", "gateway_5xx"}))
	repo := &opsRepository{db: db}
	_, err = repo.CountErrorLogs5xxBySource(context.Background(), start, end, 300, "", nil)
	require.NoError(t, err)

	// 查询常量必须与 338 迁移的部分索引谓词逐字一致。
	require.Contains(t, countErrorLogs5xxBySourceQuery, "COALESCE(status_code, 0) >= 500")
	require.Contains(t, countErrorLogs5xxBySourceQuery, "COALESCE(is_count_tokens, false) = false")
	require.Contains(t, countErrorLogs5xxBySourceQuery, "COALESCE(error_source, '') = 'upstream_http'")
	require.Contains(t, countErrorLogs5xxBySourceQuery, "COALESCE(error_source, '') <> 'upstream_http'")
}
