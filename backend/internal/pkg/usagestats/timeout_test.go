package usagestats

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/lib/pq"
)

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
