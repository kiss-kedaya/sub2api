package sweeper

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/bitget"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

type fixtureGaslessProvider struct {
	prepared, submitted, queried int
	requests                     []bitget.Request
	status                       bitget.OrderStatus
	fail                         error
}

func (p *fixtureGaslessProvider) Prepare(_ context.Context, r bitget.Request) (*bitget.Order, error) {
	p.prepared++
	p.requests = append(p.requests, r)
	if p.fail != nil {
		return nil, p.fail
	}
	o := &bitget.Order{ID: "fixture-order", Chain: r.Chain, From: r.From, To: r.To, Contract: r.Contract, Amount: r.Amount}
	o.NoGas.Available = true
	o.NoGas.PayToken = r.Contract
	o.NoGas.PayAmount = "0.05"
	o.Source.Type = "evm_7702"
	return o, nil
}
func (p *fixtureGaslessProvider) Submit(_ context.Context, id, sig string) error {
	p.submitted++
	return p.fail
}
func (p *fixtureGaslessProvider) Status(context.Context, string) (*bitget.OrderStatus, error) {
	p.queried++
	if p.fail != nil {
		return nil, p.fail
	}
	return &p.status, nil
}
func (p *fixtureGaslessProvider) ValidateSigning(*bitget.Order, bitget.Intent, time.Time) (*bitget.Validated, error) {
	return &bitget.Validated{}, nil
}

type fixtureGaslessWallet struct {
	*fixtureWallet
	signs, binds, submissions int
	delegates                 int
	intent                    bitget.Intent
}

func (w *fixtureGaslessWallet) SignBitgetOrder(_ context.Context, _ int64, _ *bitget.Order, in bitget.Intent) (*wallet.BitgetSignedOrder, error) {
	w.signs++
	w.intent = in
	return &wallet.BitgetSignedOrder{Messages: `[{"sig":"fixture"}]`, Authorization: "fixture-mac"}, nil
}
func (w *fixtureGaslessWallet) BindBitgetTransaction(context.Context, int64, string, string, bitget.Intent) error {
	w.binds++
	return nil
}
func (w *fixtureGaslessWallet) GuardBitgetSubmit(_ context.Context, _ int64, _ string, _ string, f func() (string, error)) (string, error) {
	w.submissions++
	return f()
}
func (w *fixtureGaslessWallet) VerifyBitgetRecord(context.Context, int64, string, bitget.Intent, string) error {
	return nil
}
func (w *fixtureGaslessWallet) RecordBitgetDelegate(context.Context, int64, string, string, string, json.RawMessage) error {
	w.delegates++
	return nil
}

func bitgetReceiptPool(t *testing.T, in bitget.Intent, hash string, head uint64, canonical string, mutate func(map[string]any)) *rpc.Pool {
	t.Helper()
	log := func(to, amount string) map[string]any {
		n, err := bitget.Uint(amount)
		require.NoError(t, err)
		return map[string]any{"address": in.Token, "topics": []string{"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef", common.BytesToHash(common.HexToAddress(in.From).Bytes()).Hex(), common.BytesToHash(common.HexToAddress(to).Bytes()).Hex()}, "data": common.BytesToHash(n.Bytes()).Hex()}
	}
	receipt := map[string]any{"transactionHash": hash, "status": "0x1", "blockNumber": "0x64", "blockHash": testBlockHash, "logs": []any{log(in.To, in.Net), log("0x4444444444444444444444444444444444444444", in.Fee)}}
	if mutate != nil {
		mutate(receipt)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string            `json:"method"`
			ID     any               `json:"id"`
			Params []json.RawMessage `json:"params"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			w.WriteHeader(400)
			return
		}
		var result any
		switch req.Method {
		case "eth_chainId":
			result = "0x38"
		case "eth_getTransactionReceipt":
			result = receipt
		case "eth_blockNumber":
			result = fmt.Sprintf("0x%x", head)
		case "eth_getBlockByNumber":
			result = map[string]any{"hash": canonical, "number": "0x64"}
		case "eth_getTransactionByHash":
			result = map[string]any{"hash": hash, "blockHash": testBlockHash, "chainId": "0x38", "type": "0x2", "authorizationList": []any{}}
		case "eth_getCode":
			var a string
			_ = json.Unmarshal(req.Params[0], &a)
			if strings.EqualFold(a, in.From) {
				result = "0xef0100" + strings.TrimPrefix(in.CurrentDelegate, "0x")
			} else {
				result = "0x6000"
			}
		default:
			t.Errorf("unexpected wallet RPC %s", req.Method)
			w.WriteHeader(400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	t.Cleanup(server.Close)
	return rpc.NewPoolWithHTTPClient(server.URL, server.Client())
}

func TestBitgetReceiptRequiresExactNetFeeAndCanonicalDepth(t *testing.T) {
	in := bitget.Intent{Chain: "bsc", ChainID: 56, From: "0x3333333333333333333333333333333333333333", To: testDestination, Token: "0x55d398326f99059ff775485246999027b3197955", Net: "9950000", Fee: "50000", Gross: "10000000"}
	hash := "0x" + strings.Repeat("ab", 32)
	for _, tc := range []struct {
		name      string
		head      uint64
		canonical string
		change    func(map[string]any)
		ok        bool
	}{
		{"confirmed exact net plus fee", 103, testBlockHash, nil, true},
		{"not enough confirmations", 101, testBlockHash, nil, false},
		{"reorg", 103, "0x" + strings.Repeat("bb", 32), nil, false},
		{"extra token outflow", 103, testBlockHash, func(r map[string]any) { logs := r["logs"].([]any); r["logs"] = append(logs, logs[0]) }, false},
		{"wrong token", 103, testBlockHash, func(r map[string]any) { r["logs"].([]any)[0].(map[string]any)["address"] = in.To }, false},
		{"malformed amount", 103, testBlockHash, func(r map[string]any) { r["logs"].([]any)[0].(map[string]any)["data"] = "0x1" }, false},
		{"missing fee", 103, testBlockHash, func(r map[string]any) { r["logs"] = r["logs"].([]any)[:1] }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := bitgetReceiptPool(t, in, hash, tc.head, tc.canonical, tc.change)
			collector, err := (&Executor{}).verifyBitgetReceipt(t.Context(), pool, in, hash, 2, true)
			if tc.ok {
				require.NoError(t, err)
				require.NotEmpty(t, collector)
			} else {
				require.Error(t, err)
			}
		})
	}
	bad := in
	bad.Fee = "malformed"
	_, err := (&Executor{}).verifyBitgetReceipt(t.Context(), bitgetReceiptPool(t, in, hash, 103, testBlockHash, nil), bad, hash, 2, true)
	require.Error(t, err)
}

func TestBitgetTerminalJobsNeverSignOrSubmit(t *testing.T) {
	for _, status := range []string{"manual_review", "failed", "swept"} {
		t.Run(status, func(t *testing.T) {
			p := &fixtureGaslessProvider{}
			e := &Executor{gasless: p}
			_, err := e.resumeBitget(t.Context(), &sweepJob{Status: status})
			require.NoError(t, err)
			require.Zero(t, p.queried)
			require.Zero(t, p.submitted)
		})
	}
}

func TestBitgetQuoteSubtractsFeeExactlyWithoutNativeFunding(t *testing.T) {
	e := &Executor{config: Config{Provider: "bitget", HotWalletEVM: testDestination, BitgetMaxFee: "0.5", BitgetMaxFeeBPS: 100}}
	p := &fixtureGaslessProvider{}
	e.gasless = p
	entry := PlanEntry{Chain: "bsc", Address: "0x3333333333333333333333333333333333333333", Token: "0x55d398326f99059ff775485246999027b3197955", RawAmount: new(big.Int).Exp(big.NewInt(10), big.NewInt(19), nil), Decimals: 18}
	o, in, err := e.quoteBitget(t.Context(), entry)
	require.NoError(t, err)
	require.Equal(t, 2, p.prepared)
	require.Equal(t, "10", p.requests[0].Amount)
	require.Equal(t, "9.95", p.requests[1].Amount)
	require.Equal(t, "50000000000000000", in.Fee)
	require.Equal(t, "9950000000000000000", in.Net)
	require.Equal(t, "9.95", o.Amount)
	plan := &Plan{Entries: []PlanEntry{entry}}
	e.EnrichGaslessPlan(t.Context(), plan)
	require.True(t, plan.Entries[0].Gasless)
	require.False(t, plan.Entries[0].NeedsGasTopUp)
	require.Equal(t, "0.05", plan.Entries[0].FeeAmount)
	require.Zero(t, p.submitted)
}

func TestBitgetQuoteUnavailableNeverFallsBackOrSubmits(t *testing.T) {
	p := &fixtureGaslessProvider{fail: bitget.ErrUnavailable}
	e := &Executor{config: Config{Provider: "bitget"}, gasless: p}
	entry := PlanEntry{Chain: "bsc", RawAmount: big.NewInt(10)}
	plan := &Plan{Entries: []PlanEntry{entry}}
	e.EnrichGaslessPlan(t.Context(), plan)
	require.NotEmpty(t, plan.Entries[0].QuoteError)
	require.False(t, plan.Entries[0].NeedsGasTopUp)
	require.Zero(t, p.submitted)
	entry.Chain = "plasma"
	_, _, err := e.quoteBitget(t.Context(), entry)
	require.ErrorIs(t, err, bitget.ErrUnsupportedChain)
}

func TestBitgetTaskIDCannotBeUsedAsChainHash(t *testing.T) {
	var s bitget.OrderStatus
	require.NoError(t, json.Unmarshal([]byte(`{"orderId":"fixture-order","orderStatus":"PROCESSING","txid":"getgas_task_fixture"}`), &s))
	require.False(t, bitget.Hash(s.TxID))
	require.True(t, bitget.Hash("0x"+strings.Repeat("ab", 32)))
}
