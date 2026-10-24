package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	"github.com/shopspring/decimal"
)

// HasRecordedTransfer avoids repeating receipt RPCs for an identical durable
// observation. Pending credit and credited reorg checks still run independently;
// any change to the block, amount, recipient or token requires fresh validation.
func (l *Ledger) HasRecordedTransfer(ctx context.Context, transfer scanner.Transfer, decimals int) (bool, error) {
	if transfer.Amount == nil || transfer.Amount.Sign() <= 0 || decimals < 0 || decimals > 255 || transfer.BlockHash == "" {
		return false, nil
	}
	var from, to, token, amount, blockHash, status string
	var block uint64
	err := l.db.QueryRowContext(ctx, `SELECT from_address, to_address, token, amount, block_number, block_hash, status
		FROM deposit_transactions WHERE chain = $1 AND tx_hash = $2 AND log_index = $3`,
		transfer.Chain, strings.ToLower(transfer.TxHash), transfer.LogIndex).
		Scan(&from, &to, &token, &amount, &block, &blockHash, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read recorded transfer identity: %w", err)
	}
	value, err := decimal.NewFromString(amount)
	if err != nil {
		return false, fmt.Errorf("invalid recorded transfer amount")
	}
	known := status == "pending" || status == "credited" || status == "ignored" || status == "reverted"
	return known && block == transfer.BlockNumber && strings.EqualFold(blockHash, transfer.BlockHash) &&
		strings.EqualFold(from, transfer.From) && strings.EqualFold(to, transfer.To) && strings.EqualFold(token, transfer.Token) &&
		value.Equal(decimal.NewFromBigInt(transfer.Amount, -int32(decimals))), nil
}
