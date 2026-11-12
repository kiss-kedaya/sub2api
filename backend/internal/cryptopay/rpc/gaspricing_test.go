package rpc

import (
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
)

// gasPriceNode is one endpoint. It verifies as chainID and answers
// eth_gasPrice with raw verbatim, so a malformed answer can be served too; an
// empty raw is JSON null, which is "no answer at all".
func gasPriceNode(t *testing.T, chainID uint64, raw string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result any
		switch req.Method {
		case "eth_chainId":
			result = hexutil.EncodeUint64(chainID)
		case "eth_gasPrice":
			if raw != "" {
				result = raw
			}
		default:
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
	}))
	t.Cleanup(s.Close)
	return s
}

// TestGasPriceNeverSignsBelowTheChainFloor pins the 2026-11-13 BSC incident: an
// endpoint answering 0x1 (1 wei) must not decide what the sweeper signs.
func TestGasPriceNeverSignsBelowTheChainFloor(t *testing.T) {
	ctx := t.Context()
	cases := []struct {
		name    string
		chainID uint64
		floor   int64
	}{
		{name: "bsc", chainID: 56, floor: 50_000_000},           // 0.05 gwei
		{name: "polygon", chainID: 137, floor: 30 * weiPerGwei}, // 30 gwei
		{name: "ethereum", chainID: 1, floor: 1 * weiPerGwei},   // 1 gwei
		{name: "arbitrum", chainID: 42161, floor: 10_000_000},   // 0.01 gwei
		{name: "unknown chain", chainID: 0, floor: unknownChainMinGasPriceWei},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := gasPriceNode(t, tc.chainID, "0x1")
			pool := NewPoolWithHTTPClient(srv.URL, srv.Client())
			if tc.chainID != 0 {
				verified, err := pool.ForChain(ctx, tc.chainID)
				if err != nil {
					t.Fatalf("ForChain: %v", err)
				}
				pool = verified
			}
			price, err := pool.GasPrice(ctx)
			if err != nil {
				t.Fatalf("GasPrice: %v", err)
			}
			if want := big.NewInt(tc.floor); price.Cmp(want) != 0 {
				t.Fatalf("a 1 wei quote was signed as %s, want the %s floor %s", price, tc.name, want)
			}
		})
	}
}

// TestGasPriceRejectsAnswersThatCannotBeSigned keeps a broken answer from
// becoming a transaction: zero, negative, empty and non-canonical quantities are
// not a fee.
func TestGasPriceRejectsAnswersThatCannotBeSigned(t *testing.T) {
	for _, raw := range []string{"0x0", "0x", "", "-0x1", "0x-1", "0x01", "1", "0x1junk"} {
		t.Run(raw, func(t *testing.T) {
			srv := gasPriceNode(t, 56, raw)
			pool := NewPoolWithHTTPClient(srv.URL, srv.Client()).WithChainID(56)
			if price, err := pool.GasPrice(t.Context()); err == nil {
				t.Fatalf("unusable answer %q produced a price: %s", raw, price)
			}
		})
	}
}

// TestGasPriceTakesTheMedianOfThePool: one endpoint reporting 5000 gwei while
// its peers report 20 and 50 must not set the fee, and neither may a single 1
// wei member. Only endpoints that answered are counted.
func TestGasPriceTakesTheMedianOfThePool(t *testing.T) {
	ctx := t.Context()
	nodes := []string{
		gasPriceNode(t, 1, hexutil.EncodeBig(big.NewInt(20*weiPerGwei))).URL,
		gasPriceNode(t, 1, hexutil.EncodeBig(big.NewInt(50*weiPerGwei))).URL,
		gasPriceNode(t, 1, hexutil.EncodeBig(big.NewInt(5000*weiPerGwei))).URL,
	}
	verified, err := NewPoolWithHTTPClient(strings.Join(nodes, ","), http.DefaultClient).ForChain(ctx, 1)
	if err != nil {
		t.Fatalf("ForChain: %v", err)
	}
	price, err := verified.GasPrice(ctx)
	if err != nil {
		t.Fatalf("GasPrice: %v", err)
	}
	if want := big.NewInt(50 * weiPerGwei); price.Cmp(want) != 0 {
		t.Fatalf("pool quote = %s, want the median %s (not the 5000 gwei outlier)", price, want)
	}

	// A member that cannot answer is not a sample.
	nodes = append(nodes[:2], gasPriceNode(t, 1, "0x0").URL)
	verified, err = NewPoolWithHTTPClient(strings.Join(nodes, ","), http.DefaultClient).ForChain(ctx, 1)
	if err != nil {
		t.Fatalf("ForChain: %v", err)
	}
	price, err = verified.GasPrice(ctx)
	if err != nil {
		t.Fatalf("GasPrice: %v", err)
	}
	if want := big.NewInt(50 * weiPerGwei); price.Cmp(want) != 0 {
		t.Fatalf("pool quote = %s, want %s from the two usable members", price, want)
	}
}

// TestGasPriceFloorIsNeverZero: a missing floor is the defect being fixed, so
// every chain id, known or not, must resolve to a positive minimum.
func TestGasPriceFloorIsNeverZero(t *testing.T) {
	for _, id := range []uint64{0, 1, 56, 137, 196, 8453, 9745, 42161, 999999} {
		if floor := GasPriceFloor(id); floor.Sign() <= 0 {
			t.Fatalf("chain %d has no positive gas price floor: %s", id, floor)
		}
	}
}
