package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ipnotice"
	"github.com/stretchr/testify/require"
)

func TestGatewayIPNoticeSettingIsolationAndSnapshot(t *testing.T) {
	repo := &panelRateLimitSettingRepo{values: map[string]string{}}
	settings := NewSettingService(repo, nil)
	policy := ipnotice.Settings{Enabled: true, IPs: []string{"203.0.113.1"}, Message: "operator notice", RPM: 100}
	require.NoError(t, settings.SetGatewayIPNotice(context.Background(), policy))
	require.Len(t, repo.values, 1)
	require.Contains(t, repo.values, SettingKeyGatewayIPNotice)
	policy.IPs[0] = "203.0.113.99"
	before := repo.getValueCalls
	for range 1000 {
		_, match := settings.GatewayIPNoticeSnapshot().Match("203.0.113.1")
		require.True(t, match)
	}
	require.Equal(t, before, repo.getValueCalls, "warm snapshot must not query PostgreSQL")
	repo.getValueErr = errors.New("database unavailable")
	require.Error(t, settings.RefreshGatewayIPNotice(context.Background()))
	_, match := settings.GatewayIPNoticeSnapshot().Match("203.0.113.1")
	require.True(t, match, "refresh failure preserves the last valid policy")
	repo.getValueErr = nil
	policy.Enabled = false
	require.NoError(t, settings.SetGatewayIPNotice(context.Background(), policy))
	_, match = settings.GatewayIPNoticeSnapshot().Match("203.0.113.1")
	require.False(t, match)
}
