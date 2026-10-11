package repository

import (
	"context"
	"database/sql/driver"
	"fmt"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
	"regexp"
	"strings"
	"testing"
)

func TestUsageRequestIDSearchSQL(t *testing.T) {
	for _, tc := range []struct {
		input     string
		condition string
		args      []any
	}{
		{"", "", nil}, {"   ", "", nil},
		{"req%_\\x", "(request_id ILIKE $2 OR upstream_request_id ILIKE $2)", []any{"%req\\%\\_\\\\x%"}},
		{"=client:550e8400-e29b-41d4-a716-446655440000", "(request_id IN ($2, $3, $4) OR upstream_request_id = $5)", []any{"550e8400-e29b-41d4-a716-446655440000", "client:550e8400-e29b-41d4-a716-446655440000", "local:550e8400-e29b-41d4-a716-446655440000", "client:550e8400-e29b-41d4-a716-446655440000"}},
		{"=upstream%_", "(request_id IN ($2, $3, $4) OR upstream_request_id = $5)", []any{"upstream%_", "client:upstream%_", "local:upstream%_", "upstream%_"}},
		{"^client:abc%_", "(request_id ILIKE $2 OR request_id ILIKE $3 OR request_id ILIKE $4 OR upstream_request_id ILIKE $5)", []any{"abc\\%\\_%", "client:abc\\%\\_%", "local:abc\\%\\_%", "client:abc\\%\\_%"}},
		{"  ^different-ID  ", "(request_id ILIKE $2 OR request_id ILIKE $3 OR request_id ILIKE $4 OR upstream_request_id ILIKE $5)", []any{"different-ID%", "client:different-ID%", "local:different-ID%", "different-ID%"}},
	} {
		t.Run(tc.input, func(t *testing.T) {
			conditions, args := appendUsageRequestIDCondition([]string{"user_id = $1"}, []any{int64(7)}, tc.input)
			want := []string{"user_id = $1"}
			if tc.condition != "" {
				want = append(want, tc.condition)
			}
			require.Equal(t, want, conditions)
			require.Equal(t, append([]any{int64(7)}, tc.args...), args)
		})
	}
}

func TestUsageRequestIDSearchPaginationSQL(t *testing.T) {
	for _, input := range []string{"=uuid-ID", "^abc", "fragment"} {
		for _, page := range []int{1, 3} {
			t.Run(input+string(rune('0'+page)), func(t *testing.T) {
				db, mock := newSQLMock(t)
				repo := &usageLogRepository{sql: db}
				conditions, args := appendUsageRequestIDCondition(nil, nil, input)
				query := fmt.Sprintf("SELECT %s FROM usage_logs WHERE %s ORDER BY id DESC LIMIT $%d OFFSET $%d", usageLogSelectColumns, conditions[0], len(args)+1, len(args)+2)
				// Assert the actual list path keeps both ID columns and the same predicate on later pages, without COUNT.
				driverArgs := make([]driver.Value, 0, len(args)+2)
				for _, arg := range append(args, 21, (page-1)*20) {
					driverArgs = append(driverArgs, arg)
				}
				mock.ExpectQuery("^" + regexp.QuoteMeta(query) + "$").WithArgs(driverArgs...).WillReturnRows(sqlmock.NewRows(strings.Split(usageLogSelectColumns, ", ")))
				logs, result, err := repo.ListWithFilters(context.Background(), pagination.PaginationParams{Page: page, PageSize: 20}, usagestats.UsageLogFilters{RequestID: input})
				require.NoError(t, err)
				require.Empty(t, logs)
				require.NotNil(t, result)
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}
