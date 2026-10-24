package scanner

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

var (
	// Only an explicit JSON null means absent. Transport/decode failures are
	// inconclusive and never count towards a missing-transaction alert.
	ErrReceiptNotFound     = errors.New("transaction receipt not found")
	ErrTransactionReverted = errors.New("transaction reverted")
	ErrNonCanonicalBlock   = errors.New("receipt block is not canonical")
	ErrTransferMismatch    = errors.New("receipt does not contain the observed transfer")
)

// ReceiptCheck includes the exact logs, not just the transaction's status:
// success alone says nothing about whether a saved ERC20 transfer survived.
type ReceiptCheck struct {
	Status      uint64
	BlockNumber uint64
	BlockHash   string
	TxHash      string
	Logs        []evmLog
}

// VerifyTransaction returns a receipt plus ErrTransactionReverted for an
// explicitly failed mined transaction. RPC failures are inconclusive.
func VerifyTransaction(ctx context.Context, pool *rpc.Pool, txHash string) (*ReceiptCheck, error) {
	result, err := pool.Call(ctx, "eth_getTransactionReceipt", []any{txHash})
	if err != nil {
		return nil, fmt.Errorf("get receipt: %w", err)
	}
	if bytes.Equal(bytes.TrimSpace(result), []byte("null")) {
		return nil, ErrReceiptNotFound
	}
	var receipt struct {
		Status      string   `json:"status"`
		BlockNumber string   `json:"blockNumber"`
		BlockHash   string   `json:"blockHash"`
		TxHash      string   `json:"transactionHash"`
		Logs        []evmLog `json:"logs"`
	}
	if err := json.Unmarshal(result, &receipt); err != nil {
		return nil, fmt.Errorf("decode receipt: %w", err)
	}
	status, err := hexQuantity(receipt.Status)
	if err != nil || status > 1 {
		return nil, &rpc.Failure{Kind: "response"}
	}
	block, err := hexQuantity(receipt.BlockNumber)
	if err != nil || !validHash(receipt.BlockHash) || !validHash(receipt.TxHash) || !strings.EqualFold(receipt.TxHash, txHash) {
		return nil, fmt.Errorf("invalid receipt identity or block")
	}
	checked := &ReceiptCheck{Status: status, BlockNumber: block, BlockHash: receipt.BlockHash, TxHash: receipt.TxHash, Logs: receipt.Logs}
	if status == 0 {
		return checked, ErrTransactionReverted
	}
	return checked, nil
}

// BlockIsFinal checks the safe tag. Callers use transactionIsFinal to also
// enforce confirmation depth and to fall back when safe is unavailable.
func BlockIsFinal(ctx context.Context, pool *rpc.Pool, blockNumber uint64) (bool, error) {
	result, err := pool.Call(ctx, "eth_getBlockByNumber", []any{"safe", false})
	if err != nil {
		return false, err
	}
	var block struct {
		Number string `json:"number"`
	}
	if err := json.Unmarshal(result, &block); err != nil {
		return false, err
	}
	safe, err := hexQuantity(block.Number)
	if err != nil {
		return false, fmt.Errorf("safe block unavailable: %w", err)
	}
	return blockNumber <= safe, nil
}

// Both entry points enforce configured depth, and additionally safe when
// available. Only safe-tag errors may fall back to depth; head errors may
// not. Subtraction avoids overflow on blockNumber+confirmations.
func transactionIsFinal(ctx context.Context, pool *rpc.Pool, blockNumber, confirmations uint64) (bool, error) {
	safe, safeErr := BlockIsFinal(ctx, pool, blockNumber)
	if safeErr == nil && !safe {
		return false, nil
	}
	raw, err := pool.Call(ctx, "eth_blockNumber", nil)
	if err != nil {
		return false, fmt.Errorf("finality head: %w", err)
	}
	var quantity string
	if err := json.Unmarshal(raw, &quantity); err != nil {
		return false, fmt.Errorf("decode finality head: %w", err)
	}
	head, err := hexQuantity(quantity)
	if err != nil {
		return false, fmt.Errorf("invalid finality head: %w", err)
	}
	return head >= blockNumber && head-blockNumber >= confirmations, nil
}

// BSC's safe tag is only its justified head. Its economic finality is exposed
// by finalized, and an unavailable or malformed tag must never become an
// implicit depth-only approval. Other networks retain their existing policy.
func transactionIsFinalForChain(ctx context.Context, pool *rpc.Pool, chain string, blockNumber, confirmations uint64) (bool, error) {
	if chain != "bsc" {
		return transactionIsFinal(ctx, pool, blockNumber, confirmations)
	}
	raw, err := pool.Call(ctx, "eth_getBlockByNumber", []any{"finalized", false})
	if err != nil {
		return false, fmt.Errorf("BSC finalized block: %w", err)
	}
	var block struct {
		Number string `json:"number"`
	}
	if err := json.Unmarshal(raw, &block); err != nil {
		return false, fmt.Errorf("decode BSC finalized block: %w", err)
	}
	finalized, err := hexQuantity(block.Number)
	if err != nil {
		return false, fmt.Errorf("BSC finalized block unavailable: %w", err)
	}
	if finalized < blockNumber {
		return false, nil
	}
	head, err := pool.BlockNumber(ctx)
	if err != nil {
		return false, fmt.Errorf("BSC finality head: %w", err)
	}
	return head >= blockNumber && head-blockNumber >= confirmations, nil
}

// Bind a pool to the configured EIP-155 chain. A valid receipt from another
// EVM chain must never resolve a pending deposit.
func verifyChain(ctx context.Context, pool *rpc.Pool, chain string) error {
	var want uint64
	switch chain {
	case "ethereum":
		want = 1
	case "bsc":
		want = 56
	case "polygon":
		want = 137
	case "plasma":
		want = 9745
	case "arbitrum":
		want = 42161
	case "base":
		want = 8453
	case "xlayer":
		want = 196
	default:
		return fmt.Errorf("unsupported scan chain %q", chain)
	}
	raw, err := pool.Call(ctx, "eth_chainId", nil)
	if err != nil {
		return err
	}
	var quantity string
	if err := json.Unmarshal(raw, &quantity); err != nil {
		return err
	}
	got, err := hexQuantity(quantity)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("RPC chain mismatch for %s: got %d, want %d", chain, got, want)
	}
	return nil
}

// Pool.Call rotates endpoints for every request. Checking chainId on just
// one request cannot authenticate a receipt fetched from another endpoint.
// Build an isolated pool containing only endpoints verified for this chain;
// never mutate the shared configured pool.
func verifiedChainPool(ctx context.Context, pool *rpc.Pool, chain string) (*rpc.Pool, error) {
	configured := pool.Endpoints()
	verified := make([]bool, len(configured))
	errs := make([]error, len(configured))
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var checks sync.WaitGroup
	jobs := make(chan int)
	for range min(4, len(configured)) {
		checks.Add(1)
		go func() {
			defer checks.Done()
			for index := range jobs {
				errs[index] = verifyChain(probe, pool.WithEndpoints(configured[index]), chain)
				verified[index] = errs[index] == nil
			}
		}()
	}
	for index := range configured {
		select {
		case jobs <- index:
		case <-probe.Done():
			errs[index] = probe.Err()
		}
	}
	close(jobs)
	checks.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var endpoints []string
	for index, endpoint := range configured {
		if verified[index] {
			endpoints = append(endpoints, endpoint)
		}
	}
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("no verified RPC endpoints for %s: %w", chain, errors.Join(append(errs, errors.New("chain verification failed"))...))
	}
	return pool.WithEndpoints(strings.Join(endpoints, ",")), nil
}

func verifyCanonicalBlock(ctx context.Context, pool *rpc.Pool, receipt *ReceiptCheck) error {
	raw, err := pool.Call(ctx, "eth_getBlockByNumber", []any{fmt.Sprintf("0x%x", receipt.BlockNumber), false})
	if err != nil {
		return fmt.Errorf("canonical block: %w", err)
	}
	var block struct {
		Number string `json:"number"`
		Hash   string `json:"hash"`
	}
	if err := json.Unmarshal(raw, &block); err != nil {
		return fmt.Errorf("decode canonical block: %w", err)
	}
	number, err := hexQuantity(block.Number)
	if err != nil || number != receipt.BlockNumber || !validHash(block.Hash) {
		return fmt.Errorf("canonical block unavailable or malformed")
	}
	if !strings.EqualFold(block.Hash, receipt.BlockHash) {
		return ErrNonCanonicalBlock
	}
	return nil
}

func verifyReceiptTransfer(t Transfer, receipt *ReceiptCheck) error {
	if t.Amount == nil || t.LogIndex < 0 || !validHex(t.From, 20) || !validHex(t.To, 20) ||
		t.BlockNumber != receipt.BlockNumber || !strings.EqualFold(t.BlockHash, receipt.BlockHash) || !strings.EqualFold(t.TxHash, receipt.TxHash) {
		return ErrTransferMismatch
	}
	matches := 0
	for _, log := range receipt.Logs {
		index, err := hexQuantity(log.LogIndex)
		if err != nil {
			return fmt.Errorf("invalid receipt log index: %w", err)
		}
		if index != uint64(t.LogIndex) {
			continue
		}
		block, err := hexQuantity(log.BlockNumber)
		if err != nil || block != receipt.BlockNumber || log.Removed ||
			!strings.EqualFold(log.BlockHash, receipt.BlockHash) || !strings.EqualFold(log.TxHash, t.TxHash) ||
			!strings.EqualFold(log.Address, t.Token) || len(log.Topics) != 3 ||
			!strings.EqualFold(log.Topics[0], transferTopic) ||
			!validHex(log.Topics[1], 32) || !validHex(log.Topics[2], 32) ||
			!strings.EqualFold(log.Topics[1], padTopic(t.From)) || !strings.EqualFold(log.Topics[2], padTopic(t.To)) || !validHex(log.Data, 32) {
			return ErrTransferMismatch
		}
		amount, ok := new(big.Int).SetString(log.Data[2:], 16)
		if !ok || amount.Cmp(t.Amount) != 0 {
			return ErrTransferMismatch
		}
		matches++
	}
	if matches != 1 {
		return ErrTransferMismatch
	}
	return nil
}

// VerifyObservedTransfer authenticates the exact mined transfer against a
// successful receipt and canonical block, without waiting to stop signing.
func VerifyObservedTransfer(ctx context.Context, pool *rpc.Pool, t Transfer) error {
	receipt, err := VerifyTransaction(ctx, pool, t.TxHash)
	if err != nil {
		return err
	}
	if err := verifyCanonicalBlock(ctx, pool, receipt); err != nil {
		return err
	}
	return verifyReceiptTransfer(t, receipt)
}

func hexQuantity(value string) (uint64, error) {
	if !strings.HasPrefix(value, "0x") || len(value) <= 2 {
		return 0, &rpc.Failure{Kind: "response"}
	}
	quantity, err := strconv.ParseUint(value[2:], 16, 64)
	if err != nil {
		return 0, &rpc.Failure{Kind: "response"}
	}
	return quantity, nil
}

func validHex(value string, size int) bool {
	if !strings.HasPrefix(value, "0x") || len(value) != 2+size*2 {
		return false
	}
	_, err := hex.DecodeString(value[2:])
	return err == nil
}

func validHash(hash string) bool { return validHex(hash, 32) && !IsZeroHash(hash) }

// IsZeroHash reports whether a hash is absent (used to reject unmined txs).
func IsZeroHash(hash string) bool {
	trimmed := strings.TrimPrefix(strings.ToLower(hash), "0x")
	return trimmed == "" || strings.Trim(trimmed, "0") == ""
}
