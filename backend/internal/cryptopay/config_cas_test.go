package cryptopay

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type configCASStore struct {
	value    *string
	expected *string
	writes   int
}

func (s *configCASStore) GetCryptoSetting(context.Context, string) (string, error) {
	return "", errors.New("updates must not use the cached read")
}

func (s *configCASStore) SetCryptoSetting(context.Context, string, string) error {
	return errors.New("updates must not use an unconditional write")
}

func (s *configCASStore) GetCryptoSettingUncached(context.Context, string) (string, error) {
	if s.value == nil {
		return "", service.ErrSettingNotFound
	}
	return *s.value, nil
}

func (s *configCASStore) CompareAndSwapCryptoSetting(_ context.Context, _ string, expected *string, value string) (bool, error) {
	s.expected = expected
	if (expected == nil) != (s.value == nil) || (expected != nil && *expected != *s.value) {
		return false, nil
	}
	s.writes++
	s.value = &value
	return true, nil
}

func TestConfigCASMatchesExactStoredJSONInsteadOfNormalizedEncoding(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	raw := " {\n  \"rpc\": {\"bsc\": \"https://fixture.test\"}, \"enabled\": false\n}\n"
	store := &configCASStore{value: &raw}
	cfg, expected, err := LoadConfigForUpdate(t.Context(), store)
	require.NoError(t, err)
	require.NotNil(t, expected)
	require.Equal(t, raw, *expected)
	cfg.SweepAuto = true
	require.NoError(t, SaveConfigIfUnchanged(t.Context(), store, cfg, expected))
	require.Equal(t, raw, *store.expected)
	require.Equal(t, 1, store.writes)
}

func TestConfigCASFirstSaveRejectsAnotherReplicaInitialization(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	store := &configCASStore{}
	first, firstExpected, err := LoadConfigForUpdate(t.Context(), store)
	require.NoError(t, err)
	require.Nil(t, firstExpected)
	second, secondExpected, err := LoadConfigForUpdate(t.Context(), store)
	require.NoError(t, err)
	require.Nil(t, secondExpected)
	first.RPC[ChainBSC] = "https://first.example.test"
	require.NoError(t, SaveConfigIfUnchanged(t.Context(), store, first, firstExpected))
	stored := *store.value
	second.HotWalletEVM = "0x2222222222222222222222222222222222222222"
	require.ErrorIs(t, SaveConfigIfUnchanged(t.Context(), store, second, secondExpected), ErrConfigConflict)
	require.Equal(t, 1, store.writes)
	require.Equal(t, stored, *store.value)
}

type nonAtomicConfigStore struct{}

func (nonAtomicConfigStore) GetCryptoSetting(context.Context, string) (string, error) {
	panic("must not downgrade to a cached read")
}

func (nonAtomicConfigStore) SetCryptoSetting(context.Context, string, string) error {
	panic("must not downgrade to an unconditional write")
}

func TestConfigCASDoesNotDowngradeUnsupportedOrReadOnlyStores(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	store := nonAtomicConfigStore{}
	_, _, err := LoadConfigForUpdate(t.Context(), store)
	require.ErrorIs(t, err, service.ErrAtomicSettingUnavailable)
	require.ErrorIs(t, SaveConfigIfUnchanged(t.Context(), store, DefaultCryptoConfig(), nil), service.ErrAtomicSettingUnavailable)
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_WRITE_ENABLED", "false")
	require.ErrorIs(t, SaveConfigIfUnchanged(t.Context(), store, DefaultCryptoConfig(), nil), mode.ErrReadOnly)
	_, err = NewServiceSettingStore(nil).CompareAndSwapCryptoSetting(t.Context(), SettingKeyCryptoConfig, nil, "{}")
	require.ErrorIs(t, err, mode.ErrReadOnly)
}
