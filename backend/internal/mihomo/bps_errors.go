package mihomo

import (
	"context"
	"errors"
)

// BPSAcquireError exposes only bounded diagnostic fields. It carries no proxy
// URL, session identity, node configuration or credentials.
type BPSAcquireError struct {
	Reason     string
	Candidates int
	cause      error
}

func (e *BPSAcquireError) Error() string {
	if e.cause != nil {
		return e.cause.Error()
	}
	return e.Reason
}

func (e *BPSAcquireError) Unwrap() error { return e.cause }

func bpsSelectionError(reason, message string) error {
	return &BPSAcquireError{Reason: reason, cause: errors.New(message)}
}

func bpsAcquisitionError(err error, candidates int) error {
	if err == nil {
		return nil
	}
	reason := "selection_failed"
	var selected *BPSAcquireError
	switch {
	case errors.As(err, &selected):
		reason = selected.Reason
	case errors.Is(err, context.Canceled):
		reason = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		reason = "acquisition_timeout"
	}
	return &BPSAcquireError{Reason: reason, Candidates: candidates, cause: err}
}
