package rpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type verificationNode struct {
	chainID string
	results map[string]any
	fail    map[string]error
	wait    map[string]bool
}

// Synthetic operator domains retain their production grouping semantics.
// The explicit transport keeps every request in memory; no DNS, public RPC,
// configured credential, or actual blockchain service is used by these tests.
type verificationTransport struct {
	nodes map[string]*verificationNode
	mu    sync.Mutex
	calls map[string]int
}

func (f *verificationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var request struct {
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
		return nil, err
	}
	node := f.nodes[req.URL.String()]
	if node == nil {
		return nil, errors.New("unexpected synthetic RPC endpoint")
	}
	key := request.Method
	if key == "eth_getBlockByNumber" && len(request.Params) > 0 {
		var tag string
		if json.Unmarshal(request.Params[0], &tag) == nil {
			key += ":" + tag
		}
	}
	f.mu.Lock()
	f.calls[req.URL.String()+" "+key]++
	f.mu.Unlock()
	if node.wait[key] {
		<-req.Context().Done()
		return nil, req.Context().Err()
	}
	if err := node.fail[key]; err != nil {
		return nil, err
	}
	var result any
	if request.Method == "eth_chainId" {
		result = node.chainID
	} else {
		var ok bool
		result, ok = node.results[key]
		if !ok {
			result, ok = node.results[request.Method]
		}
		if !ok {
			return nil, errors.New("unexpected synthetic RPC method")
		}
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(string(body))), Request: req}, nil
}

var verificationFixtureID atomic.Uint64

type verificationFixture struct {
	pool      *Pool
	primary   string
	anchors   []string
	transport *verificationTransport
}

func newVerificationFixture(t *testing.T, count int) *verificationFixture {
	t.Helper()
	id := verificationFixtureID.Add(1)
	fixture := &verificationFixture{primary: fmt.Sprintf("https://registry-%d.example/rpc", id),
		transport: &verificationTransport{nodes: make(map[string]*verificationNode), calls: make(map[string]int)}}
	for i := 0; i < count; i++ {
		fixture.anchors = append(fixture.anchors, fmt.Sprintf("https://reference-%d-%d.example/rpc", id, i))
	}
	for _, endpoint := range append([]string{fixture.primary}, fixture.anchors...) {
		fixture.transport.nodes[endpoint] = &verificationNode{chainID: "0x38", results: make(map[string]any), fail: make(map[string]error), wait: make(map[string]bool)}
	}
	fixture.pool = NewPoolWithHTTPClient(fixture.primary, &http.Client{Transport: fixture.transport})
	fixture.pool.SetIndependentVerification(56, fixture.anchors)
	return fixture
}

func (f *verificationFixture) setAll(method string, result any) {
	for _, node := range f.transport.nodes {
		node.results[method] = result
	}
}

func (f *verificationFixture) callsTo(endpoint string) int {
	f.transport.mu.Lock()
	defer f.transport.mu.Unlock()
	count := 0
	for key, n := range f.transport.calls {
		if strings.HasPrefix(key, endpoint+" ") {
			count += n
		}
	}
	return count
}

func verificationLog(index int, amount int64) map[string]any {
	return map[string]any{
		"address": "0x" + strings.Repeat("ab", 20),
		"topics":  []string{"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef", "0x" + strings.Repeat("ef", 32), "0x" + strings.Repeat("ab", 32)},
		"data":    fmt.Sprintf("0x%064x", amount), "blockNumber": "0x65", "blockHash": "0x" + strings.Repeat("bc", 32),
		"transactionHash": "0x" + strings.Repeat("de", 32), "logIndex": fmt.Sprintf("0x%x", index), "removed": false,
	}
}

func verificationReceipt(logs ...map[string]any) map[string]any {
	if logs == nil {
		logs = []map[string]any{}
	}
	return map[string]any{"status": "0x1", "blockNumber": "0x65", "blockHash": "0x" + strings.Repeat("bc", 32),
		"transactionHash": "0x" + strings.Repeat("de", 32), "logs": logs}
}

func uppercaseEvidence(value string) string {
	return "0x" + strings.ToUpper(value[2:])
}

func equivalentVerificationLog(t *testing.T, index int, amount int64) map[string]any {
	t.Helper()
	log := verificationLog(index, amount)
	for _, key := range []string{"address", "data", "blockHash", "transactionHash"} {
		value, ok := log[key].(string)
		if !ok {
			t.Fatalf("invalid synthetic log field %s", key)
		}
		log[key] = uppercaseEvidence(value)
	}
	topics, ok := log["topics"].([]string)
	if !ok {
		t.Fatal("invalid synthetic log topics")
	}
	for i := range topics {
		topics[i] = uppercaseEvidence(topics[i])
	}
	log["transactionIndex"], log["noncriticalNodeMetadata"] = "0x7", "different synthetic metadata"
	return log
}

func TestIndependentReferencesRejectForgedFundsOrHiddenLogs(t *testing.T) {
	for _, scenario := range []string{"inflated receipt amount", "fabricated transaction", "receipt status changed", "hidden logs", "fabricated logs", "hidden receipt"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newVerificationFixture(t, 2)
			method := "eth_getTransactionReceipt"
			var primary any = verificationReceipt(verificationLog(7, 1234567))
			var honest any = verificationReceipt(verificationLog(7, 1234567))
			switch scenario {
			case "inflated receipt amount":
				primary = verificationReceipt(verificationLog(7, 999999999))
			case "fabricated transaction":
				receipt := verificationReceipt(verificationLog(7, 1234567))
				receipt["transactionHash"] = "0x" + strings.Repeat("ff", 32)
				primary = receipt
			case "receipt status changed":
				receipt := verificationReceipt(verificationLog(7, 1234567))
				receipt["status"] = "0x0"
				honest = receipt
			case "hidden logs":
				method, primary, honest = "eth_getLogs", []any{}, []any{verificationLog(7, 1234567)}
			case "fabricated logs":
				method, primary, honest = "eth_getLogs", []any{verificationLog(7, 1234567)}, []any{}
			case "hidden receipt":
				primary = nil
			}
			fixture.setAll(method, honest)
			fixture.transport.nodes[fixture.primary].results[method] = primary
			_, err := fixture.pool.Call(t.Context(), method, []any{"synthetic-transfer"})
			if !errors.Is(err, ErrIndependentEvidence) {
				t.Fatal("untrusted primary funds evidence escaped independent verification", err)
			}
		})
	}
}

func TestIndependentReferencesAllowEquivalentEvidenceAcrossOrderingAndMetadata(t *testing.T) {
	for _, method := range []string{"eth_getLogs", "eth_getTransactionReceipt"} {
		t.Run(method, func(t *testing.T) {
			fixture := newVerificationFixture(t, 2)
			var primary any = []map[string]any{verificationLog(7, 1234567), verificationLog(9, 7654321)}
			var equivalent any = []map[string]any{equivalentVerificationLog(t, 9, 7654321), equivalentVerificationLog(t, 7, 1234567)}
			if method == "eth_getTransactionReceipt" {
				primary = verificationReceipt(verificationLog(7, 1234567), verificationLog(9, 7654321))
				fields := verificationReceipt(equivalentVerificationLog(t, 9, 7654321), equivalentVerificationLog(t, 7, 1234567))
				fields["blockHash"] = "0x" + strings.Repeat("BC", 32)
				fields["transactionHash"] = "0x" + strings.Repeat("DE", 32)
				fields["gasUsed"] = "0x1234"
				equivalent = fields
			}
			fixture.setAll(method, equivalent)
			fixture.transport.nodes[fixture.primary].results[method] = primary
			if _, err := fixture.pool.Call(t.Context(), method, []any{"synthetic-transfer"}); err != nil {
				t.Fatal("independent equivalent funds evidence was rejected", err)
			}
		})
	}
}

func TestIndependentReferencesRequireTwoDistinctOperatorDomains(t *testing.T) {
	for _, scenario := range []string{"one reference", "same operator subdomains", "wrong reference chain", "unknown chain id"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newVerificationFixture(t, 2)
			fixture.setAll("eth_getLogs", []any{})
			switch scenario {
			case "one reference":
				fixture.pool.SetIndependentVerification(56, fixture.anchors[:1])
			case "same operator subdomains":
				base := fmt.Sprintf("operator-%d.example", verificationFixtureID.Add(1))
				var aliases []string
				for _, subdomain := range []string{"a", "b", "c"} {
					endpoint := "https://" + subdomain + "." + base + "/rpc"
					fixture.transport.nodes[endpoint] = &verificationNode{chainID: "0x38", results: map[string]any{"eth_getLogs": []any{}}}
					aliases = append(aliases, endpoint)
				}
				fixture.pool.SetIndependentVerification(56, aliases)
			case "wrong reference chain":
				fixture.transport.nodes[fixture.anchors[1]].chainID = "0x1"
			case "unknown chain id":
				fixture.pool.SetIndependentVerification(0, fixture.anchors)
			}
			_, err := fixture.pool.Call(t.Context(), "eth_getLogs", []any{})
			if !errors.Is(err, ErrIndependentEvidence) {
				t.Fatal("insufficient independent chain-verified operators formed a quorum", err)
			}
		})
	}
}

func TestIndependentVerificationSurvivesDerivedPoolsAndParentReconfiguration(t *testing.T) {
	fixture := newVerificationFixture(t, 2)
	fixture.setAll("eth_getLogs", []any{verificationLog(7, 1234567)})
	fixture.transport.nodes[fixture.primary].results["eth_getLogs"] = []any{}
	derived := fixture.pool.WithEndpoints(fixture.primary)
	bound, err := fixture.pool.ForChain(t.Context(), 56)
	if err != nil {
		t.Fatal(err)
	}
	fixture.pool.ClearVerification()
	for _, pool := range []*Pool{derived, bound} {
		if !pool.RequiresIndependentVerification() {
			t.Fatal("pool derivation removed the immutable evidence policy")
		}
		if _, err := pool.Call(t.Context(), "eth_getLogs", []any{}); !errors.Is(err, ErrIndependentEvidence) {
			t.Fatal("parent reconfiguration downgraded an existing automatic pool", err)
		}
	}
}

func TestIndependentDynamicHeightsUseSecondHighestReferenceInsteadOfRegistryHead(t *testing.T) {
	for _, method := range []string{"eth_blockNumber", "eth_getBlockByNumber"} {
		for _, count := range []int{2, 3} {
			t.Run(fmt.Sprintf("%s/%d operators", method, count), func(t *testing.T) {
				fixture := newVerificationFixture(t, count)
				key, params := method, []any{}
				if method == "eth_getBlockByNumber" {
					key, params = method+":finalized", []any{"finalized", false}
				}
				value := func(height uint64) any {
					if method == "eth_blockNumber" {
						return fmt.Sprintf("0x%x", height)
					}
					return map[string]any{"number": fmt.Sprintf("0x%x", height), "hash": fmt.Sprintf("0x%064x", height)}
				}
				fixture.transport.nodes[fixture.primary].results[key] = value(999999)
				for i, height := range []uint64{100, 105, 102}[:count] {
					fixture.transport.nodes[fixture.anchors[i]].results[key] = value(height)
				}
				result, err := fixture.pool.Call(t.Context(), method, params)
				if err != nil {
					t.Fatal("different reference sampling heights were incorrectly rejected", err)
				}
				height, err := evidenceHeight(method, result)
				support := 0
				for _, witness := range []uint64{100, 105, 102}[:count] {
					if witness >= height {
						support++
					}
				}
				// Any first two successful independent references suffice. Their
				// conservative lower height need not wait for the third sample.
				if err != nil || support < 2 || height < 100 {
					t.Fatal("funds finality trusted an unsupported or registry-provided height", height, support, err)
				}
			})
		}
	}
}

func TestIndependentDynamicHeightKeepsTwoValidReferencesWhenThirdTimesOut(t *testing.T) {
	fixture := newVerificationFixture(t, 3)
	fixture.setAll("eth_blockNumber", "0x64")
	fixture.transport.nodes[fixture.anchors[1]].results["eth_blockNumber"] = "0x69"
	fixture.transport.nodes[fixture.anchors[2]].wait["eth_blockNumber"] = true
	started := time.Now()
	result, err := fixture.pool.Call(t.Context(), "eth_blockNumber", []any{})
	height, heightErr := evidenceHeight("eth_blockNumber", result)
	if err != nil || heightErr != nil || height != 100 {
		t.Fatal("a timed-out third operator discarded two independent supported heights", err, heightErr, height)
	}
	if time.Since(started) >= 2*time.Second {
		t.Fatal("two valid operators waited for a third stalled reference and consumed the reconcile budget")
	}
}

func TestConfigureEndpointsChangesTrustPolicyWithProvenance(t *testing.T) {
	fixture := newVerificationFixture(t, 2)
	fixture.setAll("eth_getLogs", []any{verificationLog(7, 1234567)})
	fixture.transport.nodes[fixture.primary].results["eth_getLogs"] = []any{}
	fixture.pool.ConfigureEndpoints(fixture.primary, false, 56, fixture.anchors)
	if fixture.pool.RequiresIndependentVerification() {
		t.Fatal("manual endpoint unexpectedly requires public references")
	}
	if _, err := fixture.pool.Call(t.Context(), "eth_getLogs", []any{}); err != nil {
		t.Fatal("manual trust behavior changed", err)
	}
	for _, anchor := range fixture.anchors {
		if fixture.callsTo(anchor) != 0 {
			t.Fatal("an operator-managed pool made an unsolicited reference call")
		}
	}
	fixture.pool.ConfigureEndpoints(fixture.primary, true, 56, fixture.anchors)
	if !fixture.pool.RequiresIndependentVerification() {
		t.Fatal("automatic endpoints inherited manual trust")
	}
	if _, err := fixture.pool.WithEndpoints(fixture.primary).Call(t.Context(), "eth_getLogs", []any{}); !errors.Is(err, ErrIndependentEvidence) {
		t.Fatal("automatic transition failed to require independent evidence", err)
	}
	fixture.pool.ConfigureEndpoints(fixture.primary, false, 56, fixture.anchors)
	before := 0
	for _, anchor := range fixture.anchors {
		before += fixture.callsTo(anchor)
	}
	if _, err := fixture.pool.Call(t.Context(), "eth_getLogs", []any{}); err != nil {
		t.Fatal("return to operator-managed endpoints retained automatic-only policy", err)
	}
	after := 0
	for _, anchor := range fixture.anchors {
		after += fixture.callsTo(anchor)
	}
	if before != after {
		t.Fatal("return to manual endpoints kept querying public references")
	}
}
