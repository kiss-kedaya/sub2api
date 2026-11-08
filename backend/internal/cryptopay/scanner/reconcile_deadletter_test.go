package scanner

import (
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

func deadLetterObservationFixture() depositObservation {
	return depositObservation{chain: "bsc", txHash: "0xabc", logIndex: 1,
		from: "0x" + strings.Repeat("22", 20), to: "0x" + strings.Repeat("33", 20),
		token: splitToken, amount: "1", blockNumber: 101,
		blockHash: "0x" + strings.Repeat("55", 32), status: "pending"}
}

// A pending deposit whose chain or token was removed from the configuration can
// never be verified again. It must leave the retry rotation with a durable
// reason instead of being rotated forever with an unchanged status.
func TestPendingObservationIsDeadLetteredWhenConfigurationNoLongerCoversIt(t *testing.T) {
	tests := []struct {
		name   string
		reason string
		chains []ChainConfig
		pools  map[string]*rpc.Pool
	}{
		{
			name:   "chain removed",
			reason: deadLetterChainRemoved,
			// No chain, no pool: nothing can verify this observation again.
		},
		{
			name:   "token no longer watched",
			reason: deadLetterTokenRemoved,
			chains: []ChainConfig{{Chain: "bsc", Tokens: []string{"0x" + strings.Repeat("99", 20)}}},
			pools:  map[string]*rpc.Pool{"bsc": rpc.NewPoolWithHTTPClient("http://127.0.0.1:1", nil)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			w := &Worker{db: db, chains: tt.chains, pools: tt.pools}
			observation := deadLetterObservationFixture()
			mock.ExpectExec(`UPDATE deposit_transactions\s+SET status = 'dead_letter', reason = \$1, last_attempt_at = NOW\(\)\s+WHERE chain = \$2 AND tx_hash = \$3 AND log_index = \$4\s+AND status = 'pending' AND reason = \$5`).
				WithArgs(tt.reason, observation.chain, observation.txHash, observation.logIndex, observation.reason).
				WillReturnResult(sqlmock.NewResult(0, 1))

			if err := w.reconcile(t.Context(), []depositObservation{observation}); err != nil {
				t.Fatalf("dead-lettering failed: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A row that a concurrent credit already resolved must keep its status: the
// compare-and-set refuses it and the pass is not an error.
func TestDeadLetterDoesNotOverwriteAConcurrentlyResolvedDeposit(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	w := &Worker{db: db}
	observation := deadLetterObservationFixture()
	mock.ExpectExec(`SET status = 'dead_letter'`).
		WithArgs(deadLetterChainRemoved, observation.chain, observation.txHash, observation.logIndex, observation.reason).
		WillReturnResult(sqlmock.NewResult(0, 0))
	if err := w.reconcile(t.Context(), []depositObservation{observation}); err != nil {
		t.Fatalf("losing the compare-and-set must not fail the pass: %v", err)
	}
}

// Credited rows are already final. They keep rotating so the risk window keeps
// being sampled; only a pending row is dead-lettered.
func TestCreditedObservationKeepsRotatingWhenChainIsGone(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	w := &Worker{db: db}
	observation := deadLetterObservationFixture()
	observation.status = "credited"
	mock.ExpectExec(`SET status = \$1, reason = \$2, last_attempt_at = NOW\(\)`).
		WithArgs("credited", "", observation.chain, observation.txHash, observation.logIndex, "credited", "").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := w.reconcile(t.Context(), []depositObservation{observation}); err != nil {
		t.Fatalf("credited rotation failed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// A chain that is still configured but has no usable RPC pool (endpoint not
// configured yet, or a failed reload) is recoverable. The row keeps rotating so
// a real deposit is not written off by a fixable operator problem.
func TestMissingPoolKeepsAPendingObservationRetryable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	observation := deadLetterObservationFixture()
	w := &Worker{db: db, chains: []ChainConfig{{Chain: "bsc", Tokens: []string{splitToken},
		Decimals: map[string]int{splitToken: 6}}}}
	mock.ExpectExec(`SET status = \$1, reason = \$2, last_attempt_at = NOW\(\)`).
		WithArgs("pending", "", observation.chain, observation.txHash, observation.logIndex, "pending", "").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := w.reconcile(t.Context(), []depositObservation{observation}); err != nil {
		t.Fatalf("missing pool must not dead-letter the deposit: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
