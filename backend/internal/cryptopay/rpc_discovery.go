package cryptopay

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
	"golang.org/x/sync/singleflight"
)

const (
	rpcDiscoveryLifetime = time.Hour
	rpcDiscoveryBackoff  = 2 * time.Minute
	rpcDiscoveryTimeout  = 35 * time.Second
)

type discoveredRPC struct {
	endpoints []string
	refreshAt time.Time
}

// rpcDiscovery shares completed probes and in-flight discovery within a process.
// Only public, verified endpoints enter this cache. Stored operator settings
// remain authoritative and are never overwritten by discovery.
type rpcDiscovery struct {
	mu      sync.Mutex
	entries map[string]discoveredRPC
	flights singleflight.Group
	fetch   func(context.Context, string) ([]ChainlistCandidate, error)
	probe   func(context.Context, string, []ChainlistCandidate, []string, int) []RPCTestResult
	now     func() time.Time
}

var automaticRPC = newRPCDiscovery()
var automaticRPCSlots = make(chan struct{}, 2)

func newRPCDiscovery() *rpcDiscovery {
	return &rpcDiscovery{
		entries: make(map[string]discoveredRPC), fetch: FetchChainlistCandidates,
		probe: ProbeBestRPCEndpoints, now: time.Now,
	}
}

func (d *rpcDiscovery) cached(chain string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.entries[chain].endpoints)
}

func (d *rpcDiscovery) resolve(ctx context.Context, chain string) ([]string, error) {
	if _, supported := chainlistIDs[chain]; !supported {
		return nil, errors.New("automatic RPC discovery is unavailable for this chain")
	}
	d.mu.Lock()
	entry, exists := d.entries[chain]
	if exists && d.now().Before(entry.refreshAt) {
		d.mu.Unlock()
		if len(entry.endpoints) == 0 {
			return nil, errors.New("automatic RPC discovery is backing off")
		}
		return slices.Clone(entry.endpoints), nil
	}
	d.mu.Unlock()

	// The bounded task belongs to the cache, not the first waiter. Cancellation
	// of one reloading instance must not cancel other waiters' discovery.
	ch := d.flights.DoChan(chain, func() (any, error) {
		d.mu.Lock()
		current, found := d.entries[chain]
		if found && d.now().Before(current.refreshAt) {
			d.mu.Unlock()
			if len(current.endpoints) == 0 {
				return nil, errors.New("automatic RPC discovery is backing off")
			}
			return slices.Clone(current.endpoints), nil
		}
		d.mu.Unlock()
		probeCtx, cancel := context.WithTimeout(context.Background(), rpcDiscoveryTimeout)
		defer cancel()
		select {
		case automaticRPCSlots <- struct{}{}:
			defer func() { <-automaticRPCSlots }()
		case <-probeCtx.Done():
			return nil, probeCtx.Err()
		}
		candidates, err := d.fetch(probeCtx, chain)
		var endpoints []string
		if err == nil {
			candidates = automaticRPCCandidates(chain, candidates)
			for _, result := range d.probe(probeCtx, chain, candidates, nil, 3) {
				if result.OK && automaticRPCEndpoint(result.Endpoint) {
					endpoints = append(endpoints, result.Endpoint)
				}
			}
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		if len(endpoints) == 0 {
			// Preserve a previously verified list during registry failure; the
			// normal pool still rotates actual RPC failures. Never persist it.
			current.refreshAt = d.now().Add(rpcDiscoveryBackoff)
			d.entries[chain] = current
			if len(current.endpoints) > 0 {
				return slices.Clone(current.endpoints), nil
			}
			return nil, errors.New("no usable public RPC endpoints discovered")
		}
		d.entries[chain] = discoveredRPC{endpoints: endpoints, refreshAt: d.now().Add(rpcDiscoveryLifetime)}
		return slices.Clone(endpoints), nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-ch:
		if result.Err != nil {
			return nil, result.Err
		}
		endpoints, ok := result.Val.([]string)
		if !ok {
			return nil, errors.New("automatic RPC discovery response is invalid")
		}
		return slices.Clone(endpoints), nil
	}
}

func automaticRPCEndpoint(endpoint string) bool {
	// Registry entries are untrusted. Automated discovery never uses cleartext,
	// credentials, templates, websocket endpoints or unusual service ports.
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if _, validateErr := urlvalidator.ValidateHTTPURL(endpoint, false, urlvalidator.ValidationOptions{}); validateErr != nil {
		return false
	}
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" &&
		parsed.User == nil && parsed.Opaque == "" && parsed.Fragment == "" &&
		(parsed.Port() == "" || parsed.Port() == "443") &&
		!strings.ContainsAny(endpoint, "${}<>\\\r\n\x00") &&
		!strings.Contains(strings.ToLower(endpoint), "api_key") &&
		!strings.Contains(strings.ToLower(endpoint), "apikey") &&
		!strings.Contains(strings.ToLower(endpoint), "your_")
}

func automaticRPCCandidates(chain string, candidates []ChainlistCandidate) []ChainlistCandidate {
	seen := make(map[string]struct{})
	var out []ChainlistCandidate
	for _, candidate := range candidates {
		candidate.URL = strings.TrimSpace(candidate.URL)
		if candidate.ChainID != chainlistIDs[chain] || !automaticRPCEndpoint(candidate.URL) {
			continue
		}
		key := strings.ToLower(candidate.URL)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func poolsForConfig(cfg CryptoConfig, previous map[string]*rpc.Pool) map[string]*rpc.Pool {
	chains := make(map[string]struct{}, len(cfg.Chains)+len(cfg.RPC))
	for _, chain := range cfg.Chains {
		chains[chain] = struct{}{}
	}
	// Removed chains with configured nodes can still have signed jobs that
	// require receipt reconciliation; they never receive new deposit work.
	for chain, endpoints := range cfg.RPC {
		if IsEVMChain(chain) && strings.TrimSpace(endpoints) != "" {
			chains[chain] = struct{}{}
		}
	}
	pools := make(map[string]*rpc.Pool, len(chains))
	for chain := range chains {
		endpoints := cfg.RPC[chain]
		if strings.TrimSpace(endpoints) == "" && slices.Contains(cfg.Chains, chain) {
			endpoints = strings.Join(automaticRPC.cached(chain), ",")
		}
		pool := previous[chain]
		if pool == nil {
			pool = rpc.NewPool(endpoints)
		} else {
			pool.SetEndpoints(endpoints)
		}
		pools[chain] = pool
	}
	return pools
}

// startRPCDiscoveryLocked starts a separate bounded maintenance loop. All
// callers hold s.mu; no network work runs under that lock or on a scan pass.
func (s *Service) startRPCDiscoveryLocked() {
	if s.discoveryCancel != nil {
		s.discoveryCancel()
		s.discoveryCancel = nil
	}
	if !service.CryptoInstanceEnabled() || !cryptoWorkersAllowed() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.discoveryCancel = cancel
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			s.refreshAutomaticRPC(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *Service) refreshAutomaticRPC(ctx context.Context) {
	s.mu.RLock()
	chains := slices.Clone(s.config.Chains)
	s.mu.RUnlock()
	for _, chain := range chains {
		if ctx.Err() != nil {
			return
		}
		s.mu.RLock()
		pool := s.pools[chain]
		eligible := slices.Contains(s.config.Chains, chain) &&
			strings.TrimSpace(s.config.RPC[chain]) == "" && pool != nil
		s.mu.RUnlock()
		if !eligible {
			continue
		}
		endpoints, err := automaticRPC.resolve(ctx, chain)
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("[cryptopay] automatic RPC discovery pending", "chain", chain)
			}
			continue
		}
		s.mu.Lock()
		// A configuration change or a replaced pool invalidates the snapshot.
		// A delayed background result can never override a manually saved node.
		if ctx.Err() == nil && s.pools[chain] == pool &&
			slices.Contains(s.config.Chains, chain) && strings.TrimSpace(s.config.RPC[chain]) == "" {
			pool.SetEndpoints(strings.Join(endpoints, ","))
		}
		s.mu.Unlock()
	}
}
