package sweeper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// A swept record is a claim about the chain, not the chain itself. A block that
// was canonical when the receipt was read can still be replaced, and a sweep
// that is rolled back leaves the funds on the source address while the record
// says "swept" forever. The audit re-reads the receipt and its canonical block
// for the recent window and turns a proven rollback into "failed", which both
// stops the false claim and frees the address for a new plan.
const (
	// sweptAuditWindow is how far back a rollback is still worth looking for.
	// The sweep ticker is hourly, so the window covers one missed tick.
	sweptAuditWindow = 2 * time.Hour
	sweptAuditLimit  = 50
	// sweptRollbackPrefix marks a failed record whose swept transaction is no
	// longer on the canonical chain. reservedPredicate excludes this prefix so
	// the address is free for a new job while the dead hash stays visible.
	sweptRollbackPrefix = "swept rollback released: "
	// transferEventTopic is ERC20 Transfer(address,address,uint256).
	transferEventTopic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
)

const sweptAuditQuery = `SELECT id, chain, from_address, to_address, token, raw_amount,
		token_decimals, sweep_tx_hash
	FROM sweep_records
	WHERE status = 'swept' AND job_version = 1 AND sweep_tx_hash <> ''
	  AND updated_at > NOW() - ($1 * INTERVAL '1 second')
	ORDER BY updated_at DESC LIMIT $2`

type sweptObservation int

const (
	sweptStillConfirmed sweptObservation = iota
	sweptMissingReceipt
	sweptRolledBack
)

// sweptLog is one receipt log of a swept token transfer.
type sweptLog struct {
	Address string   `json:"address"`
	Topics  []string `json:"topics"`
	Data    string   `json:"data"`
	Removed bool     `json:"removed"`
}

// AuditSweptRecent re-checks the receipts of recently swept jobs against the
// canonical chain and marks a proven rollback as failed. It signs and
// broadcasts nothing, and an unreadable chain leaves the record untouched.
func (e *Executor) AuditSweptRecent(ctx context.Context) (audited, rolledBack int, err error) {
	if err := mode.CheckWritable(); err != nil {
		return 0, 0, err
	}
	if e.db == nil {
		return 0, 0, fmt.Errorf("sweep database is not configured")
	}
	rows, err := e.queries().QueryContext(ctx, sweptAuditQuery, int64(sweptAuditWindow/time.Second), sweptAuditLimit)
	if err != nil {
		return 0, 0, err
	}
	jobs, err := readSweptJobs(rows)
	if err != nil {
		return 0, 0, err
	}
	var errs []error
	// One chain-id check per chain per audit, not per row: ForChain probes every
	// endpoint and the window can hold many rows from the same chain.
	bound := make(map[string]*rpc.Pool)
	broken := make(map[string]bool)
	for _, job := range jobs {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		if broken[job.Chain] {
			continue
		}
		pool, cached := bound[job.Chain]
		if !cached {
			configured := e.pools[job.Chain]
			if configured == nil {
				// A removed chain cannot corroborate anything. Keep the record
				// and say why the audit skipped it instead of guessing.
				slog.Warn("[cryptopay] swept audit skipped: no RPC pool", "job_id", job.ID, "chain", job.Chain)
				continue
			}
			verified, chainErr := configured.ForChain(ctx, uint64(ChainIDs[job.Chain]))
			if chainErr != nil {
				broken[job.Chain] = true
				errs = append(errs, fmt.Errorf("swept audit %d: %w", job.ID, chainErr))
				continue
			}
			bound[job.Chain] = verified
			pool = verified
		}
		audited++
		observation, reason, checkErr := e.checkSwept(ctx, pool, job)
		if checkErr != nil {
			slog.Warn("[cryptopay] swept audit inconclusive", "job_id", job.ID, "chain", job.Chain,
				"cause", rpc.SafeError(checkErr))
			errs = append(errs, fmt.Errorf("swept audit %d: %w", job.ID, checkErr))
			continue
		}
		switch observation {
		case sweptStillConfirmed:
			continue
		case sweptMissingReceipt:
			// A rotation to a node that lags is indistinguishable from a reorg.
			// Only a canonical mismatch or a reverted receipt is proof.
			slog.Warn("[cryptopay] swept transaction not visible to this node; keeping record",
				"job_id", job.ID, "chain", job.Chain)
			continue
		}
		changed, markErr := e.markSweptRolledBack(ctx, job.ID, reason)
		if markErr != nil {
			errs = append(errs, markErr)
			continue
		}
		if changed {
			rolledBack++
			slog.Error("[cryptopay] swept transaction rolled back; address released for a new plan",
				"job_id", job.ID, "chain", job.Chain, "reason", reason)
		}
	}
	return audited, rolledBack, errors.Join(errs...)
}

// readSweptJobs drains the audit result set before any RPC work, so a
// single-connection pool is never held by an open cursor.
func readSweptJobs(rows *sql.Rows) ([]*sweepJob, error) {
	defer func() { _ = rows.Close() }()
	var jobs []*sweepJob
	for rows.Next() {
		job := &sweepJob{}
		var raw string
		if err := rows.Scan(&job.ID, &job.Chain, &job.Address, &job.Destination, &job.Token,
			&raw, &job.Decimals, &job.SweepHash); err != nil {
			return nil, err
		}
		amount, ok := new(big.Int).SetString(raw, 10)
		if !ok || amount.Sign() <= 0 {
			return nil, fmt.Errorf("invalid immutable amount for sweep job %d", job.ID)
		}
		job.RawAmount = amount
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// checkSwept states whether the recorded sweep transfer is still on the
// canonical chain. Only a receipt for the recorded hash, at a canonical block,
// carrying the recorded recipient and amount counts as confirmed.
func (e *Executor) checkSwept(ctx context.Context, pool *rpc.Pool, job *sweepJob) (sweptObservation, string, error) {
	result, err := pool.Call(ctx, "eth_getTransactionReceipt", []any{job.SweepHash})
	if err != nil {
		return sweptStillConfirmed, "", err
	}
	if len(result) == 0 || string(result) == "null" {
		return sweptMissingReceipt, "", nil
	}
	var receipt struct {
		Hash        string     `json:"transactionHash"`
		Status      string     `json:"status"`
		BlockNumber string     `json:"blockNumber"`
		BlockHash   string     `json:"blockHash"`
		Logs        []sweptLog `json:"logs"`
	}
	if err := json.Unmarshal(result, &receipt); err != nil {
		return sweptStillConfirmed, "", err
	}
	if !strings.EqualFold(receipt.Hash, job.SweepHash) || len(receipt.BlockHash) != 66 ||
		(receipt.Status != "0x1" && receipt.Status != "0x0") {
		return sweptStillConfirmed, "", fmt.Errorf("incomplete or mismatched receipt")
	}
	if _, err := parseQuantity(receipt.BlockNumber); err != nil {
		return sweptStillConfirmed, "", err
	}
	if receipt.Status == "0x0" {
		return sweptRolledBack, "sweep transaction reverted on chain", nil
	}
	canonical, err := pool.Call(ctx, "eth_getBlockByNumber", []any{receipt.BlockNumber, false})
	if err != nil {
		return sweptStillConfirmed, "", err
	}
	var header struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(canonical, &header); err != nil {
		return sweptStillConfirmed, "", err
	}
	if !strings.EqualFold(header.Hash, receipt.BlockHash) {
		return sweptRolledBack, "sweep block is no longer canonical", nil
	}
	if !receiptCarriesSweep(receipt.Logs, job) {
		return sweptRolledBack, "recorded transfer is missing from the canonical receipt", nil
	}
	return sweptStillConfirmed, "", nil
}

// receiptCarriesSweep finds the exact token movement the record claims: the
// configured contract transferring the immutable amount to the hot wallet.
func receiptCarriesSweep(logs []sweptLog, job *sweepJob) bool {
	wantFrom, wantTo := padAddressTopic(job.Address), padAddressTopic(job.Destination)
	for _, log := range logs {
		if log.Removed || len(log.Topics) != 3 || !strings.EqualFold(log.Address, job.Token) ||
			!strings.EqualFold(log.Topics[0], transferEventTopic) ||
			!strings.EqualFold(log.Topics[1], wantFrom) || !strings.EqualFold(log.Topics[2], wantTo) {
			continue
		}
		raw := strings.TrimPrefix(log.Data, "0x")
		amount, ok := new(big.Int).SetString(raw, 16)
		if ok && amount.Cmp(job.RawAmount) == 0 {
			return true
		}
	}
	return false
}

// padAddressTopic left-pads a 20-byte address into a 32-byte log topic.
func padAddressTopic(address string) string {
	trimmed := strings.TrimPrefix(strings.ToLower(address), "0x")
	if len(trimmed) > 64 {
		trimmed = trimmed[len(trimmed)-64:]
	}
	return "0x" + strings.Repeat("0", 64-len(trimmed)) + trimmed
}

func parseQuantity(quantity string) (uint64, error) {
	return strconv.ParseUint(strings.TrimPrefix(quantity, "0x"), 16, 64)
}

func (e *Executor) markSweptRolledBack(ctx context.Context, jobID int64, reason string) (bool, error) {
	result, err := e.queries().ExecContext(ctx, `UPDATE sweep_records
		SET status = 'failed', error = $2, revision = revision + 1, updated_at = NOW()
		WHERE id = $1 AND status = 'swept' AND job_version = 1`,
		jobID, sweptRollbackPrefix+reason)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}
