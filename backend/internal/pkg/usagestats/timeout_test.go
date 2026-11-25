package usagestats

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/lib/pq"
)

func TestStatementTimeoutAndRequestCancelAreToldApart(t *testing.T) {
	timeout := &pq.Error{Code: "57014", Message: "canceling statement due to statement timeout"}
	byRequest := &pq.Error{Code: "57014", Message: "canceling statement due to user request"}
	tests := []struct {
		name          string
		err           error
		wantTimeout   bool
		wantByRequest bool
	}{
		{"nil", nil, false, false},
		{"statement timeout", timeout, true, false},
		{"wrapped statement timeout", fmt.Errorf("query: %w", timeout), true, false},
		{"cancelled on request (pg_cancel_backend, reader canceller, relayed context)", byRequest, false, true},
		{"wrapped cancel on request", fmt.Errorf("query: %w", byRequest), false, true},
		{"query_canceled without an explanation", &pq.Error{Code: "57014"}, false, false},
		{"another sqlstate with a timeout-ish text", &pq.Error{Code: "55P03", Message: "canceling statement due to lock timeout"}, false, false},
		{"another sqlstate that mentions the statement timeout", &pq.Error{Code: "42P01", Message: "statement timeout"}, false, false},
		{"context deadline is not a server statement timeout", context.DeadlineExceeded, false, false},
		{"context canceled is not a server cancel", context.Canceled, false, false},
		{"unrelated error", errors.New("canceling statement due to statement timeout"), false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsStatementTimeout(tt.err); got != tt.wantTimeout {
				t.Fatalf("IsStatementTimeout(%v) = %v, want %v", tt.err, got, tt.wantTimeout)
			}
			if got := IsStatementCanceledByRequest(tt.err); got != tt.wantByRequest {
				t.Fatalf("IsStatementCanceledByRequest(%v) = %v, want %v", tt.err, got, tt.wantByRequest)
			}
		})
	}
}

func TestIsStatsTimeout(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"deadline", context.DeadlineExceeded, true},
		{"canceled", context.Canceled, true},
		{"wrapped deadline", fmt.Errorf("get account usage stats failed: %w", context.DeadlineExceeded), true},
		{
			"postgres statement timeout",
			&pq.Error{Code: "57014", Message: "canceling statement due to statement timeout"},
			true,
		},
		{
			"wrapped postgres statement timeout",
			fmt.Errorf("query: %w", &pq.Error{Code: "57014", Message: "canceling statement due to user request"}),
			true,
		},
		{"other postgres error", &pq.Error{Code: "42P01", Message: `relation "usage_logs" does not exist`}, false},
		{"unrelated error", errors.New("connection refused"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsStatsTimeout(tt.err); got != tt.want {
				t.Fatalf("IsStatsTimeout(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
