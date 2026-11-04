package cryptopay

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

const (
	confirmTransferTopic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	confirmUSDT          = "0x55d398326f99059ff775485246999027b3197955"
	confirmUSDC          = "0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d"
)

var (
	confirmFrom     = "0x" + strings.Repeat("22", 20)
	confirmTo       = "0x" + strings.Repeat("33", 20)
	confirmStranger = "0x" + strings.Repeat("77", 20)
	// tenUSDT is 10 tokens at BSC's 18 decimals.
	tenUSDT = new(big.Int).Mul(big.NewInt(10), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
)

type chainReceipt struct {
	status    string
	block     uint64
	blockHash string
	logs      []map[string]any
	absent    bool
}

// fakeChain is an in-process JSON-RPC node. It listens on loopback only and
// never reads configuration or the environment.
type fakeChain struct {
	mu        sync.Mutex
	chainID   string
	receipts  map[string]*chainReceipt
	canonical map[uint64]string
	calls     map[string]int
	httpFail  map[string]int
	server    *httptest.Server
}

func newFakeChain(t *testing.T) *fakeChain {
	t.Helper()
	c := &fakeChain{chainID: "0x38", receipts: map[string]*chainReceipt{}, canonical: map[uint64]string{},
		calls: map[string]int{}, httpFail: map[string]int{}}
	c.server = httptest.NewServer(c.handler(t))
	t.Cleanup(c.server.Close)
	return c
}

func (c *fakeChain) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("malformed RPC request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		c.mu.Lock()
		c.calls[req.Method]++
		failure := c.httpFail[req.Method]
		var result any
		switch req.Method {
		case "eth_chainId":
			result = c.chainID
		case "eth_getTransactionReceipt":
			var hash string
			_ = json.Unmarshal(req.Params[0], &hash)
			if rc := c.receipts[strings.ToLower(hash)]; rc != nil && !rc.absent {
				result = map[string]any{"status": rc.status, "blockNumber": fmt.Sprintf("0x%x", rc.block),
					"blockHash": rc.blockHash, "transactionHash": strings.ToLower(hash), "logs": rc.logs}
			}
		case "eth_getBlockByNumber":
			var number string
			_ = json.Unmarshal(req.Params[0], &number)
			var n uint64
			_, _ = fmt.Sscanf(number, "0x%x", &n)
			if hash, ok := c.canonical[n]; ok {
				result = map[string]any{"number": number, "hash": hash}
			}
		default:
			t.Errorf("unexpected RPC method %q", req.Method)
		}
		c.mu.Unlock()
		if failure != 0 {
			w.WriteHeader(failure)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
	}
}

func (c *fakeChain) pool() *rpc.Pool {
	return rpc.NewPoolWithHTTPClient(c.server.URL, c.server.Client())
}

func (c *fakeChain) callCount(method string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[method]
}

func (c *fakeChain) totalCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := 0
	for _, n := range c.calls {
		total += n
	}
	return total
}

func chainTransferLog(token, from, to string, raw *big.Int, block uint64, blockHash, tx string, index int) map[string]any {
	pad := func(address string) string {
		return "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(strings.ToLower(address), "0x")
	}
	return map[string]any{
		"address": token, "topics": []string{confirmTransferTopic, pad(from), pad(to)},
		"data": fmt.Sprintf("0x%064x", raw), "blockNumber": fmt.Sprintf("0x%x", block),
		"transactionHash": tx, "logIndex": fmt.Sprintf("0x%x", index), "blockHash": blockHash,
	}
}
