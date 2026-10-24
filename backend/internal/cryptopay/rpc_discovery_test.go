package cryptopay

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

func TestAutomaticRPCRejectsUnsafeRegistryCandidates(t *testing.T) {
	var candidates []ChainlistCandidate
	for _, endpoint := range []string{
		"https://public.example/rpc", "https://public.example/rpc",
		"http://public.example", "wss://public.example", "https://localhost",
		"https://127.0.0.1", "https://169.254.169.254/latest/meta-data", "https://10.1.2.3",
		"https://[::1]", "https://user:secret@public.example", "https://public.example:8443",
		"https://public.example/${API_KEY}", "https://public.example/YOUR_PROJECT",
		"https://public.example/?apiKey=token", "https://public.example/#fragment",
	} {
		candidates = append(candidates, ChainlistCandidate{URL: endpoint, ChainID: 56})
	}
	candidates = append(candidates, ChainlistCandidate{URL: "https://wrong-chain.example", ChainID: 1})
	got := automaticRPCCandidates(ChainBSC, candidates)
	if len(got) != 1 || got[0].URL != "https://public.example/rpc" {
		t.Fatalf("unsafe or duplicate registry candidates survived: %+v", got)
	}
}

func discoveryFixture() *rpcDiscovery {
	d := newRPCDiscovery()
	d.fetch = func(context.Context, string) ([]ChainlistCandidate, error) {
		return []ChainlistCandidate{{URL: "https://public.example/rpc", ChainID: 56}}, nil
	}
	d.probe = func(_ context.Context, chain string, candidates []ChainlistCandidate, _ []string, want int) []RPCTestResult {
		if chain != ChainBSC || want != 3 || len(candidates) != 1 {
			return nil
		}
		return []RPCTestResult{{Endpoint: candidates[0].URL, ChainID: "0x38", OK: true}}
	}
	return d
}

func TestAutomaticRPCSingleFlightAndCacheCopies(t *testing.T) {
	d := discoveryFixture()
	var fetched atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	d.fetch = func(ctx context.Context, _ string) ([]ChainlistCandidate, error) {
		if fetched.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return []ChainlistCandidate{{URL: "https://public.example/rpc", ChainID: 56}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	const waiters = 12
	var wg sync.WaitGroup
	for range waiters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			endpoints, err := d.resolve(t.Context(), ChainBSC)
			if err != nil || !slices.Equal(endpoints, []string{"https://public.example/rpc"}) {
				t.Errorf("discovery failed: %v %v", endpoints, err)
				return
			}
			endpoints[0] = "https://changed.example"
		}()
	}
	<-started
	close(release)
	wg.Wait()
	if fetched.Load() != 1 || !slices.Equal(d.cached(ChainBSC), []string{"https://public.example/rpc"}) {
		t.Fatal("discovery was repeated or a caller changed the cache")
	}
}

func TestAutomaticRPCFailedDiscoveryBacksOffAndRecovers(t *testing.T) {
	d := discoveryFixture()
	now := time.Unix(1000, 0)
	d.now = func() time.Time { return now }
	calls := 0
	d.fetch = func(context.Context, string) ([]ChainlistCandidate, error) {
		calls++
		return nil, errors.New("fixture upstream error containing secret")
	}
	for range 2 {
		if _, err := d.resolve(t.Context(), ChainBSC); err == nil {
			t.Fatal("failed discovery must not produce an endpoint")
		}
	}
	if calls != 1 {
		t.Fatal("unavailable registry was retried inside the backoff")
	}
	now = now.Add(rpcDiscoveryBackoff)
	d.fetch = discoveryFixture().fetch
	endpoints, err := d.resolve(t.Context(), ChainBSC)
	if err != nil || len(endpoints) != 1 {
		t.Fatalf("discovery did not recover: %v %v", endpoints, err)
	}
}

func TestAutomaticRPCWaiterCancellationDoesNotCancelSharedWork(t *testing.T) {
	d := discoveryFixture()
	started, release := make(chan struct{}), make(chan struct{})
	d.fetch = func(ctx context.Context, _ string) ([]ChainlistCandidate, error) {
		close(started)
		select {
		case <-release:
			return []ChainlistCandidate{{URL: "https://public.example/rpc", ChainID: 56}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	returned := make(chan error, 1)
	go func() { _, err := d.resolve(ctx, ChainBSC); returned <- err }()
	<-started
	cancel()
	if err := <-returned; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter did not leave: %v", err)
	}
	close(release)
	if endpoints, err := d.resolve(t.Context(), ChainBSC); err != nil || len(endpoints) != 1 {
		t.Fatalf("one waiter canceled shared discovery: %v %v", endpoints, err)
	}
}

func TestAutomaticRPCNeverOverwritesAConcurrentManualSetting(t *testing.T) {
	previous := automaticRPC
	automaticRPC = discoveryFixture()
	t.Cleanup(func() { automaticRPC = previous })
	pool := rpc.NewPool("")
	s := &Service{
		config: CryptoConfig{Enabled: true, Chains: []string{ChainBSC}, RPC: map[string]string{}},
		pools:  map[string]*rpc.Pool{ChainBSC: pool},
	}
	baseProbe := automaticRPC.probe
	automaticRPC.probe = func(ctx context.Context, chain string, candidates []ChainlistCandidate, exclude []string, want int) []RPCTestResult {
		s.mu.Lock()
		s.config.RPC[chain] = "https://operator.example/rpc"
		pool.SetEndpoints(s.config.RPC[chain])
		s.mu.Unlock()
		return baseProbe(ctx, chain, candidates, exclude, want)
	}
	s.refreshAutomaticRPC(t.Context())
	if !slices.Equal(pool.Endpoints(), []string{"https://operator.example/rpc"}) {
		t.Fatal("delayed discovery overwrote a manually configured node")
	}
}

func TestAutomaticRPCOnlyFillsEnabledEmptyChainWithoutPersisting(t *testing.T) {
	previous := automaticRPC
	automaticRPC = discoveryFixture()
	t.Cleanup(func() { automaticRPC = previous })
	bsc, ethereum := rpc.NewPool(""), rpc.NewPool("https://operator.example")
	s := &Service{
		config: CryptoConfig{Enabled: true, Chains: []string{ChainBSC, ChainEthereum}, RPC: map[string]string{ChainEthereum: "https://operator.example"}},
		pools:  map[string]*rpc.Pool{ChainBSC: bsc, ChainEthereum: ethereum, ChainPolygon: rpc.NewPool("")},
	}
	s.refreshAutomaticRPC(t.Context())
	if !slices.Equal(bsc.Endpoints(), []string{"https://public.example/rpc"}) || s.config.RPC[ChainBSC] != "" {
		t.Fatal("missing enabled RPC was not filled in memory or stored config was mutated")
	}
	if !slices.Equal(ethereum.Endpoints(), []string{"https://operator.example"}) || len(s.pools[ChainPolygon].Endpoints()) != 0 {
		t.Fatal("manual RPC or disabled chain changed")
	}
}

func TestAutomaticRPCCacheRetainsGuardCoverageWhenDepositsAreDisabled(t *testing.T) {
	previous := automaticRPC
	automaticRPC = discoveryFixture()
	t.Cleanup(func() { automaticRPC = previous })
	automaticRPC.entries[ChainBSC] = discoveredRPC{
		endpoints: []string{"https://public.example/rpc"}, refreshAt: time.Now().Add(time.Hour),
	}
	cfg := CryptoConfig{Enabled: false, Chains: []string{ChainBSC}, RPC: map[string]string{}}
	first := poolsForConfig(cfg, nil)
	if !slices.Equal(first[ChainBSC].Endpoints(), []string{"https://public.example/rpc"}) {
		t.Fatal("turning off deposits removed the RPC required by the outflow guard")
	}
	cfg.RPC[ChainBSC] = "https://operator.example/rpc"
	second := poolsForConfig(cfg, first)
	if first[ChainBSC] != second[ChainBSC] || !slices.Equal(second[ChainBSC].Endpoints(), []string{"https://operator.example/rpc"}) {
		t.Fatal("pool reload did not retain shared readers or manual configuration precedence")
	}
}

func TestAutomaticRPCDoesNotStartBackgroundWorkOnReadOnlyReview(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_WRITE_ENABLED", "false")
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "false")
	s := &Service{config: CryptoConfig{Enabled: true, Chains: []string{ChainBSC}}}
	s.mu.Lock()
	s.startRPCDiscoveryLocked()
	s.mu.Unlock()
	if s.discoveryCancel != nil {
		s.Close()
		t.Fatal("read-only review started automatic network maintenance")
	}
}
