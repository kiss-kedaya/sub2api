package sweeper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"log/slog"
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
const (
	// legacyTransferGasLimit is the limit used by transactions signed before
	// the 2026-11 incident. Native stablecoin transfers can need roughly
	// 70-80k when the recipient's balance slot is first initialized, so new
	// sweeps sign with more headroom while legacy bytes stay verifiable.
	legacyTransferGasLimit = 65000
	transferGasLimit       = 90000
)

// maxConsecutiveStorageFailures bounds how many persisted-state failures a
// single run tolerates before giving up: one bad row must not abort the run,
// but a broken database must not spin through every address.
const maxConsecutiveStorageFailures = 5

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
	// chains is an optional allowlist mirrored from the planner. It is checked
	// again per entry right before signing, so a planner bug can never move
	// funds on a chain the operator did not name.
	chains []string
}

// WithChains restricts this executor to the given chains. Empty keeps the
// global behaviour so existing callers are unaffected.
func (e *Executor) WithChains(chains []string) *Executor {
	scoped := *e
	scoped.chains = normalizeChainScope(chains)
	return &scoped
}

func (e *Executor) allowsChain(chain string) bool {
	if len(e.chains) == 0 {
		return true
	}
	for _, allowed := range e.chains {
		if allowed == chain {
			return true
		}
	}
	return false
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
	// A sweep run is otherwise silent: an operator investigating "why did
	// nothing happen" has no record of what was attempted, what was skipped and
	// why. Log the run envelope and every decision that drops a job.
	startedAt := time.Now()
	scoped := e.chains
	if len(scoped) == 0 {
		scoped = []string{"all"}
	}
	slog.Info("[cryptopay] sweep run starting", "dry_run", dryRun, "chains", scoped)
	defer func() {
		slog.Info("[cryptopay] sweep run finished",
			"dry_run", dryRun, "chains", scoped,
			"planned", summary.Planned, "swept", summary.Swept,
			"failed", summary.Failed, "pending", summary.Pending,
			"gas_topups", summary.GasTopUp,
			"elapsed_ms", time.Since(startedAt).Milliseconds(),
			"error", rpc.SafeError(err))
	}()
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
	storageFailures := 0
	if !dryRun {
		jobs, loadErr := e.loadUnfinished(ctx)
		if loadErr != nil {
			slog.Error("[cryptopay] sweep could not load unfinished jobs", "error", rpc.SafeError(loadErr))
			return summary, loadErr
		}
		if len(jobs) > 0 {
			slog.Info("[cryptopay] resuming unfinished sweep jobs", "count", len(jobs))
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
				if errors.As(resumeErr, &storage) {
					// One job whose state could not be persisted must not stop the
					// other chains; it stays reserved and is observed again next run.
					// Only a run of consecutive storage failures means the database
					// itself is unusable.
					storageFailures++
					if storageFailures >= maxConsecutiveStorageFailures {
						return summary, runErrors
					}
					continue
				}
				storageFailures = 0
				if ctx.Err() != nil {
					return summary, runErrors
				}
			}
		}
	}
	plan, err := NewPlanner(e.db, e.reader, e.config, e.pools).WithChains(e.chains).BuildPlan(ctx)
	if err != nil {
		return summary, joinErrors(runErrors, err)
	}
	summary.Planned = len(plan.Entries)
	for _, entry := range plan.Entries {
		key := addressKey(entry.Chain, entry.Address)
		pair := key + "|" + strings.ToLower(entry.Token)
		if seen[pair] || blocked[key] {
			// A silent skip is indistinguishable from a bug when the operator is
			// asking why an address never got swept. Record which of the two it
			// was and the amount involved.
			slog.Info("[cryptopay] sweep entry skipped",
				"chain", entry.Chain, "address", entry.Address,
				"token", entry.Token, "amount", entry.AmountDecimal,
				"reason", map[bool]string{true: "already handled this run", false: "address blocked by an earlier failure"}[seen[pair]])
			continue
		}
		seen[pair] = true
		out, sweepErr := e.sweepOne(ctx, entry, dryRun)
		slog.Info("[cryptopay] sweep entry result",
			"chain", out.Chain, "address", out.Address, "job_id", out.JobID,
			"amount", out.Amount, "status", out.Status,
			"gas_tx", out.GasTxHash, "sweep_tx", out.SweepTxHash,
			"error", out.Error)
		if !dryRun && out.Status != "swept" {
			blocked[key] = true
		}
		summary.add(out)
		if sweepErr != nil {
			runErrors = joinErrors(runErrors, sweepErr)
			var storage *persistenceError
			if errors.As(sweepErr, &storage) {
				storageFailures++
				if storageFailures >= maxConsecutiveStorageFailures {
					return summary, runErrors
				}
				continue
			}
			storageFailures = 0
			if ctx.Err() != nil {
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
	// Last line of defence: never sign for a chain outside the operator scope.
	if !e.allowsChain(entry.Chain) {
		err := fmt.Errorf("chain %q is outside the requested sweep scope", entry.Chain)
		out.Error = rpc.SafeError(err)
		return out, err
	}
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
		if errors.Is(err, errAddressReserved) {
			// A reserved address is back-pressure, not a failed payment and not
			// a database failure. It used to be reported as a persistence error,
			// so five reserved addresses aborted the run and every address after
			// them silently stayed unswept. Leave the entry pending (no alert:
			// the blocking job is already visible in the sweep records) and let
			// the rest of the plan proceed.
			slog.Warn("[cryptopay] sweep entry skipped: address already reserved",
				"chain", entry.Chain, "address", entry.Address, "token", entry.Token)
			out.Status = "pending"
			return out, nil
		}
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
			_, observeErr = checkReceipt(ctx, pool, job.Chain, job.SweepHash, 0)
		} else if job.GasHash != "" {
			_, observeErr = checkReceipt(ctx, pool, job.Chain, job.GasHash, 0)
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
	if job.SweepRaw == "" && job.SweepHash == "" {
		// Gas-confirmed rows that made no token progress for half an hour are
		// stuck (old finality tag, lost persist); release the reservation so the
		// planner can create a fresh job from the current balance in this run.
		stuckGas := false
		if job.Status == "gas_confirmed" {
			var updated time.Time
			if err := e.queries().QueryRowContext(ctx, `SELECT updated_at FROM sweep_records WHERE id=$1`, job.ID).Scan(&updated); err == nil && !updated.IsZero() && time.Since(updated) > 30*time.Minute {
				stuckGas = true
			}
		}
		reverted := job.Status == "manual_review" && strings.Contains(job.Error, "confirmed transaction reverted")
		if stuckGas || reverted {
			job.Status = "failed"
			job.Error = "stale pending released: stuck job made no token progress"
			if _, err := e.queries().ExecContext(ctx, `UPDATE sweep_records SET status='failed', error=$2, updated_at=NOW() WHERE id=$1`, job.ID, job.Error); err != nil {
				return pendingError(job, err)
			}
			out := job.outcome()
			out.Status = "failed"
			out.Error = job.Error
			return out, nil
		}
	}
	if job.SweepRaw != "" {
		// A signed transfer that no node can see at an already-consumed nonce
		// can never land. Recover it before replaying the same dead bytes, which
		// would otherwise keep the address reserved forever.
		if job.Status == "sweep_pending" {
			released, err := e.releaseStaleTransaction(ctx, pool, job, false, true)
			if err != nil {
				return pendingError(job, err)
			}
			if released {
				return job.outcome(), nil
			}
		}
		return e.finishSweep(ctx, pool, job, required)
	}
	if job.GasRaw != "" && !job.GasConfirmed {
		beforeHash := job.GasHash
		// A top-up signed earlier that can never be mined must not hold the
		// payer for every other address on the chain.
		if err := e.refreshStuckGas(ctx, pool, job); err != nil {
			return pendingError(job, err)
		}
		// refreshStuckGas either re-funds (planned) or signs a replacement at the
		// same nonce. Only a top-up it left untouched may be concluded dead here,
		// so a freshly signed replacement is never released by mistake.
		if job.Status == "gas_pending" && job.GasHash == beforeHash {
			released, err := e.releaseStaleTransaction(ctx, pool, job, true, true)
			if err != nil {
				return pendingError(job, err)
			}
			if released {
				return job.outcome(), nil
			}
		}
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
	if err := e.checkCurrentSweepBalance(ctx, pool, job); err != nil {
		return e.jobError(ctx, job, err)
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
	if cap.Sign() <= 0 {
		// Nothing says this payment is wrong: the operator has not sized the
		// funding budget for this chain, or the quote is temporarily too high.
		// Failing a bounded attempt here eventually parked payable sweeps as
		// `failed`; the job stays retryable instead.
		slog.Warn("[cryptopay] sweep deferred: no native gas budget configured",
			"job_id", job.ID, "chain", job.Chain)
		return e.deferJob(ctx, job, fmt.Errorf("sweep gas budget is not configured for %s", job.Chain))
	}
	if transferGasBudget(price).Cmp(cap) > 0 {
		slog.Warn("[cryptopay] sweep deferred: gas quote above the configured budget",
			"job_id", job.ID, "chain", job.Chain)
		return e.deferJob(ctx, job, errGasQuoteExceedsBudget)
	}
	native, err := pool.Balance(ctx, job.Address)
	if err != nil {
		return e.jobError(ctx, job, err)
	}
	if native.Cmp(transferGasBudget(price)) < 0 {
		// A confirmed top-up that no longer covers the budget is not a reason to
		// stop. The previous top-up was sized against the gas price at that
		// moment; the price moved. Refusing to fund again left the job retrying
		// the same failing check forever — the record never advanced and the
		// chain silently lost this address. Top up the difference like any other
		// shortfall; prepareGas already caps the amount by the operator budget.
		amount := e.gasTopUpAmount(job.Chain, transferGasBudget(price), native)
		if amount.Sign() <= 0 {
			return e.deferJob(ctx, job, errGasBudgetInsufficient)
		}
		if err := e.prepareGas(ctx, pool, job, amount, price); err != nil {
			if errors.Is(err, errGasPayerPending) {
				// Another job still owns the gas nonce. This is a deferral,
				// not a failed transfer, and must not hide partial progress.
				job.Error = "awaiting gas payer transaction confirmation"
				if saveErr := e.saveJob(ctx, job); saveErr != nil {
					return pendingError(job, saveErr)
				}
				return job.outcome(), nil
			}
			if errors.Is(err, errGasBudgetInsufficient) || errors.Is(err, errGasPayerUnderfunded) {
				// Funding is a resource wait, not a payment failure: keep the
				// bounded-attempt counter untouched so a starved run never
				// turns this address into a terminal `failed` record.
				slog.Warn("[cryptopay] sweep deferred: gas funding unavailable",
					"job_id", job.ID, "chain", job.Chain,
					"category", rpc.CategoryOf(err), "error", rpc.SafeError(err))
				return e.deferJob(ctx, job, err)
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
	// Gas confirmation can take time. Include newly credited deposits before
	// signing the token transaction, while preserving a signed token verbatim.
	if err := e.checkCurrentSweepBalance(ctx, pool, job); err != nil {
		return e.jobError(ctx, job, err)
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

// maxSweepAttempts bounds how many times one job may fail and be retried. Every
// saveJob bumps revision, so it doubles as an attempt counter without a schema
// change. Without a bound a job whose failure is permanent — a token the chain
// refuses, an RPC that always rejects the payload — retries on every pass
// forever, and because reservedPredicate keeps it reserved it also blocks that
// address from ever being planned again. Bounding it moves the row to `failed`
// so it leaves the queue and an operator can see it.
const maxSweepAttempts = 25

func (e *Executor) jobError(ctx context.Context, job *sweepJob, cause error) (SweepEntry, error) {
	if errors.Is(cause, errStaleSweepPlan) {
		// The recorded amount/assignment no longer matches the credited ledger.
		// Retrying cannot fix that; release the row so the planner can create a
		// fresh job from the current balance instead of blocking the address.
		job.Status = "failed"
		job.Error = "stale pending released: " + rpc.SafeRecordedError(cause.Error())
		if _, err := e.queries().ExecContext(ctx, `UPDATE sweep_records SET status='failed', error=$2, updated_at=NOW() WHERE id=$1`, job.ID, job.Error); err != nil {
			return pendingError(job, err)
		}
		out := job.outcome()
		out.Status = "failed"
		out.Error = job.Error
		return out, nil
	}
	var storage *persistenceError
	if errors.As(cause, &storage) {
		return pendingError(job, cause)
	}
	// A transport failure or timeout says nothing about the payment. Persisting
	// it would burn one of the bounded attempts (and could eventually mark a
	// perfectly valid transaction failed), so the durable state stays exactly as
	// it was and the next pass retries.
	if rpc.IsTransient(cause) {
		slog.Warn("[cryptopay] sweep job deferred: RPC temporarily unavailable",
			"job_id", job.ID, "chain", job.Chain, "address", job.Address,
			"category", rpc.CategoryOf(cause), "endpoint_host", rpc.HostOf(cause),
			"detail", rpc.DetailOf(cause))
		return pendingError(job, cause)
	}
	// Transport errors may contain endpoint credentials. The same sanitized
	// boundary applies to operator logs and persisted payment records.
	slog.Error("[cryptopay] sweep job failed",
		"job_id", job.ID, "chain", job.Chain, "address", job.Address,
		"token", job.Token, "status", job.Status, "revision", job.Revision,
		"category", rpc.CategoryOf(cause), "endpoint_host", rpc.HostOf(cause),
		"detail", rpc.DetailOf(cause), "error", rpc.SafeError(cause))
	job.Error = rpc.SafeError(cause)
	if job.Revision >= maxSweepAttempts {
		// Terminal: stop retrying and stop reserving the address.
		job.Status = "failed"
		job.Error = rpc.SafeError(fmt.Errorf("%s (gave up after %d attempts)", job.Error, job.Revision))
	}
	err := e.saveJob(ctx, job)
	return pendingError(job, joinErrors(cause, err))
}

// deferJob records a wait-for-resources outcome without consuming a sweep
// attempt. revision doubles as the bounded-attempt counter (see jobError), so a
// job that is merely waiting for gas must not advance it: doing so pushed a
// starved address towards `failed` once per run and finally parked a payment
// that was never wrong. Only the readable reason is updated; the durable
// transaction state is left exactly as it was.
func (e *Executor) deferJob(ctx context.Context, job *sweepJob, cause error) (SweepEntry, error) {
	job.Error = rpc.SafeError(cause)
	if _, err := e.queries().ExecContext(ctx, `UPDATE sweep_records SET error=$2, updated_at=NOW()
		WHERE id=$1 AND job_version=1 AND revision=$3`, job.ID, job.Error, job.Revision); err != nil {
		// The reason is diagnostics; the payment state is untouched either way.
		slog.Warn("[cryptopay] could not record sweep deferral",
			"job_id", job.ID, "error", rpc.SafeError(err))
	}
	out := job.outcome()
	out.Status = "pending"
	out.Error = job.Error
	return out, nil
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

// GasWalletRequirement estimates one unfunded child's buffered token fee
// plus the gas wallet's own native transfer fee. The configured funding cap
// is not a minimum balance requirement.
func GasWalletRequirement(gasPrice *big.Int) *big.Int {
	return new(big.Int).Add(transferGasBudget(gasPrice), new(big.Int).Mul(gasPrice, big.NewInt(21000)))
}

func gasTopUpAmount(config Config, chain string, gasBudget, native *big.Int) *big.Int {
	need := new(big.Int).Sub(gasBudget, native)
	configured := configuredGasBudget(config, chain)
	if need.Sign() <= 0 || configured.Sign() <= 0 || need.Cmp(configured) > 0 {
		return new(big.Int) // no automatic funding above the configured budget
	}
	// GasTopUp is a ceiling, not a fixed payment. The budget already includes
	// the transfer's 50% fee margin; subtract native coin left from prior runs.
	return need
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
