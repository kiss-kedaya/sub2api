package scanner

import (
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// A 429 produced by this process's own pacing must be told apart from the node's
// in the scan failure log: the operator reads http_status=429 as the provider
// refusing, and local_rate_limit=true says nobody was asked.
func TestScanFailureLogMarksALocalRateLimit(t *testing.T) {
	attrsOf := func(failure *rpc.Failure) map[string]any {
		stage := &scanStageError{chain: "bsc", stage: "deposit", err: failure}
		out := map[string]any{}
		attrs := stage.logAttrs()
		for i := 0; i+1 < len(attrs); i += 2 {
			if key, ok := attrs[i].(string); ok {
				out[key] = attrs[i+1]
			}
		}
		return out
	}
	local := attrsOf(&rpc.Failure{Kind: "http", HTTPStatus: 429, Host: "bsc.example.test", Throttled: true})
	if local["local_rate_limit"] != true || local["http_status"] != 429 || local["endpoint_host"] != "bsc.example.test" {
		t.Fatalf("local rate limit attrs = %v", local)
	}
	remote := attrsOf(&rpc.Failure{Kind: "http", HTTPStatus: 429, Host: "bsc.example.test"})
	if _, marked := remote["local_rate_limit"]; marked {
		t.Fatalf("a node's own 429 was marked as local: %v", remote)
	}
	// The pass repeats itself after a short wait when a NODE rate limited it. A refusal
	// by this process's own pacing is not that: repeating the pass 3 s later only meets
	// the limiter again, so it is left to the chain's backoff.
	if isRateLimitedScan(&rpc.Failure{Kind: "http", HTTPStatus: 429, Throttled: true}) {
		t.Fatal("a local refusal must not make the pass wait and repeat itself")
	}
	if isRateLimitedScan(errors.Join(&rpc.Failure{Kind: "http", HTTPStatus: 429, Throttled: true})) {
		t.Fatal("neither inside a joined error")
	}
	if !isRateLimitedScan(&rpc.Failure{Kind: "http", HTTPStatus: 429, Host: "bsc.example.test"}) ||
		!isRateLimitedScan(&rpc.Failure{Kind: "remote", RPCCode: 429}) {
		t.Fatal("a node's own 429 must still make the pass wait out the rate limit and try again")
	}
	if !retryableBatchFailure(errors.Join(&rpc.Failure{Kind: "http", HTTPStatus: 429, Throttled: true})) {
		t.Fatal("a locally refused batch must be retried later like any rate-limited batch")
	}
}
