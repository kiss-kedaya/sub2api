package mihomo

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func mixedSourceManager(t *testing.T) (*Manager, string, string) {
	t.Helper()
	m := bpsTestManager(t)
	var ids []string
	for id := range m.bpsPorts {
		ids = append(ids, id)
	}
	subscription, dynamic := ids[0], ids[1]
	m.bpsMu.Lock()
	m.bpsDynamic = map[string]bool{dynamic: true}
	m.bpsMu.Unlock()
	return m, subscription, dynamic
}

func TestReadySubscriptionPriorityPreservesExistingDynamicAffinity(t *testing.T) {
	m, subscription, dynamic := mixedSourceManager(t)
	for _, id := range []string{subscription, dynamic} {
		require.NoError(t, m.checkBPSHealth(t.Context(), id, fmt.Sprintf("http://127.0.0.1:%d", m.bpsPorts[id])))
	}
	existing, err := m.acquireScopedBPSLease(t.Context(), "existing", false, map[string]bool{subscription: true})
	require.NoError(t, err)
	require.Equal(t, dynamic, existing.node)
	existing.Release()
	current, err := m.acquireScopedBPSLease(t.Context(), "existing", false, nil)
	require.NoError(t, err)
	require.Equal(t, dynamic, current.node)
	current.Release()
	fresh, err := m.acquireScopedBPSLease(t.Context(), "fresh", false, nil)
	require.NoError(t, err)
	require.Equal(t, subscription, fresh.node)
	fresh.Release()
	status := m.BPSWarmStatus()
	require.Equal(t, 1, status.ReadySubscription)
	require.Equal(t, 1, status.ReadyDynamic)
	require.Equal(t, 2, status.Ready)
}

func TestWarmSubscriptionPriorityDoesNotProbeDynamicWhenTargetIsSatisfied(t *testing.T) {
	m, subscription, dynamic := mixedSourceManager(t)
	var subscriptionCalls, dynamicCalls atomic.Int32
	m.bpsProbe = func(_ context.Context, proxy string) error {
		if proxy == fmt.Sprintf("http://127.0.0.1:%d", m.bpsPorts[subscription]) {
			subscriptionCalls.Add(1)
		} else if proxy == fmt.Sprintf("http://127.0.0.1:%d", m.bpsPorts[dynamic]) {
			dynamicCalls.Add(1)
		}
		return nil
	}
	m.warmBPSPool(t.Context(), 1)
	require.Positive(t, subscriptionCalls.Load())
	require.Zero(t, dynamicCalls.Load())
	require.Equal(t, 1, m.BPSWarmStatus().ReadySubscription)
}

func TestWarmReplenishesSubscriptionsEvenWhenDynamicCapacityIsReady(t *testing.T) {
	m, subscription, dynamic := mixedSourceManager(t)
	require.NoError(t, m.checkBPSHealth(t.Context(), dynamic, fmt.Sprintf("http://127.0.0.1:%d", m.bpsPorts[dynamic])))
	m.warmBPSPool(t.Context(), 1)
	require.Equal(t, 1, m.BPSWarmStatus().ReadySubscription)
	// A cooling subscription cannot prevent use of an already-qualified dynamic exit.
	m.bpsMu.Lock()
	m.bpsHealthAtLocked(subscription, time.Now()).retryAfter = time.Now().Add(time.Minute)
	m.bpsMu.Unlock()
	lease, err := m.acquireScopedBPSLease(t.Context(), "fallback", false, nil)
	require.NoError(t, err)
	require.Equal(t, dynamic, lease.node)
	lease.Release()
}
