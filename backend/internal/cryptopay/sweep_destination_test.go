package cryptopay

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

const (
	sweepTestEVMOriginal = "0x1111111111111111111111111111111111111111"
	sweepTestEVMChanged  = "0x2222222222222222222222222222222222222222"
	sweepTestSolana      = "7xKXtg2CW87d97TXJSDpbD5jBkheTqA83TZRuJosgAsU"
)

// sweepDestinationStore is a tiny atomic settings store. Every write is counted
// so the idempotent path can assert that nothing was persisted.
type sweepDestinationStore struct {
	value  *string
	writes int
	// failNext makes the next CAS report a conflict, letting tests exercise the
	// "someone else changed the config" branch.
	failNext bool
}

func (s *sweepDestinationStore) GetCryptoSetting(context.Context, string) (string, error) {
	if s.value == nil {
		return "", service.ErrSettingNotFound
	}
	return *s.value, nil
}

func (s *sweepDestinationStore) SetCryptoSetting(_ context.Context, _ string, value string) error {
	s.writes++
	s.value = &value
	return nil
}

func (s *sweepDestinationStore) GetCryptoSettingUncached(context.Context, string) (string, error) {
	if s.value == nil {
		return "", service.ErrSettingNotFound
	}
	return *s.value, nil
}

func (s *sweepDestinationStore) CompareAndSwapCryptoSetting(_ context.Context, _ string, expected *string, value string) (bool, error) {
	if s.failNext {
		s.failNext = false
		return false, nil
	}
	if (expected == nil) != (s.value == nil) || (expected != nil && *expected != *s.value) {
		return false, nil
	}
	s.writes++
	s.value = &value
	return true, nil
}

func (s *sweepDestinationStore) config(t *testing.T) CryptoConfig {
	t.Helper()
	cfg, err := LoadConfig(t.Context(), s)
	require.NoError(t, err)
	return cfg
}

func newSweepDestinationStore(t *testing.T, cfg CryptoConfig) *sweepDestinationStore {
	t.Helper()
	normalized, err := NormalizeConfig(cfg)
	require.NoError(t, err)
	raw, err := json.Marshal(normalized)
	require.NoError(t, err)
	value := string(raw)
	return &sweepDestinationStore{value: &value}
}

func TestValidateSweepReadyRejectsTamperedDestination(t *testing.T) {
	lock := SweepDestinationLock{EVM: sweepTestEVMOriginal, LockedAt: time.Unix(100, 0).UTC(), LockedByUserID: 41}

	t.Run("consistent destination is ready", func(t *testing.T) {
		cfg := CryptoConfig{HotWalletEVM: sweepTestEVMOriginal, SweepDestinationLock: &lock}
		require.NoError(t, cfg.ValidateSweepReady())
	})

	t.Run("a direct database edit stops the sweep", func(t *testing.T) {
		// The settings row was rewritten outside the dedicated endpoint: this is
		// the host-compromise case the lock exists for. Sweeping must refuse.
		cfg := CryptoConfig{HotWalletEVM: sweepTestEVMChanged, SweepDestinationLock: &lock}
		err := cfg.ValidateSweepReady()
		require.ErrorIs(t, err, ErrSweepDestinationLocked)
		require.Contains(t, err.Error(), "sweep disabled")
	})
}

func TestEnforceSweepDestinationLockRejectsMovedDestination(t *testing.T) {
	locked := SweepDestinationLock{EVM: sweepTestEVMOriginal, LockedAt: time.Unix(100, 0).UTC(), LockedByUserID: 41}

	t.Run("locked destination unchanged passes", func(t *testing.T) {
		cfg := CryptoConfig{HotWalletEVM: sweepTestEVMOriginal, SweepDestinationLock: &locked}
		require.NoError(t, cfg.EnforceSweepDestinationLock())
	})

	t.Run("case difference is not a change", func(t *testing.T) {
		// Same 20 bytes, different EIP-55 casing: the sweep sends to the same
		// address, so the lock must treat it as unchanged.
		const checksummed = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"
		cfg := CryptoConfig{
			HotWalletEVM:         strings.ToLower(checksummed),
			SweepDestinationLock: &SweepDestinationLock{EVM: checksummed, LockedAt: time.Unix(100, 0).UTC()},
		}
		require.NoError(t, cfg.EnforceSweepDestinationLock())
	})

	t.Run("different destination is rejected with the documented code", func(t *testing.T) {
		cfg := CryptoConfig{HotWalletEVM: sweepTestEVMChanged, SweepDestinationLock: &locked}
		err := cfg.EnforceSweepDestinationLock()
		require.ErrorIs(t, err, ErrSweepDestinationLocked)
		require.Equal(t, "SWEEP_DESTINATION_LOCKED", errorReason(t, err))
	})

	t.Run("adding a destination the lock does not pin is rejected", func(t *testing.T) {
		cfg := CryptoConfig{HotWalletEVM: sweepTestEVMOriginal, HotWalletSolana: sweepTestSolana, SweepDestinationLock: &locked}
		require.ErrorIs(t, cfg.EnforceSweepDestinationLock(), ErrSweepDestinationLocked)
	})

	t.Run("no lock means no restriction", func(t *testing.T) {
		cfg := CryptoConfig{HotWalletEVM: sweepTestEVMChanged}
		require.NoError(t, cfg.EnforceSweepDestinationLock())
	})
}

// errorReason extracts the machine-readable reason from an application error.
func errorReason(t *testing.T, err error) string {
	t.Helper()
	encoded, marshalErr := json.Marshal(err)
	require.NoError(t, marshalErr)
	var payload struct {
		Reason string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(encoded, &payload))
	return payload.Reason
}

func TestValidateSweepDestinationInput(t *testing.T) {
	t.Run("empty pair", func(t *testing.T) {
		require.ErrorIs(t, ValidateSweepDestinationInput(SweepDestination{}), errSweepDestinationRequired)
	})
	t.Run("valid lowercase evm", func(t *testing.T) {
		require.NoError(t, ValidateSweepDestinationInput(SweepDestination{EVM: sweepTestEVMOriginal}))
	})
	t.Run("valid checksummed evm", func(t *testing.T) {
		require.NoError(t, ValidateSweepDestinationInput(SweepDestination{EVM: "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"}))
	})
	t.Run("valid solana only", func(t *testing.T) {
		require.NoError(t, ValidateSweepDestinationInput(SweepDestination{Solana: sweepTestSolana}))
	})
	t.Run("invalid evm", func(t *testing.T) {
		require.ErrorIs(t, ValidateSweepDestinationInput(SweepDestination{EVM: "0x1234"}), errSweepDestinationInvalid)
	})
	t.Run("mixed case evm with bad checksum is rejected", func(t *testing.T) {
		// Same address as the EIP-55 test vector with the final nibble's case
		// flipped: still 20 valid bytes, but the checksum no longer matches.
		bad := "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAeD"
		require.ErrorIs(t, ValidateSweepDestinationInput(SweepDestination{EVM: bad}), errSweepDestinationInvalid)
	})
	t.Run("invalid solana", func(t *testing.T) {
		require.ErrorIs(t, ValidateSweepDestinationInput(SweepDestination{Solana: "0OIl"}), errSweepDestinationInvalid)
	})
}

func TestUpdateSweepDestinationCreatesLockAndIsIdempotent(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	store := newSweepDestinationStore(t, CryptoConfig{Enabled: true, Chains: []string{ChainBSC}})
	now := time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC)
	evm := sweepTestEVMChanged

	change, err := UpdateSweepDestination(t.Context(), store, SweepDestinationUpdate{
		HotWalletEVM: &evm, ActorUserID: 41, Now: now,
	})
	require.NoError(t, err)
	require.True(t, change.Changed)
	require.True(t, change.Locked)
	require.True(t, change.LockCreated)
	require.Equal(t, sweepTestEVMChanged, change.Lock.EVM)
	require.Equal(t, int64(41), change.Lock.LockedByUserID)
	require.Equal(t, now, change.Lock.LockedAt)
	require.Equal(t, 1, store.writes)

	stored := store.config(t)
	require.Equal(t, sweepTestEVMChanged, stored.HotWalletEVM)
	require.NotNil(t, stored.SweepDestinationLock)
	require.Equal(t, sweepTestEVMChanged, stored.SweepDestinationLock.EVM)

	// Re-submitting the same destination must not move the timestamp or write.
	replay, err := UpdateSweepDestination(t.Context(), store, SweepDestinationUpdate{
		HotWalletEVM: &evm, ActorUserID: 41, Now: now.Add(2 * time.Hour),
	})
	require.NoError(t, err)
	require.False(t, replay.Changed)
	require.False(t, replay.Locked)
	require.Equal(t, 1, store.writes)
	require.Equal(t, now, store.config(t).SweepDestinationLock.LockedAt)
}

func TestUpdateSweepDestinationMovesLockAfterVerification(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	lock := SweepDestinationLock{EVM: sweepTestEVMOriginal, LockedAt: time.Unix(100, 0).UTC(), LockedByUserID: 41}
	store := newSweepDestinationStore(t, CryptoConfig{Enabled: true, Chains: []string{ChainBSC}, HotWalletEVM: sweepTestEVMOriginal, SweepDestinationLock: &lock})

	// The dedicated endpoint is the documented way to move a pinned
	// destination; it rewrites both the settings row and the lock.
	evm := sweepTestEVMChanged
	now := time.Unix(200, 0).UTC()
	change, err := UpdateSweepDestination(t.Context(), store, SweepDestinationUpdate{HotWalletEVM: &evm, ActorUserID: 42, Now: now})
	require.NoError(t, err)
	require.True(t, change.Changed)
	require.True(t, change.Locked)
	require.False(t, change.LockCreated)
	require.Equal(t, sweepTestEVMOriginal, change.Previous.EVM)

	stored := store.config(t)
	require.Equal(t, sweepTestEVMChanged, stored.HotWalletEVM)
	require.Equal(t, sweepTestEVMChanged, stored.SweepDestinationLock.EVM)
	require.Equal(t, now, stored.SweepDestinationLock.LockedAt)
	require.Equal(t, int64(42), stored.SweepDestinationLock.LockedByUserID)
}

func TestUpdateSweepDestinationRejectsInvalidInputWithoutWriting(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	store := newSweepDestinationStore(t, CryptoConfig{Enabled: true, Chains: []string{ChainBSC}})
	invalid := "0xnothex"
	_, err := UpdateSweepDestination(t.Context(), store, SweepDestinationUpdate{HotWalletEVM: &invalid, ActorUserID: 41})
	require.ErrorIs(t, err, errSweepDestinationInvalid)
	require.Zero(t, store.writes)

	_, err = UpdateSweepDestination(t.Context(), store, SweepDestinationUpdate{ActorUserID: 41})
	require.ErrorIs(t, err, errSweepDestinationRequired)
	require.Zero(t, store.writes)
}

func TestUpdateSweepDestinationReturnsCASConflict(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	store := newSweepDestinationStore(t, CryptoConfig{Enabled: true, Chains: []string{ChainBSC}})
	store.failNext = true
	evm := sweepTestEVMChanged
	_, err := UpdateSweepDestination(t.Context(), store, SweepDestinationUpdate{HotWalletEVM: &evm, ActorUserID: 41})
	require.ErrorIs(t, err, ErrConfigConflict)
	require.Zero(t, store.writes)
}

func TestUpdateSweepDestinationRepairsDriftedConfigWithoutMovingLockTime(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	originalLocked := time.Unix(100, 0).UTC()
	lock := SweepDestinationLock{EVM: sweepTestEVMOriginal, LockedAt: originalLocked, LockedByUserID: 41}
	// A direct database edit moved the settings row away from the pinned value.
	store := newSweepDestinationStore(t, CryptoConfig{
		Enabled: true, Chains: []string{ChainBSC},
		HotWalletEVM: sweepTestEVMChanged, SweepDestinationLock: &lock,
	})

	original := sweepTestEVMOriginal
	change, err := UpdateSweepDestination(t.Context(), store, SweepDestinationUpdate{
		HotWalletEVM: &original, ActorUserID: 41, Now: time.Unix(300, 0).UTC(),
	})
	require.NoError(t, err)
	require.True(t, change.Changed)
	require.False(t, change.Locked)
	stored := store.config(t)
	require.Equal(t, sweepTestEVMOriginal, stored.HotWalletEVM)
	require.Equal(t, originalLocked, stored.SweepDestinationLock.LockedAt)
}

func TestPinSweepDestinationLock(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	now := time.Unix(500, 0).UTC()

	t.Run("creates lock from configured destination", func(t *testing.T) {
		store := newSweepDestinationStore(t, CryptoConfig{Enabled: true, Chains: []string{ChainBSC}, HotWalletEVM: sweepTestEVMOriginal})
		created, active, err := PinSweepDestinationLock(t.Context(), store, 41, now)
		require.NoError(t, err)
		require.True(t, created)
		require.True(t, active)
		stored := store.config(t)
		require.Equal(t, sweepTestEVMOriginal, stored.SweepDestinationLock.EVM)
		require.Equal(t, now, stored.SweepDestinationLock.LockedAt)
	})

	t.Run("refresh keeps the pinned addresses", func(t *testing.T) {
		lock := SweepDestinationLock{EVM: sweepTestEVMOriginal, LockedAt: time.Unix(100, 0).UTC(), LockedByUserID: 41}
		// A re-split must not adopt the drifted settings value as the new lock.
		store := newSweepDestinationStore(t, CryptoConfig{
			Enabled: true, Chains: []string{ChainBSC},
			HotWalletEVM: sweepTestEVMChanged, SweepDestinationLock: &lock,
		})
		created, active, err := PinSweepDestinationLock(t.Context(), store, 42, now)
		require.NoError(t, err)
		require.False(t, created)
		require.True(t, active)
		stored := store.config(t)
		require.Equal(t, sweepTestEVMOriginal, stored.SweepDestinationLock.EVM)
		require.Equal(t, now, stored.SweepDestinationLock.LockedAt)
		require.Equal(t, int64(42), stored.SweepDestinationLock.LockedByUserID)
	})

	t.Run("nothing to pin", func(t *testing.T) {
		store := newSweepDestinationStore(t, CryptoConfig{Enabled: true, Chains: []string{ChainBSC}})
		created, active, err := PinSweepDestinationLock(t.Context(), store, 41, now)
		require.NoError(t, err)
		require.False(t, created)
		require.False(t, active)
		require.Zero(t, store.writes)
	})
}

func TestSweepDestinationLockNormalizationFailsClosed(t *testing.T) {
	t.Run("both addresses empty", func(t *testing.T) {
		cfg := CryptoConfig{SweepDestinationLock: &SweepDestinationLock{LockedAt: time.Unix(1, 0).UTC()}}
		_, err := NormalizeConfig(cfg)
		require.Error(t, err)
	})
	t.Run("invalid pinned address", func(t *testing.T) {
		cfg := CryptoConfig{SweepDestinationLock: &SweepDestinationLock{EVM: "0x1234", LockedAt: time.Unix(1, 0).UTC()}}
		_, err := NormalizeConfig(cfg)
		require.Error(t, err)
	})
	t.Run("zero timestamp", func(t *testing.T) {
		cfg := CryptoConfig{SweepDestinationLock: &SweepDestinationLock{EVM: sweepTestEVMOriginal}}
		_, err := NormalizeConfig(cfg)
		require.Error(t, err)
	})
	t.Run("valid lock round-trips", func(t *testing.T) {
		cfg := CryptoConfig{SweepDestinationLock: &SweepDestinationLock{EVM: " " + sweepTestEVMOriginal + " ", LockedAt: time.Unix(1, 0).UTC()}}
		normalized, err := NormalizeConfig(cfg)
		require.NoError(t, err)
		require.Equal(t, sweepTestEVMOriginal, normalized.SweepDestinationLock.EVM)
	})
}

func TestNotifyConfigNormalization(t *testing.T) {
	t.Run("disabled empty collapses to absent", func(t *testing.T) {
		normalized, err := NormalizeConfig(CryptoConfig{Notify: &CryptoNotifyConfig{}})
		require.NoError(t, err)
		require.Nil(t, normalized.Notify)
	})
	t.Run("enabled without any channel is rejected", func(t *testing.T) {
		_, err := NormalizeConfig(CryptoConfig{Notify: &CryptoNotifyConfig{Enabled: true}})
		require.Error(t, err)
	})
	t.Run("relative webhook is rejected", func(t *testing.T) {
		_, err := NormalizeConfig(CryptoConfig{Notify: &CryptoNotifyConfig{Enabled: true, WebhookURL: "/push"}})
		require.Error(t, err)
	})
	t.Run("invalid email is rejected", func(t *testing.T) {
		_, err := NormalizeConfig(CryptoConfig{Notify: &CryptoNotifyConfig{Enabled: true, Email: "not-an-email"}})
		require.Error(t, err)
	})
	t.Run("valid channels survive", func(t *testing.T) {
		normalized, err := NormalizeConfig(CryptoConfig{Notify: &CryptoNotifyConfig{
			Enabled: true, WebhookURL: "https://api.day.app/placeholder-key", Email: "ops@example.test",
		}})
		require.NoError(t, err)
		require.NotNil(t, normalized.Notify)
		require.Equal(t, "https://api.day.app/placeholder-key", normalized.Notify.WebhookURL)
	})
}

func TestErrSweepDestinationLockedMentionsDedicatedFlow(t *testing.T) {
	require.Contains(t, ErrSweepDestinationLocked.Error(), "/admin/crypto/signer/sweep-destination")
	require.Contains(t, ErrSweepDestinationLocked.Error(), "2FA")
	require.True(t, errors.Is(ErrSweepDestinationLocked, ErrSweepDestinationLocked))
}
