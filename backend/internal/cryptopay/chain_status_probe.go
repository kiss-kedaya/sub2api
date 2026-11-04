package cryptopay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
)

// chainSignature identifies an endpoint list. It is only an in-memory cache key,
// so a changed list (an operator edit, a pool refresh) never reuses an old probe.
func chainSignature(chain string, endpoints []string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(chain))
	for _, endpoint := range endpoints {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(endpoint))
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

func (m *chainMonitor) cached(chain, signature string) ([]nodeProbe, bool) {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.cache[chain]
	if !ok || entry.signature != signature {
		return nil, false
	}
	if age := now.Sub(entry.at); age < 0 || age >= m.cacheTTL {
		return nil, false
	}
	return entry.probes, true
}

// probe returns the health of a chain's nodes. Results are cached for cacheTTL
// and concurrent callers share one in-flight probe, so any number of open pages
// cost each node one eth_chainId + eth_blockNumber pair per interval. The shared
// work is detached from the caller's context: a closed tab must not cancel what
// other waiters are about to read. The returned slice is shared and read-only.
func (m *chainMonitor) probe(ctx context.Context, chain string, wantChainID uint64, endpoints []string) ([]nodeProbe, error) {
	m.init()
	if len(endpoints) == 0 {
		return nil, nil
	}
	signature := chainSignature(chain, endpoints)
	if probes, ok := m.cached(chain, signature); ok {
		return probes, nil
	}
	waiting := m.flights.DoChan(chain+"|"+signature, func() (any, error) {
		if probes, ok := m.cached(chain, signature); ok {
			return probes, nil
		}
		probes := m.probeAll(wantChainID, endpoints)
		at := m.now()
		m.mu.Lock()
		m.cache[chain] = chainProbeEntry{signature: signature, at: at, probes: probes}
		m.mu.Unlock()
		return probes, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-waiting:
		if result.Err != nil {
			return nil, result.Err
		}
		probes, _ := result.Val.([]nodeProbe)
		return probes, nil
	}
}

func (m *chainMonitor) probeAll(wantChainID uint64, endpoints []string) []nodeProbe {
	probes := make([]nodeProbe, len(endpoints))
	var wg sync.WaitGroup
	for i, endpoint := range endpoints {
		wg.Add(1)
		go func() {
			defer wg.Done()
			probes[i] = m.probeNode(endpoint, wantChainID)
		}()
	}
	wg.Wait()
	return probes
}

// probeNode asks one node for eth_chainId and eth_blockNumber through a pool
// holding only that node, so the existing per-endpoint throttle paces it with
// scanner traffic. The whole probe has one nodeProbeTimeout budget. Failures are
// reduced to a fixed category; the error value itself is dropped here.
func (m *chainMonitor) probeNode(endpoint string, wantChainID uint64) nodeProbe {
	m.init()
	ctx, cancel := context.WithTimeout(context.Background(), m.probeTimeout)
	defer cancel()
	pool := m.newPool(endpoint)
	var out nodeProbe

	start := time.Now()
	raw, err := pool.Call(ctx, "eth_chainId", nil)
	first := time.Since(start)
	if err != nil {
		out.ErrorKind = classifyNodeError(err)
		return out
	}
	var chainHex string
	if json.Unmarshal(raw, &chainHex) != nil {
		out.ErrorKind = NodeErrBadResp
		return out
	}
	chainID, err := hexutil.DecodeUint64(chainHex)
	if err != nil {
		out.ErrorKind = NodeErrBadResp
		return out
	}
	out.Answered, out.HasLatency, out.Latency = true, true, first
	if wantChainID != 0 && chainID != wantChainID {
		out.WrongChain = true
		out.ErrorKind = NodeErrWrongChain
		return out
	}

	start = time.Now()
	head, err := pool.BlockNumber(ctx)
	second := time.Since(start)
	if err != nil {
		// A node that answered the first call but not the second has no
		// trustworthy round trip to report.
		out.HasLatency, out.Latency = false, 0
		out.ErrorKind = classifyNodeError(err)
		return out
	}
	out.HasHead, out.Head = true, head
	if second > out.Latency {
		out.Latency = second
	}
	return out
}
