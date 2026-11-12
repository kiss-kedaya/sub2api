package scanner

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

const (
	nativeTestHashA = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	nativeTestHashB = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	nativeTestHashC = "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	nativeTestHashD = "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	nativeTestHashE = "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	nativeTestBlock = "0x1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f"
)

var (
	nativeDeposit = "0x1111111111111111111111111111111111111111"
	nativeOther   = "0x2222222222222222222222222222222222222222"
	nativeToken   = "0xdac17f958d2ee523a2206206994597c13d831ec7"
	nativeSender  = "0x3333333333333333333333333333333333333333"
)

func nativeTxEntry(hash, from, to string, toNull bool, value *big.Int) map[string]any {
	entry := map[string]any{"hash": hash, "from": from, "value": "0x" + value.Text(16)}
	if toNull {
		entry["to"] = nil
	} else {
		entry["to"] = to
	}
	return entry
}

func nativeBlockResponse(t *testing.T, number uint64, hash string, txs []map[string]any) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"number": fmt.Sprintf("0x%x", number), "hash": hash, "transactions": txs,
	})
	if err != nil {
		t.Fatal(err)
	}
	return `{"jsonrpc":"2.0","id":1,"result":` + string(body) + `}`
}

// nativePool serves eth_getBlockByNumber from a fixed table and fails the test
// on any other call, so a native scan can never silently piggyback on the
// eth_getLogs path.
func nativePool(t *testing.T, blocks map[uint64]string) *rpc.Pool {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.Method != "eth_getBlockByNumber" {
			t.Errorf("native scan issued %s; native deposits must be read from blocks, not logs", request.Method)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(request.Params) < 2 {
			t.Error("block scan did not request full transactions")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var fullTxs bool
		if err := json.Unmarshal(request.Params[1], &fullTxs); err != nil || !fullTxs {
			t.Errorf("block scan must ask for full transactions, got %s", request.Params[1])
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var tag string
		if err := json.Unmarshal(request.Params[0], &tag); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		height := new(big.Int)
		if _, ok := height.SetString(strings.TrimPrefix(tag, "0x"), 16); !ok {
			t.Errorf("unexpected block tag %q", tag)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		result, ok := blocks[height.Uint64()]
		if !ok {
			result = `{"jsonrpc":"2.0","id":1,"result":null}`
		}
		_, _ = w.Write([]byte(result))
	}))
	t.Cleanup(server.Close)
	return rpc.NewPoolWithHTTPClient(server.URL, server.Client())
}

func TestNativeScanReadsValueTransfersFromTransactions(t *testing.T) {
	oneETH := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	// 2^70 wei is far beyond int64 and is exactly the kind of amount a
	// uint64-based decoder would truncate.
	huge, _ := new(big.Int).SetString("1180591620717411303424", 10)
	blocks := map[uint64]string{
		100: nativeBlockResponse(t, 100, nativeTestBlock, []map[string]any{
			nativeTxEntry(nativeTestHashA, nativeSender, nativeDeposit, false, oneETH),
			// value == 0 is a plain call, never a deposit.
			nativeTxEntry(nativeTestHashB, nativeSender, nativeDeposit, false, big.NewInt(0)),
			nativeTxEntry(nativeTestHashC, nativeSender, nativeOther, false, big.NewInt(5)),
			// A token transfer: the transaction recipient is the contract, not us.
			nativeTxEntry(nativeTestHashD, nativeSender, nativeToken, false, big.NewInt(0)),
			// Contract creation has no recipient at all.
			nativeTxEntry(nativeTestHashE, nativeSender, "", true, big.NewInt(7)),
		}),
		101: nativeBlockResponse(t, 101, nativeTestBlock, nil),
		102: nativeBlockResponse(t, 102, nativeTestBlock, []map[string]any{
			// One wei: the smallest unit of an 18-decimal asset.
			nativeTxEntry(nativeTestHashB, nativeSender, nativeDeposit, false, big.NewInt(1)),
			nativeTxEntry(nativeTestHashC, nativeSender, nativeDeposit, false, huge),
		}),
	}
	s := NewEVMScanner(nativePool(t, blocks))
	transfers, err := s.ScanNativeRange(t.Context(), "ethereum", 100, 102, []string{nativeDeposit})
	if err != nil {
		t.Fatalf("native scan failed: %v", err)
	}
	if len(transfers) != 3 {
		t.Fatalf("native scan returned %d transfers, want 3: %+v", len(transfers), transfers)
	}
	want := []struct {
		tx     string
		block  uint64
		amount *big.Int
	}{
		{nativeTestHashA, 100, oneETH},
		{nativeTestHashB, 102, big.NewInt(1)},
		{nativeTestHashC, 102, huge},
	}
	for i, expected := range want {
		got := transfers[i]
		if got.TxHash != expected.tx || got.BlockNumber != expected.block {
			t.Fatalf("transfer %d identity = %s@%d, want %s@%d", i, got.TxHash, got.BlockNumber, expected.tx, expected.block)
		}
		if got.Amount.Cmp(expected.amount) != 0 {
			t.Fatalf("transfer %d amount = %s, want %s", i, got.Amount, expected.amount)
		}
		if !got.Native || got.Token != NativeToken {
			t.Fatalf("transfer %d is not marked native: %+v", i, got)
		}
		if got.LogIndex != NativeLogIndex {
			t.Fatalf("native transfer must use the synthetic log index %d, got %d", NativeLogIndex, got.LogIndex)
		}
		if got.To != nativeDeposit || got.From != nativeSender {
			t.Fatalf("transfer %d addresses = %s -> %s", i, got.From, got.To)
		}
		if got.BlockHash != nativeTestBlock {
			t.Fatalf("transfer %d block hash = %q", i, got.BlockHash)
		}
	}
}

func TestNativeScanRejectsUnreadableBlocks(t *testing.T) {
	for name, fixture := range map[string]func() string{
		"null": func() string { return `{"jsonrpc":"2.0","id":1,"result":null}` },
		"wrong height": func() string {
			return nativeBlockResponse(t, 999, nativeTestBlock, nil)
		},
		"missing hash": func() string {
			return nativeBlockResponse(t, 100, "", nil)
		},
		"zero hash": func() string {
			return nativeBlockResponse(t, 100, "0x"+strings.Repeat("0", 64), nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			blocks := map[uint64]string{100: fixture()}
			s := NewEVMScanner(nativePool(t, blocks))
			transfers, err := s.ScanNativeRange(t.Context(), "ethereum", 100, 100, []string{nativeDeposit})
			var failure *rpc.Failure
			if err == nil || !errors.As(err, &failure) || failure.Kind != "response" {
				t.Fatalf("an unreadable block was reported as a complete scan: %v", err)
			}
			if len(transfers) != 0 {
				t.Fatalf("unreadable block produced transfers: %+v", transfers)
			}
		})
	}
}

func TestNativeScanWithoutRecipientsMakesNoRequest(t *testing.T) {
	s := NewEVMScanner(nativePool(t, nil)) // any request fails the test
	transfers, err := s.ScanNativeRange(t.Context(), "ethereum", 100, 200, nil)
	if err != nil || transfers != nil {
		t.Fatalf("empty recipient list must be a no-op, got %v %v", transfers, err)
	}
}

func nativeVerifyPool(t *testing.T, txResponse string) *rpc.Pool {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.Method != "eth_getTransactionByHash" {
			t.Errorf("native verification issued %s", request.Method)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(txResponse))
	}))
	t.Cleanup(server.Close)
	return rpc.NewPoolWithHTTPClient(server.URL, server.Client())
}

func nativeTransferFixture(amount *big.Int) Transfer {
	return Transfer{
		Chain: "ethereum", TxHash: nativeTestHashA, LogIndex: NativeLogIndex,
		From: nativeSender, To: nativeDeposit, Token: NativeToken, Native: true,
		Amount: amount, BlockNumber: 100, BlockHash: nativeTestBlock,
	}
}

func nativeReceiptFixture() *ReceiptCheck {
	return &ReceiptCheck{Status: 1, BlockNumber: 100, BlockHash: nativeTestBlock, TxHash: nativeTestHashA}
}

func TestVerifyNativeTransferAcceptsTheMinedValueTransfer(t *testing.T) {
	amount := big.NewInt(1_000_000_000_000_000_000)
	tx := nativeTxEntry(nativeTestHashA, nativeSender, nativeDeposit, false, amount)
	body, _ := json.Marshal(tx)
	pool := nativeVerifyPool(t, `{"jsonrpc":"2.0","id":1,"result":`+string(body)+`}`)
	if err := verifyNativeTransfer(t.Context(), pool, nativeTransferFixture(amount), nativeReceiptFixture()); err != nil {
		t.Fatalf("a correctly observed native deposit was rejected: %v", err)
	}
}

func TestVerifyNativeTransferRejectsMismatches(t *testing.T) {
	amount := big.NewInt(1_000_000_000_000_000_000)
	for name, fixture := range map[string]struct {
		tx       map[string]any
		transfer Transfer
	}{
		"value differs": {
			nativeTxEntry(nativeTestHashA, nativeSender, nativeDeposit, false, big.NewInt(999)),
			nativeTransferFixture(amount),
		},
		"recipient differs": {
			nativeTxEntry(nativeTestHashA, nativeSender, nativeOther, false, amount),
			nativeTransferFixture(amount),
		},
		"sender differs": {
			nativeTxEntry(nativeTestHashA, nativeOther, nativeDeposit, false, amount),
			nativeTransferFixture(amount),
		},
		"token transfer is not native": {
			// The transaction pays the token contract, never the deposit
			// address, so it can never satisfy native verification.
			nativeTxEntry(nativeTestHashA, nativeSender, nativeToken, false, big.NewInt(0)),
			nativeTransferFixture(amount),
		},
		"contract creation": {
			nativeTxEntry(nativeTestHashA, nativeSender, "", true, amount),
			nativeTransferFixture(amount),
		},
		"zero value": {
			nativeTxEntry(nativeTestHashA, nativeSender, nativeDeposit, false, big.NewInt(0)),
			nativeTransferFixture(amount),
		},
	} {
		t.Run(name, func(t *testing.T) {
			body, _ := json.Marshal(fixture.tx)
			pool := nativeVerifyPool(t, `{"jsonrpc":"2.0","id":1,"result":`+string(body)+`}`)
			err := verifyNativeTransfer(t.Context(), pool, fixture.transfer, nativeReceiptFixture())
			if err == nil {
				t.Fatal("a mismatched native transaction was accepted as a deposit")
			}
		})
	}
}

func TestVerifyNativeTransferRejectsTokenShapedTransfers(t *testing.T) {
	// Even with a matching transaction on chain, a Transfer that is not marked
	// native must never take the native path: the ERC-20 receipt check is the
	// only authority for token deposits.
	amount := big.NewInt(1_000_000_000_000_000_000)
	tx := nativeTxEntry(nativeTestHashA, nativeSender, nativeDeposit, false, amount)
	body, _ := json.Marshal(tx)
	pool := nativeVerifyPool(t, `{"jsonrpc":"2.0","id":1,"result":`+string(body)+`}`)
	transfer := nativeTransferFixture(amount)
	transfer.Native = false
	transfer.Token = nativeToken
	if err := verifyNativeTransfer(t.Context(), pool, transfer, nativeReceiptFixture()); err == nil {
		t.Fatal("native verification accepted a transfer that is not marked native")
	}
}

func TestTransferDecimalsResolvesNativeCoin(t *testing.T) {
	chain := ChainConfig{
		Chain: "ethereum", Tokens: []string{nativeToken}, Decimals: map[string]int{nativeToken: 6},
		Native: true, NativeDecimals: 18,
	}
	if decimals, ok := transferDecimals(chain, NativeToken); !ok || decimals != 18 {
		t.Fatalf("native decimals = %d/%v, want 18/true", decimals, ok)
	}
	if decimals, ok := transferDecimals(chain, nativeToken); !ok || decimals != 6 {
		t.Fatalf("token decimals regressed: %d/%v", decimals, ok)
	}
	if _, ok := transferDecimals(chain, "0x9999999999999999999999999999999999999999"); ok {
		t.Fatal("an unwatched token resolved decimals")
	}
	// A chain that does not scan native coin must reject it instead of
	// guessing 18 and crediting a token contract's deposit as ETH.
	off := ChainConfig{Chain: "polygon", Tokens: []string{nativeToken}, Decimals: map[string]int{nativeToken: 6}}
	if _, ok := transferDecimals(off, NativeToken); ok {
		t.Fatal("a chain without native scanning accepted a native transfer")
	}
}

func TestDecimalToRawUnitsHandles18DecimalAssets(t *testing.T) {
	cases := []struct {
		value    string
		decimals int
		want     string
	}{
		{"1", 18, "1000000000000000000"},
		{"0.000000000000000001", 18, "1"},
		{"0.1", 18, "100000000000000000"},
		// Beyond int64 (9.22e18): the raw value needs big.Int.
		{"100", 18, "100000000000000000000"},
		{"123456789012345678.901234567890123456", 18, "123456789012345678901234567890123456"},
	}
	for _, tt := range cases {
		raw, err := decimalToRawUnits(tt.value, tt.decimals)
		if err != nil {
			t.Fatalf("decimalToRawUnits(%q) failed: %v", tt.value, err)
		}
		if raw.String() != tt.want {
			t.Fatalf("decimalToRawUnits(%q) = %s, want %s", tt.value, raw, tt.want)
		}
	}
	if _, err := decimalToRawUnits("0.0000000000000000001", 18); err == nil {
		t.Fatal("a value finer than 1 wei was accepted")
	}
}
