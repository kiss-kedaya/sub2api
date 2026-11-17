package cryptopay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type reloadConfigRepository struct {
	service.SettingRepository // Unexpected writes must fail.
	mu                        sync.RWMutex
	raw                       string
}

func (r *reloadConfigRepository) GetValue(context.Context, string) (string, error) {
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
