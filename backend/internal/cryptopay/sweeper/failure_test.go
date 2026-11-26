package sweeper

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
)

// The step words are written to sweep_records.error; rpc.SafeRecordedError must
// keep every one of them in front of a reason, or the record silently collapses
// into the generic sentence on its next read.
func TestEveryStepIsKeptByTheRecordSanitiser(t *testing.T) {
	if len(allSteps) != 10 {
		t.Fatalf("the step vocabulary changed (%d steps): update rpc.sweepStepPrefixes in the same change", len(allSteps))
	}
	seen := map[string]bool{}
	for _, step := range allSteps {
		if seen[step] {
			t.Fatalf("duplicate step %q", step)
		}
		seen[step] = true
		want := step + ": RPC connection failed"
		if got := rpc.SafeRecordedError(want); got != want {
			t.Errorf("step %q is not kept by rpc.SafeRecordedError: %q", step, got)
		}
	}
}

// Real wallet and mode errors used to reach the record and the run status as the
// generic sentence. Each sentinel's own text must now survive, word for word.
func TestSignerSentinelsKeepTheirTextInRecords(t *testing.T) {
	sentinels := append([]error{wallet.ErrVaultUnavailable, wallet.ErrAuditUnavailable}, signingHalts...)
	for _, err := range sentinels {
		if got := rpc.SafeRecordedError(err.Error()); got != err.Error() {
			t.Errorf("%q is recorded as %q", err.Error(), got)
		}
		if got := rpc.SafeError(fmt.Errorf("wrapped: %w", err)); got != rpc.SafeRecordedError("wrapped: "+err.Error()) {
			t.Errorf("SafeError disagrees with SafeRecordedError for %q", err.Error())
		}
	}
}

func TestWaitReasonsMatchWhatTheExecutorWritesAndTheSanitiserKeeps(t *testing.T) {
	for _, reason := range []string{reasonAwaitingReceipt, reasonAwaitingGasPayer} {
		if got := rpc.SafeRecordedError(reason); got != reason {
			t.Errorf("%q is not kept by the sanitiser: %q", reason, got)
		}
	}
	if reasonAwaitingReceipt != "awaiting canonical receipt and required confirmations" ||
		reasonAwaitingGasPayer != "awaiting gas payer transaction confirmation" {
		t.Fatal("the wait reasons are part of the alert and drain contract; change them with rpc.SafeRecordedError")
	}
}

func TestIsFatalRunError(t *testing.T) {
	timeout := rpc.NewTransportFailure(context.DeadlineExceeded)
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"signer locked", wallet.ErrSignerShareRequired, true},
		{"security lock", wallet.ErrSecurityLocked, true},
		{"signing disabled", wallet.ErrSigningDisabled, true},
		{"no authorisation", wallet.ErrSigningAuthorizationRequired, true},
		{"wallet missing", wallet.ErrWalletNotConfigured, true},
		{"rate limited", wallet.ErrSignerRateLimited, true},
		{"read only", mode.ErrReadOnly, true},
		{"lock held by another instance", ErrSweepLockHeld, true},
		{"wrapped", fmt.Errorf("sign: %w", wallet.ErrSignerShareRequired), true},
		{"joined with a job error", errors.Join(errors.New("job"), atStep(StepSign, wallet.ErrSecurityLocked)), true},
		{"tagged", atStep(StepSign, wallet.ErrSigningDisabled), true},
		// Job-level failures must not be fatal, or one chain's trouble stops the rest.
		{"rpc transport", rpc.NewTransportFailure(errors.New("x")), false},
		// An RPC call that timed out wraps DeadlineExceeded; the run's context is not over.
		{"rpc timeout", timeout, false},
		{"canceled context alone", context.Canceled, false},
		{"vault read failed", wallet.ErrVaultUnavailable, false},
		{"audit write failed", wallet.ErrAuditUnavailable, false},
		{"storage", &persistenceError{errors.New("disk full")}, false},
		{"validation", atStep(StepValidate, errors.New("invalid sweep plan")), false},
		{"gas wait", errGasPayerUnderfunded, false},
	} {
		if got := IsFatalRunError(tc.err); got != tc.want {
			t.Errorf("%s: IsFatalRunError = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestFailureCategory(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"transport", rpc.NewTransportFailure(errors.New("x")), rpc.CategoryTransport},
		{"timeout", rpc.NewTransportFailure(context.DeadlineExceeded), rpc.CategoryTimeout},
		{"http 429", &rpc.Failure{Kind: "http", HTTPStatus: 429}, rpc.CategoryTransport},
		{"http 403", &rpc.Failure{Kind: "http", HTTPStatus: 403}, rpc.CategoryAuthConfig},
		{"rejected", &rpc.Failure{Kind: "remote", Reason: "nonce too low"}, rpc.CategoryRejectedKnown},
		{"invalid response", &rpc.Failure{Kind: "response"}, rpc.CategoryUnknown},
		{"signer", atStep(StepSign, wallet.ErrSignerShareRequired), CategoryWallet},
		{"read only", mode.ErrReadOnly, CategoryWallet},
		{"storage", &persistenceError{errors.New("disk full")}, CategoryStorage},
		{"lock", ErrSweepLockHeld, CategoryLock},
		{"gas budget", errGasBudgetInsufficient, CategoryGas},
		{"gas quote", errGasQuoteExceedsBudget, CategoryGas},
		{"gas payer", errGasPayerUnderfunded, CategoryGas},
		{"stale plan", fmt.Errorf("%w: moved", errStaleSweepPlan), CategoryStale},
		{"own rule", errors.New("invalid sweep plan"), CategoryOther},
	} {
		if got := FailureCategory(tc.err); got != tc.want {
			t.Errorf("%s: FailureCategory = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Tagging decorates; it must never change what an error says or what errors.Is
// and errors.As find, because the executor, the sanitiser and the tests all
// rely on both.
func TestAtStepOnlyDecoratesAndTheInnermostStepWins(t *testing.T) {
	base := rpc.NewTransportFailure(errors.New("x"))
	tagged := atStep(StepGasPrice, base)
	if tagged.Error() != base.Error() || rpc.SafeError(tagged) != rpc.SafeError(base) {
		t.Fatalf("the text changed: %q", tagged.Error())
	}
	if !rpc.IsTransient(tagged) || FailureCategory(tagged) != rpc.CategoryTransport {
		t.Fatal("the wrapped failure is no longer visible")
	}
	if got := stepOf(atStep(StepGasFund, tagged)); got != StepGasPrice {
		t.Fatalf("an outer call overrode the deeper stage: %q", got)
	}
	if got := stepOf(atStep(StepGasFund, errors.Join(errors.New("a"), tagged))); got != StepGasPrice {
		t.Fatalf("a tag inside a joined error was lost: %q", got)
	}
	if atStep(StepSign, nil) != nil || stepOf(nil) != "" || stepOf(errors.New("untagged")) != "" {
		t.Fatal("nil and untagged errors must stay as they are")
	}
	// A storage failure is a persist failure wherever it surfaces.
	storage := &persistenceError{errors.New("disk full")}
	if got := stepOf(atStep(StepSign, storage)); got != StepPersist {
		t.Fatalf("a storage failure was labelled %q", got)
	}
	if !errors.Is(atStep(StepSign, wallet.ErrSecurityLocked), wallet.ErrSecurityLocked) {
		t.Fatal("errors.Is no longer reaches the sentinel")
	}
}

func TestNeedsAttention(t *testing.T) {
	for _, tc := range []struct {
		name    string
		e       SweepEntry
		want    bool
		waiting bool
	}{
		{"swept", SweepEntry{Status: "swept"}, false, false},
		{"failed", SweepEntry{Status: "failed"}, true, false},
		{"manual review without a reason", SweepEntry{Status: "manual_review"}, true, false},
		{"pending without a reason", SweepEntry{Status: "pending"}, false, false},
		{"pending on confirmations", SweepEntry{Status: "pending", Error: reasonAwaitingReceipt}, false, true},
		{"pending on the gas payer", SweepEntry{Status: "pending", Error: reasonAwaitingGasPayer}, false, true},
		{"pending on an RPC error", SweepEntry{Status: "pending", Error: "RPC connection failed"}, true, false},
		{"pending on a gas wait the operator must fix", SweepEntry{Status: "pending", Error: errGasPayerUnderfunded.Error()}, true, false},
		{"planned (dry run)", SweepEntry{Status: "planned", Error: "x"}, false, false},
		{"a wait reason on a failed row is still a failure", SweepEntry{Status: "failed", Error: reasonAwaitingReceipt}, true, false},
	} {
		if got := tc.e.NeedsAttention(); got != tc.want {
			t.Errorf("%s: NeedsAttention = %v, want %v", tc.name, got, tc.want)
		}
		if got := tc.e.Waiting(); got != tc.waiting {
			t.Errorf("%s: Waiting = %v, want %v", tc.name, got, tc.waiting)
		}
	}
}

// "Advanced" is what a manual drain uses to tell progress from a stall, so every
// forward move must count and nothing else may.
func TestJobMarkAdvancedTo(t *testing.T) {
	base := jobMark{gasHash: "g1", sweepHash: "", gasConfirmed: false}
	for _, tc := range []struct {
		name string
		from jobMark
		to   jobMark
		want bool
	}{
		{"nothing changed", base, base, false},
		{"first gas transaction signed", jobMark{}, jobMark{gasHash: "g1"}, true},
		{"gas transaction replaced at the same nonce", base, jobMark{gasHash: "g2"}, true},
		{"gas confirmed", base, jobMark{gasHash: "g1", gasConfirmed: true}, true},
		{"sweep signed", jobMark{gasHash: "g1", gasConfirmed: true}, jobMark{gasHash: "g1", gasConfirmed: true, sweepHash: "s1"}, true},
		{"sweep replaced", jobMark{sweepHash: "s1"}, jobMark{sweepHash: "s2"}, true},
		{"swept", jobMark{sweepHash: "s1"}, jobMark{sweepHash: "s1", swept: true}, true},
		{"a hash cleared for a re-sign is not progress", jobMark{sweepHash: "s1"}, jobMark{}, false},
		{"gas unconfirmed again is not progress", jobMark{gasHash: "g1", gasConfirmed: true}, jobMark{gasHash: "g1"}, false},
		{"already swept stays swept", jobMark{swept: true}, jobMark{swept: true}, false},
	} {
		if got := tc.from.advancedTo(tc.to); got != tc.want {
			t.Errorf("%s: advancedTo = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestFailureLimiterAllowsOneLinePerJobAndReasonPerWindow(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	l := &failureLimiter{seen: map[string]time.Time{}, now: func() time.Time { return now }}
	if !l.allow("1|sign/wallet") {
		t.Fatal("the first line must be written")
	}
	if l.allow("1|sign/wallet") {
		t.Fatal("a repeat inside the window must be dropped")
	}
	if !l.allow("1|nonce/transport") || !l.allow("2|sign/wallet") {
		t.Fatal("a different reason or job has its own allowance")
	}
	now = now.Add(failureLogEvery - time.Second)
	if l.allow("1|sign/wallet") {
		t.Fatal("still inside the window")
	}
	now = now.Add(2 * time.Second)
	if !l.allow("1|sign/wallet") {
		t.Fatal("the window passed, the failure must be logged again")
	}
	// Memory stays bounded however many jobs fail.
	for i := 0; i < 3*failureLogLimit; i++ {
		l.allow(fmt.Sprintf("job-%d", i))
	}
	if len(l.seen) > failureLogLimit {
		t.Fatalf("the limiter grew to %d entries", len(l.seen))
	}
}
