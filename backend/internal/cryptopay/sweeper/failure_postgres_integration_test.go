//go:build integration

package sweeper

import (
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// The failure reason is a diagnostic: it must name where a job stopped without
// touching what the state machine reads. revision is the attempt counter,
// updated_at is how long the job has been stuck, and a finished row is never
// rewritten. sqlmock can only check the statement text; this checks what
// PostgreSQL does with it.
func TestPostgresFailureReasonIsADiagnosticOnly(t *testing.T) {
	db := isolatedSweepPostgres(t)
	e, _, _, entry := setupExecutor(t)
	e.db = db
	ctx := t.Context()

	job, err := e.createJob(ctx, entry)
	if err != nil {
		t.Fatal(err)
	}
	stuckSince := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Microsecond)
	if _, err := db.ExecContext(ctx, `UPDATE sweep_records SET updated_at=$2, revision=3 WHERE id=$1`, job.ID, stuckSince); err != nil {
		t.Fatal(err)
	}
	job.Revision = 3

	read := func() (reason, status string, revision int64, updated time.Time) {
		t.Helper()
		if err := db.QueryRowContext(ctx, `SELECT error,status,revision,updated_at FROM sweep_records WHERE id=$1`, job.ID).
			Scan(&reason, &status, &revision, &updated); err != nil {
			t.Fatal(err)
		}
		return reason, status, revision, updated
	}

	cause := atStep(StepBalanceRead, rpc.NewTransportFailure(errors.New("fixture")))
	e.recordFailureReason(ctx, job, cause)
	reason, status, revision, updated := read()
	if reason != "balance_read: RPC connection failed" || status != "planned" || revision != 3 || !updated.Equal(stuckSince) {
		t.Fatalf("the reason write touched the state: reason=%q status=%s revision=%d updated=%v (want %v)",
			reason, status, revision, updated, stuckSince)
	}
	// What was written is what the sanitiser keeps on read.
	if got := rpc.SafeRecordedError(reason); got != reason {
		t.Fatalf("stored reason is rewritten on read: %q", got)
	}

	// A writer that is out of date (the row moved on) cannot overwrite the reason.
	stale := *job
	stale.Revision, stale.Error = 2, ""
	e.recordFailureReason(ctx, &stale, atStep(StepGasPrice, cause))
	if again, _, _, _ := read(); again != reason {
		t.Fatalf("a stale revision overwrote the reason: %q", again)
	}

	// A finished row is never rewritten.
	if _, err := db.ExecContext(ctx, `UPDATE sweep_records SET status='failed', error='stale pending released: x' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	finished := *job
	finished.Error = ""
	e.recordFailureReason(ctx, &finished, atStep(StepNonce, cause))
	if final, status, _, _ := read(); final != "stale pending released: x" || status != "failed" {
		t.Fatalf("a finished row was rewritten: %q %s", final, status)
	}
}
