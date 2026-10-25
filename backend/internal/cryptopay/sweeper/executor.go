package sweeper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/ethereum/go-ethereum/common"
)

var ChainIDs = map[string]int64{
	"ethereum": 1,
	"bsc":      56, "polygon": 137, "plasma": 9745,
	"arbitrum": 42161, "base": 8453, "xlayer": 196,
}

func isZeroAddress(address string) bool {
	trimmed := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(address)), "0x")
	return trimmed != "" && strings.Trim(trimmed, "0") == ""
}

type TokenSpec struct {
	Contract string
	Decimals int
}

const selBalanceOf = "0x70a08231"
const transferGasLimit = 65000

// Executor owns the database lock and the durable transaction state machine.
// Signed bytes are committed before broadcast. Unknown outcomes never cause
// a replacement signature or a fresh gas payment.
type Executor struct {
	db       *sql.DB
	pools    map[string]*rpc.Pool
	wallet   TransactionWallet
	config   Config
	tokens   map[string]TokenSpec
	reader   BalanceReader
	hotIndex uint32 // constructor compatibility; the local wallet supplies its gas index
	store    jobDB  // Run's exclusive autocommit connection
	// Only an explicitly authorized RunAndWait opts into bounded receipt
	// polling. The nonblocking Run contract remains available to observers.
	confirmationDeadline time.Time
	confirmationInterval time.Duration
}

func NewExecutor(db *sql.DB, pools map[string]*rpc.Pool, wallet TransactionWallet,
	config Config, tokens map[string]TokenSpec, reader BalanceReader, hotIndex uint32) *Executor {
	return &Executor{db: db, pools: pools, wallet: wallet, config: config,
		tokens: tokens, reader: reader, hotIndex: hotIndex}
}

type Summary struct {
	DryRun   bool         `json:"dry_run"`
	Planned  int          `json:"planned"`
	Swept    int          `json:"swept"`
	Failed   int          `json:"failed"`
	Pending  int          `json:"pending"`
	GasTopUp int          `json:"gas_topups"`
	Entries  []SweepEntry `json:"entries"`
}

type SweepEntry struct {
	JobID       int64  `json:"job_id,omitempty"`
	Chain       string `json:"chain"`
	Token       string `json:"token,omitempty"`
	Address     string `json:"address"`
	Amount      string `json:"amount"`
	GasTxHash   string `json:"gas_tx_hash"`
	SweepTxHash string `json:"sweep_tx_hash"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
}

func (s *Summary) add(out SweepEntry) {
	s.Entries = append(s.Entries, out)
	switch out.Status {
	case "swept":
		s.Swept++
	case "failed", "manual_review":
		s.Failed++
	case "pending":
		s.Pending++
	}
	if out.GasTxHash != "" {
		s.GasTopUp++
	}
}

// Run reconciles unfinished jobs before planning, even below threshold or
// when the balance reader excludes pending addresses. One global lock also
// serializes gas-wallet nonces across instances.
func (e *Executor) Run(ctx context.Context, dryRun bool) (summary *Summary, err error) {
	if !dryRun {
		if err := mode.CheckWritable(); err != nil {
			return nil, err
		}
	}
	summary = &Summary{DryRun: dryRun}
	if !dryRun {
		conn, release, lockErr := e.lock(ctx)
		if lockErr != nil {
			return summary, lockErr
		}
		defer func() { err = joinErrors(err, release()) }()
		run := *e // concurrent runs must not replace each other's connection
		run.store = conn
		e = &run
	}
	// A stale balance reader must not re-plan the same address/token just
	// reconciled. An unresolved nonce also blocks new work for other tokens
	// at that address, while all previously signed jobs are still observed.
	seen := make(map[string]bool)
	blocked := make(map[string]bool)
	var runErrors error
	if !dryRun {
		jobs, loadErr := e.loadUnfinished(ctx)
		if loadErr != nil {
			return summary, loadErr
		}
		for _, job := range jobs {
			key := addressKey(job.Chain, job.Address)
			seen[key+"|"+strings.ToLower(job.Token)] = true
			out, resumeErr := e.resume(ctx, job)
			if out.Status != "swept" {
				blocked[key] = true
			}
			summary.add(out)
			if resumeErr != nil {
				runErrors = joinErrors(runErrors, resumeErr)
				var storage *persistenceError
				if errors.As(resumeErr, &storage) || ctx.Err() != nil {
					return summary, runErrors
				}
			}
		}
	}
	plan, err := NewPlanner(e.db, e.reader, e.config, e.pools).BuildPlan(ctx)
	if err != nil {
		return summary, joinErrors(runErrors, err)
	}
	summary.Planned = len(plan.Entries)
	for _, entry := range plan.Entries {
		key := addressKey(entry.Chain, entry.Address)
		pair := key + "|" + strings.ToLower(entry.Token)
		if seen[pair] || blocked[key] {
			continue
		}
		seen[pair] = true
		out, sweepErr := e.sweepOne(ctx, entry, dryRun)
		if !dryRun && out.Status != "swept" {
			blocked[key] = true
		}
		summary.add(out)
		if sweepErr != nil {
			runErrors = joinErrors(runErrors, sweepErr)
			var storage *persistenceError
			if errors.As(sweepErr, &storage) || ctx.Err() != nil {
				return summary, runErrors
			}
		}
	}
	return summary, runErrors
}

func addressKey(chain, address string) string { return chain + "|" + strings.ToLower(address) }

func (e *Executor) validateEntry(entry PlanEntry) (TokenSpec, *rpc.Pool, error) {
	spec, ok := e.tokens[entry.Chain+"|"+strings.ToLower(entry.Token)]
	if !ok || !common.IsHexAddress(spec.Contract) || isZeroAddress(spec.Contract) ||
		!strings.EqualFold(spec.Contract, entry.Token) || spec.Decimals != entry.Decimals {
		return spec, nil, fmt.Errorf("no matching token/decimals configured for chain/contract")
	}
	if !common.IsHexAddress(entry.Address) || isZeroAddress(entry.Address) ||
		entry.RawAmount == nil || entry.RawAmount.Sign() <= 0 || entry.RawAmount.BitLen() > 256 ||
		entry.Decimals < 0 || entry.Decimals > 18 || ChainIDs[entry.Chain] <= 0 {
		return spec, nil, fmt.Errorf("invalid sweep plan")
	}
	pool := e.pools[entry.Chain]
	if pool == nil {
		return spec, nil, fmt.Errorf("no rpc pool for chain")
	}
	return spec, pool, nil
}

func (e *Executor) sweepOne(ctx context.Context, entry PlanEntry, dryRun bool) (SweepEntry, error) {
	out := SweepEntry{Chain: entry.Chain, Token: entry.Token, Address: entry.Address, Status: "failed"}
	if entry.RawAmount != nil {
		entry.RawAmount = new(big.Int).Set(entry.RawAmount)
		out.Amount = entry.RawAmount.String()
	}
	_, _, err := e.validateEntry(entry)
	if err != nil {
		out.Error = rpc.SafeError(err)
		return out, err
	}
	if dryRun {
		out.Status = "planned"
		return out, nil
	}
	if !common.IsHexAddress(e.config.HotWalletEVM) || isZeroAddress(e.config.HotWalletEVM) ||
		strings.EqualFold(e.config.HotWalletEVM, entry.Address) {
		err = fmt.Errorf("sweep refused: invalid receiving address")
		out.Error = rpc.SafeError(err)
		return out, err
	}
	if e.config.Confirmations[entry.Chain] == 0 {
		err = fmt.Errorf("sweep refused: confirmation depth is not configured for %s", entry.Chain)
		out.Error = rpc.SafeError(err)
		return out, err
	}
	job, err := e.createJob(ctx, entry)
	if err != nil {
		out.Error = rpc.SafeError(err)
		return out, err
	}
	return e.resume(ctx, job)
}

func (e *Executor) resume(ctx context.Context, job *sweepJob) (SweepEntry, error) {
	out := job.outcome()
	if job.Version == 0 || job.Status == "manual_review" {
		out.Status = "manual_review"
		if out.Error == "" {
			out.Error = "legacy or ambiguous transaction; reconcile hashes manually before releasing address"
		}
		return out, nil
	}
	if (job.SweepHash != "" && job.SweepRaw == "") ||
		(job.GasHash != "" && job.GasRaw == "") ||
		(job.Status == "sweep_pending" && job.SweepRaw == "") ||
		(job.Status == "gas_pending" && job.GasRaw == "") {
		job.Status = "manual_review"
		return e.jobError(ctx, job, fmt.Errorf("reserved transaction bytes are missing; automatic signing refused"))
	}
	// Existing hashes are observed using persisted metadata, even when the
	// current token/chain/destination configuration no longer permits new work.
	pool := e.pools[job.Chain]
	if pool == nil {
		return e.jobError(ctx, job, fmt.Errorf("unfinished job %d needs an RPC pool for %s", job.ID, job.Chain))
	}
	pool, err := pool.ForChain(ctx, uint64(ChainIDs[job.Chain]))
	if err != nil {
		return e.jobError(ctx, job, err)
	}
	required := e.config.Confirmations[job.Chain]
	if required == 0 {
		var observeErr error
		if job.SweepHash != "" {
			_, observeErr = checkReceipt(ctx, pool, job.SweepHash, 0)
		} else if job.GasHash != "" {
			_, observeErr = checkReceipt(ctx, pool, job.GasHash, 0)
		}
		return e.jobError(ctx, job, joinErrors(fmt.Errorf("confirmation depth is not configured for %s", job.Chain), observeErr))
	}
	if required < job.Confirmations {
		required = job.Confirmations
	} else if required > job.Confirmations {
		job.Confirmations = required
		if err := e.saveJob(ctx, job); err != nil {
			return pendingError(job, err)
		}
	}
	if job.SweepRaw != "" {
		return e.finishSweep(ctx, pool, job, required)
	}
	if job.GasRaw != "" && !job.GasConfirmed {
		confirmed, err := e.settle(ctx, pool, job, true, required)
		if err != nil {
			return pendingError(job, err)
		}
		if !confirmed {
			return job.outcome(), nil
		}
	}
	if _, _, err := e.validateEntry(job.PlanEntry); err != nil {
		return e.jobError(ctx, job, err)
	}
	// Extra uncredited balance never changes the immutable amount.
	balance, err := e.tokenBalance(ctx, pool, job.Token, job.Address)
	if err != nil {
		return e.jobError(ctx, job, err)
	}
	if balance.Cmp(job.RawAmount) < 0 {
		return e.jobError(ctx, job, fmt.Errorf("live balance %s is below planned amount %s", balance, job.RawAmount))
	}
	busy, err := e.addressBusy(ctx, job)
	if err != nil {
		return pendingError(job, &persistenceError{err})
	}
	if busy {
		return e.jobError(ctx, job, fmt.Errorf("address has another unresolved transaction"))
	}
	price, err := pool.GasPrice(ctx)
	if err != nil {
		return e.jobError(ctx, job, err)
	}
	// GasTopUp is the operator's native-coin budget, not a minimum that an
	// untrusted RPC quote may increase. Bound fees even if the child already
	// has native coin, so a forged gas quote cannot burn that balance either.
	cap := configuredGasBudget(e.config, job.Chain)
	if cap.Sign() <= 0 || transferGasBudget(price).Cmp(cap) > 0 {
		return e.jobError(ctx, job, fmt.Errorf("RPC gas quote exceeds configured native funding budget"))
	}
	native, err := pool.Balance(ctx, job.Address)
	if err != nil {
		return e.jobError(ctx, job, err)
	}
	if native.Cmp(transferGasBudget(price)) < 0 {
		if job.GasConfirmed {
			return e.jobError(ctx, job, fmt.Errorf("confirmed gas top-up is insufficient; automatic repeat funding refused"))
		}
		if err := e.prepareGas(ctx, pool, job, e.gasTopUpAmount(job.Chain, transferGasBudget(price), native), price); err != nil {
			if errors.Is(err, errGasPayerPending) {
				// Another job still owns the gas nonce. This is a deferral,
				// not a failed transfer, and must not hide partial progress.
				job.Error = "awaiting gas payer transaction confirmation"
				if saveErr := e.saveJob(ctx, job); saveErr != nil {
					return pendingError(job, saveErr)
				}
				return job.outcome(), nil
			}
			return e.jobError(ctx, job, err)
		}
		confirmed, err := e.settle(ctx, pool, job, true, required)
		if err != nil {
			return pendingError(job, err)
		}
		if !confirmed {
			return job.outcome(), nil
		}
	}
	if err := e.prepareSweep(ctx, pool, job, price); err != nil {
		return e.jobError(ctx, job, err)
	}
	return e.finishSweep(ctx, pool, job, required)
}

func (e *Executor) finishSweep(ctx context.Context, pool *rpc.Pool, job *sweepJob, required uint64) (SweepEntry, error) {
	_, err := e.settle(ctx, pool, job, false, required)
	if err != nil {
		return pendingError(job, err)
	}
	return job.outcome(), nil
}

func (e *Executor) jobError(ctx context.Context, job *sweepJob, cause error) (SweepEntry, error) {
	var storage *persistenceError
	if errors.As(cause, &storage) {
		return pendingError(job, cause)
	}
	job.Error = rpc.SafeError(cause)
	err := e.saveJob(ctx, job)
	return pendingError(job, joinErrors(cause, err))
}

func pendingError(job *sweepJob, err error) (SweepEntry, error) {
	out := job.outcome()
	if out.Status == "swept" { // final persistence may have failed
		out.Status = "pending"
	}
	out.Error = rpc.SafeError(err)
	return out, err
}

func (e *Executor) tokenBalance(ctx context.Context, pool *rpc.Pool, contract, address string) (*big.Int, error) {
	data := selBalanceOf + strings.Repeat("0", 24) + strings.TrimPrefix(strings.ToLower(address), "0x")
	result, err := pool.Call(ctx, "eth_call", []any{map[string]string{"to": contract, "data": data}, "latest"})
	if err != nil {
		return nil, err
	}
	var hexStr string
	if err := json.Unmarshal(result, &hexStr); err != nil {
		return nil, err
	}
	value, ok := new(big.Int).SetString(strings.TrimPrefix(hexStr, "0x"), 16)
	if !ok || value.Sign() < 0 || value.BitLen() > 256 {
		return nil, fmt.Errorf("invalid balanceOf result")
	}
	return value, nil
}

func (e *Executor) gasTopUpAmount(chain string, gasBudget, native *big.Int) *big.Int {
	return gasTopUpAmount(e.config, chain, gasBudget, native)
}

func transferGasBudget(gasPrice *big.Int) *big.Int {
	budget := new(big.Int).Mul(gasPrice, big.NewInt(transferGasLimit))
	return budget.Div(budget.Mul(budget, big.NewInt(3)), big.NewInt(2))
}

func gasTopUpAmount(config Config, chain string, gasBudget, native *big.Int) *big.Int {
	need := new(big.Int).Sub(gasBudget, native)
	configured := configuredGasBudget(config, chain)
	if need.Sign() <= 0 || configured.Sign() <= 0 || need.Cmp(configured) > 0 {
		return new(big.Int) // no automatic funding above the configured budget
	}
	return configured
}

func configuredGasBudget(config Config, chain string) *big.Int {
	amount := config.GasTopUp[chain]
	if amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return new(big.Int)
	}
	return decimalToRawUnits(amount, 18)
}

func decimalToRawUnits(value float64, decimals int) *big.Int {
	scaled := new(big.Float).SetFloat64(value)
	scaled.Mul(scaled, new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)))
	raw, _ := scaled.Int(nil)
	if raw == nil {
		return big.NewInt(0)
	}
	return raw
}

func amountString(raw *big.Int, decimals int) string {
	if raw == nil {
		return "0"
	}
	if decimals <= 0 {
		return raw.String()
	}
	digits := raw.String()
	for len(digits) <= decimals {
		digits = "0" + digits
	}
	whole := digits[:len(digits)-decimals]
	frac := strings.TrimRight(digits[len(digits)-decimals:], "0")
	if frac == "" {
		return whole
	}
	return whole + "." + frac
}
