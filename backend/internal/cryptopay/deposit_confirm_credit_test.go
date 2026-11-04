package cryptopay

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func TestManualConfirmCreditsAPendingDepositExactlyOnce(t *testing.T) {
	e := newConfirmEnv(t)
	id, _ := e.seed(t, 1)

	first := e.confirm(id)
	require.Equal(t, DepositConfirmCredited, first.Status, first.Message)
	require.NotNil(t, first.OrderID)
	require.Equal(t, payment.OrderStatusCompleted, e.client.PaymentOrder.GetX(t.Context(), *first.OrderID).Status)
	require.Equal(t, 67.0, e.balance(t), "10 USDT at 6.7")
	require.Equal(t, 1, e.orders(t))
	require.Equal(t, "credited", e.status(t, id))
	require.Equal(t, 1, e.chain.callCount("eth_getTransactionReceipt"))
	require.Equal(t, 1, e.chain.callCount("eth_getBlockByNumber"))
	require.Equal(t, 1, e.processor.count())

	// Clicking again must be a no-op: the row is already credited, so neither the
	// chain nor the ledger is consulted.
	second := e.confirm(id)
	require.Equal(t, DepositConfirmAlreadyCredited, second.Status, second.Message)
	require.Equal(t, first.OrderID, second.OrderID)
	require.Equal(t, 67.0, e.balance(t))
	require.Equal(t, 1, e.orders(t))
	require.Equal(t, 1, e.chain.callCount("eth_getTransactionReceipt"))
	require.Equal(t, 1, e.processor.count())
}

func TestManualConfirmBatchSummaryCountsAndKeepsRequestOrder(t *testing.T) {
	e := newConfirmEnv(t)
	good1, _ := e.seed(t, 1)
	good2, _ := e.seed(t, 2)
	bad, badSpec := e.seed(t, 3)
	e.chain.receipts[badSpec.tx].status = "0x0"
	done, _ := e.seed(t, 4)
	require.Equal(t, DepositConfirmCredited, e.confirm(done).Status)
	const missing = int64(987654)
	probesBefore := e.chain.callCount("eth_chainId")

	summary := e.confirmer.run(t.Context(), []int64{good1, missing, bad, done, good2})
	got := make([]string, 0, len(summary.Results))
	ids := make([]int64, 0, len(summary.Results))
	for _, r := range summary.Results {
		got = append(got, r.Status)
		ids = append(ids, r.ID)
	}
	require.Equal(t, []int64{good1, missing, bad, done, good2}, ids)
	require.Equal(t, []string{DepositConfirmCredited, DepositConfirmRejected, DepositConfirmRejected,
		DepositConfirmAlreadyCredited, DepositConfirmCredited}, got)
	require.Equal(t, 2, summary.Credited)
	require.Equal(t, 2, summary.Failed, "already_credited is neither a credit nor a failure")
	require.Equal(t, 3*67.0, e.balance(t), "three distinct deposits credited, the reverted one is not")
	require.Equal(t, 3, e.orders(t))
	require.Equal(t, "pending", e.status(t, bad))
	// One chain-id probe serves the whole batch, not one per id.
	require.Equal(t, probesBefore+1, e.chain.callCount("eth_chainId"))
}

// The lock is a convenience, not the safety property. With no lock at all, a
// crowd of simultaneous confirmations must still credit once: the deterministic
// order identity, the unique payment_orders key and the status guard decide.
func TestManualConfirmConcurrentWithoutAnyLockStillCreditsOnce(t *testing.T) {
	e := newConfirmEnv(t)
	e.confirmer.locker = grantAllLocker{}
	id, _ := e.seed(t, 1)

	const workers = 8
	results := make([]DepositConfirmResult, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i] = e.confirm(id)
		}()
	}
	close(start)
	wg.Wait()

	credited, already := 0, 0
	for _, r := range results {
		switch r.Status {
		case DepositConfirmCredited:
			credited++
		case DepositConfirmAlreadyCredited:
			already++
		default:
			t.Fatalf("unexpected outcome %q: %s", r.Status, r.Message)
		}
	}
	require.Equal(t, 1, credited)
	require.Equal(t, workers-1, already)
	require.Equal(t, 67.0, e.balance(t))
	require.Equal(t, 1, e.orders(t))
	require.Equal(t, "credited", e.status(t, id))
}

func TestManualConfirmLockMakesASecondConfirmationOfTheSameDepositWait(t *testing.T) {
	e := newConfirmEnv(t)
	id, _ := e.seed(t, 1)
	entered, release := make(chan struct{}), make(chan struct{})
	e.processor.before = func() {
		close(entered)
		<-release
	}

	firstDone := make(chan DepositConfirmResult, 1)
	go func() { firstDone <- e.confirm(id) }()
	<-entered

	busy := e.confirm(id)
	require.Equal(t, DepositConfirmError, busy.Status)
	require.Contains(t, busy.Message, "正在被另一次确认处理")
	require.Equal(t, 1, e.processor.count(), "the second request must not reach the ledger")

	close(release)
	first := <-firstDone
	require.Equal(t, DepositConfirmCredited, first.Status, first.Message)
	require.Equal(t, 67.0, e.balance(t))
	require.Equal(t, DepositConfirmAlreadyCredited, e.confirm(id).Status)
	require.Equal(t, 1, e.orders(t))
}

func TestManualConfirmLockFailureTouchesNothingAndLeaksNothing(t *testing.T) {
	e := newConfirmEnv(t)
	id, _ := e.seed(t, 1)
	e.locker.err = errors.New("password authentication failed for user secret-db-user at 10.9.8.7")
	before := e.snapshot(t)

	result := e.confirm(id)
	require.Equal(t, DepositConfirmError, result.Status)
	require.NotContains(t, result.Message, "secret-db-user")
	require.NotContains(t, result.Message, "10.9.8.7")
	require.Equal(t, before, e.snapshot(t))
	require.Zero(t, e.chain.totalCalls(), "no chain call before the lock is held")
	require.Zero(t, e.processor.count())
}

func TestManualConfirmStopsStartingNewIDsOnceTheBatchContextIsDone(t *testing.T) {
	e := newConfirmEnv(t)
	id, _ := e.seed(t, 1)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	summary := e.confirmer.run(ctx, []int64{id})
	require.Equal(t, DepositConfirmError, summary.Results[0].Status)
	require.Contains(t, summary.Results[0].Message, "可安全重试")
	require.Zero(t, e.chain.totalCalls())
	require.Equal(t, "pending", e.status(t, id))
}
