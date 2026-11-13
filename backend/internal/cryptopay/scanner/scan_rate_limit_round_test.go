package scanner

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// A rate limit is the one scan failure worth repeating inside the same tick:
// the block range is fine, only the request rate was too high. Retrying it keeps
// the cursor moving instead of losing the whole interval.
func TestIsRateLimitedScan(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"http 429", &rpc.Failure{Kind: "remote", HTTPStatus: http.StatusTooManyRequests}, true},
		{"rpc 429", &rpc.Failure{Kind: "remote", RPCCode: 429}, true},
		{"wrapped http 429", fmt.Errorf("stage deposit: %w", &rpc.Failure{Kind: "remote", HTTPStatus: http.StatusTooManyRequests}), true},
		{"joined with 429 second", errors.Join(errors.New("other"), &rpc.Failure{Kind: "remote", RPCCode: 429}), true},
		{"rpc 500", &rpc.Failure{Kind: "remote", HTTPStatus: http.StatusInternalServerError}, false},
		{"rejected params", &rpc.Failure{Kind: "remote", RPCCode: -32602}, false},
		{"plain error", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRateLimitedScan(tc.err); got != tc.want {
				t.Fatalf("isRateLimitedScan(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
