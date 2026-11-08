package sweeper

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// A receipt that satisfies the configured depth is not final: the head it was
// measured from can itself be reorged. When the chain exposes a finality tag
// the mined block must also be behind it.
func TestSweptReceiptNeedsDepthAndFinality(t *testing.T) {
	e, _, chain, _ := setupExecutor(t)
	chain.mined = true
	chain.txs, chain.blocks = map[string]bool{"0xhash": true}, map[string]uint64{"0xhash": 100}
	chain.head = 110 // depth = 10, well above the required 2

	chain.finalHeight = 99 // chain has not finalized the block yet
	state, err := checkReceipt(t.Context(), e.pools["bsc"], "bsc", "0xhash", 2)
	if err != nil || state != receiptWaiting {
		t.Fatalf("depth alone finalized the sweep: state=%v err=%v", state, err)
	}

	chain.finalHeight = 100 // the block itself is now final
	state, err = checkReceipt(t.Context(), e.pools["bsc"], "bsc", "0xhash", 2)
	if err != nil || state != receiptConfirmed {
		t.Fatalf("final block was not confirmed: state=%v err=%v", state, err)
	}
}

// Chains whose nodes do not serve a finality tag keep the configured depth
// rule that governed confirmation before the tag check existed.
func TestSweepFinalityFallsBackToDepthWhenNoTagIsServed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*localChain)
	}{
		{"tag rejected", func(c *localChain) { c.finalityErr = true }},
		{"tag null", func(c *localChain) { c.finalityNull = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, chain, _ := setupExecutor(t)
			chain.mined = true
			chain.txs, chain.blocks = map[string]bool{"0xhash": true}, map[string]uint64{"0xhash": 100}
			chain.head = 110
			tc.setup(chain)
			state, err := checkReceipt(t.Context(), e.pools["bsc"], "bsc", "0xhash", 2)
			if err != nil || state != receiptConfirmed {
				t.Fatalf("unavailable finality tag must keep depth policy: state=%v err=%v", state, err)
			}
		})
	}
}

// BSC's safe tag is only its justified head; using it would weaken the
// economic finality the chain exposes through finalized.
func TestFinalityTagsPreferFinalizedAndNeverUseBSCSafe(t *testing.T) {
	if tags := finalityTags("bsc"); len(tags) != 1 || tags[0] != "finalized" {
		t.Fatalf("bsc finality tags = %v", tags)
	}
	tags := finalityTags("ethereum")
	if len(tags) != 2 || tags[0] != "finalized" || tags[1] != "safe" {
		t.Fatalf("ethereum finality tags = %v", tags)
	}
}

func TestTagHeightReportsNullAsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
	}))
	t.Cleanup(server.Close)
	pool := rpc.NewPoolWithHTTPClient(server.URL, server.Client())
	height, ok, err := tagHeight(t.Context(), pool, "finalized")
	if err != nil || ok || height != 0 {
		t.Fatalf("null tag must be reported as unavailable: height=%d ok=%t err=%v", height, ok, err)
	}
}

func TestTagHeightRejectsMalformedBlock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"number":"finalized"}}`))
	}))
	t.Cleanup(server.Close)
	pool := rpc.NewPoolWithHTTPClient(server.URL, server.Client())
	if _, _, err := tagHeight(t.Context(), pool, "finalized"); err == nil {
		t.Fatal("a malformed tag height must not silently pass as final")
	}
}
