package cryptopay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/ledger"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestReloadStopsWorkerOutsideConfigLock(t *testing.T) {
	for _, tc := range []struct {
		name, instance string
		enabled        bool
		wantWorker     bool
	}{
		{"rebuild scanner", "true", true, true},
		{"delegate scanner", "false", true, false},
		{"disable channel", "true", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CRYPTO_INSTANCE_ENABLED", tc.instance)
			t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "true")
			t.Setenv("CRYPTO_ADMIN_TEST_MODE", "")
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			var logCalls atomic.Int32
			node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct{ Method string }
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				var result any
				switch req.Method {
				case "eth_chainId":
					result = "0x38"
				case "eth_blockNumber":
					result = "0x67"
				case "eth_getLogs":
					logCalls.Add(1)
					result = []any{}
				default:
					t.Errorf("unexpected fixture RPC method: %s", req.Method)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
			}))
			t.Cleanup(node.Close)
			cfg := DefaultCryptoConfig()
			cfg.Enabled = tc.enabled
			cfg.NativeScan = NativeScanTxList
			cfg.RPC[ChainBSC] = node.URL
			cfg.HotWalletEVM = "0x2222222222222222222222222222222222222222"
			repo := &reloadConfigRepository{}
			repo.set(t, cfg)
			settings := service.NewSettingService(repo, nil)
			oldConfig := cfg
			oldConfig.Enabled = true
			oldConfig.HotWalletEVM = "0x1111111111111111111111111111111111111111"
			s := &Service{db: db, settings: settings, config: oldConfig, wallet: wallet.New(nil),
				addresses: NewAddressService(db, settings), pools: map[string]*rpc.Pool{
					ChainBSC: rpc.NewPoolWithHTTPClient(node.URL, node.Client()),
				}}
			chain, ok := chainScanConfig(ChainBSC, 3, 2000, 0)
			require.True(t, ok)
			chain.NativeSource = NativeScanTxList
			old := scanner.NewWorker(db, s.pools, []scanner.ChainConfig{chain}, scanner.NewCursorStore(db),
				ledger.New(db, nil, nil, 1, 10000, 1),
				func(context.Context, string) ([]string, error) { return []string{confirmTo}, nil }, time.Hour, 2000)
			s.worker = old
			ownReady, ownDone := make(chan struct{}), make(chan struct{})
			observed := make(chan string, 1)
			old.SetOwnWallets(func(ctx context.Context, _ string) ([]string, error) {
				close(ownReady)
				// Reload's Stop cancels this context immediately before the read locks.
				<-ctx.Done()
				observed <- s.Config().HotWalletEVM
				_, _ = s.HotAddresses(ctx)
				close(ownDone)
				return nil, nil
			})
			const scanKey = int64(0x43525950544f5343)
			mock.ExpectQuery("SELECT pg_try_advisory_lock").WithArgs(scanKey).
				WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
			mock.ExpectQuery("SELECT pg_advisory_unlock").WithArgs(scanKey).
				WillReturnRows(sqlmock.NewRows([]string{"unlocked"}).AddRow(true))
			old.Start()
			select {
			case <-ownReady:
			case <-time.After(2 * time.Second):
				t.Fatal("worker never reached the own-wallet callback")
			}
			reloaded := make(chan struct{})
			go func() { s.Reload(t.Context()); close(reloaded) }()
			select {
			case <-reloaded:
			case <-time.After(2 * time.Second):
				t.Fatal("reload deadlocked waiting for the own-wallet config read")
			}
			t.Cleanup(s.Close)
			require.Equal(t, oldConfig.HotWalletEVM, <-observed, "publish config only after the old scan exits")
			select {
			case <-ownDone:
			default:
				t.Fatal("reload returned before the old callback completed")
			}
			stopped := make(chan struct{})
			go func() { old.Stop(); close(stopped) }()
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("Stop did not return after reload")
			}
			require.Equal(t, cfg.HotWalletEVM, s.Config().HotWalletEVM)
			require.Equal(t, tc.wantWorker, s.worker != nil)
			if tc.wantWorker {
				require.NotSame(t, old, s.worker)
				// Isolate scanner lifecycle from wallet-vault I/O; no real wallet/database.
				s.worker.SetSecurityGuards(nil, nil)
				mock.ExpectQuery("SELECT address FROM deposit_addresses").WillReturnRows(sqlmock.NewRows([]string{"address"}).AddRow(confirmTo))
				mock.ExpectQuery("SELECT last_block").WillReturnRows(sqlmock.NewRows([]string{"last_block"}))
				mock.ExpectExec("INSERT INTO crypto_scan_cursors").WithArgs(ChainBSC, uint64(0)).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectQuery("SELECT address FROM deposit_addresses").WillReturnRows(sqlmock.NewRows([]string{"address"}).AddRow(confirmTo))
				mock.ExpectExec("INSERT INTO crypto_scan_cursors").WithArgs(ChainBSC, uint64(100)).WillReturnResult(sqlmock.NewResult(0, 1))
				before := logCalls.Load()
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				require.NoError(t, s.worker.ScanOnce(ctx))
				require.Greater(t, logCalls.Load(), before, "replacement worker must read logs and advance its cursor")
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
