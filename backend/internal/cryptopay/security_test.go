package cryptopay

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
)

func TestSecurityPauseDoesNotWaitOnReloadAndPreservesOtherConfig(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	svc := &Service{db: db, wallet: wallet.New(db), config: CryptoConfig{Enabled: true, Chains: []string{"base"}, SweepAuto: true}}
	// The scanner can discover an incident while reload holds this mutex
	// waiting for it. Pausing/notifying must never reacquire that mutex.
	mock.ExpectExec("UPDATE settings SET value=jsonb_set").WithArgs(SettingKeyCryptoConfig).WillReturnResult(sqlmock.NewResult(0, 1))
	svc.mu.Lock()
	done := make(chan error, 1)
	go func() {
		done <- svc.handleSecurityIncident(context.Background(), wallet.SecurityStatus{Enabled: true, Initialized: true, Locked: true, DepositsPaused: true, Reason: "unauthorized_token_outflow"})
	}()
	select {
	case err := <-done:
		svc.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		svc.mu.Unlock()
		t.Fatal("security pause deadlocked with configuration reload")
	}
	cfg := svc.Config()
	if cfg.Enabled || !cfg.SweepAuto || len(cfg.Chains) != 1 || cfg.Chains[0] != "base" {
		t.Fatal("pause altered unrelated settings", cfg)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestFailedPersistentChannelPauseStillStopsLocalActivity(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	svc := &Service{db: db, wallet: wallet.New(db), config: CryptoConfig{Enabled: true}}
	mock.ExpectExec("UPDATE settings SET value=jsonb_set").WillReturnError(errors.New("database offline"))
	if err := svc.handleSecurityIncident(t.Context(), wallet.SecurityStatus{Locked: true}); err == nil {
		t.Fatal("persistence failure hidden")
	}
	if !svc.securityPaused.Load() || svc.Config().Enabled {
		t.Fatal("write failure reopened local payment gate")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
