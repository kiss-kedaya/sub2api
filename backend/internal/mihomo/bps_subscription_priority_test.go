package mihomo

import (
	"context"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func subscriptionPriorityManager(t *testing.T) *Manager {
	m := bpsTestManager(t)
	for i := 0; i < 40; i++ {
		m.saved.Nodes = append(m.saved.Nodes, map[string]any{"name": fmt.Sprintf("test-dynamic-%d", i)})
	}
	_, err := m.config(m.saved)
	require.NoError(t, err)
	for _, node := range m.saved.Nodes {
		if name, ok := node["name"].(string); ok && strings.HasPrefix(name, "test-dynamic-") {
			m.bpsDynamic[harvestDigest(node)] = true
		}
	}
	return m
}
func TestBPSWarmSubscriptionTierBeforeDynamic(t *testing.T) {
	m := subscriptionPriorityManager(t)
	var dynamicCalls atomic.Int32
	m.bpsProbe = func(_ context.Context, proxy string) error {
		for node, port := range m.bpsPorts {
			if proxy == fmt.Sprintf("http://127.0.0.1:%d", port) && m.bpsDynamic[node] {
				dynamicCalls.Add(1)
			}
		}
		return nil
	}
	m.warmBPSPool(t.Context(), 2)
	require.Equal(t, 2, m.BPSWarmStatus().ReadySubscription)
	require.Zero(t, dynamicCalls.Load())
}
func TestBPSSubscriptionPreferenceKeepsHealthyAffinity(t *testing.T) {
	m := subscriptionPriorityManager(t)
	dynamic := ""
	for node := range m.bpsDynamic {
		dynamic = node
		break
	}
	require.NoError(t, m.checkBPSHealth(t.Context(), dynamic, fmt.Sprintf("http://127.0.0.1:%d", m.bpsPorts[dynamic])))
	existing, err := m.acquireBPSLease(t.Context(), "existing", nil)
	require.NoError(t, err)
	require.True(t, m.bpsDynamic[existing.node])
	existing.Release()
	// Even a filled dynamic pool must make room for subscription qualification.
	m.warmBPSPool(t.Context(), 1)
	require.Positive(t, m.BPSWarmStatus().ReadySubscription)
	m.bpsMu.Lock()
	for i := 0; i < 30; i++ {
		m.bpsHealthAtLocked(dynamic, time.Now()).modelQuality.observe(true, time.Now())
	}
	m.bpsMu.Unlock()
	again, err := m.acquireBPSLease(t.Context(), "existing", nil)
	require.NoError(t, err)
	require.Equal(t, existing.node, again.node)
	again.Release()
	fresh, err := m.acquireBPSLease(t.Context(), "new", nil)
	require.NoError(t, err)
	require.False(t, m.bpsDynamic[fresh.node])
	fresh.Release()
}
func TestBPSDynamicRemainsFallbackAfterSubscriptionsFail(t *testing.T) {
	m := subscriptionPriorityManager(t)
	m.bpsProbe = func(_ context.Context, proxy string) error {
		for node, port := range m.bpsPorts {
			if proxy == fmt.Sprintf("http://127.0.0.1:%d", port) && !m.bpsDynamic[node] {
				return errors.New("subscription down")
			}
		}
		return nil
	}
	m.warmBPSPool(t.Context(), 2)
	m.warmBPSPool(t.Context(), 2)
	require.Zero(t, m.BPSWarmStatus().ReadySubscription)
	require.Positive(t, m.BPSWarmStatus().ReadyDynamic)
	lease, err := m.acquireBPSLease(t.Context(), "fallback", nil)
	require.NoError(t, err)
	require.True(t, m.bpsDynamic[lease.node])
	lease.Release()
}

func TestBPSCanceledSubscriptionProbeRetainsPriority(t *testing.T) {
	m := subscriptionPriorityManager(t)
	candidates, _, err := m.bpsProbeCandidates("inspect", nil)
	require.NoError(t, err)
	fast, slow := candidates[0].proxy, candidates[1].proxy
	started := make(chan struct{})
	var slowCalls, dynamicCalls atomic.Int32
	m.bpsProbe = func(ctx context.Context, proxy string) error {
		if proxy == fast {
			<-started
			return nil
		}
		if proxy == slow {
			if slowCalls.Add(1) == 1 {
				close(started)
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		}
		dynamicCalls.Add(1)
		return nil
	}
	m.warmBPSPool(t.Context(), 2)
	require.Equal(t, int32(2), slowCalls.Load())
	require.Zero(t, dynamicCalls.Load(), "losing a probe race must not demote an otherwise healthy subscription")
	require.Equal(t, 2, m.BPSWarmStatus().ReadySubscription)
}

func TestBPSWarmRetriesEligibleSubscriptionBeforeDynamic(t *testing.T) {
	m := subscriptionPriorityManager(t)
	candidates, _, err := m.bpsProbeCandidates("inspect", nil)
	require.NoError(t, err)
	first, retry := candidates[0], candidates[1]
	require.True(t, first.subscription)
	require.True(t, retry.subscription)
	require.NoError(t, m.checkBPSHealth(t.Context(), first.node, first.proxy))
	m.bpsMu.Lock()
	health := m.bpsHealthAtLocked(retry.node, time.Now())
	health.failures = 1
	health.retryAfter = time.Now().Add(-time.Second)
	m.bpsMu.Unlock()
	var dynamicCalls atomic.Int32
	m.bpsProbe = func(_ context.Context, proxy string) error {
		if proxy != retry.proxy && proxy != first.proxy {
			dynamicCalls.Add(1)
		}
		return nil
	}
	m.warmBPSPool(t.Context(), 2)
	require.Equal(t, 2, m.BPSWarmStatus().ReadySubscription)
	require.Zero(t, dynamicCalls.Load(), "an eligible subscription must finish requalification before spending capacity on dynamic exits")
}
