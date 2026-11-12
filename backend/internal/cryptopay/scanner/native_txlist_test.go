package scanner

import (
	"encoding/json"
	"math/big"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/explorer"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

func explorerRow(hash, from, to string, value *big.Int, block uint64) explorer.Tx {
	lowered := to
	return explorer.Tx{Hash: hash, From: from, To: &lowered, Value: value, BlockNumber: block}
}

func TestNativeTransferFromExplorerTxKeepsTheSharedIdentity(t *testing.T) {
	amount := big.NewInt(1_000_000_000_000_000_000)
	transfer, ok := NativeTransferFromExplorerTx("bsc", explorerRow(nativeTestHashA, nativeSender, nativeDeposit, amount, 100))
	if !ok {
		t.Fatal("a positive native transfer to the recipient was rejected")
	}
	if transfer.LogIndex != NativeLogIndex || !transfer.Native || transfer.Token != NativeToken {
		t.Fatalf("transfer is not on the shared native identity: %+v", transfer)
	}
	if transfer.BlockHash != "" {
		t.Fatalf("an account-API row must not invent a block hash: %q", transfer.BlockHash)
	}
	if transfer.Amount.Cmp(amount) != 0 || transfer.BlockNumber != 100 {
		t.Fatalf("transfer amount/block = %s/%d", transfer.Amount, transfer.BlockNumber)
	}
}

func TestNativeTransferFromExplorerTxRejectsNonPayingRows(t *testing.T) {
	zero := explorerRow(nativeTestHashA, nativeSender, nativeDeposit, big.NewInt(0), 100)
	if _, ok := NativeTransferFromExplorerTx("bsc", zero); ok {
		t.Fatal("a zero-value contract call became a native deposit")
	}
	creation := explorerRow(nativeTestHashB, nativeSender, "", big.NewInt(5), 100)
	creation.To = nil
	if _, ok := NativeTransferFromExplorerTx("bsc", creation); ok {
		t.Fatal("a contract creation became a native deposit")
	}
}

func TestExplorerTxIncomingMatchesOnlyTheWatchedAddress(t *testing.T) {
	amount := big.NewInt(1)
	if !ExplorerTxIncoming(explorerRow(nativeTestHashA, nativeSender, nativeDeposit, amount, 1), nativeDeposit) {
		t.Fatal("a payment to the watched recipient was not matched")
	}
	if ExplorerTxIncoming(explorerRow(nativeTestHashA, nativeSender, nativeOther, amount, 1), nativeDeposit) {
		t.Fatal("a payment to another address matched the watched recipient")
	}
	// A token transfer's transaction recipient is the token contract, not us.
	if ExplorerTxIncoming(explorerRow(nativeTestHashA, nativeSender, nativeToken, amount, 1), nativeDeposit) {
		t.Fatal("a token-contract call matched the watched recipient")
	}
}

// buildNativePool serves eth_getTransactionByHash with the mined transfer used
// by the empty-block-hash verification test.
func buildNativePool(t *testing.T, tx map[string]any) *rpc.Pool {
	t.Helper()
	body, err := json.Marshal(tx)
	if err != nil {
		t.Fatal(err)
	}
	return nativeVerifyPool(t, `{"jsonrpc":"2.0","id":1,"result":`+string(body)+`}`)
}

func TestVerifyNativeTransferWithoutBlockHashUsesTheReceipt(t *testing.T) {
	amount := big.NewInt(1_000_000_000_000_000_000)
	tx := nativeTxEntry(nativeTestHashA, nativeSender, nativeDeposit, false, amount)
	pool := buildNativePool(t, tx)
	transfer := nativeTransferFixture(amount)
	transfer.BlockHash = "" // the txlist path has no block hash

	if err := verifyNativeTransfer(t.Context(), pool, transfer, nativeReceiptFixture()); err != nil {
		t.Fatalf("a txlist observation was rejected: %v", err)
	}

	// The height from the account API must still match the receipt: that is the
	// remaining binding between the provider's claim and the chain.
	wrongBlock := transfer
	wrongBlock.BlockNumber = 101
	if err := verifyNativeTransfer(t.Context(), pool, wrongBlock, nativeReceiptFixture()); err == nil {
		t.Fatal("a transfer claiming another height was accepted")
	}

	// An observed block hash is still enforced.
	wrongHash := transfer
	wrongHash.BlockHash = nativeTestHashB
	if err := verifyNativeTransfer(t.Context(), pool, wrongHash, nativeReceiptFixture()); err == nil {
		t.Fatal("a mismatched observed block hash was accepted")
	}
}

func TestNativeBlockScanFollowsTheConfiguredSource(t *testing.T) {
	fallback := ChainConfig{Chain: "bsc", Native: true}
	if !fallback.NativeBlockScan() {
		t.Fatal("the fallback source must keep reading blocks for the native coin")
	}
	blocks := ChainConfig{Chain: "bsc", Native: true, NativeSource: NativeScanBlocks}
	if !blocks.NativeBlockScan() {
		t.Fatal("an explicit blocks source must read blocks")
	}
	txlist := ChainConfig{Chain: "bsc", Native: true, NativeSource: NativeScanTxList}
	if txlist.NativeBlockScan() {
		t.Fatal("the txlist source must not read every block for the native coin")
	}
	// Native stays enabled so transferDecimals still resolves the coin.
	withDecimals := txlist
	withDecimals.NativeDecimals = 18
	if decimals, ok := transferDecimals(withDecimals, NativeToken); !ok || decimals != 18 {
		t.Fatalf("txlist source lost native decimals: %d/%v", decimals, ok)
	}
	off := ChainConfig{Chain: "bsc", NativeSource: NativeScanTxList}
	if off.NativeBlockScan() {
		t.Fatal("a chain without native scanning reported block scans")
	}
}
