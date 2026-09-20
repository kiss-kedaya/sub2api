package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPaymentRechargeCenterSettingDefaultsOffAndPreservesOmittedUpdates(t *testing.T) {
	ctx := context.Background()
	repo := &paymentConfigSettingRepoStub{values: map[string]string{SettingEnabledPaymentTypes: "epusdt"}}
	svc := NewPaymentConfigService(nil, repo, nil)
	cfg, err := svc.GetPaymentConfig(ctx)
	require.NoError(t, err)
	require.False(t, cfg.RechargeCenterEnabled)

	for _, enabled := range []bool{true, false} {
		require.NoError(t, svc.UpdatePaymentConfig(ctx, UpdatePaymentConfigRequest{RechargeCenterEnabled: &enabled}))
		cfg, err = svc.GetPaymentConfig(ctx)
		require.NoError(t, err)
		require.Equal(t, enabled, cfg.RechargeCenterEnabled)
		require.Equal(t, []string{"epusdt"}, cfg.EnabledTypes)

		paymentEnabled := true
		require.NoError(t, svc.UpdatePaymentConfig(ctx, UpdatePaymentConfigRequest{Enabled: &paymentEnabled}))
		cfg, err = svc.GetPaymentConfig(ctx)
		require.NoError(t, err)
		require.Equal(t, enabled, cfg.RechargeCenterEnabled, "unrelated updates must preserve the entry setting")
	}
}
