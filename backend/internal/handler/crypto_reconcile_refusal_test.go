package handler

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
)

func TestReconcileRefusalIsAConflictWithAFixedMessage(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		message string
	}{
		{"sweep running", cryptopay.ErrScanBusy, "A sweep is running; reconcile after it finishes"},
		{"sweep running, wrapped", fmt.Errorf("wrapped: %w", cryptopay.ErrScanBusy), "A sweep is running; reconcile after it finishes"},
		{"old record", sweeper.ErrLegacySweepRecord, "This is an old record handled by hand and cannot be reconciled here"},
		{"old record, wrapped", fmt.Errorf("x: %w", sweeper.ErrLegacySweepRecord), "This is an old record handled by hand and cannot be reconciled here"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, message, ok := reconcileRefusal(tc.err)
			if !ok || status != http.StatusConflict || message != tc.message {
				t.Fatalf("status=%d message=%q ok=%v", status, message, ok)
			}
		})
	}
	// Anything else keeps its existing handling (and its own status).
	for _, err := range []error{errors.New("persist sweep job: disk full"), nil} {
		if _, _, ok := reconcileRefusal(err); ok {
			t.Fatalf("%v must not be treated as a refusal", err)
		}
	}
}
