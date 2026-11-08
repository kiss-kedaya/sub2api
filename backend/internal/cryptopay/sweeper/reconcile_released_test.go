package sweeper

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestReconcileNeverResurrectsAReleasedStaleJob: a stale record whose
// reservation was deliberately released must not be turned back into a live
// reservation by reconcile — a newer job may already own the address. A late
// confirmation is still a real state correction and must be recorded.
func TestReconcileNeverResurrectsAReleasedStaleJob(t *testing.T) {
	cases := []struct {
		name       string
		mined      bool
		wantStatus string
		wantAction string
	}{
		{name: "absent stays released", mined: false, wantStatus: "failed", wantAction: ReconcileObserved},
		{name: "late confirmation is swept", mined: true, wantStatus: "swept", wantAction: ReconcileConfirmed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, mock, chain, entry := setupExecutor(t)
			chain.mined, chain.txs["0xhash"] = tc.mined, tc.mined
			released := &sweepJob{PlanEntry: entry, ID: 1, Version: 1, Destination: testDestination,
				Status: "failed", Confirmations: 2,
				Error: staleReleasePrefix + "tx absent after 3 replays; nonce 3 is taken in the pending pool; released"}
			released.SweepNonce, released.SweepRaw, released.SweepHash = "3", "0xraw", "0xhash"

			mock.ExpectQuery(`SELECT pg_try_advisory_lock`).WithArgs(sweepLockKey).
				WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
			mock.ExpectQuery(`SELECT id, chain, from_address`).WithArgs(int64(1)).
				WillReturnRows(jobRows(released))
			if tc.mined {
				expectWriteID(mock, 1, "swept", nil, nil)
			}
			mock.ExpectQuery(`SELECT pg_advisory_unlock`).WithArgs(sweepLockKey).
				WillReturnRows(sqlmock.NewRows([]string{"unlocked"}).AddRow(true))

			out, err := e.Reconcile(t.Context(), 1)
			if err != nil {
				t.Fatalf("reconcile failed: %v", err)
			}
			if out.Status != tc.wantStatus || out.Action != tc.wantAction || out.Message == "" {
				t.Fatalf("released record was rewritten: %+v", out)
			}
			if len(chain.signs) != 0 || len(chain.raws) != 0 {
				t.Fatalf("reconcile signed or broadcast: signs=%d sends=%d", len(chain.signs), len(chain.raws))
			}
		})
	}
}
