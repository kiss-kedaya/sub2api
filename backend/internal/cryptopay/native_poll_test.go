package cryptopay

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/explorer"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
)

var (
	pollDeposit     = "0x1111111111111111111111111111111111111111"
	pollHot         = "0x2222222222222222222222222222222222222222"
	pollSender      = "0x3333333333333333333333333333333333333333"
	pollExternal    = "0x4444444444444444444444444444444444444444"
	pollChild       = "0x5555555555555555555555555555555555555555"
	pollUSDToken    = "0x55d398326f99059ff775485246999027b3197955"
	pollHashDeposit = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	pollHashOutflow = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	pollHashGas     = "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

type listCall struct {
	chain   string
	address string
	start   uint64
	end     uint64
}

type fakeLister struct {
	rows  map[string][]explorer.Tx
	err   error
	calls []listCall
}

func (f *fakeLister) TxList(_ context.Context, chain, address string, start, end uint64) ([]explorer.Tx, error) {
	f.calls = append(f.calls, listCall{chain: chain, address: address, start: start, end: end})
	if f.err != nil {
		return nil, f.err
	}
	return f.rows[strings.ToLower(address)], nil
}

type memCursor struct {
	values map[string]uint64
	saves  int
}

func (m *memCursor) Load(_ context.Context, key string) (uint64, error) {
	return m.values[key], nil
}

func (m *memCursor) Save(_ context.Context, key string, lastBlock uint64) error {
	if m.values == nil {
		m.values = map[string]uint64{}
	}
	m.values[key] = lastBlock
	m.saves++
	return nil
}

type fakeSink struct {
	deposits []scanner.Transfer
	outflows []scanner.Transfer
	err      error
}

func (f *fakeSink) ProcessNativeDeposit(_ context.Context, _ scanner.ChainConfig, t scanner.Transfer) error {
	if f.err != nil {
		return f.err
	}
	f.deposits = append(f.deposits, t)
	return nil
}

func (f *fakeSink) ScanNativeOutflow(_ context.Context, _ scanner.ChainConfig, t scanner.Transfer) error {
	f.outflows = append(f.outflows, t)
	return nil
}

func pollFixture(head uint64, lister *fakeLister, sink *fakeSink) (*nativePoll, *memCursor) {
	cursor := &memCursor{values: map[string]uint64{}}
	poll := newNativePoll([]scanner.ChainConfig{{Chain: "bsc", Native: true, NativeDecimals: 18, Confirmations: 3}})
	poll.lister = lister
	poll.cursor = cursor
	poll.sink = sink
	poll.addresses = func(context.Context, string) ([]string, error) { return []string{pollDeposit}, nil }
	poll.hotWallet = func(context.Context) (string, error) { return pollHot, nil }
	poll.head = func(context.Context, string) (uint64, error) { return head, nil }
	poll.report = func(string, error) {}
	return poll, cursor
}

func txRow(hash, from, to string, value *big.Int, block uint64) explorer.Tx {
	lowered := strings.ToLower(to)
	return explorer.Tx{Hash: hash, From: from, To: &lowered, Value: value, BlockNumber: block}
}

func TestNativePollAdvancesCursorIncrementally(t *testing.T) {
	amount := big.NewInt(1_000_000_000_000_000_000)
	lister := &fakeLister{rows: map[string][]explorer.Tx{
		pollDeposit: {txRow(pollHashDeposit, pollSender, pollDeposit, amount, 900)},
	}}
	sink := &fakeSink{}
	poll, cursor := pollFixture(1000, lister, sink)

	if err := poll.runOnce(t.Context()); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	// Head 1000, 3 confirmations: the first window is the bounded lookback
	// behind block 997 up to the head, so an unconfirmed payment is seen too.
	first := lister.calls[0]
	if first.start != 997-nativeFirstRunLookback || first.end != 1000 {
		t.Fatalf("first window = %d..%d", first.start, first.end)
	}
	if got := cursor.values["in:bsc:"+pollDeposit]; got != 997 {
		t.Fatalf("cursor after first poll = %d", got)
	}
	if len(sink.deposits) != 1 {
		t.Fatalf("deposits = %d, want 1", len(sink.deposits))
	}
	if !sink.deposits[0].Native || sink.deposits[0].LogIndex != scanner.NativeLogIndex ||
		sink.deposits[0].Amount.Cmp(amount) != 0 {
		t.Fatalf("deposit = %+v", sink.deposits[0])
	}

	// A later poll asks only for the unread head plus a bounded overlap for a
	// late-indexed row; it never restarts from the beginning.
	lister.calls = nil
	poll.head = func(context.Context, string) (uint64, error) { return 1010, nil }
	if err := poll.runOnce(t.Context()); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	second := lister.calls[0]
	wantStart := uint64(997 + 1 - nativePollOverlap)
	if second.start != wantStart || second.end != 1010 {
		t.Fatalf("incremental window = %d..%d, want %d..1010", second.start, second.end, wantStart)
	}
	if second.start <= first.start {
		t.Fatalf("incremental window did not advance: %d -> %d", first.start, second.start)
	}
	if got := cursor.values["in:bsc:"+pollDeposit]; got != 1007 {
		t.Fatalf("cursor after second poll = %d", got)
	}
}

func TestNativePollObservesUnconfirmedDepositsWithoutAdvancingCursor(t *testing.T) {
	// The query reaches the head so a payment shows up as pending immediately,
	// but the cursor must stop at the confirmed height and re-read the block
	// until finality (the pending record itself is durable).
	amount := big.NewInt(1_000_000_000_000_000_000)
	lister := &fakeLister{rows: map[string][]explorer.Tx{
		pollDeposit: {txRow(pollHashDeposit, pollSender, pollDeposit, amount, 1000)},
	}}
	sink := &fakeSink{}
	poll, cursor := pollFixture(1000, lister, sink)
	if err := poll.runOnce(t.Context()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(sink.deposits) != 1 {
		t.Fatalf("an unconfirmed payment was not observed: %d", len(sink.deposits))
	}
	if got := cursor.values["in:bsc:"+pollDeposit]; got != 997 {
		t.Fatalf("cursor = %d, want the confirmed height 997", got)
	}
}

func TestNativePollDoesNotCreditTheSameTxTwice(t *testing.T) {
	amount := big.NewInt(5)
	row := txRow(pollHashDeposit, pollSender, pollDeposit, amount, 900)
	// A provider can repeat a row (overlapping windows, a retried answer). The
	// poller must hand it to the ledger once; the ledger unique key is the
	// second line of defence.
	lister := &fakeLister{rows: map[string][]explorer.Tx{pollDeposit: {row, row}}}
	sink := &fakeSink{}
	poll, _ := pollFixture(1000, lister, sink)
	if err := poll.runOnce(t.Context()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(sink.deposits) != 1 {
		t.Fatalf("deposits = %d, want 1", len(sink.deposits))
	}
}

func TestNativePollKeepsCursorWhenTheProviderFails(t *testing.T) {
	lister := &fakeLister{err: errors.New("provider down")}
	sink := &fakeSink{}
	poll, cursor := pollFixture(1000, lister, sink)
	reported := ""
	poll.report = func(chain string, _ error) { reported = chain }

	err := poll.runOnce(t.Context())
	if err == nil {
		t.Fatal("a provider failure was reported as a complete poll")
	}
	if reported != "bsc" {
		t.Fatalf("failure was not reported: %q", reported)
	}
	if cursor.saves != 0 || len(cursor.values) != 0 {
		t.Fatalf("cursor advanced over an unread range: %+v", cursor.values)
	}
	if len(sink.deposits) != 0 {
		t.Fatalf("failed poll produced deposits: %d", len(sink.deposits))
	}
}

func TestNativePollKeepsCursorWhenVerificationFails(t *testing.T) {
	amount := big.NewInt(7)
	lister := &fakeLister{rows: map[string][]explorer.Tx{
		pollDeposit: {txRow(pollHashDeposit, pollSender, pollDeposit, amount, 900)},
	}}
	sink := &fakeSink{err: errors.New("receipt not final")}
	poll, cursor := pollFixture(1000, lister, sink)
	if err := poll.runOnce(t.Context()); err == nil {
		t.Fatal("an unprocessed row was reported as complete")
	}
	if cursor.saves != 0 {
		t.Fatalf("cursor advanced past an unprocessed deposit: %+v", cursor.values)
	}
}

func TestNativePollDetectsHotWalletNativeOutflow(t *testing.T) {
	amount := big.NewInt(1_000_000_000_000_000_000)
	zero := big.NewInt(0)
	lister := &fakeLister{rows: map[string][]explorer.Tx{
		// Incoming rows exist too: the outflow phase must not confuse them.
		pollDeposit: {txRow(pollHashDeposit, pollSender, pollDeposit, amount, 900)},
		pollHot: {
			// The hot wallet spending native coin to an address we do not own.
			txRow(pollHashOutflow, pollHot, pollExternal, amount, 910),
			// A gas top-up to our own child address: the poller still reports
			// it, and scanNativeOutflow treats it as internal movement.
			txRow(pollHashGas, pollHot, pollChild, big.NewInt(1), 911),
			// A token call pays no native value and must never be an outflow.
			txRow(pollHashDeposit, pollHot, pollUSDToken, zero, 912),
		},
	}}
	sink := &fakeSink{}
	poll, _ := pollFixture(1000, lister, sink)
	if err := poll.runOnce(t.Context()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(sink.outflows) != 2 {
		t.Fatalf("outflows = %d, want the two positive native spends: %+v", len(sink.outflows), sink.outflows)
	}
	if sink.outflows[0].TxHash != pollHashOutflow || sink.outflows[0].To != pollExternal {
		t.Fatalf("first outflow = %+v", sink.outflows[0])
	}
	// An outgoing row never creates a deposit.
	for _, deposit := range sink.deposits {
		if deposit.TxHash == pollHashOutflow {
			t.Fatal("an outflow was credited as a deposit")
		}
	}
}

func TestNativePollDetectsNativeOutflowFromAWatchedAddress(t *testing.T) {
	// One account-API answer carries both directions, so a watched deposit
	// address spending its native coin is caught by the same request that
	// would have looked for its deposits.
	amount := big.NewInt(1_000_000_000_000_000_000)
	lister := &fakeLister{rows: map[string][]explorer.Tx{
		pollDeposit: {
			txRow(pollHashDeposit, pollSender, pollDeposit, big.NewInt(5), 900),
			txRow(pollHashOutflow, pollDeposit, pollExternal, amount, 901),
		},
	}}
	sink := &fakeSink{}
	poll, _ := pollFixture(1000, lister, sink)
	if err := poll.runOnce(t.Context()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(sink.deposits) != 1 || sink.deposits[0].TxHash != pollHashDeposit {
		t.Fatalf("deposits = %+v", sink.deposits)
	}
	if len(sink.outflows) != 1 || sink.outflows[0].TxHash != pollHashOutflow {
		t.Fatalf("outflows = %+v", sink.outflows)
	}
}

func TestNativePollLeavesTheHotAddressToItsOwnCursor(t *testing.T) {
	// The hot wallet is also returned by the active-address list. Its spend
	// must be read through the dedicated hot cursor, not twice.
	amount := big.NewInt(1)
	lister := &fakeLister{rows: map[string][]explorer.Tx{
		pollHot: {txRow(pollHashOutflow, pollHot, pollExternal, amount, 900)},
	}}
	sink := &fakeSink{}
	poll, _ := pollFixture(1000, lister, sink)
	poll.addresses = func(context.Context, string) ([]string, error) { return []string{pollHot}, nil }
	if err := poll.runOnce(t.Context()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	// The hot query below owns the address, so it is reported exactly once.
	if len(sink.outflows) != 1 {
		t.Fatalf("hot wallet outflow reported %d times", len(sink.outflows))
	}
}

func TestNativeOutflowVerdictUsesTheErc20Rule(t *testing.T) {
	watch := []string{pollHot, pollChild}
	amount := big.NewInt(1_000_000_000_000_000_000)
	native := scanner.Transfer{Native: true, Token: scanner.NativeToken, Amount: amount, To: pollExternal}

	if !nativeOutflowVerdict(native, watch, false) {
		t.Fatal("an unauthorised external native outflow was not flagged")
	}
	if nativeOutflowVerdict(native, watch, true) {
		t.Fatal("an authorised native sweep was flagged")
	}
	topUp := native
	topUp.To = pollChild
	if nativeOutflowVerdict(topUp, watch, false) {
		t.Fatal("a gas top-up to our own address was flagged")
	}
	zero := native
	zero.Amount = big.NewInt(0)
	if nativeOutflowVerdict(zero, watch, false) {
		t.Fatal("a zero native value was flagged")
	}
}

func TestNativePollOutflowOnlySkipsIncoming(t *testing.T) {
	// The block-scan fallback already discovers deposits; the poll then only
	// guards the hot wallet.
	amount := big.NewInt(9)
	lister := &fakeLister{rows: map[string][]explorer.Tx{
		pollDeposit: {txRow(pollHashDeposit, pollSender, pollDeposit, amount, 900)},
		pollHot:     {txRow(pollHashOutflow, pollHot, pollExternal, amount, 910)},
	}}
	sink := &fakeSink{}
	poll, _ := pollFixture(1000, lister, sink)
	poll.outflowOnly = true
	if err := poll.runOnce(t.Context()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(sink.deposits) != 0 {
		t.Fatalf("outflow-only poll processed %d deposits", len(sink.deposits))
	}
	if len(sink.outflows) != 1 {
		t.Fatalf("outflow-only poll outflows = %d", len(sink.outflows))
	}
}

func TestNativePollIgnoresStablecoinTransfers(t *testing.T) {
	// The stablecoin path is the existing eth_getLogs scan. A txlist row for a
	// token contract call carries no native value and must not surface here.
	zero := big.NewInt(0)
	lister := &fakeLister{rows: map[string][]explorer.Tx{
		pollDeposit: {
			txRow(pollHashDeposit, pollSender, pollUSDToken, zero, 900),
			// A native value transfer to someone else is not our deposit.
			txRow(pollHashOutflow, pollSender, pollExternal, big.NewInt(11), 901),
		},
	}}
	sink := &fakeSink{}
	poll, _ := pollFixture(1000, lister, sink)
	if err := poll.runOnce(t.Context()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(sink.deposits) != 0 {
		t.Fatalf("token/foreign rows produced native deposits: %+v", sink.deposits)
	}
}

func TestNativePollPageLimitRereadsTheLastBlock(t *testing.T) {
	rows := make([]explorer.Tx, 0, txListPageLimit)
	for i := 0; i < txListPageLimit; i++ {
		// Every row is an unrelated transfer from a third party to itself.
		rows = append(rows, txRow(pollHashDeposit, pollSender, pollExternal, big.NewInt(1), 900))
	}
	lister := &fakeLister{rows: map[string][]explorer.Tx{pollDeposit: rows}}
	sink := &fakeSink{}
	poll, cursor := pollFixture(1000, lister, sink)
	if err := poll.runOnce(t.Context()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	// A full page may have truncated the window, so the cursor stops before
	// the last returned block and the next poll re-reads it.
	if got := cursor.values["in:bsc:"+pollDeposit]; got != 899 {
		t.Fatalf("cursor after a full page = %d, want 899", got)
	}
}
