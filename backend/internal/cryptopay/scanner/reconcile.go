package scanner

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

const missingReceiptPrefix = "reorg_missing_receipt:"
const missingReceiptThreshold = 3
const reconcileRPCBudget = 2 * time.Second
const reconcilePersistenceReserve = 500 * time.Millisecond

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
		   AND (last_attempt_at IS NULL OR last_attempt_at < NOW() -
		       CASE WHEN chain = 'bsc' THEN INTERVAL '5 seconds' ELSE INTERVAL '1 minute' END)
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

func findReincludedTransfer(receipt *ReceiptCheck, observation depositObservation, decimals int) (Transfer, error) {
	if receipt == nil || receipt.Status != 1 || len(receipt.Logs) == 0 {
		return Transfer{}, ErrTransferMismatch
	}
	want, err := observation.transfer(decimals)
	if err != nil {
		return Transfer{}, err
	}
	var match *Transfer
	for _, log := range receipt.Logs {
		if log.Removed || len(log.Topics) != 3 || !strings.EqualFold(log.Address, want.Token) ||
			!strings.EqualFold(log.Topics[0], transferTopic) || !validHex(log.Topics[1], 32) || !validHex(log.Topics[2], 32) ||
			!strings.EqualFold(log.Topics[1], padTopic(want.From)) || !strings.EqualFold(log.Topics[2], padTopic(want.To)) || !validHex(log.Data, 32) {
			continue
		}
		amount, ok := new(big.Int).SetString(log.Data[2:], 16)
		if !ok || amount.Cmp(want.Amount) != 0 {
			continue
		}
		index, indexErr := hexQuantity(log.LogIndex)
		block, blockErr := hexQuantity(log.BlockNumber)
		if indexErr != nil || blockErr != nil || block != receipt.BlockNumber ||
			!strings.EqualFold(log.BlockHash, receipt.BlockHash) || !strings.EqualFold(log.TxHash, receipt.TxHash) {
			continue
		}
		candidate := Transfer{Chain: observation.chain, TxHash: strings.ToLower(receipt.TxHash), LogIndex: int(index),
			From: want.From, To: want.To, Token: want.Token, Amount: new(big.Int).Set(want.Amount),
			BlockNumber: receipt.BlockNumber, BlockHash: receipt.BlockHash}
		if match != nil {
			return Transfer{}, ErrTransferMismatch
		}
		match = &candidate
	}
	if match == nil {
		return Transfer{}, ErrTransferMismatch
	}
	return *match, nil
}

func (w *Worker) updateReincludedObservation(ctx context.Context, old depositObservation, transfer Transfer) (bool, error) {
	if old.logIndex == transfer.LogIndex {
		result, err := w.db.ExecContext(ctx, `UPDATE deposit_transactions SET block_number = $1, block_hash = $2, last_attempt_at = NOW() WHERE chain = $3 AND tx_hash = $4 AND log_index = $5 AND status = 'pending' AND reason = $6 AND order_id IS NULL`,
			transfer.BlockNumber, transfer.BlockHash, old.chain, old.txHash, old.logIndex, old.reason)
		if err != nil {
			return false, fmt.Errorf("refresh pending observation at reincluded log: %w", err)
		}
		affected, err := result.RowsAffected()
		return affected == 1, err
	}
	// Refuse to relocate an unlinked pending deposit when a PAID order for
	// its old deterministic identity already exists. Retrying that order via
	// the generic payment endpoint could otherwise mint the amount twice.
	oldTradeNo := depositOrderNo(old.chain, old.txHash, old.logIndex)
	var oldOrderExists bool
	if err := w.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM payment_orders WHERE out_trade_no = $1)`, oldTradeNo).Scan(&oldOrderExists); err != nil {
		return false, fmt.Errorf("check prior order before moving pending log identity: %w", err)
	}
	if oldOrderExists {
		return false, fmt.Errorf("pending reinclusion has a prior deterministic order; manual reconciliation required")
	}
	result, err := w.db.ExecContext(ctx, `UPDATE deposit_transactions SET log_index = $1, block_number = $2, block_hash = $3, last_attempt_at = NOW() WHERE chain = $4 AND tx_hash = $5 AND log_index = $6 AND status = 'pending' AND reason = $7 AND order_id IS NULL`, transfer.LogIndex, transfer.BlockNumber, transfer.BlockHash, old.chain, old.txHash, old.logIndex, old.reason)
	if err != nil {
		return false, fmt.Errorf("move pending observation to reincluded log: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

func depositOrderNo(chain, txHash string, logIndex int) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", chain, strings.ToLower(txHash), logIndex)))
	return "crypto-" + hex.EncodeToString(digest[:20])
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
	// A backlog of slow receipt/reorg checks must not monopolize the shared
	// scanner lock and keep newly mined deposits waiting. Completed attempts
	// rotate durably; the remaining rows get the next bounded pass.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
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
		// A slow receipt must leave time to persist this row's check clock.
		// Using the same expiring context for RPC and UPDATE otherwise leaves
		// the oldest row first forever, starving every pending row behind it.
		deadline, _ := ctx.Deadline() // The pass always has a ten-second cap.
		if time.Until(deadline) <= reconcilePersistenceReserve {
			return errors.Join(append(errs, context.DeadlineExceeded)...)
		}
		reason, status := p.reason, p.status
		reincluded := false
		// An RPC failure breaks a sequence of confirmed absences; it is never
		// evidence of a reorg. Successful observations clear the suspicion too.
		if strings.HasPrefix(reason, missingReceiptPrefix) {
			reason = ""
		}
		check, checked := verified[p.chain]
		if !checked {
			// Chain probing isolates stalled endpoints for up to three seconds.
			// Keep that once-per-chain budget separate from receipt work, or a
			// two-second parent would discard healthy results with a slow peer.
			poolCtx, cancelPool := context.WithDeadline(ctx, deadline.Add(-reconcilePersistenceReserve))
			check.pool, check.err = verifiedChainPool(poolCtx, pool, p.chain)
			cancelPool()
			// A per-row timeout is inconclusive. The next row gets a fresh
			// attempt rather than inheriting an exhausted verification budget.
			if !errors.Is(check.err, context.DeadlineExceeded) && !errors.Is(check.err, context.Canceled) {
				verified[p.chain] = check
			}
		}
		budget := min(reconcileRPCBudget, time.Until(deadline)-reconcilePersistenceReserve)
		rpcCtx, cancelObservation := context.WithTimeout(ctx, max(0, budget))
		pool, verifyErr := check.pool, check.err
		if verifyErr == nil {
			receipt, receiptErr := VerifyTransaction(rpcCtx, pool, p.txHash)
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
				verifyErr = verifyCanonicalBlock(rpcCtx, pool, receipt)
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
						if p.status == "pending" {
							transfer, verifyErr = findReincludedTransfer(receipt, p, decimals)
							if errors.Is(verifyErr, ErrTransferMismatch) {
								status, reason, verifyErr = "reverted", "reorg_transfer_changed", nil
							} else if verifyErr == nil {
								reincluded = true
							}
						} else {
							status, reason, verifyErr = "reverted", "reorg_transfer_changed", nil
						}
					}
					if verifyErr == nil && p.status == "pending" {
						final, err := transactionIsFinalForChain(rpcCtx, pool, chain.Chain, receipt.BlockNumber, chain.Confirmations)
						verifyErr = err
						if err == nil && final {
							cancelObservation()
							var changed bool
							if reincluded {
								changed, err = w.updateReincludedObservation(ctx, p, transfer)
							} else {
								// Persist the attempt before fulfillment. Failure keeps
								// the deposit retryable and must never be hidden.
								changed, err = w.updateObservation(ctx, p, status, reason)
							}
							if err != nil {
								errs = append(errs, err)
								continue
							}
							if !changed {
								continue
							}
							if !reincluded && status != "pending" {
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
		cancelObservation()
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
