package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// ReceiptCheck is the pre-credit verification for one EVM transaction.
// A transfer only counts after the chain says it succeeded AND the block is
// final enough that a reorg cannot silently erase it.
type ReceiptCheck struct {
	Status      uint64 // 1 = success, 0 = reverted
	BlockNumber uint64
	BlockHash   string
}

// VerifyTransaction fetches the receipt and rejects reverted transactions.
// This is the guard against crediting a transfer that the chain later
// reverts or that was broadcast but never mined.
func VerifyTransaction(ctx context.Context, pool *rpc.Pool, txHash string) (*ReceiptCheck, error) {
	result, err := pool.Call(ctx, "eth_getTransactionReceipt", []any{txHash})
	if err != nil {
		return nil, fmt.Errorf("get receipt %s: %w", txHash, err)
	}
	if len(result) == 0 || string(result) == "null" {
		return nil, fmt.Errorf("transaction %s has no receipt yet", txHash)
	}
	var receipt struct {
		Status      string `json:"status"`
		BlockNumber string `json:"blockNumber"`
		BlockHash   string `json:"blockHash"`
	}
	if err := json.Unmarshal(result, &receipt); err != nil {
		return nil, fmt.Errorf("decode receipt %s: %w", txHash, err)
	}
	status := parseHexUint(receipt.Status)
	if status != 1 {
		return nil, fmt.Errorf("transaction %s reverted (status=%s)", txHash, receipt.Status)
	}
	return &ReceiptCheck{
		Status:      status,
		BlockNumber: parseHexUint(receipt.BlockNumber),
		BlockHash:   receipt.BlockHash,
	}, nil
}

// BlockIsFinal reports whether blockNumber sits at or below the chain's
// "safe" tag. Chains without the tag (older clients) fall back to the
// caller's confirmation-count rule.
func BlockIsFinal(ctx context.Context, pool *rpc.Pool, blockNumber uint64) (bool, error) {
	result, err := pool.Call(ctx, "eth_getBlockByNumber", []any{"safe", false})
	if err != nil {
		return false, err
	}
	if len(result) == 0 || string(result) == "null" {
		return false, fmt.Errorf("safe block tag unavailable")
	}
	var block struct {
		Number string `json:"number"`
	}
	if err := json.Unmarshal(result, &block); err != nil {
		return false, err
	}
	safe := parseHexUint(block.Number)
	if safe == 0 {
		return false, fmt.Errorf("safe block number missing")
	}
	return blockNumber <= safe, nil
}

// IsZeroHash reports whether a hash is absent (used to reject unmined txs).
func IsZeroHash(hash string) bool {
	trimmed := strings.TrimPrefix(hash, "0x")
	return trimmed == "" || strings.Trim(trimmed, "0") == ""
}
