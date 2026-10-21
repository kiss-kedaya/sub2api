package cryptopay

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/ledger"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Nil dependencies make any accidental shared-database access observable.
// This includes replay of already-signed sweeps, which needs no unlocked key.
func TestAdminTestModeRejectsWritesBeforeAccessingSharedDatabase(t *testing.T) {
	for _, value := range []string{"true", "1", "malformed"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("CRYPTO_ADMIN_TEST_MODE", value)
			w := wallet.New(nil)
			a := addresses.NewService(nil, w)
			e := sweeper.NewExecutor(nil, nil, w, sweeper.Config{}, nil, nil, 0)
			s := &Service{wallet: w, addresses: a, executor: e, config: CryptoConfig{Enabled: true}}
			l := ledger.New(nil, nil, nil, 1, 100, 1)
			c := &DepositCreditor{}
			checks := map[string]func() error{
				"unlock":              func() error { return s.UnlockWallet(t.Context(), "passphrase", "", "") },
				"import":              func() error { return s.ImportHotMnemonic(t.Context(), "passphrase", "mnemonic") },
				"manual sweep":        func() error { _, err := s.RunSweep(t.Context(), false); return err },
				"direct sweep replay": func() error { _, err := e.Run(t.Context(), false); return err },
				"deposit assignment":  func() error { _, err := s.DepositInfo(t.Context(), 42); return err },
				"payment assignment":  func() error { _, err := s.ResolveDepositAddress(t.Context(), 42); return err },
				"pool maintenance":    func() error { return a.EnsurePool(t.Context()) },
				"address status":      func() error { return a.SetStatus(t.Context(), 1, "disabled") },
				"config save":         func() error { return SaveConfig(t.Context(), nil, CryptoConfig{}) },
				"setting write":       func() error { return NewServiceSettingStore(nil).SetCryptoSetting(t.Context(), "crypto.config", "{}") },
				"ledger credit":       func() error { _, err := l.Process(t.Context(), scanner.Transfer{}, 6); return err },
				"ledger pending":      func() error { return l.ProcessPending(t.Context(), scanner.Transfer{}, 6) },
				"fulfillment":         func() error { return c.CreditDeposit(t.Context(), ledger.CreditRequest{}) },
			}
			for name, call := range checks {
				t.Run(name, func(t *testing.T) {
					if err := call(); !errors.Is(err, mode.ErrReadOnly) {
						t.Fatalf("want read-only rejection before database/RPC access, got %v", err)
					}
				})
			}
		})
	}
}

type readOnlyConfigRepository struct {
	service.SettingRepository // every unexpected call, including writes, fails
	raw                       string
}

func (r readOnlyConfigRepository) GetValue(context.Context, string) (string, error) {
	return r.raw, nil
}

func TestAdminTestModeStartupWithSharedEnabledConfigDoesNotStartWorkers(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	cfg := DefaultCryptoConfig()
	cfg.Enabled, cfg.SweepAuto = true, true
	cfg.RPC["bsc"] = "http://127.0.0.1:1"
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	settings := service.NewSettingService(readOnlyConfigRepository{raw: string(raw)}, nil)
	s := NewService(db, settings, nil, NewAddressService(db, settings))
	s.Reload(t.Context())
	if s.maintCancel != nil || cryptoWorkersAllowed() {
		t.Fatal("shared enabled config started maintenance")
	}
	status, err := s.WalletStatus(t.Context())
	if err != nil || status.Unlocked || status.SigningEnabled {
		t.Fatalf("wallet enabled in test instance: %+v %v", status, err)
	}
	if _, err := s.RunSweep(t.Context(), false); !errors.Is(err, mode.ErrReadOnly) {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
