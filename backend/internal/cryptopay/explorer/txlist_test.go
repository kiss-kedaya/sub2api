package explorer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	txHash  = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	deposit = "0x1111111111111111111111111111111111111111"
	sender  = "0x3333333333333333333333333333333333333333"
)

func oneRow() string {
	return `{"blockNumber":"12345","hash":"` + txHash + `","from":"` + sender +
		`","to":"` + deposit + `","value":"1000000000000000000"}`
}

// captureServer answers with body and records the last query, so a test can
// assert the exact module/action/range the poller sends.
func captureServer(t *testing.T, body string, query *string, calls *int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		if query != nil {
			*query = r.URL.RawQuery
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func testClient(t *testing.T, serverURL, apiKey string, cfg Config) *Client {
	t.Helper()
	cfg.Endpoints = map[string]Endpoint{"bsc": {BaseURL: serverURL, ChainID: 56}}
	cfg.APIKey = apiKey
	if cfg.Sleep == nil {
		cfg.Sleep = func(context.Context, time.Duration) error { return nil }
	}
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func TestTxListParsesRowsAndSendsIncrementalRange(t *testing.T) {
	var query string
	var calls int32
	server := captureServer(t, `{"status":"1","message":"OK","result":[`+oneRow()+`]}`, &query, &calls)
	client := testClient(t, server.URL, "key-123", Config{})

	rows, err := client.TxList(t.Context(), "bsc", deposit, 12000, 12345)
	if err != nil {
		t.Fatalf("TxList: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Hash != txHash || row.From != sender || row.To == nil || *row.To != deposit {
		t.Fatalf("row = %+v", row)
	}
	if row.Value.String() != "1000000000000000000" || row.BlockNumber != 12345 {
		t.Fatalf("row value/block = %s/%d", row.Value, row.BlockNumber)
	}
	// The v2 API needs chainid; the range must be the incremental one.
	for _, want := range []string{
		"chainid=56", "module=account", "action=txlist",
		"address=" + deposit, "startblock=12000", "endblock=12345", "sort=asc", "apikey=key-123",
	} {
		if !strings.Contains(query, want) {
			t.Fatalf("query %q is missing %q", query, want)
		}
	}
	// One call: the polling cost must not scale with the block span.
	if calls != 1 {
		t.Fatalf("calls = %d, want exactly 1 per address window", calls)
	}
}

func TestTxListTreatsNoTransactionsAsEmpty(t *testing.T) {
	var calls int32
	server := captureServer(t, `{"status":"0","message":"No transactions found","result":[]}`, nil, &calls)
	client := testClient(t, server.URL, "key", Config{})
	rows, err := client.TxList(t.Context(), "bsc", deposit, 1, 2)
	if err != nil || len(rows) != 0 {
		t.Fatalf("empty answer = %v / %v", rows, err)
	}
	if calls != 1 {
		t.Fatalf("an empty answer must not be retried, calls = %d", calls)
	}
}

func TestTxListRetriesRateLimitWithBackoff(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			_, _ = w.Write([]byte(`{"status":"0","message":"NOTOK","result":"Max rate limit reached"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"1","message":"OK","result":[` + oneRow() + `]}`))
	}))
	t.Cleanup(server.Close)
	var sleeps []time.Duration
	cfg := Config{
		Retries: 3,
		Backoff: time.Second,
		Sleep: func(_ context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return nil
		},
	}
	client := testClient(t, server.URL, "key", cfg)
	rows, err := client.TxList(t.Context(), "bsc", deposit, 1, 2)
	if err != nil || len(rows) != 1 {
		t.Fatalf("retry did not recover: %v / %v", rows, err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if len(sleeps) != 1 || sleeps[0] != time.Second {
		t.Fatalf("backoff = %v, want one 1s wait", sleeps)
	}
}

func TestTxListStopsOnRejectedAPIKey(t *testing.T) {
	var calls int32
	server := captureServer(t, `{"status":"0","message":"NOTOK","result":"Invalid API Key"}`, nil, &calls)
	client := testClient(t, server.URL, "bad", Config{})
	_, err := client.TxList(t.Context(), "bsc", deposit, 1, 2)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if calls != 1 {
		t.Fatalf("a rejected key must not be retried, calls = %d", calls)
	}
}

func TestTxListRequiresAPIKeyBeforeAnyRequest(t *testing.T) {
	var calls int32
	server := captureServer(t, `{"status":"1","message":"OK","result":[]}`, nil, &calls)
	client := testClient(t, server.URL, "", Config{})
	_, err := client.TxList(t.Context(), "bsc", deposit, 1, 2)
	if !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("err = %v, want ErrNoAPIKey", err)
	}
	if calls != 0 {
		t.Fatalf("no key must mean no request, calls = %d", calls)
	}
}

func TestTxListRejectsMalformedRows(t *testing.T) {
	body := `{"status":"1","message":"OK","result":[{"blockNumber":"12","hash":"` + txHash +
		`","from":"` + sender + `","to":"` + deposit + `","value":"not-a-number"}]}`
	var calls int32
	server := captureServer(t, body, nil, &calls)
	client := testClient(t, server.URL, "key", Config{})
	if _, err := client.TxList(t.Context(), "bsc", deposit, 1, 2); err == nil {
		t.Fatal("a malformed value was accepted")
	}
	if calls != 1 {
		t.Fatalf("a malformed row is not transient, calls = %d", calls)
	}
}

func TestNewClientRejectsQueryInEndpoint(t *testing.T) {
	_, err := NewClient(Config{Endpoints: map[string]Endpoint{
		"bsc": {BaseURL: "https://api.bscscan.com/v2/api?chainid=56"},
	}})
	if err == nil {
		t.Fatal("an endpoint with a query string was accepted")
	}
}

func TestTxListIncrementalRangeIsEmptyBeforeStart(t *testing.T) {
	var calls int32
	server := captureServer(t, `{"status":"1","message":"OK","result":[]}`, nil, &calls)
	client := testClient(t, server.URL, "key", Config{})
	rows, err := client.TxList(t.Context(), "bsc", deposit, 20, 10)
	if err != nil || rows != nil {
		t.Fatalf("inverted range = %v / %v", rows, err)
	}
	if calls != 0 {
		t.Fatalf("inverted range must not call the provider, calls = %d", calls)
	}
}
