package mihomo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

const bpsPortBase = 19000
const bpsMaxNodes = 4096
const bpsMaxSessions = 4096
const bpsSessionIdleTTL = 30 * time.Minute

type bpsSession struct {
	node       string // digest of the complete outbound definition, not its display name
	generation uint64 // local dynamic observation window, not a measured exit IP
	active     int
	failed     bool
	lastUsed   time.Time
}

func bpsEligible(s saved, name string) bool {
	return name != "" && countryAllowed(s, name) && (s.Disabled[name] == "" || s.Disabled[name] == "used")
}

// One immutable local port per outbound identity. Ports are never reassigned in
// a process: an HTTP connection pool or an in-flight session must not silently
// switch nodes after subscription edits. Removed/excluded nodes become REJECT.
func (m *Manager) bpsListeners(s saved) ([]any, error) {
	m.bpsMu.Lock()
	defer m.bpsMu.Unlock()
	if m.bpsPorts == nil {
		m.bpsPorts = make(map[string]int)
	}
	dynamic := make(map[string]bool, len(s.DynamicProxies))
	for _, value := range s.DynamicProxies {
		node, err := dynamicProxyNode(value)
		if err != nil {
			return nil, err
		}
		dynamic[harvestDigest(node)] = true
	}
	m.bpsDynamic = dynamic
	targets := make(map[string]string)
	for _, n := range s.Nodes {
		name, _ := n["name"].(string)
		if !bpsEligible(s, name) {
			continue
		}
		id := harvestDigest(n)
		if _, ok := m.bpsPorts[id]; !ok {
			if len(m.bpsPorts) >= bpsMaxNodes {
				return nil, errors.New("BPS proxy node capacity exceeded")
			}
			m.bpsPorts[id] = bpsPortBase + len(m.bpsPorts)
		}
		targets[id] = name
	}
	listeners := make([]any, 0, len(m.bpsPorts))
	ids := make([]string, 0, len(m.bpsPorts))
	for id := range m.bpsPorts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return m.bpsPorts[ids[i]] < m.bpsPorts[ids[j]] })
	for _, id := range ids {
		target := targets[id]
		if target == "" {
			target = "REJECT"
		}
		port := m.bpsPorts[id]
		listeners = append(listeners, map[string]any{"name": fmt.Sprintf("BPS-NODE-%d", port), "type": "mixed", "listen": "127.0.0.1", "port": port, "proxy": target})
	}
	return listeners, nil
}

// AcquireBPSSession pins a scoped client session until idle expiry or a dynamic
// observation window expires. Concurrent requests retain the binding through
// response-body closure. No harvest gate or selector is held/changed.
func AcquireBPSSession(ctx context.Context, scope string) (string, func(), error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if scope == "" {
		return "", nil, errors.New("BPS session identity required")
	}
	m := managedManager()
	if m == nil {
		return "", nil, errors.New("managed Mihomo is not running")
	}
	lease, err := m.acquireBPSLease(ctx, scope, nil)
	if err != nil {
		return "", nil, err
	}
	return lease.ProxyURL, lease.Release, nil
}

func (m *Manager) acquireBPSSession(scope string, now time.Time) (string, func(), error) {
	return m.acquireBPSSessionExcluding(scope, now, nil)
}

func (m *Manager) acquireBPSSessionExcluding(scope string, now time.Time, excluded map[string]bool) (string, func(), error) {
	return m.acquireBPSPreferredSession(scope, now, excluded, "")
}

func (m *Manager) acquireBPSPreferredSession(scope string, now time.Time, excluded map[string]bool, preferred string) (string, func(), error) {
	digest := sha256.Sum256([]byte(scope))
	key := hex.EncodeToString(digest[:])
	m.bpsMu.Lock()
	defer m.bpsMu.Unlock()
	eligible, err := m.bpsEligibleNodesLocked(now, excluded)
	if err != nil {
		return "", nil, err
	}
	loads, activeLoads := m.bpsSessionLoadsLocked(now)
	binding := m.bpsSessions[key]
	if binding != nil && (!eligible[binding.node] || binding.failed) {
		// Keep in-flight requests on their original exit. Rebind only after
		// the last response closes; late reports cannot poison a new binding.
		if binding.active > 0 {
			return "", nil, bpsSelectionError("session_draining", "bound BPS node unavailable while requests are active")
		}
		delete(m.bpsSessions, key)
		binding = nil
	}
	if preferred != "" && !eligible[preferred] {
		return "", nil, bpsSelectionError("no_eligible_nodes", "selected BPS proxy is no longer eligible")
	}
	if preferred != "" && binding != nil && binding.active == 0 && binding.node != preferred {
		delete(m.bpsSessions, key)
		binding = nil
	}
	if binding == nil {
		if len(m.bpsSessions) >= bpsMaxSessions {
			return "", nil, bpsSelectionError("session_capacity", "BPS session capacity exceeded")
		}
		node := ""
		bestScore := -1.0
		for id := range eligible {
			if preferred != "" && id != preferred {
				// Re-rank already verified exits under the binding lock. A stale
				// candidate snapshot must not pile concurrent sessions onto one
				// winner when another immediately usable exit has lower load.
				if !now.Before(m.bpsHealthAtLocked(id, now).verifiedUntil) {
					continue
				}
			}
			if m.bpsStaticMode {
				if _, ok := m.bpsStatic[id]; !ok {
					continue
				}
			} else if _, ok := m.bpsPorts[id]; !ok {
				continue
			}
			score := m.bpsQualityScoreLocked(id, activeLoads[id], loads[id], now)
			subscription := !m.bpsStaticMode && !m.bpsDynamic[id]
			bestSubscription := !m.bpsStaticMode && !m.bpsDynamic[node]
			if node == "" || (subscription && !bestSubscription) || (subscription == bestSubscription && (score > bestScore || (score == bestScore && id < node))) {
				node, bestScore = id, score
			}
		}
		if node == "" {
			return "", nil, bpsSelectionError("no_eligible_nodes", "no eligible BPS proxy nodes")
		}
		binding = &bpsSession{node: node, generation: m.bpsHealthAtLocked(node, now).generation}
		m.bpsSessions[key] = binding
	}
	target := ""
	if m.bpsStaticMode {
		staticURL, ok := m.bpsStatic[binding.node]
		if !ok {
			return "", nil, bpsSelectionError("listener_unavailable", "bound BPS proxy unavailable")
		}
		target = staticURL
	} else {
		port, ok := m.bpsPorts[binding.node]
		if !ok {
			return "", nil, bpsSelectionError("listener_unavailable", "bound BPS listener unavailable")
		}
		target = fmt.Sprintf("http://127.0.0.1:%d", port)
	}
	binding.active++
	binding.lastUsed = now
	var once sync.Once
	release := func() {
		once.Do(func() {
			m.bpsMu.Lock()
			defer m.bpsMu.Unlock()
			binding.active--
			binding.lastUsed = time.Now()
		})
	}
	return target, release, nil
}

// Both callers hold bpsMu; policy and ranking share the same snapshot rules.
func (m *Manager) bpsEligibleNodesLocked(now time.Time, excluded map[string]bool) (map[string]bool, error) {
	eligible := make(map[string]bool)
	if m.bpsStaticMode {
		// Static pool: membership is pushed from the admin proxy list; no kernel,
		// country filter or listener requirement. Cooldown gating is shared.
		for id := range m.bpsStatic {
			m.bpsHealthAtLocked(id, now)
			if !excluded[id] && !m.bpsCoolingLocked(id, now) {
				eligible[id] = true
			}
		}
	} else {
		m.mu.Lock()
		snapshot := m.saved
		ready := m.state.Running && !m.closed
		// Copy only eligible identities while protected; saved maps may be replaced.
		for _, n := range snapshot.Nodes {
			name, _ := n["name"].(string)
			id := harvestDigest(n)
			m.bpsHealthAtLocked(id, now)
			if bpsEligible(snapshot, name) && !excluded[id] && !m.bpsCoolingLocked(id, now) {
				eligible[id] = true
			}
		}
		m.mu.Unlock()
		if !ready {
			return nil, bpsSelectionError("manager_unavailable", "managed Mihomo is not running")
		}
	}
	return eligible, nil
}

func (m *Manager) bpsSessionLoadsLocked(now time.Time) (map[string]int, map[string]int) {
	if m.bpsSessions == nil {
		m.bpsSessions = make(map[string]*bpsSession)
	}
	loads := make(map[string]int)
	activeLoads := make(map[string]int)
	for k, b := range m.bpsSessions {
		h := m.bpsHealthAtLocked(b.node, now)
		expired := m.bpsDynamic[b.node] && b.generation != h.generation
		if b.active == 0 && (expired || now.Sub(b.lastUsed) >= bpsSessionIdleTTL) {
			delete(m.bpsSessions, k)
			continue
		}
		loads[b.node]++
		activeLoads[b.node] += b.active
	}
	return loads, activeLoads
}
