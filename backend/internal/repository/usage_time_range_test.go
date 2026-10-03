package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestUsageTrendSecondRangeDoesNotUseWholeBucketAggregates(t *testing.T) {
	for _, granularity := range []string{"hour", "day"} {
		t.Run(granularity, func(t *testing.T) {
			db, mock := newSQLMock(t)
			repo := &usageLogRepository{sql: db}
			start := time.Date(2026, 10, 3, 4, 43, 28, 0, time.UTC)
			end := time.Date(2026, 10, 3, 5, 2, 9, 0, time.UTC)
			mock.ExpectQuery("FROM usage_logs").WithArgs(start, end).
				WillReturnRows(sqlmock.NewRows([]string{"date", "requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_tokens", "cost", "actual_cost"}))
			_, err := repo.getUsageTrendWithFilters(context.Background(), start, end, granularity, 0, 0, 0, 0, "", "", nil, nil, nil, "", nil, nil)
			require.NoError(t, err)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
