package sweeper

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"math/big"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// These tests pin the multi-address planning failure mode: an address that is
// already reserved is back-pressure, not a database outage, and must never stop
// the addresses behind it in the same run.

func multiAddrEntry(address string, raw int64) PlanEntry {
	return PlanEntry{Chain: "bsc", Address: address, DerivationIndex: 42,
		Token: testToken, RawAmount: big.NewInt(raw), Decimals: 6}
}

// planFor turns eligible entries into the planner's balance snapshot.
func planFor(entries ...PlanEntry) *fakeReader {
	balances := make([]AddressBalance, 0, len(entries))
	for _, e := range entries {
		balances = append(balances, AddressBalance{Chain: e.Chain, Address: e.Address,
			Token: e.Token, DerivationIndex: e.DerivationIndex, Raw: e.RawAmount, Decimals: e.Decimals})
	}
	return &fakeReader{balances: map[string][]AddressBalance{"bsc": balances}}
}

// multiAddrAddress returns a distinct, syntactically valid child address.
func multiAddrAddress(n int) string { return fmt.Sprintf("0x%040d", n) }

func anyArgs(n int) []driver.Value {
	args := make([]driver.Value, n)
	for i := range args {
		args[i] = sqlmock.AnyArg()
	}
	return args
}

// expectInsertReserved simulates the NOT EXISTS guard matching: the address is
// already held by another job, so the insert returns no row.
func expectInsertReserved(m sqlmock.Sqlmock) {
	m.ExpectQuery("INSERT INTO sweep_records").WithArgs(anyArgs(9)...).WillReturnError(sql.ErrNoRows)
}

func expectInsertAccepted(m sqlmock.Sqlmock, id int64) {
	m.ExpectQuery("INSERT INTO sweep_records").WithArgs(anyArgs(9)...).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(id))
}

func expectAddressFree(m sqlmock.Sqlmock, address string, id int64) {
	m.ExpectQuery(`SELECT EXISTS .*lower\(from_address\)`).WithArgs("bsc", address, id).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
}

// TestCreateJobReportsReservedAddressWithoutPersistenceError is the unit-level
// regression: dedup back-pressure is not a database failure.
func TestCreateJobReportsReservedAddressWithoutPersistenceError(t *testing.T) {
	e, m, _, entry := setupExecutor(t)
	m.ExpectQuery("INSERT INTO sweep_records").WithArgs(anyArgs(9)...).WillReturnError(sql.ErrNoRows)

	job, err := e.createJob(t.Context(), entry)
	if job != nil {
		t.Fatalf("a refused insert returned a job: %+v", job)
	}
	if !errors.Is(err, errAddressReserved) {
		t.Fatalf("reserved address error = %v, want errAddressReserved", err)
	}
	var storage *persistenceError
	if errors.As(err, &storage) {
		t.Fatal("reserved address was reported as a persistence failure")
	}
}

// TestReservedAddressesDoNotAbortTheRun reproduces the production stall: with
// the old classification, each reserved address advanced the consecutive
// storage-failure counter, so the run gave up after five and every later
// address silently stayed unswept.
func TestReservedAddressesDoNotAbortTheRun(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	// One address more than the storage-failure budget is reserved, and the
	// fixture's own (signable) address follows them.
	reserved := maxConsecutiveStorageFailures + 1
	entries := make([]PlanEntry, 0, reserved+1)
	for i := 1; i <= reserved; i++ {
		entries = append(entries, multiAddrEntry(multiAddrAddress(i), 10_000_000))
	}
	entries = append(entries, entry)
	e.reader = planFor(entries...)
	e.config.MinPerAddress = map[string]float64{"bsc": 1}

	expectRunStart(m)
	for i := 0; i < reserved; i++ {
		expectInsertReserved(m)
	}
	expectInsertAccepted(m, 7)
	expectAddressFree(m, entry.Address, 7)
	expectWriteID(m, 7, "sweep_pending", nil, nil)
	expectWriteID(m, 7, "swept", nil, nil)
	expectUnlock(m)

	summary, err := e.Run(t.Context(), false)
	if err != nil {
		t.Fatalf("a reserved address failed the run: %v", err)
	}
	if summary.Swept != 1 || summary.Pending != reserved || summary.Planned != len(entries) ||
		len(summary.Entries) != len(entries) {
		t.Fatalf("partial plan was dropped: %+v", summary)
	}
	if len(c.raws) != 1 {
		t.Fatalf("the later address was never swept: sends=%d", len(c.raws))
	}
	// The single broadcast must be signed by the child that was still owed a
	// sweep, not by any of the reserved addresses.
	var tx types.Transaction
	if err := tx.UnmarshalBinary(common.FromHex(c.raws[0])); err != nil {
		t.Fatalf("decode broadcast: %v", err)
	}
	sender, err := types.Sender(types.LatestSignerForChainID(big.NewInt(56)), &tx)
	if err != nil || sender != common.HexToAddress(entry.Address) {
		t.Fatalf("swept sender = %v, want %s (%v)", sender, entry.Address, err)
	}
}

// TestStorageFailureBudgetStillStopsTheRun proves the fix only reclassified
// back-pressure: a genuinely broken database still bounds the run.
func TestStorageFailureBudgetStillStopsTheRun(t *testing.T) {
	e, m, _, _ := setupExecutor(t)
	entries := make([]PlanEntry, 0, maxConsecutiveStorageFailures+1)
	for i := 1; i <= maxConsecutiveStorageFailures+1; i++ {
		entries = append(entries, multiAddrEntry(multiAddrAddress(i), 10_000_000))
	}
	e.reader = planFor(entries...)
	e.config.MinPerAddress = map[string]float64{"bsc": 1}

	expectRunStart(m)
	for i := 0; i < maxConsecutiveStorageFailures; i++ {
		m.ExpectQuery("INSERT INTO sweep_records").WithArgs(anyArgs(9)...).
			WillReturnError(errors.New("disk is on fire"))
	}
	expectUnlock(m)

	summary, err := e.Run(t.Context(), false)
	if err == nil || summary.Swept != 0 {
		t.Fatalf("a broken database was reported as progress: %+v %v", summary, err)
	}
	if len(summary.Entries) != maxConsecutiveStorageFailures {
		t.Fatalf("storage failures were not bounded: %d entries", len(summary.Entries))
	}
}
