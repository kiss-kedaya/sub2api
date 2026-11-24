package service

import (
	"errors"
	"fmt"
	"strings"
)

// SelectionFailureCounts is the machine-readable part of a "no available
// accounts" failure: how the candidate pool broke down when the scheduler came
// up empty. Handlers classify the response (404 model not supported, 403 plan
// gated, 429 model rate limited, otherwise 503) from these counters and never
// from the error text, because that text embeds the caller-controlled model
// name and a route-level summary and so cannot be trusted to hold the verdict.
type SelectionFailureCounts struct {
	Pool              int
	ModelNotSupported int
	ModelPlanGated    int
	ModelRateLimited  int
}

// selectionFailureError is ErrNoAvailableAccounts plus the structured counters.
// message is the human-readable diagnostic for logs and ops; nothing parses it.
type selectionFailureError struct {
	counts  SelectionFailureCounts
	message string
}

func (e *selectionFailureError) Error() string { return e.message }

func (e *selectionFailureError) Unwrap() error { return ErrNoAvailableAccounts }

// NewSelectionFailureError builds a "no available accounts" error that carries
// the scheduler's counters. It matches errors.Is(err, ErrNoAvailableAccounts).
func NewSelectionFailureError(counts SelectionFailureCounts, message string) error {
	return &selectionFailureError{counts: counts, message: message}
}

// SelectionFailureCountsFromError returns the counters of the first structured
// selection failure in err's chain. Errors that were not built with counters
// (bare ErrNoAvailableAccounts, channel restriction, ...) report ok=false.
func SelectionFailureCountsFromError(err error) (SelectionFailureCounts, bool) {
	var failure *selectionFailureError
	if errors.As(err, &failure) {
		return failure.counts, true
	}
	return SelectionFailureCounts{}, false
}

// supportingModelClause renders " supporting model: X" for the log text of a
// no-available error. The model is caller-controlled, so it always sits outside
// the trailing parenthesised diagnostics.
func supportingModelClause(requestedModel string) string {
	if strings.TrimSpace(requestedModel) == "" {
		return ""
	}
	return " supporting model: " + requestedModel
}

// noAvailableAccountsDetail is a counter-less no-available error: the pool was
// never examined (nothing schedulable, wait queue exhausted), so there is no
// model verdict for the classifier to read and the response stays a 503.
func noAvailableAccountsDetail(requestedModel, detail string) error {
	return fmt.Errorf("%w%s (%s)", ErrNoAvailableAccounts, supportingModelClause(requestedModel), detail)
}

// noAvailableAccountsFromStats is the no-available error produced after every
// account was examined: scope (optional) and the per-reason summary go in the
// trailing parentheses, the counters travel in the structured part.
func noAvailableAccountsFromStats(requestedModel, scope string, stats selectionFailureStats) error {
	detail := summarizeSelectionFailureStats(stats)
	if scope != "" {
		detail = scope + " " + detail
	}
	return NewSelectionFailureError(stats.failureCounts(), ErrNoAvailableAccounts.Error()+supportingModelClause(requestedModel)+" ("+detail+")")
}
