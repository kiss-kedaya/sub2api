package scanner

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

const missingReceiptPrefix = "reorg_missing_receipt:"
const missingReceiptThreshold = 3

type depositObservation struct {
	chain, txHash, from, to, token, amount, blockHash string
	logIndex                                          int
	blockNumber                                       uint64
	reason, status                                    string
}

// Close the result set before RPC/UPDATE work, including with a one-connection
// pool. Rotation uses the persisted check clock and survives restarts.
func readObservations(rows *sql.Rows, status string) ([]depositObservation, error) {
	var out []depositObservation
	for rows.Next() {
		p := depositObservation{status: status}
		if err := rows.Scan(&p.chain, &p.txHash, &p.logIndex, &p.from, &p.to, &p.token,
			&p.amount, &p.blockNumber, &p.blockHash, &p.reason); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return out, nil
}

// recheckCredited rotates through the risk window instead of selecting the
// latest 200 forever. Suspected missing receipts stay eligible past the window
// until resolved. The window starts at order completion, since pending can
// last longer than an hour. Legacy rows fall back to observation creation;
// last_attempt_at only rotates the queue and never renews the window.
// These updates never change balances or fulfillment orders.
func (w *Worker) recheckCredited(ctx context.Context) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	rows, err := w.db.QueryContext(ctx, `
		SELECT d.chain, d.tx_hash, d.log_index, d.from_address, d.to_address, d.token,
		       d.amount::text, d.block_number, d.block_hash, d.reason
		  FROM deposit_transactions d
		  LEFT JOIN payment_orders o ON o.id = d.order_id
		 WHERE d.status = 'credited'
		   AND (COALESCE(o.completed_at, d.created_at) > NOW() - INTERVAL '1 hour'
		        OR d.reason LIKE 'reorg_missing_receipt:%')
		 ORDER BY COALESCE(d.last_attempt_at, d.created_at), d.id
		 LIMIT 200`)
	if err != nil {
		return err
	}
	observations, err := readObservations(rows, "credited")
	if err != nil {
		return err
	}
	return w.reconcile(ctx, observations)
}

// retryPending uses the same receipt, canonical-log and finality checks as
// live scanning. Each attempted row rotates even if its RPC remains broken.
func (w *Worker) retryPending(ctx context.Context) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	rows, err := w.db.QueryContext(ctx, `
		SELECT chain, tx_hash, log_index, from_address, to_address, token,
		       amount::text, block_number, block_hash, reason
		  FROM deposit_transactions
		 WHERE status = 'pending'
		   AND (last_attempt_at IS NULL OR last_attempt_at < NOW() - INTERVAL '1 minute')
		 ORDER BY COALESCE(last_attempt_at, created_at), id
		 LIMIT 50`)
	if err != nil {
		return err
	}
	observations, err := readObservations(rows, "pending")
	if err != nil {
		return err
	}
	return w.reconcile(ctx, observations)
}

func (w *Worker) chainConfig(chain string) (ChainConfig, bool) {
	for _, c := range w.chains {
		if c.Chain == chain {
			return c, true
		}
	}
	return ChainConfig{}, false
}

func watchedDecimals(chain ChainConfig, token string) (int, bool) {
	watched := false
	for _, contract := range chain.Tokens {
		if strings.EqualFold(contract, token) {
			watched = true
			break
		}
	}
	if !watched {
		return 0, false
	}
	for contract, decimals := range chain.Decimals {
		if strings.EqualFold(contract, token) && decimals >= 0 && decimals <= 255 {
			return decimals, true
		}
	}
	return 0, false
}

func (p depositObservation) transfer(decimals int) (Transfer, error) {
	raw, err := decimalToRawUnits(p.amount, decimals)
	if err != nil {
		return Transfer{}, err
	}
	return Transfer{Chain: p.chain, TxHash: p.txHash, LogIndex: p.logIndex,
		From: p.from, To: p.to, Token: p.token, Amount: raw,
		BlockNumber: p.blockNumber, BlockHash: p.blockHash}, nil
}

func (w *Worker) reconcile(ctx context.Context, observations []depositObservation) error {
	var errs []error
	type poolCheck struct {
		pool *rpc.Pool
		err  error
	}
	verified := make(map[string]poolCheck)
	for _, p := range observations {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		pool := w.pools[p.chain]
		chain, configured := w.chainConfig(p.chain)
		decimals, watched := watchedDecimals(chain, p.token)
		if pool == nil || !configured || !watched {
			// Disabled chains/tokens can otherwise occupy the first page
			// forever and starve every configured chain behind them.
			if _, err := w.updateObservation(ctx, p, p.status, p.reason); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		reason, status := p.reason, p.status
		// An RPC failure breaks a sequence of confirmed absences; it is never
		// evidence of a reorg. Successful observations clear the suspicion too.
		if strings.HasPrefix(reason, missingReceiptPrefix) {
			reason = ""
		}
		check, checked := verified[p.chain]
		if !checked {
			check.pool, check.err = verifiedChainPool(ctx, pool, p.chain)
			verified[p.chain] = check
		}
		pool, verifyErr := check.pool, check.err
		if verifyErr == nil {
			receipt, receiptErr := VerifyTransaction(ctx, pool, p.txHash)
			switch {
			case errors.Is(receiptErr, ErrReceiptNotFound):
				if p.status == "pending" {
					// A pending transfer is not evidence of a reorg. RPC nodes may
					// lag receipt indexing after exposing a log; keep it retryable.
					status, reason = "pending", ""
					break
				}
				count := 0
				if strings.HasPrefix(p.reason, missingReceiptPrefix) {
					count, _ = strconv.Atoi(strings.TrimPrefix(p.reason, missingReceiptPrefix))
				}
				count++
				if count >= missingReceiptThreshold {
					status, reason = "reverted", "reorg_receipt_missing"
				} else {
					reason = missingReceiptPrefix + strconv.Itoa(count)
				}
			case receiptErr != nil && !errors.Is(receiptErr, ErrTransactionReverted):
				verifyErr = receiptErr
			default:
				verifyErr = verifyCanonicalBlock(ctx, pool, receipt)
				if errors.Is(verifyErr, ErrNonCanonicalBlock) {
					status, reason, verifyErr = "reverted", "reorg_noncanonical_block", nil
				} else if verifyErr == nil && errors.Is(receiptErr, ErrTransactionReverted) {
					status, reason = "reverted", "reorg_receipt_reverted"
				} else if verifyErr == nil {
					transfer, err := p.transfer(decimals)
					verifyErr = err
					if verifyErr == nil {
						verifyErr = verifyReceiptTransfer(transfer, receipt)
					}
					if errors.Is(verifyErr, ErrTransferMismatch) {
						status, reason, verifyErr = "reverted", "reorg_transfer_changed", nil
					} else if verifyErr == nil && p.status == "pending" {
						final, err := transactionIsFinal(ctx, pool, receipt.BlockNumber, chain.Confirmations)
						verifyErr = err
						if err == nil && final {
							// Persist the attempt before fulfillment. Failure keeps
							// the deposit retryable and must never be hidden.
							changed, err := w.updateObservation(ctx, p, status, reason)
							if err != nil {
								errs = append(errs, err)
								continue
							}
							if !changed {
								continue
							}
							if _, err := w.handler.Process(ctx, transfer, decimals); err != nil {
								errs = append(errs, err)
							}
							continue
						}
					}
				}
			}
		}
		if verifyErr != nil {
			if strings.HasPrefix(p.reason, missingReceiptPrefix) {
				reason = missingReceiptPrefix + "0"
			}
			errs = append(errs, fmt.Errorf("recheck %s/%s/%d: %w", p.chain, p.txHash, p.logIndex, verifyErr))
		}
		changed, err := w.updateObservation(ctx, p, status, reason)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		// Persist first, notify once on the guarded credited -> reverted
		// transition. No refund or debit is attempted here.
		if changed && p.status == "credited" && status == "reverted" && w.onReorg != nil {
			w.onReorg(p.chain, p.txHash)
		}
	}
	return errors.Join(errs...)
}

func (w *Worker) updateObservation(ctx context.Context, p depositObservation, status, reason string) (bool, error) {
	result, err := w.db.ExecContext(ctx, `UPDATE deposit_transactions
		SET status = $1, reason = $2, last_attempt_at = NOW()
		WHERE chain = $3 AND tx_hash = $4 AND log_index = $5
		  AND status = $6 AND reason = $7`,
		status, reason, p.chain, p.txHash, p.logIndex, p.status, p.reason)
	if err != nil {
		return false, fmt.Errorf("persist deposit recheck: %w", err)
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}
