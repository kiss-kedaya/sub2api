package scanner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

func TestOutgoingScannerUsesSenderTopicAndChecksExactReceipt(t *testing.T) {
	transfer := testTransfer()
	fixture := newRPCFixture(transfer)
	pool := fixture.pool(t)
	transfers, err := NewEVMScanner(pool).ScanOutgoingRange(t.Context(), transfer.Chain, 100, 105, []string{transfer.Token}, []string{transfer.From})
	if err != nil || len(transfers) != 1 {
		t.Fatal("outgoing transfer missed", transfers, err)
	}
	fixture.mu.Lock()
	filter := fixture.logRanges[0]
	fixture.mu.Unlock()
	if len(filter.Topics) != 2 || len(filter.Topics[1]) != 1 || filter.Topics[1][0] != padTopic(transfer.From) {
		t.Fatal("outflow incorrectly filtered by recipient", filter.Topics)
	}
	if err := VerifyObservedTransfer(t.Context(), pool, transfers[0]); err != nil {
		t.Fatal("valid mined outflow rejected", err)
	}
	for _, mutation := range []string{"forged amount", "wrong block", "reverted", "missing transfer"} {
		t.Run(mutation, func(t *testing.T) {
			f := newRPCFixture(transfer)
			switch mutation {
			case "forged amount":
				setFixtureMapValue(f.receipt, "logs", []evmLog{})
			case "wrong block":
				setFixtureMapValue(f.canonical, "hash", "0x"+strings.Repeat("aa", 32))
			case "reverted":
				setFixtureMapValue(f.receipt, "status", "0x0")
			case "missing transfer":
				setFixtureMapValue(f.receipt, "logs", []evmLog{})
			}
			if err := VerifyObservedTransfer(t.Context(), f.pool(t), transfer); err == nil {
				t.Fatal("unproven outflow accepted as a security incident")
			}
		})
	}
}

func TestSecurityGuardRunsBeforePendingCreditAndDatabaseWork(t *testing.T) {
	pg := &scannerMockPG{}
	worker, fixture := scannerLockFixture(t, pg)
	incident := errors.New("persistent wallet incident")
	worker.SetSecurityGuards(func(context.Context) error { return incident }, nil)
	if err := worker.RunOnce(t.Context()); !errors.Is(err, incident) {
		t.Fatal("pending reconciliation bypassed incident guard", err)
	}
	_, _, events, _ := pg.snapshot()
	if len(events) != 0 || fixture.callCount("eth_chainId") != 0 {
		t.Fatal("security guard reached payment work", events)
	}
}

func TestEveryRangeChecksOutflowsBeforeCreditAndCursorProgress(t *testing.T) {
	for _, lagging := range []bool{false, true} {
		t.Run(map[bool]string{false: "contiguous", true: "live_window"}[lagging], func(t *testing.T) {
			transfer := testTransfer()
			fixture := newRPCFixture(transfer)
			worker, mock, handler := workerFixture(t, fixture)
			if lagging {
				fixture.head = "0x3e8"
				worker.maxRange = 100
			}
			incident := errors.New("unapproved token outflow")
			var windows [][2]uint64
			worker.SetSecurityGuards(nil, func(_ context.Context, _ *EVMScanner, _ *rpc.Pool, _ ChainConfig, from, to uint64) error {
				windows = append(windows, [2]uint64{from, to})
				if len(windows) == 1 {
					return nil
				} // latest mined window precedes cursor work
				return incident
			})
			mock.ExpectQuery("SELECT last_block FROM crypto_scan_cursors").WithArgs("bsc").WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(100))
			if err := worker.ScanOnce(t.Context()); !errors.Is(err, incident) {
				t.Fatal("outflow guard did not stop range", err)
			}
			if len(windows) == 0 || len(handler.pending)+len(handler.processed) != 0 || fixture.callCount("eth_getLogs") != 0 {
				t.Fatal("credit/log scan ran past security incident", windows)
			}
		})
	}
}

func TestOutflowMonitoringRunsWhenNoDepositRecipientsExist(t *testing.T) {
	transfer := testTransfer()
	fixture := newRPCFixture(transfer)
	worker, mock, _ := workerFixture(t, fixture)
	worker.addresses = func(context.Context, string) ([]string, error) { return nil, nil }
	incident := errors.New("gas wallet outflow")
	calls := 0
	worker.SetSecurityGuards(nil, func(context.Context, *EVMScanner, *rpc.Pool, ChainConfig, uint64, uint64) error {
		calls++
		return incident
	})
	_ = mock // the head failure no longer stops the pass; the unmocked cursor read fails and the incident must still surface
	if err := worker.ScanOnce(t.Context()); !errors.Is(err, incident) || calls != 1 {
		t.Fatal("empty recipient pool hid gas wallet outflow", calls, err)
	}
}

func TestSecurityCursorNamespaceAndGuardOnlyPassNeverCreditDeposits(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	cursor := NewNamespacedCursorStore(db, "wallet-security:")
	mock.ExpectQuery("SELECT last_block FROM crypto_scan_cursors").WithArgs("wallet-security:bsc").WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(100))
	last, err := cursor.Load(t.Context(), "bsc")
	if err != nil || last != 100 {
		t.Fatal(last, err)
	}
	mock.ExpectExec("INSERT INTO crypto_scan_cursors").WithArgs("wallet-security:bsc", int64(105)).WillReturnResult(sqlmock.NewResult(1, 1))
	if err := cursor.Save(t.Context(), "bsc", 105); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	pg := &scannerMockPG{}
	worker, _ := scannerLockFixture(t, pg)
	if err := worker.RunGuardScan(t.Context()); err == nil {
		t.Fatal("guard scan accepted a deposit-credit handler")
	}
	worker.handler = nil
	worker.addresses = func(context.Context, string) ([]string, error) { return nil, nil }
	worker.SetSecurityGuards(nil, func(context.Context, *EVMScanner, *rpc.Pool, ChainConfig, uint64, uint64) error { return nil })
	if err := worker.RunGuardScan(t.Context()); err != nil {
		t.Fatal(err)
	}
	scannerAssertSteps(t, pg, "acquire", "load cursor", "save cursor", "release")
}
