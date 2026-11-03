package rpc

import (
	"strings"
	"sync"
)

// EndpointLimits is what one provider will accept in a single eth_getLogs.
//
// Public and paid nodes disagree wildly: measured limits range from 25 blocks
// (blockrazor) and 50 (1rpc) to 1000 with a 20000-log cap (publicnode), and a
// provider that caps blocks usually also caps addresses. Without this the
// scanner only learns a limit by hitting it, so every pass spends requests on
// rejects and a strict node poisons the whole chain even when a permissive one
// is in the same pool.
//
// Zero means "unknown, use the caller's default".
type EndpointLimits struct {
	MaxBlocks    uint64
	MaxAddresses int
}

type limitsRegistry struct {
	mu     sync.RWMutex
	limits map[string]EndpointLimits
}

var providerLimits = &limitsRegistry{limits: make(map[string]EndpointLimits)}

// SetEndpointLimits records what an endpoint accepts. Called from config loading
// so operators can tune a provider without a rebuild.
func SetEndpointLimits(endpoint string, limits EndpointLimits) {
	key := endpointKey(endpoint)
	if key == "" {
		return
	}
	providerLimits.mu.Lock()
	providerLimits.limits[key] = limits
	providerLimits.mu.Unlock()
}

// ReplaceEndpointLimits swaps the whole registry. An empty map clears it and
// restores the pre-split behaviour, so a bad config cannot make scanning worse
// than it was before this existed.
func ReplaceEndpointLimits(limits map[string]EndpointLimits) {
	providerLimits.mu.Lock()
	providerLimits.limits = make(map[string]EndpointLimits, len(limits))
	for endpoint, value := range limits {
		if key := endpointKey(endpoint); key != "" {
			providerLimits.limits[key] = value
		}
	}
	providerLimits.mu.Unlock()
}

// EndpointLimitsFor returns the recorded limits for an endpoint, if any.
func EndpointLimitsFor(endpoint string) (EndpointLimits, bool) {
	providerLimits.mu.RLock()
	defer providerLimits.mu.RUnlock()
	value, ok := providerLimits.limits[endpointKey(endpoint)]
	return value, ok
}

// endpointKey normalises a URL so a trailing slash or case difference does not
// silently miss the configured limits.
func endpointKey(endpoint string) string {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(trimmed), "/")
}
