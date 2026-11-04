package cryptopay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	"github.com/stretchr/testify/require"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func captureLogs(t *testing.T) *lockedBuffer {
	t.Helper()
	buf := &lockedBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return buf
}

// A ledger outcome is never inferred from Process's return value alone: the
// row decides. Cases that need PostgreSQL-only SQL (the ledger's ignore path
// writes NOW()) are stood in for by a processor that edits the row the way that
// path would.
func TestManualConfirmReportsWhatTheLedgerActuallyDid(t *testing.T) {
	setRow := func(e *confirmEnv, status, reason string) func(context.Context, scanner.Transfer, int) (bool, error) {
		return func(ctx context.Context, tr scanner.Transfer, _ int) (bool, error) {
			_, err := e.db.ExecContext(ctx, `UPDATE deposit_transactions SET status = $1, reason = $2
				WHERE chain = $3 AND tx_hash = $4 AND log_index = $5`, status, reason, tr.Chain, tr.TxHash, tr.LogIndex)
			return false, err
		}
	}
	for _, tc := range []struct {
		name            string
		setup           func(e *confirmEnv)
		status, message string
		rowStatus       string
		forbid          []string
	}{
		{name: "sender is a system address", rowStatus: "pending", status: DepositConfirmRejected, message: "账本拒绝入账",
			setup: func(e *confirmEnv) { e.resolver.system[strings.ToLower(confirmFrom)] = true }},
		{name: "recipient is not a deposit address", rowStatus: "pending", status: DepositConfirmRejected, message: "账本拒绝入账",
			setup: func(e *confirmEnv) { e.resolver.deposit = map[string]bool{} }},
		{name: "ledger fails with database text", rowStatus: "pending", status: DepositConfirmError, message: "入账处理失败",
			forbid: []string{"secret-db-user", "10.9.8.7", "5432"},
			setup: func(e *confirmEnv) {
				e.processor.replace = func(context.Context, scanner.Transfer, int) (bool, error) {
					return false, errors.New("dial tcp 10.9.8.7:5432: password authentication failed for user secret-db-user")
				}
			}},
		{name: "ledger times out", rowStatus: "pending", status: DepositConfirmError, message: "超时或被取消",
			setup: func(e *confirmEnv) {
				e.processor.replace = func(context.Context, scanner.Transfer, int) (bool, error) {
					return false, fmt.Errorf("credit deposit: %w", context.DeadlineExceeded)
				}
			}},
		{name: "instance is read-only", rowStatus: "pending", status: DepositConfirmError, message: "只读测试模式",
			setup: func(e *confirmEnv) {
				e.processor.replace = func(context.Context, scanner.Transfer, int) (bool, error) {
					return false, fmt.Errorf("mark credited: %w", mode.ErrReadOnly)
				}
			}},
		{name: "ledger ignores the transfer below the minimum", rowStatus: "ignored", status: DepositConfirmRejected,
			message: "低于最小入账金额", setup: func(e *confirmEnv) { e.processor.replace = setRow(e, "ignored", "below_min") }},
		{name: "ledger flags a reorg duplicate", rowStatus: "reverted", status: DepositConfirmRejected,
			message: "需要人工核对", setup: func(e *confirmEnv) {
				e.processor.replace = setRow(e, "reverted", "reorg_duplicate_credit_manual_review")
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLogs(t)
			e := newConfirmEnv(t)
			id, _ := e.seed(t, 1)
			tc.setup(e)

			result := e.confirm(id)
			require.Equal(t, tc.status, result.Status, result.Message)
			require.Contains(t, result.Message, tc.message)
			require.Nil(t, result.OrderID)
			for _, secret := range tc.forbid {
				require.NotContains(t, result.Message, secret)
				require.NotContains(t, logs.String(), secret, "logs must carry the sanitised cause only")
			}
			require.Equal(t, tc.rowStatus, e.status(t, id))
			require.Equal(t, 0.0, e.balance(t), "a declined deposit must not move the balance")
			require.Zero(t, e.orders(t))
		})
	}
}

// Another instance credited the row between our read and our ledger call. The
// ledger credited it once; this request must say so rather than claim the credit.
func TestManualConfirmReportsACreditMadeByAnotherCallerAsAlreadyCredited(t *testing.T) {
	e := newConfirmEnv(t)
	id, _ := e.seed(t, 1)
	e.processor.replace = func(ctx context.Context, tr scanner.Transfer, decimals int) (bool, error) {
		if _, err := e.processor.inner.Process(ctx, tr, decimals); err != nil {
			return false, err
		}
		return false, nil
	}
	result := e.confirm(id)
	require.Equal(t, DepositConfirmAlreadyCredited, result.Status, result.Message)
	require.NotNil(t, result.OrderID)
	require.Equal(t, 67.0, e.balance(t))
	require.Equal(t, 1, e.orders(t))
}

func TestManualConfirmNeverLeaksTheRPCEndpointOrItsCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		breakIt       func(e *confirmEnv)
	}{
		{name: "node answers 503 to the receipt", message: "status 503",
			breakIt: func(e *confirmEnv) { e.chain.httpFail["eth_getTransactionReceipt"] = 503 }},
		{name: "node is unreachable", message: "没有通过链 ID 校验的 RPC 节点",
			breakIt: func(e *confirmEnv) { e.chain.server.Close() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLogs(t)
			e := newConfirmEnv(t)
			id, _ := e.seed(t, 1)
			u, err := url.Parse(e.chain.server.URL)
			require.NoError(t, err)
			host := u.Host
			u.User = url.UserPassword("rpcuser-SECRET", "rpcpass-SECRET")
			u.Path = "/v1/APIKEY-SECRET-123"
			pool := rpc.NewPoolWithHTTPClient(u.String(), e.chain.server.Client())
			e.confirmer.pool = func(string) (*rpc.Pool, error) { return pool, nil }
			tc.breakIt(e)
			before := e.snapshot(t)

			result := e.confirm(id)
			require.Equal(t, DepositConfirmError, result.Status, result.Message)
			require.Contains(t, result.Message, tc.message)
			require.Equal(t, before, e.snapshot(t))
			for _, secret := range []string{"rpcuser-SECRET", "rpcpass-SECRET", "APIKEY-SECRET-123", host, "127.0.0.1", "http://"} {
				require.NotContains(t, result.Message, secret)
				require.NotContains(t, logs.String(), secret)
			}
		})
	}
}

func TestManualConfirmLogsWhoConfirmedWhichDepositWithoutRPCDetails(t *testing.T) {
	logs := captureLogs(t)
	e := newConfirmEnv(t)
	id, s := e.seed(t, 1)
	require.Equal(t, DepositConfirmCredited, e.confirm(id).Status)

	out := logs.String()
	require.Contains(t, out, "manual deposit confirmation")
	require.Contains(t, out, "admin_id=9")
	require.Contains(t, out, fmt.Sprintf("deposit_id=%d", id))
	require.Contains(t, out, "chain=bsc")
	require.Contains(t, out, "tx_hash="+s.tx)
	require.Contains(t, out, "status=credited")
	require.NotContains(t, out, e.chain.server.URL)
	require.NotContains(t, out, "127.0.0.1")
}
