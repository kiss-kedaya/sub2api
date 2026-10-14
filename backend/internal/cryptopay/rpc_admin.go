// RPC endpoint helpers for the admin console: probing one endpoint and
// pulling candidate endpoints from the public chainlist.org registry.
//
// These are read-only and additive: nothing here mutates stored config.
package cryptopay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// chainlistIDs maps an internal chain id to the EIP-155 chain id used by
// chainlist.org. Chains absent here cannot be fetched (plasma is not listed).
var chainlistIDs = map[string]int64{
	ChainBSC:      56,
	ChainPolygon:  137,
	ChainArbitrum: 42161,
	ChainBase:     8453,
	ChainXLayer:   196,
}

// RPCTestResult reports one endpoint's health for the console.
type RPCTestResult struct {
	Endpoint  string `json:"endpoint"`
	OK        bool   `json:"ok"`
	ChainID   string `json:"chain_id,omitempty"`
	Block     uint64 `json:"block,omitempty"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

// ChainlistCandidate is one endpoint offered by chainlist.org.
type ChainlistCandidate struct {
	URL       string `json:"url"`
	ChainID   int64  `json:"chain_id"`
	ChainName string `json:"chain_name"`
}

// rpcHTTPClient is shared by the probe and the registry fetch. The registry
// payload is ~2.3 MB, so the timeout must cover a slow international link.
var rpcHTTPClient = &http.Client{Timeout: 20 * time.Second}

// ProbeRPCEndpoint calls eth_chainId + eth_blockNumber on one endpoint and
// reports latency. It never mutates state.
func ProbeRPCEndpoint(ctx context.Context, chain, endpoint string) *RPCTestResult {
	endpoint = strings.TrimSpace(endpoint)
	out := &RPCTestResult{Endpoint: endpoint}
	if endpoint == "" {
		out.Error = "empty endpoint"
		return out
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		out.Error = "endpoint must start with http:// or https://"
		return out
	}
	start := time.Now()
	chainID, err := rpcCallString(ctx, endpoint, "eth_chainId")
	if err != nil {
		out.Error = err.Error()
		out.LatencyMS = time.Since(start).Milliseconds()
		return out
	}
	blockHex, err := rpcCallString(ctx, endpoint, "eth_blockNumber")
	if err != nil {
		out.Error = err.Error()
		out.LatencyMS = time.Since(start).Milliseconds()
		return out
	}
	out.LatencyMS = time.Since(start).Milliseconds()
	out.OK = true
	out.ChainID = chainID
	out.Block = parseHexUint64(blockHex)
	// A wrong chain id is the classic copy-paste error; surface it loudly.
	if want, known := chainlistIDs[chain]; known {
		if got := parseHexUint64(chainID); got != uint64(want) {
			out.OK = false
			out.Error = fmt.Sprintf("chain id mismatch: got %d, want %d", got, want)
		}
	}
	return out
}

// rpcCallString runs one JSON-RPC method against a raw endpoint.
func rpcCallString(ctx context.Context, endpoint, method string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": method, "params": []any{},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := rpcHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}
	var decoded struct {
		Result string `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&decoded); err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	if decoded.Error != nil {
		return "", fmt.Errorf("%s", decoded.Error.Message)
	}
	if decoded.Result == "" {
		return "", fmt.Errorf("empty result")
	}
	return decoded.Result, nil
}

// parseHexUint64 converts a 0x-prefixed quantity; invalid input yields 0.
func parseHexUint64(hex string) uint64 {
	hex = strings.TrimPrefix(strings.TrimSpace(hex), "0x")
	if hex == "" {
		return 0
	}
	var out uint64
	if _, err := fmt.Sscanf(hex, "%x", &out); err != nil {
		return 0
	}
	return out
}

// FetchChainlistCandidates pulls the public chainlist.org registry and
// returns the endpoints for one chain. The registry URL and shape were
// verified on 2026-10-14: /rpcs.json, an array of {chainId, name, rpc[]}.
func FetchChainlistCandidates(ctx context.Context, chain string) ([]ChainlistCandidate, error) {
	chainID, ok := chainlistIDs[chain]
	if !ok {
		return nil, fmt.Errorf("chain %q has no chainlist mapping", chain)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://chainlist.org/rpcs.json", nil)
	if err != nil {
		return nil, err
	}
	resp, err := rpcHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch chainlist: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("chainlist: http %d", resp.StatusCode)
	}
	var registry []struct {
		ChainID int64  `json:"chainId"`
		Name    string `json:"name"`
		RPC     []struct {
			URL      string `json:"url"`
			Tracking string `json:"tracking"`
		} `json:"rpc"`
	}
	// The registry is ~2.3 MB; 16 MB of headroom keeps a growth spurt safe.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&registry); err != nil {
		return nil, fmt.Errorf("decode chainlist: %w", err)
	}
	for _, entry := range registry {
		if entry.ChainID != chainID {
			continue
		}
		out := make([]ChainlistCandidate, 0, len(entry.RPC))
		for _, r := range entry.RPC {
			url := strings.TrimSpace(r.URL)
			// Skip websocket and template entries: the scanner speaks HTTP.
			if url == "" || !strings.HasPrefix(url, "http") {
				continue
			}
			out = append(out, ChainlistCandidate{
				URL: url, ChainID: entry.ChainID, ChainName: entry.Name,
			})
		}
		return out, nil
	}
	return nil, fmt.Errorf("chain %d not present in the registry", chainID)
}

// ProbeBestRPCEndpoints probes candidates in parallel and returns the
// fastest healthy ones (chain id must match, head must answer). It skips
// endpoints already present in `exclude` so a re-run never re-adds a node
// the operator already has. Bounded fan-out keeps the registry list
// (40-60 entries) from opening that many sockets at once.
func ProbeBestRPCEndpoints(
	ctx context.Context,
	chain string,
	candidates []ChainlistCandidate,
	exclude []string,
	want int,
) []RPCTestResult {
	if want <= 0 {
		want = 3
	}
	skip := make(map[string]struct{}, len(exclude))
	for _, e := range exclude {
		skip[strings.ToLower(strings.TrimSpace(e))] = struct{}{}
	}

	type result struct {
		res *RPCTestResult
	}
	// Bound the work: the registry lists 40-60 endpoints for popular
	// chains, but the first ones are the well-known public nodes anyway.
	// Probing all of them serially hit 35s; 20 candidates at 10-way
	// concurrency with a 6s per-probe deadline lands under ~12s worst case.
	if len(candidates) > 20 {
		candidates = candidates[:20]
	}
	sem := make(chan struct{}, 10)
	out := make(chan result, len(candidates))
	var wg sync.WaitGroup
	for _, c := range candidates {
		url := strings.TrimSpace(c.URL)
		if url == "" {
			continue
		}
		if _, dup := skip[strings.ToLower(url)]; dup {
			continue
		}
		wg.Add(1)
		go func(endpoint string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			probeCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
			defer cancel()
			res := ProbeRPCEndpoint(probeCtx, chain, endpoint)
			out <- result{res: res}
		}(url)
	}
	wg.Wait()
	close(out)

	healthy := make([]*RPCTestResult, 0, len(candidates))
	for r := range out {
		if r.res != nil && r.res.OK {
			healthy = append(healthy, r.res)
		}
	}
	// Fastest first: latency is the only ranking signal we have.
	for i := 1; i < len(healthy); i++ {
		for j := i; j > 0 && healthy[j].LatencyMS < healthy[j-1].LatencyMS; j-- {
			healthy[j], healthy[j-1] = healthy[j-1], healthy[j]
		}
	}
	if len(healthy) > want {
		healthy = healthy[:want]
	}
	final := make([]RPCTestResult, 0, len(healthy))
	for _, r := range healthy {
		final = append(final, *r)
	}
	return final
}

