package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

const splitToken = "0x1111111111111111111111111111111111111111"

// splitLogsPool imitates a public node that rejects any eth_getLogs spanning
// more than maxBlocks blocks or filtering more than maxAddresses recipients.
// Accepted queries return exactly the logs matching range and recipients.
func splitLogsPool(t *testing.T, maxBlocks uint64, maxAddresses int, logs []evmLog) (*rpc.Pool, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string          `json:"method"`
			Params []getLogsParams `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Method != "eth_getLogs" || len(request.Params) != 1 {
			t.Errorf("unexpected RPC request %q", request.Method)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		calls.Add(1)
		filter := request.Params[0]
		from, to := parseHexUint(filter.FromBlock), parseHexUint(filter.ToBlock)
		var recipients []any
		if len(filter.Topics) > 2 {
			recipients = filter.Topics[2]
		}
		w.Header().Set("Content-Type", "application/json")
		if to-from+1 > maxBlocks || len(recipients) > maxAddresses {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32005,"message":"query exceeds max results 20000"}}`))
			return
		}
		watched := make(map[string]bool, len(recipients))
		for _, topic := range recipients {
			if s, ok := topic.(string); ok {
				watched[s] = true
			}
		}
		matched := []evmLog{}
		for _, log := range logs {
			block := parseHexUint(log.BlockNumber)
			if block >= from && block <= to && watched[log.Topics[2]] {
				matched = append(matched, log)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": matched})
	}))
	t.Cleanup(server.Close)
	return rpc.NewPoolWithHTTPClient(server.URL, server.Client()), &calls
}

func splitRecipients(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("0x%040x", i+1)
	}
	return out
}

func splitLog(i int, block uint64, to string) evmLog {
	return logFor(Transfer{Chain: "bsc", TxHash: fmt.Sprintf("0x%064x", i+1), LogIndex: i,
		From: "0x" + strings.Repeat("22", 20), To: to, Token: splitToken, Amount: big.NewInt(int64(1000 + i)),
		BlockNumber: block, BlockHash: "0x" + strings.Repeat("55", 32)})
}

func TestGetLogsSplitsBlocksThenAddressesAndKeepsEveryTransfer(t *testing.T) {
	recipients := splitRecipients(120)
	logs := []evmLog{
		splitLog(0, 100, recipients[0]),
		splitLog(1, 101, recipients[119]),
		splitLog(2, 102, recipients[59]),
		splitLog(3, 103, recipients[60]),
	}
	// More than 2 blocks or more than 60 addresses is "too many results".
	pool, calls := splitLogsPool(t, 2, 60, logs)
	transfers, err := NewEVMScanner(pool).ScanRange(t.Context(), "bsc", 100, 103, []string{splitToken}, recipients)
	if err != nil {
		t.Fatal("recoverable getLogs rejections were not split", err)
	}
	seen := make(map[string]bool)
	for _, transfer := range transfers {
		key := fmt.Sprintf("%s/%d", transfer.TxHash, transfer.LogIndex)
		if seen[key] {
			t.Fatal("split queries returned a duplicate transfer", key)
		}
		seen[key] = true
	}
	if len(transfers) != len(logs) {
		t.Fatal("split scan lost transfers", len(transfers), transfers)
	}
	// 1 rejected root, then per 2-block half: 1 rejected + per block (1 rejected + 2 accepted).
	if got := calls.Load(); got != 15 {
		t.Fatal("unexpected eth_getLogs request count", got)
	}
}

func TestGetLogsSplitHasRequestBudgetAndReturnsError(t *testing.T) {
	for _, fixture := range []struct {
		name      string
		maxBlocks uint64
		to        uint64
	}{
		{"always_fails", 0, 1100},      // every query rejected
		{"only_single_blocks", 1, 199}, // needs 199 requests, above the budget
	} {
		t.Run(fixture.name, func(t *testing.T) {
			recipients := splitRecipients(1)
			pool, calls := splitLogsPool(t, fixture.maxBlocks, 200, []evmLog{splitLog(0, 150, recipients[0])})
			transfers, err := NewEVMScanner(pool).ScanRange(t.Context(), "bsc", 100, fixture.to, []string{splitToken}, recipients)
			var logsErr *LogsError
			var failure *rpc.Failure
			if err == nil || transfers != nil || !errors.As(err, &logsErr) || !errors.As(err, &failure) || failure.Kind != "remote" {
				t.Fatal("unrecoverable range reported as scanned", transfers, err)
			}
			if got := calls.Load(); got <= 1 || got > 1+maxGetLogsSplitRequests {
				t.Fatal("split request count is not bounded", got)
			}
			if strings.Contains(err.Error(), "max results") || strings.Contains(err.Error(), "127.0.0.1") {
				t.Fatal("node text or endpoint leaked into the scan error", err)
			}
		})
	}
}

func TestGetLogsDoesNotSplitRateLimitedQueriesAndKeepsSuccessfulBatches(t *testing.T) {
	recipients := splitRecipients(batchSize + 1)
	transfer := splitLog(0, 105, recipients[0])
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Params []getLogsParams `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Params) != 1 {
			t.Errorf("invalid getLogs request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		call := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if call == 2 {
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1,
				"error": map[string]any{"code": 429, "message": "rate limited"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": []evmLog{transfer}})
	}))
	t.Cleanup(server.Close)

	transfers, err := NewEVMScanner(rpc.NewPoolWithHTTPClient(server.URL, server.Client())).ScanRange(
		t.Context(), "bsc", 100, 110, []string{splitToken}, recipients)
	if err == nil || calls.Load() != 2 {
		t.Fatalf("rate limit should fail once without splitting: calls=%d err=%v", calls.Load(), err)
	}
	if len(transfers) != 1 || transfers[0].TxHash != transfer.TxHash {
		t.Fatalf("successful address batch was lost after another batch was rate limited: %+v", transfers)
	}
}

func TestGetLogsDoesNotSplitHTTPRateLimits(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)
	_, err := NewEVMScanner(rpc.NewPoolWithHTTPClient(server.URL, server.Client())).ScanRange(
		t.Context(), "bsc", 100, 110, []string{splitToken}, splitRecipients(1))
	if err == nil || calls.Load() != 1 {
		t.Fatalf("HTTP 429 should fail without splitting: calls=%d err=%v", calls.Load(), err)
	}
}

func TestGetLogsDoesNotSplitAfterContextEnds(t *testing.T) {
	pool, calls := splitLogsPool(t, 0, 200, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewEVMScanner(pool).ScanRange(ctx, "bsc", 100, 1100, []string{splitToken}, splitRecipients(1)); err == nil {
		t.Fatal("canceled scan reported success")
	}
	if got := calls.Load(); got > 1 {
		t.Fatal("canceled scan kept splitting", got)
	}
}

func TestHeadSecurityFailureStillScansDeposits(t *testing.T) {
	transfer := testTransfer()
	fixture := newRPCFixture(transfer)
	worker, mock, handler := workerFixture(t, fixture)
	headFailure := &rpc.Failure{Kind: "transport"}
	var windows [][2]uint64
	worker.SetSecurityGuards(nil, func(_ context.Context, _ *EVMScanner, _ *rpc.Pool, _ ChainConfig, from, to uint64) error {
		windows = append(windows, [2]uint64{from, to})
		if len(windows) == 1 {
			return headFailure // only the latest mined head window fails
		}
		return nil
	})
	mock.ExpectQuery(`SELECT last_block`).WithArgs("bsc").WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(100))
	mock.ExpectExec(`INSERT INTO crypto_scan_cursors`).WithArgs("bsc", int64(105)).WillReturnResult(sqlmock.NewResult(0, 1))
	err := worker.ScanOnce(t.Context())
	if !errors.Is(err, headFailure) {
		t.Fatal("head security failure was hidden from the caller", err)
	}
	var staged *scanStageError
	if !errors.As(err, &staged) || staged.chain != "bsc" || staged.stage != "security" {
		t.Fatal("head security failure lost its chain and stage", err)
	}
	if len(windows) != 2 || windows[1] != [2]uint64{101, 105} {
		t.Fatal("deposit range skipped its own security check", windows)
	}
	if fixture.callCount("eth_getLogs") == 0 || len(handler.pending)+len(handler.processed) != 1 {
		t.Fatal("deposit scan was blocked by the head security window")
	}
}

func TestScanFailuresAreGroupedPerChainWithSafeAttributes(t *testing.T) {
	logsErr := &LogsError{Chain: "bsc", FromBlock: 10, ToBlock: 10, Batch: 3, Addresses: 25,
		Err: fmt.Errorf("all rpc endpoints failed: %w", &rpc.Failure{Kind: "remote", RPCCode: -32005})}
	err := errors.Join(
		fmt.Errorf("scan bsc: %w", tagStage("bsc", "deposit", errors.Join(stageErr("bsc", "deposit", 1, 20, logsErr)))),
		fmt.Errorf("scan eth: %w", stageErr("eth", "head", 0, 0, &rpc.Failure{Kind: "transport"})),
		errors.New("pending retry: database unavailable"),
	)
	byChain := make(map[string][]*scanStageError)
	var other []error
	collectScanFailures(err, byChain, &other)
	if len(byChain["bsc"]) != 1 || len(byChain["eth"]) != 1 || len(other) != 1 {
		t.Fatal("failures were not grouped per chain", byChain, other)
	}
	attrs := fmt.Sprintln(byChain["bsc"][0].logAttrs()...)
	for _, want := range []string{"stage deposit", "query_from 10", "batch 3", "addresses 25", "kind remote", "rpc_code -32005"} {
		if !strings.Contains(attrs, want) {
			t.Fatal("missing log attribute", want, attrs)
		}
	}
}
