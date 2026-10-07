package config

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestAccountPoolOwnerConfig(t *testing.T) {
	t.Run("disabled by default", func(t *testing.T) {
		resetViperWithJWTSecret(t)
		t.Setenv("SECURITY_ACCOUNT_POOL_OWNER_USER_ID", "")
		cfg, err := Load()
		require.NoError(t, err)
		require.Zero(t, cfg.Security.AccountPoolOwnerUserID)
	})
	t.Run("stable ID from environment", func(t *testing.T) {
		resetViperWithJWTSecret(t)
		t.Setenv("SECURITY_ACCOUNT_POOL_OWNER_USER_ID", "7")
		cfg, err := Load()
		require.NoError(t, err)
		require.Equal(t, int64(7), cfg.Security.AccountPoolOwnerUserID)
	})
	t.Run("reject negative configured ID", func(t *testing.T) {
		resetViperWithJWTSecret(t)
		viper.Set("security.account_pool_owner_user_id", -1)
		_, err := Load()
		require.ErrorContains(t, err, "security.account_pool_owner_user_id")
	})
}
