package cryptopay

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/stretchr/testify/require"
)

const testSecurityInvalidatedAt = "2026-11-20T19:51:46Z"

// expectSecurityPauseOnce queues the claim + conditional disable + commit that
// a single lock episode applies exactly once.
func expectSecurityPauseOnce(mock sqlmock.Sqlmock, episode string) {
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO settings").
		WithArgs(SettingKeySecurityPauseMarker, episode).
		WillReturnRows(sqlmock.NewRows([]string{"claimed"}).AddRow(1))
	mock.ExpectExec("UPDATE settings SET value=jsonb_set").
		WithArgs(SettingKeyCryptoConfig).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
}

func TestSecurityPauseDoesNotWaitOnReloadAndPreservesOtherConfig(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	svc := &Service{db: db, wallet: wallet.New(db), config: CryptoConfig{Enabled: true, Chains: []string{"base"}, SweepAuto: true}}
	// The scanner can discover an incident while reload holds this mutex
	// waiting for it. Pausing/notifying must never reacquire that mutex.
	expectSecurityPauseOnce(mock, "unauthorized_token_outflow")
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
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	svc := &Service{db: db, wallet: wallet.New(db), config: CryptoConfig{Enabled: true}}
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO settings").WillReturnError(errors.New("database offline"))
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

// The security monitor observes a persistent lock every tick. The channel has
// to be paused once per incident, not on every observation: otherwise an
// operator's explicit re-enable is silently reverted seconds later.
func TestSecurityPauseIsAppliedOncePerIncident(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	svc := &Service{db: db, wallet: wallet.New(db), config: CryptoConfig{Enabled: true}}
	status := wallet.SecurityStatus{Enabled: true, Initialized: true, Locked: true, DepositsPaused: true, Reason: "deposit_address_changed", LockedAt: testSecurityInvalidatedAt}

	expectSecurityPauseOnce(mock, testSecurityInvalidatedAt)
	require.NoError(t, svc.handleSecurityIncident(t.Context(), status))

	// An operator re-enables the channel; the same incident must not rewrite it.
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO settings").
		WithArgs(SettingKeySecurityPauseMarker, testSecurityInvalidatedAt).
		WillReturnRows(sqlmock.NewRows([]string{"claimed"}))
	mock.ExpectCommit()
	require.NoError(t, svc.handleSecurityIncident(t.Context(), status), "a repeated observation must not fail the guard")
	require.NoError(t, mock.ExpectationsWereMet(), "re-enable survived the repeated guard tick without another write")

	// A genuinely new incident pauses the channel again.
	next := wallet.SecurityStatus{Enabled: true, Locked: true, Reason: "unauthorized_token_outflow", LockedAt: "2026-11-21T01:02:03Z"}
	expectSecurityPauseOnce(mock, next.LockedAt)
	require.NoError(t, svc.handleSecurityIncident(t.Context(), next))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSecurityLockEpisodeFallsBackToReason(t *testing.T) {
	if got := securityLockEpisode(wallet.SecurityStatus{Locked: true, LockedAt: " 2026-11-20T19:51:46Z "}); got != "2026-11-20T19:51:46Z" {
		t.Fatalf("episode = %q, want the trimmed lock time", got)
	}
	if got := securityLockEpisode(wallet.SecurityStatus{Locked: true, Reason: "vault_unavailable"}); got != "vault_unavailable" {
		t.Fatalf("episode = %q, want the reason when LockedAt is empty", got)
	}
	if got := securityLockEpisode(wallet.SecurityStatus{Locked: true}); got != "locked" {
		t.Fatalf("episode = %q, want the stable fallback", got)
	}
}
