package usagestats

import (
	"context"
	"errors"

	"github.com/lib/pq"
)

// pgQueryCanceled is PostgreSQL's SQLSTATE for a statement the server aborted,
// which includes statement_timeout expiry and a cancelled connection.
const pgQueryCanceled = "57014"

// IsStatsTimeout reports whether err is a cancelled or timed-out usage
// aggregation: the caller's deadline expired, the client went away, or
// PostgreSQL aborted the statement. Admin handlers use it to answer with a
// readable 503 (narrow the window) instead of a generic 500.
func IsStatsTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == pq.ErrorCode(pgQueryCanceled)
}
