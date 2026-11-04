package scanner

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

func TestSecurityRPCFailureKeepsInboundTransferPending(t *testing.T) {
	transfer := testTransfer()
	fixture := newRPCFixture(transfer)
	worker, _, handler := workerFixture(t, fixture)
	worker.securityRange = func(context.Context, *EVMScanner, *rpc.Pool, ChainConfig, uint64, uint64) error {
		return rpc.NewTransportFailure(context.DeadlineExceeded)
	}

	if err := worker.processScanRange(t.Context(), NewEVMScanner(worker.pools["bsc"]), worker.pools["bsc"], worker.chains[0], 100, 105, []string{transfer.To}); err != nil {
		t.Fatal(err)
	}
	if len(handler.pending) != 1 || len(handler.processed) != 0 {
		t.Fatalf("inbound transfer must be retained as pending: pending=%d processed=%d", len(handler.pending), len(handler.processed))
	}
}
