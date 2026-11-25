package usagestats

import (
	"context"
	"errors"
	"strings"

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

// pgCancelMessage returns the server's message of a PostgreSQL query_canceled (57014) error.
func pgCancelMessage(err error) (string, bool) {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == pq.ErrorCode(pgQueryCanceled) {
		return pqErr.Message, true
	}
	return "", false
}

// IsStatementTimeout reports whether PostgreSQL aborted the statement because it ran past statement_timeout
// ("canceling statement due to statement timeout"): the same statement will run out of time again.
// Unlike IsStatsTimeout it does not match a statement somebody stopped.
func IsStatementTimeout(err error) bool {
	msg, ok := pgCancelMessage(err)
	return ok && strings.Contains(msg, "statement timeout")
}

// IsStatementCanceledByRequest reports a query_canceled error whose message says it was cancelled on request
// ("canceling statement due to user request"): an operator's pg_cancel_backend, a migration's reader canceller,
// or the driver relaying a cancelled context. It says nothing about the next attempt.
func IsStatementCanceledByRequest(err error) bool {
	msg, ok := pgCancelMessage(err)
	return ok && strings.Contains(msg, "user request")
}
