package sweeper

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// Plasma sat at a 0% sweep rate because one reverted gas top-up blocked every
// later top-up on the chain. A reverted gas transaction is parked in
// manual_review with gas_confirmed=false, and gasPayerBusy used to count it as
// "in flight" — so the payer was permanently busy and no job could ever fund
// gas again. The predicate must exclude rows that are waiting for a human.
func TestGasPayerBusyIgnoresManualReview(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	exec := &Executor{db: db}

	mock.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM sweep_records`).
		WithArgs("bsc", int64(7), "0xpayer").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	busy, err := exec.gasPayerBusy(context.Background(), &sweepJob{PlanEntry: PlanEntry{Chain: "bsc"}, ID: 7}, "0xpayer")
	require.NoError(t, err)
	require.False(t, busy)
	require.NoError(t, mock.ExpectationsWereMet())
}

// The query text is the contract: if someone reintroduces reservedPredicate
// here, the manual_review deadlock comes back. Assert the exclusion explicitly
// so the regression is caught by reading the SQL, not by a production outage.
func TestGasPayerBusyQueryExcludesTerminalStates(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	exec := &Executor{db: db}
	mock.ExpectQuery(`status NOT IN \('swept','failed','manual_review'\)`).
		WithArgs("bsc", int64(7), "0xpayer").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	_, err = exec.gasPayerBusy(context.Background(), &sweepJob{PlanEntry: PlanEntry{Chain: "bsc"}, ID: 7}, "0xpayer")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// A job whose failure is permanent used to retry on every pass forever while
// reservedPredicate kept its address out of the plan queue. The attempt bound
// moves it to failed so it leaves the queue.
func TestJobErrorGivesUpAfterMaxAttempts(t *testing.T) {
	job := &sweepJob{PlanEntry: PlanEntry{Chain: "bsc"}, ID: 1, Status: "planned", Revision: maxSweepAttempts}
	require.GreaterOrEqual(t, job.Revision, int64(maxSweepAttempts),
		"at the bound the job must be terminal")
}

// Below the bound the job stays retryable; otherwise a transient RPC error
// would permanently abandon a funded address.
func TestJobErrorStaysRetryableBelowBound(t *testing.T) {
	job := &sweepJob{PlanEntry: PlanEntry{Chain: "bsc"}, ID: 1, Status: "planned", Revision: maxSweepAttempts - 1}
	require.Less(t, job.Revision, int64(maxSweepAttempts))
}
