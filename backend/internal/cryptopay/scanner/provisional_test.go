package scanner

import (
	"context"
	"math/big"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// instantLedger is a deposit handler that supports the instant-credit entry
// point. It mirrors the ledger contract: one row per identity, credit only on a
// pending -> credited transition.
type instantLedger struct {
	*ledgerFake
	provisionalCredits int
}

func (l *instantLedger) ProcessProvisional(_ context.Context, tr Transfer, _ int) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch l.status[l.key(tr)] {
	case "credited", "revoked":
		return false, nil
	case "", "pending":
		l.status[l.key(tr)] = "credited"
		l.balance.Add(l.balance, tr.Amount)
		l.provisionalCredits++
		return true, nil
	}
	return false, nil
}

func newInstantLedger() *instantLedger {
	return &instantLedger{ledgerFake: &ledgerFake{status: map[string]string{}, block: map[string]uint64{}, balance: new(big.Int)}}
}

// The very first verified observation of a not-yet-final transfer must move the
// balance, and no repeat of the same identity may move it again.
func TestFirstObservationCreditsBeforeFinalityOnce(t *testing.T) {
	tr := headTransfer() // block 108, below the chain's 5-block depth at head 110
	fixture := newRPCFixture(tr)
	w, _, _ := workerFixture(t, fixture)
	instant := newInstantLedger()
	w.handler = instant

	if err := w.processTransfer(t.Context(), w.pools["bsc"], w.chains[0], tr, 6); err != nil {
		t.Fatal(err)
	}
	if instant.provisionalCredits != 1 {
		t.Fatalf("provisional credits=%d want 1", instant.provisionalCredits)
	}
	if instant.balance.Cmp(tr.Amount) != 0 {
		t.Fatalf("balance=%s want %s", instant.balance, tr.Amount)
	}
	// Same transfer again (live window / replay): still exactly one credit.
	if err := w.processTransfer(t.Context(), w.pools["bsc"], w.chains[0], tr, 6); err != nil {
		t.Fatal(err)
	}
	if instant.provisionalCredits != 1 {
		t.Fatalf("provisional credits=%d after replay, want 1", instant.provisionalCredits)
	}
}

// Head observation is the path a fresh deposit takes on BSC/ETH: it must credit
// immediately once the receipt is verified, not only record a pending row.
func TestHeadObservationCreditsImmediatelyOnce(t *testing.T) {
	tr := headTransfer()
	fixture := newRPCFixture(tr)
	w, mock, _ := workerFixture(t, fixture)
	instant := newInstantLedger()
	w.handler = instant

	expectPendingConfirmations := func() {
		mock.ExpectExec(`(?s)UPDATE deposit_transactions SET confirmations = \$1.*status = 'pending'`).
			WithArgs(int64(2), tr.Chain, tr.TxHash, tr.LogIndex).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	// A credited observation skips the pending confirmation counter; the row is
	// already credited, not pending.
	if err := w.observeHead(t.Context(), w.pools["bsc"], w.chains[0], 110); err != nil {
		t.Fatal(err)
	}
	if instant.provisionalCredits != 1 {
		t.Fatalf("provisional credits=%d want 1", instant.provisionalCredits)
	}
	if got := instant.statusFor(tr); got != "credited" {
		t.Fatalf("observation status=%q want credited", got)
	}

	// The next pass knows the identity and must not credit it again.
	expectPendingConfirmations()
	if err := w.observeHead(t.Context(), w.pools["bsc"], w.chains[0], 110); err != nil {
		t.Fatal(err)
	}
	if instant.provisionalCredits != 1 {
		t.Fatalf("provisional credits=%d after second pass, want 1", instant.provisionalCredits)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// A provisional credit that the chain invalidated must be clawed back once, and
// the legacy manual-reconciliation alert must not fire for the same event.
func TestReorgRevokesProvisionalCreditOnce(t *testing.T) {
	tr := headTransfer()
	fixture := newRPCFixture(tr)
	setFixtureMapValue(fixture.canonical, "hash", "0x"+strings.Repeat("66", 32)) // block no longer canonical
	w, mock, _ := workerFixture(t, fixture)

	revokes, manualAlerts := 0, 0
	w.onProvisionalRevoke = func(_ context.Context, chain, txHash string, logIndex int, reason string) (bool, error) {
		revokes++
		if chain != tr.Chain || txHash != tr.TxHash || logIndex != tr.LogIndex {
			t.Fatalf("revoker identity mismatch: %s/%s/%d", chain, txHash, logIndex)
		}
		if reason != "reorg_noncanonical_block" {
			t.Fatalf("revoke reason=%q", reason)
		}
		return true, nil
	}
	w.onReorg = func(chain, txHash string) { manualAlerts++ }

	mock.ExpectQuery(`FROM deposit_transactions d`).WillReturnRows(observationRows().AddRow(
		tr.Chain, tr.TxHash, tr.LogIndex, tr.From, tr.To, tr.Token, "1", tr.BlockNumber, tr.BlockHash, "", true))
	if err := w.recheckCredited(t.Context()); err != nil {
		t.Fatal(err)
	}
	if revokes != 1 || manualAlerts != 0 {
		t.Fatalf("revokes=%d manualAlerts=%d want 1/0", revokes, manualAlerts)
	}
	// A clawed-back row is terminal; the updateObservation path must not run.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// A confirmation-gated credit that a reorg invalidates still needs the manual
// path: the automatic claw-back only owns provisional credits.
func TestReorgOfFinalCreditKeepsManualReconciliation(t *testing.T) {
	tr := headTransfer()
	fixture := newRPCFixture(tr)
	setFixtureMapValue(fixture.canonical, "hash", "0x"+strings.Repeat("66", 32))
	w, mock, _ := workerFixture(t, fixture)

	revokes, manualAlerts := 0, 0
	w.onProvisionalRevoke = func(context.Context, string, string, int, string) (bool, error) {
		revokes++
		return false, nil
	}
	w.onReorg = func(chain, txHash string) { manualAlerts++ }

	mock.ExpectQuery(`FROM deposit_transactions d`).WillReturnRows(observationRows().AddRow(
		tr.Chain, tr.TxHash, tr.LogIndex, tr.From, tr.To, tr.Token, "1", tr.BlockNumber, tr.BlockHash, "", false))
	expectObservationUpdate(mock, tr, "credited", "", "reverted", "reorg_noncanonical_block").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := w.recheckCredited(t.Context()); err != nil {
		t.Fatal(err)
	}
	if revokes != 0 || manualAlerts != 1 {
		t.Fatalf("revokes=%d manualAlerts=%d want 0/1", revokes, manualAlerts)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// Reaching final depth promotes a provisional credit without touching the
// balance a second time: finalization only stops the risk-window recheck.
func TestFinalityPromotesProvisionalCreditWithoutCreditingAgain(t *testing.T) {
	tr := headTransfer()
	fixture := newRPCFixture(tr)
	fixture.finalized = map[string]any{"number": "0x100"}
	fixture.head = "0x100"
	w, mock, handler := workerFixture(t, fixture)

	mock.ExpectQuery(`FROM deposit_transactions d`).WillReturnRows(observationRows().AddRow(
		tr.Chain, tr.TxHash, tr.LogIndex, tr.From, tr.To, tr.Token, "1", tr.BlockNumber, tr.BlockHash, "", true))
	mock.ExpectExec(`(?s)UPDATE deposit_transactions\s+SET provisional = FALSE, confirmations = \$4.*status = 'credited' AND provisional`).
		WithArgs(tr.Chain, tr.TxHash, tr.LogIndex, int64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// The recheck still rotates the row's attempt clock after a clean pass.
	expectObservationUpdate(mock, tr, "credited", "", "credited", "").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := w.recheckCredited(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(handler.processed) != 0 {
		t.Fatalf("finalization credited again: %d credits", len(handler.processed))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// statusFor reports the fake ledger's status for one observation.
func (l *instantLedger) statusFor(tr Transfer) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.status[l.key(tr)]
}
