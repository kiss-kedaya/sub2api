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
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"golang.org/x/sync/singleflight"
)

// chainlistIDs maps an internal chain id to the EIP-155 chain id used by
// chainlist.org. Chains absent here cannot be fetched.
var chainlistIDs = map[string]int64{
	ChainEthereum: 1,
	ChainBSC:      56,
	ChainPlasma:   9745,
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
var rpcHTTPClient = rpc.NewPublicHTTPClient(20 * time.Second)

// ProbeRPCEndpoint checks the methods deposits actually need. A node that
// serves the chain head but rejects logs is not a usable payment scanner.
func ProbeRPCEndpoint(ctx context.Context, chain, endpoint string) *RPCTestResult {
	endpoint = strings.TrimSpace(endpoint)
	out := &RPCTestResult{}
	if endpoint == "" {
		out.Error = "empty endpoint"
		return out
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		out.Error = "endpoint must start with http:// or https://"
		return out
	}
	// Admin-only is not a reason to allow probing the host's own network:
	// a leaked admin token could otherwise read cloud metadata.
	validated, err := urlvalidator.ValidateHTTPURL(endpoint, true, urlvalidator.ValidationOptions{})
	if err != nil {
		out.Error = "endpoint is not an allowed public URL"
		return out
	}
	parsed, err := url.Parse(validated)
	if err != nil || parsed.User != nil || parsed.Fragment != "" || parsed.Opaque != "" || strings.ContainsAny(endpoint, "\\\r\n\x00") || strings.Contains(parsed.Hostname(), "%") {
		out.Error = "endpoint is not an allowed public URL"
		return out
	}
	endpoint = validated
	start := time.Now()
	if !IsEVMChain(chain) {
		out.Error = "unsupported chain"
		return out
	}
	chainID, err := rpcCallString(ctx, endpoint, "eth_chainId")
	if err != nil {
		out.Error = rpc.SafeError(err)
		out.LatencyMS = time.Since(start).Milliseconds()
		return out
	}
	blockHex, err := rpcCallString(ctx, endpoint, "eth_blockNumber")
	if err != nil {
		out.Error = rpc.SafeError(err)
		out.LatencyMS = time.Since(start).Milliseconds()
		return out
	}
	out.LatencyMS = time.Since(start).Milliseconds()
	parsedChainID, chainErr := hexutil.DecodeUint64(chainID)
	parsedBlock, blockErr := hexutil.DecodeUint64(blockHex)
	if chainErr != nil || blockErr != nil {
		out.Error = "RPC response is invalid"
		return out
	}
	out.ChainID = hexutil.EncodeUint64(parsedChainID)
	out.Block = parsedBlock
	// A wrong chain id is the classic copy-paste error; surface it loudly.
	if want, known := chainlistIDs[chain]; known {
		if got := parseHexUint64(chainID); got != uint64(want) {
			out.OK = false
			out.Error = fmt.Sprintf("chain id mismatch: got %d, want %d", got, want)
			return out
		}
	}
	pool := rpc.NewPoolWithHTTPClient(endpoint, rpcHTTPClient)
	if err := probeDepositCapabilities(ctx, pool, chain, parsedBlock); err != nil {
		out.Error = rpc.SafeError(err)
		out.LatencyMS = time.Since(start).Milliseconds()
		return out
	}
	out.OK = true
	out.LatencyMS = time.Since(start).Milliseconds()
	if out.OK {
		// Successful candidates are configuration values the operator explicitly
		// requested. Failure responses never echo their potentially secret URL.
		out.Endpoint = endpoint
	}
	return out
}

func probeDepositCapabilities(ctx context.Context, pool *rpc.Pool, chain string, head uint64) error {
	tokens := watchedTokens[chain]
	if len(tokens) == 0 {
		return &rpc.Failure{Kind: "request"}
	}
	contracts := make([]string, 0, len(tokens))
	for _, token := range tokens {
		contracts = append(contracts, token.Contract)
	}
	// A known token contract cannot legitimately receive its own ordinary
	// customer deposits. Narrowing the recipient avoids large unfiltered logs
	// while exercising the exact token-array/topic-array filter the scanner uses.
	block := hexutil.EncodeUint64(head)
	filter := map[string]any{"fromBlock": block, "toBlock": block, "address": contracts,
		"topics": []any{[]string{"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"}, nil,
			[]string{"0x" + strings.Repeat("0", 24) + strings.TrimPrefix(strings.ToLower(contracts[0]), "0x")}}}
	logs, err := pool.Call(ctx, "eth_getLogs", []any{filter})
	if err != nil {
		return err
	}
	var decoded []json.RawMessage
	if !bytes.HasPrefix(bytes.TrimSpace(logs), []byte("[")) || json.Unmarshal(logs, &decoded) != nil {
		return &rpc.Failure{Kind: "response"}
	}
	if chain == ChainBSC {
		finalized, err := pool.Call(ctx, "eth_getBlockByNumber", []any{"finalized", false})
		if err != nil {
			return err
		}
		var block struct {
			Number string `json:"number"`
			Hash   string `json:"hash"`
		}
		if json.Unmarshal(finalized, &block) != nil {
			return &rpc.Failure{Kind: "response"}
		}
		number, err := hexutil.DecodeUint64(block.Number)
		if err != nil || len(block.Hash) != 66 {
			return &rpc.Failure{Kind: "response"}
		}
		// On fast chains the finalized head can move beyond the head read
		// before the log probe. Refresh once before calling the node invalid.
		if number > head {
			current, err := pool.BlockNumber(ctx)
			if err != nil {
				return err
			}
			if number > current {
				return &rpc.Failure{Kind: "response"}
			}
		}
	}
	if _, err := pool.Balance(ctx, contracts[0]); err != nil {
		return err
	}
	// A missing receipt is a valid result, but an unavailable method is not.
	// The all-zero transaction hash never represents a real payment and avoids
	// relying on unrelated customer history just to validate an RPC provider.
	receipt, err := pool.Call(ctx, "eth_getTransactionReceipt", []any{"0x" + strings.Repeat("0", 64)})
	if err != nil {
		return err
	}
	if !bytes.Equal(bytes.TrimSpace(receipt), []byte("null")) {
		return &rpc.Failure{Kind: "response"}
	}
	return nil
}

// rpcCallString runs one JSON-RPC method against a raw endpoint.
func rpcCallString(ctx context.Context, endpoint, method string) (string, error) {
	return rpcCallStringWithClient(ctx, rpcHTTPClient, endpoint, method)
}

func rpcCallStringWithClient(ctx context.Context, client *http.Client, endpoint, method string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": method, "params": []any{},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", &rpc.Failure{Kind: "request"}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", rpc.NewTransportFailure(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", &rpc.Failure{Kind: "http", HTTPStatus: resp.StatusCode}
	}
	var decoded struct {
		Result string `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&decoded); err != nil {
		return "", &rpc.Failure{Kind: "response"}
	}
	if decoded.Error != nil {
		return "", &rpc.Failure{Kind: "remote", RPCCode: decoded.Error.Code}
	}
	if decoded.Result == "" {
		return "", &rpc.Failure{Kind: "response"}
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
	registry, err := cachedChainlistRegistry(ctx)
	if err != nil {
		return nil, err
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

type chainlistRecord struct {
	ChainID int64  `json:"chainId"`
	Name    string `json:"name"`
	RPC     []struct {
		URL      string `json:"url"`
		Tracking string `json:"tracking"`
	} `json:"rpc"`
}

var chainlistRegistry struct {
	sync.Mutex
	entries     []chainlistRecord
	expires     time.Time
	failedUntil time.Time
	flight      singleflight.Group
}

func cachedChainlistRegistry(ctx context.Context) ([]chainlistRecord, error) {
	chainlistRegistry.Lock()
	if len(chainlistRegistry.entries) > 0 && time.Now().Before(chainlistRegistry.expires) {
		entries := chainlistRegistry.entries
		chainlistRegistry.Unlock()
		return entries, nil
	}
	if time.Now().Before(chainlistRegistry.failedUntil) {
		chainlistRegistry.Unlock()
		return nil, fmt.Errorf("chainlist request is backing off")
	}
	chainlistRegistry.Unlock()
	result := chainlistRegistry.flight.DoChan("registry", func() (value any, err error) {
		chainlistRegistry.Lock()
		if len(chainlistRegistry.entries) > 0 && time.Now().Before(chainlistRegistry.expires) {
			entries := chainlistRegistry.entries
			chainlistRegistry.Unlock()
			return entries, nil
		}
		if time.Now().Before(chainlistRegistry.failedUntil) {
			chainlistRegistry.Unlock()
			return nil, fmt.Errorf("chainlist request is backing off")
		}
		chainlistRegistry.Unlock()
		defer func() {
			if err != nil {
				chainlistRegistry.Lock()
				chainlistRegistry.failedUntil = time.Now().Add(rpcDiscoveryBackoff)
				chainlistRegistry.Unlock()
			}
		}()
		fetchCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, "https://chainlist.org/rpcs.json", nil)
		if err != nil {
			return nil, fmt.Errorf("chainlist request unavailable")
		}
		resp, err := rpcHTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("chainlist request failed")
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("chainlist: http %d", resp.StatusCode)
		}
		var registry []chainlistRecord
		// The registry is shared across chains and remains immutable after
		// publication. Growth and malformed responses stay strictly bounded.
		if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&registry); err != nil || len(registry) == 0 {
			return nil, fmt.Errorf("chainlist response is invalid")
		}
		chainlistRegistry.Lock()
		chainlistRegistry.entries, chainlistRegistry.expires = registry, time.Now().Add(30*time.Minute)
		chainlistRegistry.failedUntil = time.Time{}
		chainlistRegistry.Unlock()
		return registry, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case out := <-result:
		if out.Err != nil {
			return nil, out.Err
		}
		registry, ok := out.Val.([]chainlistRecord)
		if !ok {
			return nil, fmt.Errorf("chainlist response is invalid")
		}
		return registry, nil
	}
}

// ProbeBestRPCEndpoints probes candidates in parallel and returns the
// fastest healthy ones (chain id, head and deposit scan methods must work). It skips
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
