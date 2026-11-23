package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The messages are what the providers answered in production on 2026-11-24 (dRPC
// free plan, Tatum, 1RPC) plus the other wordings seen in the wild.
func TestRangeLimitFromMessage(t *testing.T) {
	cases := []struct {
		message string
		want    uint64
		ok      bool
	}{
		{"ranges over 100 blocks are not supported on free plan", 100, true},
		{"Requested range is over limit of 100", 100, true},
		{"eth_getLogs is limited to 0 - 50 blocks range", 50, true},
		{"Log response size exceeded. You can make eth_getLogs requests with up to a 2K block range and no limit on the response size", 2000, true},
		{"exceed maximum block range: 5000", 5000, true},
		{"query exceeds max block range 100,000", 100000, true},
		// not range limits
		{"429 Too Many Requests: 10 requests per second", 0, false},
		{"daily request limit reached", 0, false},
		{"Insufficient RU balance", 0, false},
		{"Public daily ceiling of 10000 requests exceeded", 0, false},
		{"query returned more than 10000 results", 0, false},
		// a number that is not a plausible span is a parse accident
		{"ranges over 3 blocks are not supported", 0, false},
		{"", 0, false},
	}
	for _, tc := range cases {
		got, ok := rangeLimitFromMessage(tc.message)
		if got != tc.want || ok != tc.ok {
			t.Errorf("rangeLimitFromMessage(%q) = (%d, %v), want (%d, %v)", tc.message, got, ok, tc.want, tc.ok)
		}
	}
}

func TestClassifyRemoteRecognisesRangeLimitWording(t *testing.T) {
	for _, message := range []string{
		"ranges over 100 blocks are not supported on free plan",
		"Requested range is over limit of 100",
		"eth_getLogs is limited to 0 - 50 blocks range",
		"query returned more than 10000 results",
	} {
		if got := classifyRemote(message); got != ReasonRangeLimit {
			t.Errorf("classifyRemote(%q) = %q, want %q", message, got, ReasonRangeLimit)
		}
	}
	if got := classifyRemote("daily request limit reached"); got == ReasonRangeLimit {
		t.Errorf("a quota message must not read as a range limit")
	}
}

func TestGetLogsSpan(t *testing.T) {
	span, ok := getLogsSpan("eth_getLogs", []any{map[string]any{"fromBlock": "0x64", "toBlock": "0xc7"}})
	if !ok || span != 100 {
		t.Fatalf("span = (%d, %v), want (100, true)", span, ok)
	}
	if _, ok := getLogsSpan("eth_blockNumber", nil); ok {
		t.Fatal("only eth_getLogs has a span")
	}
	if _, ok := getLogsSpan("eth_getLogs", []any{map[string]any{"fromBlock": "latest", "toBlock": "latest"}}); ok {
		t.Fatal("tags have no numeric span")
	}
}

func getLogsParamsFor(from, to uint64) []any {
	return []any{map[string]any{"fromBlock": fmt.Sprintf("0x%x", from), "toBlock": fmt.Sprintf("0x%x", to)}}
}

// A node that refuses wide queries the way dRPC does: HTTP 400 with the limit in
// the body, while narrow queries succeed.
func rangeLimitedNode(limit uint64, hits *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		var req struct {
			Params []struct {
				FromBlock string `json:"fromBlock"`
				ToBlock   string `json:"toBlock"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var from, to uint64
		_, _ = fmt.Sscanf(req.Params[0].FromBlock, "0x%x", &from)
		_, _ = fmt.Sscanf(req.Params[0].ToBlock, "0x%x", &to)
		w.Header().Set("Content-Type", "application/json")
		if to-from+1 > limit {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"error":{"code":35,"message":"ranges over %d blocks are not supported on free plan"}}`, limit)
			return
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":[]}`))
	}))
}

func alwaysFailingNode(status int, hits *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(status)
	}))
}

func TestPoolLearnsRangeLimitAndStopsSendingWideQueriesThere(t *testing.T) {
	shrinkRetryDelays(t)
	var limitedHits, goodHits atomic.Int32
	limited := rangeLimitedNode(100, &limitedHits)
	defer limited.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		goodHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":[]}`))
	}))
	defer good.Close()

	// Both nodes are probed once per call in rotation order, so run enough calls
	// for the limited node to be reached, then check it is never asked again.
	pool := NewPoolWithHTTPClient(limited.URL+","+good.URL, limited.Client())
	for i := 0; i < 8; i++ {
		if _, _, err := pool.CallEndpoint(context.Background(), "eth_getLogs", getLogsParamsFor(1000, 2999)); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if got := limitedHits.Load(); got != 1 {
		t.Fatalf("the limited node should be asked once, learn its limit, then be skipped; it saw %d requests", got)
	}
	if got := goodHits.Load(); got != 8 {
		t.Fatalf("the capable node should serve every call, saw %d", got)
	}
	// A query inside the limit still goes to the limited node: it is healthy.
	before := limitedHits.Load()
	for i := 0; i < 6; i++ {
		if _, _, err := pool.CallEndpoint(context.Background(), "eth_getLogs", getLogsParamsFor(1000, 1049)); err != nil {
			t.Fatalf("narrow call %d: %v", i, err)
		}
	}
	if limitedHits.Load() == before {
		t.Fatal("a narrow query must still be allowed on the limited node")
	}
}

func TestPoolReportsSplittableFailureWhenNoEndpointCanServeTheSpan(t *testing.T) {
	shrinkRetryDelays(t)
	var aHits, bHits atomic.Int32
	a := rangeLimitedNode(100, &aHits)
	defer a.Close()
	b := rangeLimitedNode(50, &bHits)
	defer b.Close()
	pool := NewPoolWithHTTPClient(a.URL+","+b.URL, a.Client())

	_, _, err := pool.CallEndpoint(context.Background(), "eth_getLogs", getLogsParamsFor(1, 1000))
	var failure *Failure
	if err == nil || !errors.As(err, &failure) {
		t.Fatalf("expected a Failure, got %v", err)
	}
	if failure.Kind != "remote" || failure.RPCCode == 429 || failure.Reason != ReasonRangeLimit {
		t.Fatalf("the scanner splits only remote, non-429 failures: got %+v", failure)
	}
	if got := failure.Error(); !strings.Contains(got, ReasonRangeLimit) {
		t.Fatalf("durable error text must carry the fixed reason, got %q", got)
	}
	afterFirst := aHits.Load() + bHits.Load()

	// Now both limits are known: the next wide call spends no request at all.
	if _, _, err := pool.CallEndpoint(context.Background(), "eth_getLogs", getLogsParamsFor(1, 1000)); err == nil {
		t.Fatal("expected the wide query to keep failing as splittable")
	}
	if got := aHits.Load() + bHits.Load(); got != afterFirst {
		t.Fatalf("a known-impossible span must not cost requests: %d -> %d", afterFirst, got)
	}

	// Halving until it fits succeeds, which is what the scanner's splitter does.
	if _, _, err := pool.CallEndpoint(context.Background(), "eth_getLogs", getLogsParamsFor(1, 50)); err != nil {
		t.Fatalf("a span inside both limits must succeed: %v", err)
	}
}

func TestPoolDoesNotTreatRateLimitAsRangeLimit(t *testing.T) {
	shrinkRetryDelays(t)
	var hits atomic.Int32
	srv := alwaysFailingNode(http.StatusTooManyRequests, &hits)
	defer srv.Close()
	pool := NewPoolWithHTTPClient(srv.URL, srv.Client())
	_, _, err := pool.CallEndpoint(context.Background(), "eth_getLogs", getLogsParamsFor(1, 5000))
	var failure *Failure
	if !errors.As(err, &failure) || failure.Kind != "http" || failure.HTTPStatus != 429 {
		t.Fatalf("a 429 must stay a 429 (the scanner does not split rate limits), got %v", err)
	}
}

// spanAllowedFor is a test-side wrapper that takes the lock the production
// helper expects its callers to hold.
func (t *endpointHealthTracker) spanAllowedFor(endpoint string, span uint64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.spanAllowedLocked(endpoint, span, nowFunc())
}

func TestLearnedLimitSurvivesASuccessAndExpires(t *testing.T) {
	tracker := newEndpointHealthTracker()
	now := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = time.Now })

	tracker.learnSpan("https://node.example", 100)
	tracker.fail("https://node.example", &Failure{Kind: "http", HTTPStatus: 503})
	tracker.succeed("https://node.example")
	if tracker.spanAllowedFor("https://node.example", 500) {
		t.Fatal("recovering from a fault must not forget a stated range limit")
	}
	if !tracker.spanAllowedFor("https://node.example", 100) {
		t.Fatal("a span at the limit is allowed")
	}
	now = now.Add(learnedSpanTTL + time.Second)
	if !tracker.spanAllowedFor("https://node.example", 500) {
		t.Fatal("a stated limit must expire so an upgraded plan is used again")
	}
	// the smaller of two statements wins while both are fresh
	tracker.learnSpan("https://node.example", 200)
	tracker.learnSpan("https://node.example", 80)
	tracker.learnSpan("https://node.example", 150)
	if tracker.spanAllowedFor("https://node.example", 100) {
		t.Fatal("the tightest fresh limit must win")
	}
}

func jsonErrorNode(status int, body string, hits *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

// The three quota answers the Polygon providers gave on 2026-11-24.
func TestQuotaAnswersKeepAnEndpointOutOfRotationForLong(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"quiknode daily limit", 429, `{"jsonrpc":"2.0","id":1,"error":{"code":-32003,"message":"daily request limit reached"}}`},
		{"blazingnode daily ceiling", 429, `{"jsonrpc":"2.0","id":null,"error":{"code":-32029,"message":"Public daily ceiling of 10000 requests exceeded. Get a plan."}}`},
		{"blockpi prepaid balance", 503, `{"jsonrpc":"2.0","error":{"code":10007,"message":"Insufficient RU balance"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shrinkRetryDelays(t)
			var deadHits, goodHits atomic.Int32
			dead := jsonErrorNode(tc.status, tc.body, &deadHits)
			defer dead.Close()
			good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				goodHits.Add(1)
				jsonRPCSuccess(w)
			}))
			defer good.Close()
			pool := NewPoolWithHTTPClient(dead.URL+","+good.URL, dead.Client())
			for i := 0; i < 10; i++ {
				if _, err := pool.Call(context.Background(), "eth_blockNumber", nil); err != nil {
					t.Fatalf("call %d: %v", i, err)
				}
			}
			if got := deadHits.Load(); got != 1 {
				t.Fatalf("an endpoint that said its quota is gone must be asked once, not on every call; it saw %d requests", got)
			}
		})
	}
}

func TestPlainRateLimitStillOnlyCoolsDownBriefly(t *testing.T) {
	shrinkRetryDelays(t)
	var hits atomic.Int32
	srv := jsonErrorNode(429, `{"error":{"code":429,"message":"Too many requests, slow down (50 req/s)"}}`, &hits)
	defer srv.Close()
	pool := NewPoolWithHTTPClient(srv.URL, srv.Client())
	_, err := pool.Call(context.Background(), "eth_blockNumber", nil)
	var failure *Failure
	if !errors.As(err, &failure) || failure.Quota {
		t.Fatalf("a burst rate limit must not be treated as an exhausted quota: %+v", failure)
	}
	if got := endpointCooldown(err); got != transientEndpointCooldown {
		t.Fatalf("burst rate limit cooldown = %v, want %v", got, transientEndpointCooldown)
	}
}

func TestQuotaFailureCooldownIsLongAndNotCappedAtTwoMinutes(t *testing.T) {
	tracker := newEndpointHealthTracker()
	now := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = time.Now })
	err := &Failure{Kind: "http", HTTPStatus: 429, Quota: true}
	tracker.fail("https://dead.example", err)
	if got := tracker.eligible([]string{"https://dead.example", "https://ok.example"}); len(got) != 1 || got[0] != "https://ok.example" {
		t.Fatalf("quota-dead endpoint must be skipped, got %v", got)
	}
	now = now.Add(14 * time.Minute)
	if got := tracker.eligible([]string{"https://dead.example", "https://ok.example"}); len(got) != 1 {
		t.Fatalf("still inside the 15 minute window, got %v", got)
	}
	now = now.Add(2 * time.Minute)
	if got := tracker.eligible([]string{"https://dead.example", "https://ok.example"}); len(got) != 2 {
		t.Fatalf("after 15 minutes the endpoint is probed again, got %v", got)
	}
}

func TestTransactionErrorsAreNeverReadAsProviderQuota(t *testing.T) {
	shrinkRetryDelays(t)
	var hits atomic.Int32
	srv := jsonErrorNode(200, `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"insufficient funds for gas * price + value: daily balance too low"}}`, &hits)
	defer srv.Close()
	pool := NewPoolWithHTTPClient(srv.URL, srv.Client())
	_, err := pool.Call(context.Background(), "eth_sendRawTransaction", []any{"0x00"})
	var failure *Failure
	if !errors.As(err, &failure) || failure.Quota {
		t.Fatalf("a JSON-RPC transaction error must never trigger the provider-quota cooldown: %+v", failure)
	}
	if got := endpointCooldown(err); got != 0 {
		t.Fatalf("a transaction rejection says nothing about node health, cooldown = %v", got)
	}
}

// A rate limited capable node plus a node that refuses the width: the caller must
// be told about the rate limit (retry later), never about the width (which the
// scanner would answer by splitting into even more requests).
func TestRateLimitIsNotMaskedByAnotherNodesRangeRefusal(t *testing.T) {
	shrinkRetryDelays(t)
	var limitedHits, busyHits atomic.Int32
	limited := rangeLimitedNode(100, &limitedHits)
	defer limited.Close()
	busy := jsonErrorNode(429, `{"error":{"message":"Too many requests, slow down"}}`, &busyHits)
	defer busy.Close()
	for _, order := range []string{limited.URL + "," + busy.URL, busy.URL + "," + limited.URL} {
		pool := NewPoolWithHTTPClient(order, limited.Client())
		_, _, err := pool.CallEndpoint(context.Background(), "eth_getLogs", getLogsParamsFor(1, 1000))
		var failure *Failure
		if !errors.As(err, &failure) || failure.Kind != "http" || failure.HTTPStatus != 429 {
			t.Fatalf("order %q: want the 429 to be reported, got %v", order, err)
		}
	}
}

func TestHTTPRangeRejectionIsReportedAsSplittableRemoteFailure(t *testing.T) {
	shrinkRetryDelays(t)
	var hits atomic.Int32
	srv := rangeLimitedNode(100, &hits)
	defer srv.Close()
	pool := NewPoolWithHTTPClient(srv.URL, srv.Client())
	_, _, err := pool.CallEndpoint(context.Background(), "eth_getLogs", getLogsParamsFor(1, 1000))
	var failure *Failure
	if !errors.As(err, &failure) || failure.Kind != "remote" || failure.RPCCode == 429 || failure.Reason != ReasonRangeLimit || failure.SpanLimit != 100 {
		t.Fatalf("an HTTP 400 range rejection must surface as a splittable remote range failure carrying the stated limit, got %+v", failure)
	}
	if got := SafeError(err); !strings.Contains(got, ReasonRangeLimit) {
		t.Fatalf("SafeError must keep the fixed reason, got %q", got)
	}
}

// N concurrent calls that hit one rate limit are one event. Counting them N times
// pushed the cooldown from seconds to the two minute cap in a single burst, so a
// node that merely disliked a burst was written off for minutes.
func TestConcurrentFailuresOfOneBurstAreOneCooldownStep(t *testing.T) {
	tracker := newEndpointHealthTracker()
	now := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = time.Now })
	rateLimited := &Failure{Kind: "http", HTTPStatus: 429}
	const endpoint = "https://node.example"

	for i := 0; i < 5; i++ { // the five batches of one scan stage, all rejected together
		tracker.fail(endpoint, rateLimited)
	}
	tracker.mu.Lock()
	failures, until := tracker.states[endpoint].failures, tracker.states[endpoint].until
	tracker.mu.Unlock()
	if failures != 1 || !until.Equal(now.Add(transientEndpointCooldown)) {
		t.Fatalf("one burst must be one step: failures=%d cooldown=%v", failures, until.Sub(now))
	}

	// A failure after the cooldown lapsed is the next step (doubling), as before.
	now = now.Add(transientEndpointCooldown + time.Second)
	tracker.fail(endpoint, rateLimited)
	tracker.mu.Lock()
	failures, until = tracker.states[endpoint].failures, tracker.states[endpoint].until
	tracker.mu.Unlock()
	if failures != 2 || !until.Equal(now.Add(2*transientEndpointCooldown)) {
		t.Fatalf("second event must double the cooldown: failures=%d cooldown=%v", failures, until.Sub(now))
	}
}

// Everything in a node's reply is node-controlled. Classification patterns run
// over it, so an unbounded message turned one reply into seconds of CPU; the text
// is cut to 512 bytes before anything looks at it.
func TestNodeErrorTextIsBoundedBeforeClassification(t *testing.T) {
	shrinkRetryDelays(t)
	var hits atomic.Int32
	huge := strings.Repeat("x", 6<<20)
	srv := jsonErrorNode(200, `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"`+huge+`"}}`, &hits)
	defer srv.Close()
	pool := NewPoolWithHTTPClient(srv.URL, srv.Client())
	start := time.Now()
	_, err := pool.Call(context.Background(), "eth_blockNumber", nil)
	var failure *Failure
	if !errors.As(err, &failure) || failure.Kind != "remote" {
		t.Fatalf("expected a remote failure, got %v", err)
	}
	if len(failure.Message) > maxNodeMessageBytes {
		t.Fatalf("node text must be bounded, kept %d bytes", len(failure.Message))
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("classifying a huge reply took %v", took)
	}
}

// A provider's HTTP error page can echo the request URL, API key included. The
// text is used to classify the failure and then dropped: only the fixed phrase,
// the numbers and the flag survive, so there is nothing for a log line to leak.
func TestHTTPErrorTextIsNeverKeptOnTheFailure(t *testing.T) {
	shrinkRetryDelays(t)
	var hits atomic.Int32
	echoed := "Insufficient RU balance for " + strings.Join([]string{"https://node.example/v2", "0123456789abcdef0123456789abcdef"}, "/")
	srv := jsonErrorNode(503, `{"error":{"code":10007,"message":"`+echoed+`"}}`, &hits)
	defer srv.Close()
	pool := NewPoolWithHTTPClient(srv.URL, srv.Client())
	_, err := pool.Call(context.Background(), "eth_blockNumber", nil)
	var failure *Failure
	if !errors.As(err, &failure) || failure.Kind != "http" || failure.HTTPStatus != 503 {
		t.Fatalf("expected the 503, got %v", err)
	}
	if !failure.Quota {
		t.Fatal("the quota flag is the whole point of reading the body")
	}
	if failure.Message != "" || failure.Detail() != "" {
		t.Fatalf("the provider's text must not survive on the failure: message=%q detail=%q", failure.Message, failure.Detail())
	}
}

// Every scan pass derives its own pool from the worker's. If each derived pool
// starts with an empty health record, a node that just said "daily limit
// reached" is forgotten five seconds later and asked again, forever.
func TestDerivedPoolsShareEndpointHealth(t *testing.T) {
	shrinkRetryDelays(t)
	var deadHits, goodHits atomic.Int32
	dead := jsonErrorNode(429, `{"error":{"code":-32003,"message":"daily request limit reached"}}`, &deadHits)
	defer dead.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		goodHits.Add(1)
		jsonRPCSuccess(w)
	}))
	defer good.Close()
	list := dead.URL + "," + good.URL
	parent := NewPoolWithHTTPClient(list, dead.Client())
	for i := 0; i < 4; i++ { // the first pass finds out
		if _, err := parent.Call(context.Background(), "eth_blockNumber", nil); err != nil {
			t.Fatalf("parent call %d: %v", i, err)
		}
	}
	if got := deadHits.Load(); got != 1 {
		t.Fatalf("setup: the dead node should have been asked once, saw %d", got)
	}
	for name, derive := range map[string]func() *Pool{
		"Snapshot":      parent.Snapshot,
		"WithEndpoints": func() *Pool { return parent.WithEndpoints(list) },
		"WithChainID":   func() *Pool { return parent.WithChainID(137) },
	} {
		for i := 0; i < 4; i++ { // later passes must not ask it again
			if _, err := derive().Call(context.Background(), "eth_blockNumber", nil); err != nil {
				t.Fatalf("%s call %d: %v", name, i, err)
			}
		}
		if got := deadHits.Load(); got != 1 {
			t.Fatalf("%s forgot the cooldown: the dead node was asked again (%d requests)", name, got)
		}
	}
}

// The only nodes able to serve a wide query are cooling down (a spent quota or a
// rate limit) while the healthy one takes narrow queries only. Probing the cooling
// nodes anyway returned THEIR hard error - not something the scanner can split on -
// so the chain froze, and every probe kept the node rate limited and defeated its
// quota cooldown. The pool must report the query as too wide instead.
func TestWideQueryIsReportedSplittableWhenEveryCapableNodeIsCooling(t *testing.T) {
	shrinkRetryDelays(t)
	now := time.Date(2026, 11, 24, 6, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = time.Now })

	var narrowHits, wideHits atomic.Int32
	narrow := rangeLimitedNode(100, &narrowHits)
	defer narrow.Close()
	wide := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wideHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":[]}`)) // it could serve the wide query - once its cooldown is over
	}))
	defer wide.Close()
	pool := NewPoolWithHTTPClient(narrow.URL+","+wide.URL, narrow.Client())
	endpoints := pool.Endpoints()
	health := pool.healthTracker()
	health.learnSpan(endpoints[0], 100)
	health.fail(endpoints[1], &Failure{Kind: "http", HTTPStatus: 429, Quota: true})

	_, _, err := pool.CallEndpoint(context.Background(), "eth_getLogs", getLogsParamsFor(1, 1000))
	var failure *Failure
	if err == nil || !errors.As(err, &failure) || failure.Reason != ReasonRangeLimit || failure.SpanLimit != 100 {
		t.Fatalf("a wide query with every capable node cooling must be the splittable failure carrying the healthy limit (100), got %v", err)
	}
	if narrowHits.Load() != 0 || wideHits.Load() != 0 {
		t.Fatalf("no request may be spent on it: narrow=%d wide=%d", narrowHits.Load(), wideHits.Load())
	}
	// the pieces the scanner splits it into are served by the healthy node, and the
	// cooling one is still left alone
	if _, _, err := pool.CallEndpoint(context.Background(), "eth_getLogs", getLogsParamsFor(1, 100)); err != nil {
		t.Fatalf("a query inside the healthy node's limit must succeed: %v", err)
	}
	if wideHits.Load() != 0 {
		t.Fatalf("the cooling node must not be probed while a healthy one can serve the call, saw %d", wideHits.Load())
	}
	// once the cooldown is over the wide query goes to the capable node again
	now = now.Add(16 * time.Minute)
	if _, _, err := pool.CallEndpoint(context.Background(), "eth_getLogs", getLogsParamsFor(1, 1000)); err != nil {
		t.Fatalf("after the cooldown the capable node serves the wide query: %v", err)
	}
	if wideHits.Load() != 1 {
		t.Fatalf("the recovered node should have served exactly that call, saw %d", wideHits.Load())
	}
}

// When the WHOLE pool is cooling nothing is healthy to fall back on: the capable
// nodes are probed (a node that may have recovered beats failing the call).
func TestWideQueryProbesCapableNodesWhenTheWholePoolIsCooling(t *testing.T) {
	shrinkRetryDelays(t)
	now := time.Date(2026, 11, 24, 6, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = time.Now })

	var narrowHits, wideHits atomic.Int32
	narrow := rangeLimitedNode(100, &narrowHits)
	defer narrow.Close()
	wide := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wideHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":[]}`))
	}))
	defer wide.Close()
	pool := NewPoolWithHTTPClient(narrow.URL+","+wide.URL, narrow.Client())
	endpoints := pool.Endpoints()
	health := pool.healthTracker()
	health.learnSpan(endpoints[0], 100)
	health.fail(endpoints[0], &Failure{Kind: "http", HTTPStatus: 429})
	health.fail(endpoints[1], &Failure{Kind: "http", HTTPStatus: 429})

	if _, _, err := pool.CallEndpoint(context.Background(), "eth_getLogs", getLogsParamsFor(1, 1000)); err != nil {
		t.Fatalf("with the whole pool cooling the capable node must be tried: %v", err)
	}
	if wideHits.Load() != 1 || narrowHits.Load() != 0 {
		t.Fatalf("exactly the capable node should be asked: narrow=%d wide=%d", narrowHits.Load(), wideHits.Load())
	}
}
