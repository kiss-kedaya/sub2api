package sweeper

import (
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// The incident fixture: nonce 2 was consumed by another transaction, no node
// can see the reserved bytes, and the record still holds the address.
func TestStaleSweepReleasesWhenMinedNonceAlreadyConsumed(t *testing.T) {
	e, mock, chain, entry := setupExecutor(t)
	chain.txs = map[string]bool{} // no node ever saw the reserved bytes
	chain.childNonce = "0x4"      // the account mined nonce 3 in the loop
	job := staleJobForTest(entry, 93)

	expectStaleRelease(mock, 93)
	released, err := e.releaseStaleTransaction(t.Context(), e.pools["bsc"], job, false, true)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if !released || job.Status != "failed" {
		t.Fatalf("a consumed nonce must release the record, got released=%v status=%s", released, job.Status)
	}
	for _, want := range []string{"stale pending released:", "nonce 2 already consumed", "latest nonce 4"} {
		if !strings.Contains(job.Error, want) {
			t.Fatalf("reason %q is missing %q", job.Error, want)
		}
	}
	chain.mu.Lock()
	defer chain.mu.Unlock()
	if len(chain.raws) != 0 {
		t.Fatalf("the consumed-nonce path must not rebroadcast, sent %d", len(chain.raws))
	}
}

// A transaction any endpoint can still see is never released, whatever the
// nonce says: it may confirm at any moment.
func TestStaleSweepKeepsTransactionANodeCanSee(t *testing.T) {
	e, _, chain, entry := setupExecutor(t)
	job := staleJobForTest(entry, 94)
	chain.txs[job.SweepHash] = true
	chain.childNonce = "0x4"

	released, err := e.releaseStaleTransaction(t.Context(), e.pools["bsc"], job, false, true)
	if err != nil || released {
		t.Fatalf("a visible transaction must stay reserved: released=%v err=%v", released, err)
	}
	if job.Status != "sweep_pending" {
		t.Fatalf("status changed to %s", job.Status)
	}
}

// The nonce slot is free: the transaction may simply be waiting in a mempool.
// Releasing here is what would cause a duplicate sweep.
func TestStaleSweepKeepsWhenNonceIsStillFree(t *testing.T) {
	e, _, chain, entry := setupExecutor(t)
	chain.childNonce, chain.childPending = "0x2", "0x2"
	job := staleJobForTest(entry, 95)

	released, err := e.releaseStaleTransaction(t.Context(), e.pools["bsc"], job, false, true)
	if err != nil || released {
		t.Fatalf("a free nonce must never be released: released=%v err=%v", released, err)
	}
	if job.Status != "sweep_pending" {
		t.Fatalf("status changed to %s", job.Status)
	}
}

// A young record whose nonce is only taken in the pending pool must be given
// time to confirm; nothing is released before the age threshold.
func TestStaleSweepKeepsYoungJobWithPoolConsumedNonce(t *testing.T) {
	e, mock, chain, entry := setupExecutor(t)
	chain.childNonce, chain.childPending = "0x2", "0x3"
	job := staleJobForTest(entry, 96)
	expectStaleAge(mock, 96, 5)

	released, err := e.releaseStaleTransaction(t.Context(), e.pools["bsc"], job, false, true)
	if err != nil || released {
		t.Fatalf("a young job must not be released: released=%v err=%v", released, err)
	}
}

// Old record, nonce taken in the pool, exact bytes replayed and still absent:
// only then is the reservation released. The reason has to be readable.
func TestStaleSweepReleasesOldRecordAfterReplayingExactBytes(t *testing.T) {
	e, mock, chain, entry := setupExecutor(t)
	chain.childNonce, chain.childPending = "0x2", "0x3"
	chain.sendError = "nonce too low" // the node refuses the stale nonce every time
	job := staleJobForTest(entry, 97)
	e.config.StalePendingAge = time.Minute
	e.config.StalePendingRebroadcastDelay = time.Nanosecond
	expectStaleAge(mock, 97, 120)
	expectStaleRelease(mock, 97)

	released, err := e.releaseStaleTransaction(t.Context(), e.pools["bsc"], job, false, true)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if !released || job.Status != "failed" {
		t.Fatalf("released=%v status=%s", released, job.Status)
	}
	if !strings.Contains(job.Error, "3 replays") || !strings.Contains(job.Error, "pending pool") {
		t.Fatalf("reason does not explain the replays: %q", job.Error)
	}
	chain.mu.Lock()
	defer chain.mu.Unlock()
	if len(chain.raws) != staleRebroadcasts {
		t.Fatalf("replayed %d times, want %d", len(chain.raws), staleRebroadcasts)
	}
	for _, raw := range chain.raws {
		if raw != job.SweepRaw {
			t.Fatalf("replay changed the signed bytes: %q", raw)
		}
	}
}

// An unreadable chain must never be read as "the transaction is gone".
func TestStaleSweepKeepsRecordWhenTheChainIsUnreadable(t *testing.T) {
	e, _, _, entry := setupExecutor(t)
	e.pools["bsc"] = rpc.NewPoolWithHTTPClient("", nil)
	job := staleJobForTest(entry, 98)

	released, err := e.releaseStaleTransaction(t.Context(), e.pools["bsc"], job, false, true)
	if err != nil || released {
		t.Fatalf("an unreadable chain must keep the reservation: released=%v err=%v", released, err)
	}
}

// The operator reconcile view is read-only: it may release a mined-past nonce
// but never rebroadcasts, even for an old record.
func TestStaleSweepReconcilePathNeverRebroadcasts(t *testing.T) {
	e, _, chain, entry := setupExecutor(t)
	chain.childNonce, chain.childPending = "0x2", "0x3"
	job := staleJobForTest(entry, 99)
	e.config.StalePendingAge = time.Minute

	released, err := e.releaseStaleTransaction(t.Context(), e.pools["bsc"], job, false, false)
	if err != nil || released {
		t.Fatalf("read-only reconcile must not release: released=%v err=%v", released, err)
	}
	chain.mu.Lock()
	defer chain.mu.Unlock()
	if len(chain.raws) != 0 {
		t.Fatalf("read-only reconcile broadcast %d times", len(chain.raws))
	}
}

// The release sentence is written by us and must survive the record sanitiser
// unchanged, otherwise the operator sees the generic failure text again.
func TestStaleReleaseReasonSurvivesRecordedErrorSanitising(t *testing.T) {
	reason := staleReleasePrefix + "tx absent on chain; nonce 2 already consumed (latest nonce 3); released"
	if got := rpc.SafeRecordedError(reason); got != reason {
		t.Fatalf("recorded reason was rewritten: %q -> %q", reason, got)
	}
	if !strings.Contains(reservedPredicate, "stale pending released:%") {
		t.Fatal("reservedPredicate must stop treating a released record as a reservation")
	}
}

func staleJobForTest(entry PlanEntry, id int64) *sweepJob {
	return &sweepJob{PlanEntry: entry, ID: id, Version: 1, Revision: 3,
		Status: "sweep_pending", Confirmations: 2,
		SweepNonce: "2", SweepRaw: "0xdeadbeef", SweepHash: "0xhash" + string(rune('a'+id%26))}
}

func expectStaleAge(mock sqlmock.Sqlmock, id, minutes int64) {
	mock.ExpectQuery(`SELECT GREATEST\(0, EXTRACT\(EPOCH FROM \(NOW\(\)-created_at\)\)/60\)::bigint`).
		WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{"age"}).AddRow(minutes))
}

func expectStaleRelease(mock sqlmock.Sqlmock, id int64) {
	args := []driver.Value{id, "failed"}
	for i := 0; i < 12; i++ {
		args = append(args, sqlmock.AnyArg())
	}
	mock.ExpectExec(`UPDATE sweep_records SET`).
		WithArgs(args...).
		WillReturnResult(sqlmock.NewResult(0, 1))
}
