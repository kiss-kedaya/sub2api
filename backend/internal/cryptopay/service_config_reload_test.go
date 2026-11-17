package cryptopay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/ledger"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type reloadConfigRepository struct {
	service.SettingRepository // Other repository operations must fail.
	mu                        sync.RWMutex
	raw                       string
	reads                     atomic.Int32
}

func (r *reloadConfigRepository) GetValue(context.Context, string) (string, error) {
	r.reads.Add(1)
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.raw, nil
}

func (r *reloadConfigRepository) set(t *testing.T, cfg CryptoConfig) {
	t.Helper()
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	r.mu.Lock()
	r.raw = string(raw)
	r.mu.Unlock()
}

func (r *reloadConfigRepository) Set(_ context.Context, _ string, raw string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.raw = raw
	return nil
}

func TestConfigPollLoadsChangedSnapshotOnce(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "false")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "")
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "true")
	repo := &reloadConfigRepository{}
	cfg := DefaultCryptoConfig()
	repo.set(t, cfg)
	s := &Service{settings: service.NewSettingService(repo, nil), wallet: wallet.New(nil)}
	t.Cleanup(s.Close)
	s.Reload(t.Context())
	require.EqualValues(t, 1, repo.reads.Load(), "manual reload reads settings once")
	cfg.ScanRange[ChainBSC] = 100
	repo.set(t, cfg)
	repo.reads.Store(0)
	reloaded, err := s.reloadIfConfigChanged(t.Context())
	require.NoError(t, err)
	require.True(t, reloaded)
	require.EqualValues(t, 1, repo.reads.Load(), "polling must apply its already-loaded snapshot")
	require.Equal(t, 100, s.Config().ScanRange[ChainBSC])
}

func TestDelegatedConfigPollRecoversAfterStartupLoadFailure(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "false")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "")
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "true")
	repo := &reloadConfigRepository{}
	cfg := DefaultCryptoConfig()
	cfg.Enabled, cfg.NativePollSeconds = true, 86400
	repo.set(t, cfg)
	settings := service.NewSettingService(repo, nil)
	s := NewService(nil, settings, nil, nil, nil)
	t.Cleanup(s.Close)
	require.False(t, s.Config().Enabled)
	// Isolate config recovery from the independent wallet-security database.
	s.wallet = wallet.New(nil)
	cfg.NativePollSeconds = DefaultNativePollSeconds
	repo.set(t, cfg)
	reloaded, err := s.reloadIfConfigChanged(t.Context())
	require.NoError(t, err)
	require.True(t, reloaded)
	require.True(t, s.Config().Enabled)
	require.Nil(t, s.worker)
	require.Nil(t, s.maintCancel)
}

func TestConfigPollRetriesTransientGuardFailureWithoutConfigChange(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "false")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "")
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "true")
	s, mock := newChannelGateService(t)
	t.Cleanup(s.Close)
	s.wallet.EnableSecurityMonitoring()
	_, err := LoadConfig(t.Context(), NewServiceSettingStore(s.settings))
	require.NoError(t, err)
	mock.ExpectBegin().WillReturnError(errors.New("temporary database failure"))
	s.Reload(t.Context())
	require.False(t, s.Config().Enabled)
	mock.ExpectBegin().WillReturnError(errors.New("temporary database failure"))
	_, err = s.reloadIfConfigChanged(t.Context())
	require.ErrorIs(t, err, wallet.ErrVaultUnavailable)
	require.NoError(t, mock.ExpectationsWereMet(), "unchanged config must retry a transient wallet guard failure")
	// A healthy check can now apply the very same stored row.
	s.wallet = wallet.New(nil)
	reloaded, err := s.reloadIfConfigChanged(t.Context())
	require.NoError(t, err)
	require.True(t, reloaded)
	require.True(t, s.Config().Enabled)
	reloaded, err = s.reloadIfConfigChanged(t.Context())
	require.NoError(t, err)
	require.False(t, reloaded, "a successful reload must clear the retry marker")
}

func TestConfigPollRetriesAfterStoredConfigReturnsToAppliedValue(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "false")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "")
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "true")
	repo := &reloadConfigRepository{}
	cfg := DefaultCryptoConfig()
	repo.set(t, cfg)
	s := NewService(nil, service.NewSettingService(repo, nil), nil, nil, nil)
	t.Cleanup(s.Close)
	cfg.NativePollSeconds = 86400
	repo.set(t, cfg)
	_, err := s.reloadIfConfigChanged(t.Context())
	require.Error(t, err)
	cfg.NativePollSeconds = DefaultNativePollSeconds
	repo.set(t, cfg)
	reloaded, err := s.reloadIfConfigChanged(t.Context())
	require.NoError(t, err)
	require.True(t, reloaded, "a prior load failure must retry even if storage returns to the old fingerprint")
}

func TestConfigPollerDoesNotReopenSecurityPausedChannel(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "false")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "")
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "true")
	s, mock := newChannelGateService(t)
	t.Cleanup(s.Close)
	s.Reload(t.Context())
	s.securityPaused.Store(true)
	s.mu.Lock()
	s.config.Enabled = false
	s.mu.Unlock()
	// Storage can still say enabled if the incident's pause write failed.
	reloaded, err := s.reloadIfConfigChanged(t.Context())
	require.NoError(t, err)
	require.False(t, reloaded, "the paused local snapshot is not a config change")
	require.False(t, s.Config().Enabled)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDelegatedConfigPollerRecoversAndCloses(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "false")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "")
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "true")
	repo := &reloadConfigRepository{}
	cfg := DefaultCryptoConfig()
	cfg.Enabled, cfg.NativePollSeconds = true, 86400
	repo.set(t, cfg)
	s := NewService(nil, service.NewSettingService(repo, nil), nil, nil, nil)
	t.Cleanup(s.Close)
	require.False(t, s.Config().Enabled)
	require.Nil(t, s.configCancel, "disabled background jobs must not start a config watcher")
	s.wallet = wallet.New(nil)
	cfg.NativePollSeconds = DefaultNativePollSeconds
	repo.set(t, cfg)
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "")
	s.startConfigPoller(t.Context(), time.Millisecond)
	done := s.configDone
	require.NotNil(t, done)
	s.startConfigPoller(t.Context(), time.Millisecond)
	require.Equal(t, done, s.configDone, "reload/start must never duplicate the watcher")
	require.Eventually(t, func() bool { return s.Config().Enabled }, time.Second, time.Millisecond)
	s.Close()
	select {
	case <-done:
	default:
		t.Fatal("Close returned with the config watcher still running")
	}
	require.Nil(t, s.worker)
	require.Nil(t, s.maintCancel)
}

func TestConfigPollerStartsIndependentlyOfScanning(t *testing.T) {
	for _, tc := range []struct {
		name, disabled, readOnly string
		wantPoller               bool
	}{
		{name: "delegated scanner", wantPoller: true},
		{name: "background jobs disabled", disabled: "true"},
		{name: "read only", readOnly: "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CRYPTO_INSTANCE_ENABLED", "false")
			t.Setenv("CRYPTO_ADMIN_TEST_MODE", tc.readOnly)
			t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", tc.disabled)
			s := NewService(nil, nil, nil, nil, nil)
			t.Cleanup(s.Close)
			require.Equal(t, tc.wantPoller, s.configCancel != nil)
			require.Nil(t, s.maintCancel)
		})
	}
}

func TestConfigPollerCloseDoesNotWaitForSweep(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "false")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "")
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "")
	s := NewService(nil, nil, nil, nil, nil)
	t.Cleanup(s.Close)
	s.configCancel()
	<-s.configDone
	s.configCancel = nil
	s.startConfigPoller(t.Context(), time.Millisecond)
	s.sweepMu.Lock()
	checked := make(chan bool, 1)
	go func() {
		reloaded, _ := s.reloadIfConfigChanged(t.Context())
		checked <- reloaded
	}()
	select {
	case reloaded := <-checked:
		if reloaded {
			s.sweepMu.Unlock()
			t.Fatal("config watcher reloaded while a sweep held the lock")
		}
	case <-time.After(time.Second):
		s.sweepMu.Unlock()
		<-checked
		t.Fatal("config check blocked behind a sweep")
	}
	done := make(chan struct{})
	go func() {
		s.Close()
		close(done)
	}()
	select {
	case <-done:
		s.sweepMu.Unlock()
	case <-time.After(time.Second):
		s.sweepMu.Unlock()
		<-done
		t.Fatal("closing the independent config watcher waited for a sweep")
	}
}

type reloadCreditor struct {
	requests []ledger.CreditRequest
}

func (c *reloadCreditor) CreditDeposit(_ context.Context, req ledger.CreditRequest) error {
	c.requests = append(c.requests, req)
	return nil
}

func TestConfigPollAppliesMultiplierAcrossInstances(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "")
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "true")
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		mock.ExpectClose()
		require.NoError(t, db.Close())
	})
	tr := scanner.Transfer{Chain: ChainBSC, TxHash: "0x" + strings.Repeat("ab", 32), LogIndex: 1,
		From: confirmFrom, To: confirmTo, Token: confirmUSDT, Amount: new(big.Int).Set(tenUSDT),
		BlockNumber: 100, BlockHash: "0x" + strings.Repeat("cd", 32)}
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Method string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		var result any
		switch req.Method {
		case "eth_getTransactionReceipt":
			result = map[string]any{"status": "0x1", "blockNumber": "0x64", "blockHash": tr.BlockHash,
				"transactionHash": tr.TxHash, "logs": []any{chainTransferLog(tr.Token, tr.From, tr.To, tr.Amount, tr.BlockNumber, tr.BlockHash, tr.TxHash, tr.LogIndex)}}
		case "eth_getBlockByNumber":
			result = map[string]any{"number": "0x64", "hash": tr.BlockHash}
		case "eth_blockNumber":
			result = "0x67"
		default:
			t.Errorf("unexpected fixture RPC method %s", req.Method)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(node.Close)
	repo := &reloadConfigRepository{}
	cfg := DefaultCryptoConfig()
	cfg.Enabled = true
	cfg.RPC[ChainBSC] = node.URL
	repo.set(t, cfg)
	admin := &Service{settings: service.NewSettingService(repo, nil), wallet: wallet.New(nil)}
	t.Cleanup(admin.Close)
	creditor := &reloadCreditor{}
	scanSettings := service.NewSettingService(repo, nil)
	s := &Service{db: db, settings: scanSettings, wallet: wallet.New(nil), creditor: creditor,
		addresses: NewAddressService(db, scanSettings), pools: map[string]*rpc.Pool{
			ChainBSC: rpc.NewPoolWithHTTPClient(node.URL, node.Client()),
		}}
	t.Cleanup(s.Close)
	admin.Reload(t.Context())
	s.Reload(t.Context())
	oldWorker := s.worker
	cfg.CreditMultiplier = 7.5
	require.NoError(t, SaveConfig(t.Context(), NewServiceSettingStore(admin.settings), cfg))
	admin.Reload(t.Context())
	reloaded, err := s.reloadIfConfigChanged(t.Context())
	require.NoError(t, err)
	require.True(t, reloaded, "scanner must detect a multiplier-only save on another instance")
	require.NotSame(t, oldWorker, s.worker)
	chains, err := s.worker.ScanChains(ChainBSC)
	require.NoError(t, err)
	mock.ExpectQuery("SELECT from_address, to_address").WillReturnRows(sqlmock.NewRows([]string{"from_address", "to_address", "token", "amount", "block_number", "block_hash", "status"}))
	mock.ExpectQuery("SELECT d.status").WillReturnRows(sqlmock.NewRows([]string{"status", "order_id", "user_id", "amount", "native_price_usd", "native_price_at", "native_price_source"}))
	assignmentColumns := []string{"id", "user_id", "chain", "address", "derivation_index", "status"}
	mock.ExpectQuery("SELECT id, user_id, chain").WithArgs(addresses.FamilyEVM, tr.From).WillReturnRows(sqlmock.NewRows(assignmentColumns))
	mock.ExpectQuery("SELECT id, status FROM crypto_address_pool").WithArgs(addresses.FamilyEVM, tr.From).WillReturnRows(sqlmock.NewRows([]string{"id", "status"}))
	mock.ExpectQuery("SELECT id, user_id, chain").WithArgs(addresses.FamilyEVM, tr.To).WillReturnRows(sqlmock.NewRows(assignmentColumns).AddRow(1, 42, addresses.FamilyEVM, tr.To, 1, "active"))
	mock.ExpectExec("INSERT INTO deposit_transactions").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE deposit_transactions SET status = 'credited'").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE deposit_transactions SET confirmations").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.worker.ProcessObservedTransfer(t.Context(), chains[0], tr))
	require.Len(t, creditor.requests, 1)
	require.Equal(t, 75.0, creditor.requests[0].CreditUSDT, "10 USDT must use the new 7.5 multiplier in the scanner's ledger")
	require.NoError(t, mock.ExpectationsWereMet())
	worker, provider := s.worker, s.nativePrice
	for range 3 {
		reloaded, err = s.reloadIfConfigChanged(t.Context())
		require.NoError(t, err)
		require.False(t, reloaded, "the same normalized stored config must not restart workers")
		require.Same(t, worker, s.worker)
		require.Same(t, provider, s.nativePrice)
	}
}

func TestConfigPollAppliesScanConcurrency(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "")
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "true")
	repo := &reloadConfigRepository{}
	cfg := DefaultCryptoConfig()
	cfg.Enabled = true
	repo.set(t, cfg)
	s := &Service{settings: service.NewSettingService(repo, nil), wallet: wallet.New(nil)}
	t.Cleanup(s.Close)
	s.Reload(t.Context())
	old := s.worker
	require.NotNil(t, old)
	cfg.ScanConcurrency[ChainBSC] = 32
	repo.set(t, cfg)
	reloaded, err := s.reloadIfConfigChanged(t.Context())
	require.NoError(t, err)
	require.True(t, reloaded, "changing concurrency alone must reload")
	require.NotSame(t, old, s.worker)
	chains, err := s.worker.ScanChains(ChainBSC)
	require.NoError(t, err)
	require.Equal(t, 32, chains[0].ScanConcurrency)
	reloaded, err = s.reloadIfConfigChanged(t.Context())
	require.NoError(t, err)
	require.False(t, reloaded, "unchanged concurrency must not churn the worker")
}

func TestConfigReloadFailuresAreVisibleAtErrorLevel(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "false")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "")
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "true")
	for _, guardFailure := range []bool{false, true} {
		name := "invalid config"
		if guardFailure {
			name = "wallet guard unavailable"
		}
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelError})))
			t.Cleanup(func() { slog.SetDefault(previous) })
			cfg := DefaultCryptoConfig()
			cfg.Enabled = true
			cfg.NativePollSeconds = 86400
			message, reason := "config load failed", "native_poll_seconds must be between 10 and 3600"
			w := wallet.New(nil)
			if guardFailure {
				cfg.NativePollSeconds = DefaultNativePollSeconds
				w.EnableSecurityMonitoring()
				message, reason = "channel paused by wallet security guard", wallet.ErrVaultUnavailable.Error()
			}
			repo := &reloadConfigRepository{}
			repo.set(t, cfg)
			s := &Service{settings: service.NewSettingService(repo, nil), wallet: w}
			t.Cleanup(s.Close)
			s.Reload(t.Context())
			require.Contains(t, logs.String(), message)
			require.Contains(t, logs.String(), reason)
		})
	}
}
