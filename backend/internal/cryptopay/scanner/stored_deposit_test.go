package scanner

import (
	"errors"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

func storedFor(tr Transfer, amount string) StoredDeposit {
	return StoredDeposit{Chain: tr.Chain, TxHash: tr.TxHash, LogIndex: tr.LogIndex, From: tr.From, To: tr.To,
		Token: tr.Token, Amount: amount, BlockNumber: tr.BlockNumber, BlockHash: tr.BlockHash}
}

// verifyStored binds the fixture pool to the chain first, exactly like the
// production caller, so the chain-id probe is part of every case.
func verifyStored(t *testing.T, f *rpcFixture, stored StoredDeposit) (Transfer, error) {
	t.Helper()
	pool, err := VerifiedChainPool(t.Context(), f.pool(t), stored.Chain)
	if err != nil {
		t.Fatalf("fixture pool must pass chain verification: %v", err)
	}
	return VerifyStoredDeposit(t.Context(), pool, stored, 6)
}

func TestVerifyStoredDepositAcceptsMatchingReceiptWithoutFinalityOrSecurityGates(t *testing.T) {
	tr := testTransfer()
	for _, amount := range []string{"1", "1.000000", "1.000000000000000000"} {
		t.Run(amount, func(t *testing.T) {
			f := newRPCFixture(tr)
			// A young block: neither safe nor finalized has reached it and the head
			// is only one block above it. Manual confirmation must not wait.
			f.safe = map[string]any{"number": "0x1"}
			f.finalized = map[string]any{"number": "0x1"}
			f.head = "0x66"
			got, err := verifyStored(t, f, storedFor(tr, amount))
			if err != nil {
				t.Fatalf("matching receipt rejected: %v", err)
			}
			if got.Amount.Cmp(tr.Amount) != 0 || got.LogIndex != tr.LogIndex || got.Decimals != 6 ||
				got.BlockNumber != tr.BlockNumber || got.BlockHash != tr.BlockHash || got.Chain != "bsc" {
				t.Fatalf("transfer was not rebuilt from the stored row: %+v", got)
			}
			if f.callCount("eth_getTransactionReceipt") != 1 || f.callCount("canonical") != 1 {
				t.Fatalf("receipt and canonical block must each be checked once: %v", f.calls)
			}
			for _, gate := range []string{"safe", "finalized", "eth_blockNumber", "eth_getLogs"} {
				if f.callCount(gate) != 0 {
					t.Fatalf("manual verification must not consult %s", gate)
				}
			}
		})
	}
}

func TestVerifyStoredDepositRefusesAnythingTheChainDoesNotConfirm(t *testing.T) {
	tr := testTransfer()
	other := tr
	other.LogIndex = 8
	for _, tc := range []struct {
		name   string
		mutate func(f *rpcFixture)
		stored func() StoredDeposit
		want   error
	}{
		{name: "reverted transaction", mutate: func(f *rpcFixture) { setFixtureMapValue(f.receipt, "status", "0x0") }, want: ErrTransactionReverted},
		{name: "receipt not found", mutate: func(f *rpcFixture) { f.receipt = nil }, want: ErrReceiptNotFound},
		{name: "block is no longer canonical", mutate: func(f *rpcFixture) {
			setFixtureMapValue(f.canonical, "hash", "0x"+strings.Repeat("99", 32))
		}, want: ErrNonCanonicalBlock},
		{name: "amount differs", stored: func() StoredDeposit { return storedFor(tr, "2") }, want: ErrTransferMismatch},
		{name: "recipient differs", stored: func() StoredDeposit {
			s := storedFor(tr, "1")
			s.To = "0x" + strings.Repeat("77", 20)
			return s
		}, want: ErrTransferMismatch},
		{name: "sender differs", stored: func() StoredDeposit {
			s := storedFor(tr, "1")
			s.From = "0x" + strings.Repeat("77", 20)
			return s
		}, want: ErrTransferMismatch},
		{name: "token differs", stored: func() StoredDeposit {
			s := storedFor(tr, "1")
			s.Token = "0x" + strings.Repeat("77", 20)
			return s
		}, want: ErrTransferMismatch},
		{name: "stored block number differs", stored: func() StoredDeposit {
			s := storedFor(tr, "1")
			s.BlockNumber++
			return s
		}, want: ErrTransferReincluded},
		{name: "stored block hash differs while chain content matches", stored: func() StoredDeposit {
			s := storedFor(tr, "1")
			s.BlockHash = "0x" + strings.Repeat("88", 32)
			return s
		}, want: ErrTransferReincluded},
		{name: "same transfer at another log index", stored: func() StoredDeposit {
			s := storedFor(tr, "1")
			s.LogIndex = 8
			return s
		}, want: ErrTransferReincluded},
		{name: "no stored block hash is a data problem not a reorg", stored: func() StoredDeposit {
			s := storedFor(tr, "1")
			s.BlockHash = ""
			return s
		}, want: ErrTransferMismatch},
		{name: "two identical logs make a relocation ambiguous", mutate: func(f *rpcFixture) {
			setFixtureMapValue(f.receipt, "logs", []evmLog{logFor(tr), logFor(other)})
		}, stored: func() StoredDeposit {
			s := storedFor(tr, "1")
			s.LogIndex = 9
			return s
		}, want: ErrTransferMismatch},
		{name: "more precision than the token has", stored: func() StoredDeposit { return storedFor(tr, "1.0000001") }, want: ErrStoredDepositInvalid},
		{name: "zero amount", stored: func() StoredDeposit { return storedFor(tr, "0") }, want: ErrStoredDepositInvalid},
		{name: "garbage amount", stored: func() StoredDeposit { return storedFor(tr, "abc") }, want: ErrStoredDepositInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRPCFixture(tr)
			if tc.mutate != nil {
				tc.mutate(f)
			}
			stored := storedFor(tr, "1")
			if tc.stored != nil {
				stored = tc.stored()
			}
			got, err := verifyStored(t, f, stored)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if got.Amount != nil || got.TxHash != "" {
				t.Fatalf("a refused deposit must not hand a transfer to the ledger: %+v", got)
			}
		})
	}
}

// Two identical Transfer logs in one transaction (a batch payout) are two
// deposits. Each stored log index must still verify on its own.
func TestVerifyStoredDepositAcceptsEachOfTwoIdenticalLogs(t *testing.T) {
	tr := testTransfer()
	second := tr
	second.LogIndex = 8
	for _, index := range []int{7, 8} {
		f := newRPCFixture(tr)
		setFixtureMapValue(f.receipt, "logs", []evmLog{logFor(tr), logFor(second)})
		stored := storedFor(tr, "1")
		stored.LogIndex = index
		got, err := verifyStored(t, f, stored)
		if err != nil || got.LogIndex != index {
			t.Fatalf("log %d: got %+v err=%v", index, got, err)
		}
	}
}

// A receipt for another EVM chain can be perfectly valid. The pool must refuse
// to talk to it before any receipt is requested.
func TestVerifiedChainPoolRefusesAWrongChainBeforeAnyReceiptCall(t *testing.T) {
	tr := testTransfer()
	f := newRPCFixture(tr)
	f.chainID = "0x1"
	if _, err := VerifiedChainPool(t.Context(), f.pool(t), "bsc"); err == nil {
		t.Fatal("an Ethereum node must not pass for BSC")
	}
	if f.callCount("eth_getTransactionReceipt") != 0 {
		t.Fatal("no receipt may be fetched from an unverified chain")
	}
	if _, err := VerifiedChainPool(t.Context(), nil, "bsc"); err == nil {
		t.Fatal("a missing pool must be an error")
	}
	if _, err := VerifiedChainPool(t.Context(), rpc.NewPool(""), "bsc"); err == nil {
		t.Fatal("a pool without endpoints must be an error")
	}
}

// Transport and node failures are inconclusive. They must not be reported as
// any of the sentinel "the chain says no" errors.
func TestVerifyStoredDepositRPCFailureIsNotAChainVerdict(t *testing.T) {
	tr := testTransfer()
	f := newRPCFixture(tr)
	pool, err := VerifiedChainPool(t.Context(), f.pool(t), "bsc")
	if err != nil {
		t.Fatal(err)
	}
	f.fail = "eth_getTransactionReceipt"
	_, err = VerifyStoredDeposit(t.Context(), pool, storedFor(tr, "1"), 6)
	if err == nil {
		t.Fatal("an unavailable node must not verify a deposit")
	}
	for _, verdict := range []error{ErrTransactionReverted, ErrReceiptNotFound, ErrNonCanonicalBlock, ErrTransferMismatch, ErrTransferReincluded, ErrStoredDepositInvalid} {
		if errors.Is(err, verdict) {
			t.Fatalf("RPC failure was reported as %v", verdict)
		}
	}
	if strings.Contains(rpc.SafeError(err), "127.0.0.1") {
		t.Fatalf("sanitised error leaked the endpoint: %q", rpc.SafeError(err))
	}
}
