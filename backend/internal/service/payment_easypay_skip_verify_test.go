package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseEasyPaySkipUpstreamVerify(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{"true", true},
		{"1", true},
		{" YES ", true},
		{"On", true},
		{"false", false},
		{"", false},
		{"garbage", false},
	} {
		require.Equal(t, tc.want, parseEasyPaySkipUpstreamVerify(tc.raw), "raw=%q", tc.raw)
	}
}

func TestEasyPaySkipUpstreamVerifyWithoutConfigService(t *testing.T) {
	require.False(t, (&PaymentService{}).easyPaySkipUpstreamVerify(context.Background()))
}
