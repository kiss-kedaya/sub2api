package sweeper

import (
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
)

// Job steps name the stage at which a sweep job could not go on. Together with
// the failure category they are a failure's reason code ("balance_read/transport").
//
// The vocabulary is fixed on purpose: it is written to sweep_records.error, to
// logs, to run summaries and to operator alerts, so it must never carry node
// text, URLs or addresses. rpc.sweepStepPrefixes mirrors this list; a test pins
// the two together.
const (
	StepValidate    = "validate"     // the job, token, chain or RPC pool is not what was planned
	StepBalanceRead = "balance_read" // credited or on-chain balance could not be read or is too low
	StepAddressBusy = "address_busy" // another unresolved transaction owns the address
	StepGasPrice    = "gas_price"    // the gas quote could not be read or is above the budget
	StepGasFund     = "gas_fund"     // the gas top-up could not be prepared or funded
	StepNonce       = "nonce"        // the nonce could not be read
	StepSign        = "sign"         // the wallet refused, or the signed bytes did not verify
	StepBroadcast   = "broadcast"    // the transaction could not be sent
	StepReceipt     = "receipt"      // the receipt could not be read or confirmed
	StepPersist     = "persist"      // the job state could not be written
)

// allSteps lists the vocabulary above for the test that pins rpc to it.
var allSteps = []string{StepValidate, StepBalanceRead, StepAddressBusy, StepGasPrice, StepGasFund,
	StepNonce, StepSign, StepBroadcast, StepReceipt, StepPersist}

// Failure categories that are not an RPC category (rpc.Category*). They answer
// "what has to change for this to work", not "what did the node say".
const (
	CategoryWallet    = "wallet"     // the signer is locked, disabled or not authorised, or the process is read-only
	CategoryStorage   = "storage"    // the job state could not be read or written
	CategoryGas       = "gas"        // gas budget, quote or payer funds: waiting for the operator or the market
	CategoryStale     = "stale_plan" // the credited ledger no longer matches the job
	CategoryLock      = "lock"       // another instance holds the sweep lock
	CategoryEndpoints = "endpoints"  // no RPC endpoint could be verified for the chain
	CategoryOther     = "other"      // the sweeper's own validation or business rule
)

// ErrSweepLockHeld means another instance holds the database sweep lock. It is
// run-wide, not a job failure: nothing may be sent until that run is over.
var ErrSweepLockHeld = errors.New("sweep already running in another instance")

// errChainUnverified stands for rpc.Pool.ForChain finding no endpoint it could
// verify for the job's chain. ForChain reports that as a plain error whether
// every node was unreachable or none served this chain, so it was neither
// "transient" nor readable: the record said "Payment operation failed" and the
// attempt counter went up on every pass. It is RPC trouble, not a verdict on the
// payment, and is handled like a transport failure.
var errChainUnverified = errors.New("no RPC endpoint could be verified for the chain")

// stepError tags an error with the job stage that produced it. It only
// decorates: Error and Unwrap pass straight through, so errors.Is/As
// (rpc.Failure, persistenceError, the wallet sentinels) and every message
// comparison behave exactly as before.
type stepError struct {
	step string
	err  error
}

func (e *stepError) Error() string { return e.err.Error() }
func (e *stepError) Unwrap() error { return e.err }

// atStep tags err with step unless a deeper stage already did, because the
// innermost call knows best. A storage failure stays a persist failure wherever
// it surfaces.
func atStep(step string, err error) error {
	if err == nil {
		return nil
	}
	var tagged *stepError
	var storage *persistenceError
	if errors.As(err, &tagged) || errors.As(err, &storage) {
		return err
	}
	return &stepError{step: step, err: err}
}

// stepOf returns the stage that produced err, or "" when it was never tagged.
func stepOf(err error) string {
	var tagged *stepError
	if errors.As(err, &tagged) {
		return tagged.step
	}
	var storage *persistenceError
	if errors.As(err, &storage) {
		return StepPersist
	}
	return ""
}

// hasStorageFailure reports whether err contains a failed job-state write.
func hasStorageFailure(err error) bool {
	var storage *persistenceError
	return errors.As(err, &storage)
}

// jobMark is the part of a job's durable state that says how far it has come.
type jobMark struct {
	gasHash, sweepHash  string
	gasConfirmed, swept bool
}

func markOf(j *sweepJob) jobMark {
	return jobMark{gasHash: j.GasHash, sweepHash: j.SweepHash, gasConfirmed: j.GasConfirmed, swept: j.Status == "swept"}
}

// advancedTo reports a forward move between two marks: a transaction was signed
// (or replaced), the gas top-up confirmed, or the sweep landed. A job that was
// only retried, only waited, or moved to failed has not advanced.
func (m jobMark) advancedTo(n jobMark) bool {
	return (n.gasHash != "" && n.gasHash != m.gasHash) ||
		(n.sweepHash != "" && n.sweepHash != m.sweepHash) ||
		(n.gasConfirmed && !m.gasConfirmed) ||
		(n.swept && !m.swept)
}

// signingHalts are the conditions under which nothing can be signed or sent and
// no retry inside one run can change that: whichever job comes next fails the
// same way until an operator acts.
var signingHalts = []error{
	mode.ErrReadOnly,
	wallet.ErrSignerShareRequired,
	wallet.ErrSignerRateLimited,
	wallet.ErrSecurityLocked,
	wallet.ErrSigningDisabled,
	wallet.ErrSigningAuthorizationRequired,
	wallet.ErrWalletNotConfigured,
}

func isSigningHalt(err error) bool {
	for _, target := range signingHalts {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// IsFatalRunError reports whether err ends the whole run rather than one job:
// the signer is locked, disabled or not authorised, the process is read-only, or
// another instance holds the sweep lock. Continuing would only repeat the same
// refusal for every remaining job (and, for a signer, spend an attempt on each),
// so a manual drain stops at once and leaves the state exactly as it is.
//
// A canceled or expired context is deliberately not listed: an RPC call that
// timed out wraps context.DeadlineExceeded too, and that is an ordinary job
// failure. The caller asks its own context instead.
func IsFatalRunError(err error) bool {
	return err != nil && (isSigningHalt(err) || errors.Is(err, ErrSweepLockHeld))
}

// FailureCategory is a short, fixed word for what kind of problem err is. RPC
// failures keep the rpc package's categories, so one vocabulary covers both.
func FailureCategory(err error) string {
	var storage *persistenceError
	switch {
	case err == nil:
		return ""
	case errors.As(err, &storage):
		return CategoryStorage
	case errors.Is(err, ErrSweepLockHeld):
		return CategoryLock
	case errors.Is(err, errChainUnverified):
		return CategoryEndpoints
	case isSigningHalt(err):
		return CategoryWallet
	case errors.Is(err, errGasBudgetInsufficient), errors.Is(err, errGasPayerUnderfunded),
		errors.Is(err, errGasQuoteExceedsBudget), errors.Is(err, errGasPayerPending):
		return CategoryGas
	case errors.Is(err, errStaleSweepPlan):
		return CategoryStale
	}
	var failure *rpc.Failure
	if errors.As(err, &failure) {
		return failure.Category()
	}
	return CategoryOther
}

// Reasons a pending job reports while it only waits for the chain. They are not
// failures: no alert, and a manual drain does not count them as a stall.
const (
	reasonAwaitingReceipt  = "awaiting canonical receipt and required confirmations"
	reasonAwaitingGasPayer = "awaiting gas payer transaction confirmation"
)

// Waiting reports a pending job that only waits for the chain: its own
// confirmations, or the gas payer's top-up for another job. A pending job with
// no reason at all is not waiting for anything we know of.
func (e SweepEntry) Waiting() bool {
	if e.Status != "pending" {
		return false
	}
	reason := rpc.SafeRecordedError(e.Error)
	return reason == reasonAwaitingReceipt || reason == reasonAwaitingGasPayer
}

// NeedsAttention reports whether an entry is something an operator should look
// at: a failed or manual-review job, or a pending job that carries a reason
// other than an ordinary wait for the chain.
func (e SweepEntry) NeedsAttention() bool {
	switch e.Status {
	case "failed", "manual_review":
		return true
	case "pending":
		return rpc.SafeRecordedError(e.Error) != "" && !e.Waiting()
	}
	return false
}

// SweepFailure is one distinct job failure a manual drain went through. Count
// is the number of rounds it happened in. Every field is a number or a word
// from the fixed vocabulary above: no node text, URL or address.
type SweepFailure struct {
	JobID    int64  `json:"job_id,omitempty"`
	Chain    string `json:"chain,omitempty"`
	Step     string `json:"step,omitempty"`
	Category string `json:"category,omitempty"`
	Count    int    `json:"count"`
}

// failureLogEvery is how often one job may log the same failure. A manual drain
// retries a job every round, and the process samples repeated log lines (the
// first few per second per message, then one in a thousand), so an unthrottled
// retry loop would drown the very lines that matter. The durable reason in
// sweep_records.error and the run summary carry the rest.
const failureLogEvery = 5 * time.Minute

// failureLogLimit bounds the limiter's memory.
const failureLogLimit = 512

type failureLimiter struct {
	mu   sync.Mutex
	seen map[string]time.Time
	now  func() time.Time
}

var jobFailureLog = &failureLimiter{seen: map[string]time.Time{}, now: time.Now}

// allow reports whether key may log now, and records that it did.
func (l *failureLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if last, ok := l.seen[key]; ok && now.Sub(last) < failureLogEvery {
		return false
	}
	if len(l.seen) >= failureLogLimit {
		for k, last := range l.seen {
			if now.Sub(last) >= failureLogEvery {
				delete(l.seen, k)
			}
		}
		if len(l.seen) >= failureLogLimit {
			clear(l.seen)
		}
	}
	l.seen[key] = now
	return true
}

// logJobFailure logs why a job could not advance, at ERROR so it survives
// LOG_LEVEL=error, at most once per job and reason per failureLogEvery. Host is
// the endpoint hostname (never the URL); detail is the node's own words after
// rpc.SanitizeNodeMessage removed URLs, credentials and hex values.
func logJobFailure(msg string, job *sweepJob, cause error) {
	step, category := stepOf(cause), FailureCategory(cause)
	if !jobFailureLog.allow(strconv.FormatInt(job.ID, 10) + "|" + step + "/" + category) {
		return
	}
	slog.Error(msg,
		"job_id", job.ID, "chain", job.Chain, "address", job.Address, "token", job.Token,
		"status", job.Status, "revision", job.Revision,
		"step", step, "category", category, "endpoint_host", rpc.HostOf(cause),
		"detail", rpc.DetailOf(cause), "error", rpc.SafeError(cause))
}
