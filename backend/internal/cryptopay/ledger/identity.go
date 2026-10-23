package ledger

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

var ErrReorgedDuplicateTransfer = errors.New("same transfer was already credited from another block; manual reconciliation required")

// ReorgedTransferAlreadyCredited detects a prior credited (or linked) log for
// the transaction in a different block. Re-inclusion can change block-global
// log indices and execution output; the block hash still allows multiple
// legitimate Transfer logs from one transaction in the same block.
func ReorgedTransferAlreadyCredited(
	ctx context.Context,
	db *sql.DB,
	chain, txHash string,
	logIndex int,
	blockHash string,
) (bool, error) {
	if db == nil || strings.TrimSpace(blockHash) == "" {
		return false, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT log_index, status, order_id
		FROM deposit_transactions
		WHERE chain = $1 AND tx_hash IN ($2, UPPER($2)) AND log_index <> $3
		  AND COALESCE(block_hash, '') <> ''
		  AND LOWER(block_hash) <> LOWER($4)
		  AND (status IN ('credited', 'pending', 'reverted') OR order_id IS NOT NULL)`,
		chain, txHash, logIndex, blockHash)
	if err != nil {
		return false, fmt.Errorf("check prior credited transfer: %w", err)
	}
	type priorObservation struct {
		logIndex int
		status   string
		orderID  sql.NullInt64
	}
	var previous []priorObservation
	for rows.Next() {
		var prior priorObservation
		if err := rows.Scan(&prior.logIndex, &prior.status, &prior.orderID); err != nil {
			_ = rows.Close()
			return false, fmt.Errorf("read prior credited transfer: %w", err)
		}
		previous = append(previous, prior)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return false, fmt.Errorf("read prior credited transfer: %w", err)
	}
	if err := rows.Close(); err != nil {
		return false, fmt.Errorf("close prior credited transfer rows: %w", err)
	}
	for _, prior := range previous {
		if prior.status == "credited" || prior.orderID.Valid {
			return true, nil
		}
		// A crash can leave the completed payment order durable while its
		// deposit row still has no order_id. Recover the same deterministic
		// identity the creditor would have used for that earlier log index.
		_, outTradeNo := OrderIdentity(chain, txHash, prior.logIndex)
		var exists bool
		if err := db.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM payment_orders WHERE out_trade_no = $1)`, outTradeNo).Scan(&exists); err != nil {
			return false, fmt.Errorf("check prior transfer order: %w", err)
		}
		if exists {
			return true, nil
		}
	}
	return false, nil
}

// OrderIdentity is stable across retries, including when the order was
// created but the deposit's order_id could not yet be persisted.
func OrderIdentity(chain, txHash string, logIndex int) (rechargeCode, outTradeNo string) {
	txHash = strings.ToLower(txHash)
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", chain, txHash, logIndex)))
	return "crypto-" + hex.EncodeToString(digest[:12]), "crypto-" + hex.EncodeToString(digest[:20])
}
