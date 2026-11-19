package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadDelegatePayments(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  string
		want bool
	}{
		{name: "default"},
		{name: "delegate", env: "true", want: true},
		{name: "local", env: "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			t.Setenv("SERVER_DELEGATE_PAYMENTS", tc.env)
			cfg, err := Load()
			require.NoError(t, err)
			require.Equal(t, tc.want, cfg.Server.DelegatePayments)
		})
	}
}

func TestLoadRejectsMalformedDelegatePayments(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("SERVER_DELEGATE_PAYMENTS", "ture")
	_, err := LoadForBootstrap()
	require.ErrorContains(t, err, "server.delegate_payments")
}
