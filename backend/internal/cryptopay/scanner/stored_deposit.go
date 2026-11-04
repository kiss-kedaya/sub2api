package scanner

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

var (
	// ErrTransferReincluded means the canonical receipt still carries exactly one
	// identical Transfer log, but not at the block/log position that was stored.
	// That is the reorg case reconcile handles by relocating a pending row. A
	// manual confirmation must never relocate anything, so it refuses instead.
	ErrTransferReincluded = errors.New("transfer was re-included at a different block or log position")
	// ErrStoredDepositInvalid means the stored row itself cannot be compared with
	// chain data (unparsable amount, more precision than the token has, or a
	// non-positive amount).
	ErrStoredDepositInvalid = errors.New("stored deposit cannot be compared with chain data")
)

// StoredDeposit is a persisted deposit_transactions row, in the shape the
// operator tooling reads it. Amount is the NUMERIC value in display units, the
// same text reconcile reads back with amount::text.
type StoredDeposit struct {
	Chain       string
	TxHash      string
	LogIndex    int
	From        string
	To          string
	Token       string
	Amount      string
	BlockNumber uint64
	BlockHash   string
}

// VerifiedChainPool binds a pool to the configured EIP-155 chain exactly as the
// reconcile pass does: an isolated snapshot that only keeps endpoints whose
// eth_chainId matches. The shared pool is never mutated.
func VerifiedChainPool(ctx context.Context, pool *rpc.Pool, chain string) (*rpc.Pool, error) {
	if pool == nil {
		return nil, fmt.Errorf("no rpc pool for chain %s", chain)
	}
	return verifiedChainPool(ctx, pool, chain)
}

// VerifyStoredDeposit re-authenticates one stored deposit against the chain
// using the same primitives as reconcile: a successful receipt, the canonical
// block at the receipt's height, and exactly one matching Transfer log at the
// stored log index. It is read-only, and it deliberately skips the finality and
// security-coverage gates, because the caller is an authorised operator who
// accepts the reorg risk of a young block. The block-hash comparison still
// rejects a block that is no longer canonical.
//
// On success it returns the Transfer rebuilt from the stored row (not from the
// chain), ready to be handed to the ledger. decimals must come from the trusted
// token table; a stored amount with more precision than the token has is
// refused rather than rounded.
func VerifyStoredDeposit(ctx context.Context, pool *rpc.Pool, stored StoredDeposit, decimals int) (Transfer, error) {
	observation := depositObservation{
		chain: stored.Chain, txHash: strings.ToLower(stored.TxHash), from: stored.From, to: stored.To,
		token: stored.Token, amount: stored.Amount, blockHash: stored.BlockHash,
		logIndex: stored.LogIndex, blockNumber: stored.BlockNumber, status: "pending",
	}
	transfer, err := observation.transfer(decimals)
	if err != nil {
		return Transfer{}, fmt.Errorf("%w: %w", ErrStoredDepositInvalid, err)
	}
	if transfer.Amount.Sign() <= 0 {
		return Transfer{}, ErrStoredDepositInvalid
	}
	// ErrReceiptNotFound, ErrTransactionReverted and RPC failures pass through
	// unchanged so the caller can tell "the chain says no" from "try again".
	receipt, err := VerifyTransaction(ctx, pool, transfer.TxHash)
	if err != nil {
		return Transfer{}, err
	}
	if err := verifyCanonicalBlock(ctx, pool, receipt); err != nil {
		return Transfer{}, err
	}
	err = verifyReceiptTransfer(transfer, receipt)
	if err == nil {
		transfer.Decimals = decimals
		return transfer, nil
	}
	// Reuse reconcile's content match only to explain the failure. A row with no
	// usable stored block hash is a data problem, not a reorg, so it stays a
	// plain mismatch.
	if errors.Is(err, ErrTransferMismatch) && validHash(stored.BlockHash) {
		if _, relocated := findReincludedTransfer(receipt, observation, decimals); relocated == nil {
			return Transfer{}, ErrTransferReincluded
		}
	}
	return Transfer{}, err
}
