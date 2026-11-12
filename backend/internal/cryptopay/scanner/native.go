package scanner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// NativeToken is the pseudo-contract that identifies a chain's native mainnet
// coin: ETH on Ethereum, BNB on BSC. It is deliberately not a hex address.
//
// The whole deposit pipeline keys on one opaque token string: the scanner's
// allowlist (ChainConfig.Native + Decimals), the ledger identity
// (chain, tx_hash, log_index), the sweep token map and the balance reader's
// "chain|token" lookup. Reusing that string as the native asset identifier is
// what lets native-coin deposits share the existing idempotency, confirmation
// and crediting path instead of growing a second ledger.
const NativeToken = "native"

// NativeLogIndex is the synthetic log index persisted for a native-coin
// deposit. A native transfer emits no log, so there is no log index to reuse,
// yet uq_deposit_tx_chain_hash_log is (chain, tx_hash, log_index) and the row
// needs an identity. A transaction carries at most one native value transfer
// (one `to`/`value` pair), and no real log index reaches this value, so a
// native deposit and a token deposit mined in the same transaction stay
// distinct rows.
const NativeLogIndex = 2_000_000_000

// ErrTransactionNotFound means the node answered null for a transaction hash
// whose receipt it already served. That is a lagging endpoint, not a reorg, so
// callers keep the observation retryable instead of concluding anything.
var ErrTransactionNotFound = errors.New("transaction not found")

// nativeTxJSON decodes `to` as a pointer because contract creation carries
// JSON null. A null recipient is never a deposit to us.
type nativeTxJSON struct {
	Hash  string  `json:"hash"`
	From  string  `json:"from"`
	To    *string `json:"to"`
	Value string  `json:"value"`
}

type nativeBlock struct {
	Number       string         `json:"number"`
	Hash         string         `json:"hash"`
	Transactions []nativeTxJSON `json:"transactions"`
}

// ScanNativeRange finds native-coin deposits in [fromBlock, toBlock].
//
// EVM exposes native value transfers only in transactions, never in logs, so
// this cannot reuse the ERC-20 eth_getLogs path: it reads each block with full
// transactions and matches `to == a watched recipient && value > 0`. Blocks are
// read concurrently through the same pool and the same bounded worker count as
// the log scan, and the result is assembled in block order so it is identical
// to a sequential scan.
//
// A token transfer is never reported here: its transaction `to` is the token
// contract, not our deposit address.
func (s *EVMScanner) ScanNativeRange(ctx context.Context, chain string, fromBlock, toBlock uint64, recipients []string) ([]Transfer, error) {
	if len(recipients) == 0 || toBlock < fromBlock {
		return nil, nil
	}
	// A range wider than this cannot come from the configured scan span and
	// would be turned into an absurd allocation below.
	if toBlock-fromBlock >= 1<<31 {
		return nil, fmt.Errorf("native scan range %d-%d is too wide", fromBlock, toBlock)
	}
	recipientSet := make(map[string]struct{}, len(recipients))
	for _, r := range recipients {
		recipientSet[strings.ToLower(r)] = struct{}{}
	}

	blocks := int(toBlock - fromBlock + 1)
	results := make([]batchResult, blocks)
	runBatches(ctx, blocks, s.scanWorkers(), func(index int) {
		block := fromBlock + uint64(index)
		transfers, err := s.nativeBlockTransfers(ctx, chain, block, recipientSet)
		results[index] = batchResult{done: true, transfers: transfers, err: err}
	})

	// Assemble strictly by block index: neither the transfer order nor the
	// joined error order depends on which request finished first.
	var out []Transfer
	var scanErrs []error
	unscanned := -1
	for index, result := range results {
		switch {
		case !result.done:
			// Never started because ctx ended first. Skipping it silently
			// would report a partly scanned range as complete.
			if unscanned < 0 {
				unscanned = index
			}
		case result.err != nil:
			scanErrs = append(scanErrs, result.err)
		default:
			out = append(out, result.transfers...)
		}
	}
	if unscanned >= 0 {
		cause := errors.New("native block was not scanned")
		if ctxErr := ctx.Err(); ctxErr != nil {
			cause = rpc.NewTransportFailure(ctxErr)
		}
		scanErrs = append(scanErrs, fmt.Errorf("native block %d of %d-%d: %w",
			fromBlock+uint64(unscanned), fromBlock, toBlock, cause))
	}
	return out, errors.Join(scanErrs...)
}

// nativeBlockTransfers reads one block with full transactions and returns the
// native transfers that paid a watched recipient.
func (s *EVMScanner) nativeBlockTransfers(ctx context.Context, chain string, block uint64, recipients map[string]struct{}) ([]Transfer, error) {
	raw, _, err := s.pool.CallEndpoint(ctx, "eth_getBlockByNumber", []any{fmt.Sprintf("0x%x", block), true})
	if err != nil {
		return nil, err
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		// A node that cannot answer for this height would otherwise be
		// indistinguishable from a block with no deposits.
		return nil, &rpc.Failure{Kind: "response"}
	}
	var decoded nativeBlock
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("getBlock decode: %w", err)
	}
	// The answer must be the block that was asked for. A lagging or faulty
	// endpoint must not be able to shift a deposit onto another height.
	number, err := hexQuantity(decoded.Number)
	if err != nil || number != block || !validHash(decoded.Hash) {
		return nil, &rpc.Failure{Kind: "response"}
	}
	var out []Transfer
	for _, tx := range decoded.Transactions {
		if tx.To == nil || !validHex(tx.Hash, 32) || !validHex(*tx.To, 20) || !validHex(tx.From, 20) {
			continue
		}
		to := strings.ToLower(*tx.To)
		if _, ok := recipients[to]; !ok {
			continue
		}
		// `value` is a hex quantity with an 0x prefix; the scan needs the full
		// 256-bit range, so it cannot use hexQuantity's uint64 decoder.
		value, ok := new(big.Int).SetString(strings.TrimPrefix(tx.Value, "0x"), 16)
		if !ok || value.Sign() <= 0 {
			// value == 0 pays nothing: a plain contract call, not a deposit.
			continue
		}
		out = append(out, Transfer{
			Chain:       chain,
			TxHash:      strings.ToLower(tx.Hash),
			LogIndex:    NativeLogIndex,
			From:        strings.ToLower(tx.From),
			To:          to,
			Token:       NativeToken,
			Amount:      value,
			Native:      true,
			BlockNumber: block,
			BlockHash:   decoded.Hash,
		})
	}
	return out, nil
}

// fetchNativeTransaction loads the mined transaction a native deposit claims
// to be. Transport and decode failures are inconclusive: the caller keeps the
// observation retryable.
func fetchNativeTransaction(ctx context.Context, pool *rpc.Pool, txHash string) (nativeTxJSON, error) {
	raw, err := pool.Call(ctx, "eth_getTransactionByHash", []any{txHash})
	if err != nil {
		return nativeTxJSON{}, err
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nativeTxJSON{}, ErrTransactionNotFound
	}
	var decoded nativeTxJSON
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nativeTxJSON{}, fmt.Errorf("decode native transaction: %w", err)
	}
	return decoded, nil
}

// verifyNativeTransfer authenticates a native-coin deposit against the mined
// transaction itself: the receipt proves the transaction succeeded and is
// canonical, and to/value/from prove it paid the address that was observed.
//
// A token transfer can never satisfy this: its transaction `to` is the token
// contract, so it does not match the watched deposit address.
func verifyNativeTransfer(ctx context.Context, pool *rpc.Pool, t Transfer, receipt *ReceiptCheck) error {
	if t.Amount == nil || t.Amount.Sign() <= 0 || !t.Native ||
		!validHex(t.From, 20) || !validHex(t.To, 20) ||
		t.BlockNumber != receipt.BlockNumber || !strings.EqualFold(t.BlockHash, receipt.BlockHash) ||
		!strings.EqualFold(t.TxHash, receipt.TxHash) {
		return ErrTransferMismatch
	}
	tx, err := fetchNativeTransaction(ctx, pool, t.TxHash)
	if err != nil {
		return err
	}
	if tx.To == nil ||
		!strings.EqualFold(tx.Hash, t.TxHash) ||
		!strings.EqualFold(*tx.To, t.To) || !strings.EqualFold(tx.From, t.From) {
		return ErrTransferMismatch
	}
	value, ok := new(big.Int).SetString(strings.TrimPrefix(tx.Value, "0x"), 16)
	if !ok || value.Sign() <= 0 || value.Cmp(t.Amount) != 0 {
		return ErrTransferMismatch
	}
	return nil
}
