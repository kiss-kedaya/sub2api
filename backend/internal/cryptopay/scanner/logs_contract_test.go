package scanner

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

func logsContractPool(t *testing.T, logsResponse string) *rpc.Pool {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch request.Method {
		case "eth_chainId":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x38"}`))
		case "eth_blockNumber":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x6e"}`))
		case "eth_getLogs":
			_, _ = w.Write([]byte(logsResponse))
		default:
			t.Errorf("unexpected RPC during invalid-log scan: %s", request.Method)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	return rpc.NewPoolWithHTTPClient(server.URL, server.Client())
}

func TestDepositAndOutflowScansRejectNullOrMalformedLogResults(t *testing.T) {
	tr := testTransfer()
	for _, outgoing := range []bool{false, true} {
		for _, fixture := range []struct {
			name, response string
		}{
			{"null", `{"jsonrpc":"2.0","id":1,"result":null}`},
			{"missing", `{"jsonrpc":"2.0","id":1}`},
			{"wrong_type", `{"jsonrpc":"2.0","id":1,"result":{}}`},
			{"null_member", `{"jsonrpc":"2.0","id":1,"result":[null]}`},
			{"empty_object_member", `{"jsonrpc":"2.0","id":1,"result":[{}]}`},
		} {
			direction := "deposit/"
			if outgoing {
				direction = "outflow/"
			}
			t.Run(direction+fixture.name, func(t *testing.T) {
				s := NewEVMScanner(logsContractPool(t, fixture.response))
				var err error
				if outgoing {
					_, err = s.ScanOutgoingRange(t.Context(), tr.Chain, 100, 105, []string{tr.Token}, []string{tr.From})
				} else {
					_, err = s.ScanRange(t.Context(), tr.Chain, 100, 105, []string{tr.Token}, []string{tr.To})
				}
				var failure *rpc.Failure
				if err == nil || !errors.As(err, &failure) || failure.Kind != "response" {
					t.Fatal("invalid result was mistaken for a successfully checked range", err)
				}
			})
		}
	}
}

func TestInvalidLogResultCannotAdvancePaymentCursor(t *testing.T) {
	for _, response := range []string{
		`{"jsonrpc":"2.0","id":1,"result":null}`,
		`{"jsonrpc":"2.0","id":1}`,
		`{"jsonrpc":"2.0","id":1,"result":{}}`,
	} {
		t.Run(response, func(t *testing.T) {
			worker, mock, handler := workerFixture(t, newRPCFixture(testTransfer()))
			worker.pools["bsc"] = logsContractPool(t, response)
			mock.ExpectQuery(`SELECT last_block`).WithArgs("bsc").
				WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(100))
			var failure *rpc.Failure
			err := worker.ScanOnce(t.Context())
			if err == nil || !errors.As(err, &failure) || failure.Kind != "response" {
				t.Fatal("invalid logs allowed the payment scan to report success", err)
			}
			// No cursor write is expected. sqlmock rejects an attempted advance,
			// and it would replace the required RPC failure above with a DB error.
			if len(handler.pending)+len(handler.processed) != 0 {
				t.Fatal("invalid scan caused a payment persistence attempt")
			}
		})
	}
}

func TestValidEmptyLogArrayStillAdvancesPaymentCursor(t *testing.T) {
	worker, mock, handler := workerFixture(t, newRPCFixture(testTransfer()))
	worker.pools["bsc"] = logsContractPool(t, `{"jsonrpc":"2.0","id":1,"result":[]}`)
	mock.ExpectQuery(`SELECT last_block`).WithArgs("bsc").
		WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(100))
	mock.ExpectExec(`INSERT INTO crypto_scan_cursors`).WithArgs("bsc", int64(105)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := worker.ScanOnce(t.Context()); err != nil {
		t.Fatal("verified empty scan could not advance its cursor", err)
	}
	if len(handler.pending)+len(handler.processed) != 0 {
		t.Fatal("empty logs created a payment")
	}
}
