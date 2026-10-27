package mihomo

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBPSWarmPoolReusesIPManagementSourcesAndStatus(t *testing.T) {
	m := sourceTestManager(t)
	address := "https://subscription.invalid/?token=test-private-token"
	source := subscriptionID(address)
	m.saved.URLs = []string{address}
	m.saved.SubscriptionLabels = map[string]string{source: "Airport"}
	m.saved.SubscriptionCache = map[string]subscriptionCache{source: cachedSource("shared-exit")}
	m.saved.DownloadMode = SubscriptionDownloadDirect
	require.NoError(t, m.resolveSources(t.Context(), &m.saved, false))
	_, err := m.config(m.saved)
	require.NoError(t, err)
	var probes atomic.Int32
	m.bpsProbe = func(context.Context, string) error { probes.Add(1); return nil }
	m.warmBPSPool(t.Context(), 2)
	status := m.Status()
	require.Equal(t, SubscriptionDownloadDirect, status.DownloadMode)
	require.Len(t, status.SubscriptionItems, 1)
	require.Equal(t, source, status.SubscriptionItems[0].ID)
	require.Equal(t, "Airport", status.SubscriptionItems[0].Label)
	require.Len(t, status.NodeStates, 1)
	require.Equal(t, []string{source}, status.NodeStates[0].SubscriptionIDs)
	require.Equal(t, 2, status.BPSWarmPool.Target)
	require.Equal(t, 1, status.BPSWarmPool.Ready)
	require.Equal(t, 1, status.BPSWarmPool.ReadySubscription)
	encoded, err := json.Marshal(status)
	require.NoError(t, err)
	require.Contains(t, string(encoded), "subscription_download_mode")
	require.Contains(t, string(encoded), "bps_warm_pool")
	require.NotContains(t, string(encoded), "test-private-token")
	before := probes.Load()
	first, err := AcquireBPSLease(t.Context(), "ip-management:first")
	require.NoError(t, err)
	defer first.Release()
	second, err := AcquireBPSLease(t.Context(), "ip-management:second")
	require.NoError(t, err)
	defer second.Release()
	require.Equal(t, first.ProxyURL, second.ProxyURL, "ready exits are reused when capacity is scarce")
	require.Equal(t, before, probes.Load(), "user requests must not run qualification probes")
	require.NoError(t, m.Submit("subscription_disable/"+source, nil, false))
	status = waitSourceOperation(t, m)
	require.Empty(t, status.Error)
	require.Equal(t, SubscriptionDownloadDirect, status.DownloadMode)
	require.False(t, status.SubscriptionItems[0].Enabled)
	require.Zero(t, status.BPSWarmPool.Ready)
	_, err = AcquireBPSLease(t.Context(), "ip-management:first")
	require.Error(t, err, "disabled bindings must drain instead of switching active requests")
	first.Release()
	second.Release()
	_, err = AcquireBPSLease(t.Context(), "ip-management:new")
	require.Error(t, err, "disabled subscription must not remain available to BPS")
	require.Equal(t, before, probes.Load())
}
