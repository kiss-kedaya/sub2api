package sweeper

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/bitget"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/stretchr/testify/require"
)

// Observation fixtures deliberately cannot derive keys or send transactions.
// Signature/MAC checks use the real wallet tests; this suite checks how their
// outcomes drive the outflow scanner's authorize, wait, or incident decision.
type observedBitgetProvider struct {
	*fixtureGaslessProvider
	states map[string]*bitget.OrderStatus
	errors map[string]error
	reads  []string
}

func (p *observedBitgetProvider) Status(_ context.Context, id string) (*bitget.OrderStatus, error) {
	p.reads = append(p.reads, id)
	return p.states[id], p.errors[id]
}

type observedBitgetWallet struct {
	*fixtureGaslessWallet
	verifyErrors map[int64]error
	verified     []int64
	bindError    error
}

func (w *observedBitgetWallet) VerifyBitgetRecord(_ context.Context, jobID int64, _ string, _ bitget.Intent, _ string) error {
	w.verified = append(w.verified, jobID)
	return w.verifyErrors[jobID]
}

func (w *observedBitgetWallet) BindBitgetTransaction(_ context.Context, _ int64, _ string, _ string, _ bitget.Intent) error {
	w.binds++
	return w.bindError
}

type observedBitgetJob struct {
	id     int64
	order  string
	in     bitget.Intent
	bound  string
	signed string
}

func observedBitgetIntent(created int64) bitget.Intent {
	return bitget.Intent{
		Chain: "bsc", ChainID: 56, Index: 7,
		From: "0x3333333333333333333333333333333333333333", To: testDestination,
		Token: "0x55d398326f99059ff775485246999027b3197955", Decimals: 18,
		Net: "9950000000000000000", Fee: "50000000000000000", Gross: "10000000000000000000",
		MaxFee: "0.5", MaxFeeBPS: 100, CreatedAt: created,
	}
}

func newObservedBitgetExecutor(t *testing.T) (*Executor, sqlmock.Sqlmock, *observedBitgetProvider, *observedBitgetWallet) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		_ = db.Close()
	})
	p := &observedBitgetProvider{fixtureGaslessProvider: &fixtureGaslessProvider{}, states: map[string]*bitget.OrderStatus{}, errors: map[string]error{}}
	w := &observedBitgetWallet{fixtureGaslessWallet: &fixtureGaslessWallet{}, verifyErrors: map[int64]error{}}
	e := &Executor{db: db, wallet: w, gasless: p, config: Config{Provider: "bitget"}}
	return e, mock, p, w
}

func expectObservedBitgetJobs(t *testing.T, mock sqlmock.Sqlmock, in bitget.Intent, hash string, jobs ...observedBitgetJob) {
	t.Helper()
	rows := sqlmock.NewRows([]string{"job_id", "order_id", "intent", "bound_tx_hash", "signed_messages"})
	for _, j := range jobs {
		raw, err := json.Marshal(j.in)
		require.NoError(t, err)
		signed := j.signed
		if signed == "" {
			signed = `[{"msgType":"call","sig":"offline-fixture"}]`
		}
		rows.AddRow(j.id, j.order, raw, j.bound, signed)
	}
	mock.ExpectQuery("SELECT b.job_id,b.order_id,b.intent,b.bound_tx_hash,b.signed_messages").
		WithArgs(in.Chain, in.From, hash).WillReturnRows(rows).RowsWillBeClosed()
}

func assertBitgetObservationCannotSend(t *testing.T, p *observedBitgetProvider, w *observedBitgetWallet) {
	t.Helper()
	require.Zero(t, p.prepared)
	require.Zero(t, p.submitted)
	require.Zero(t, w.signs)
	require.Zero(t, w.submissions)
}

func TestBitgetOutflowObservationSurvivesSwitchToNativeProvider(t *testing.T) {
	for _, provider := range []string{"bitget", "native"} {
		t.Run(provider, func(t *testing.T) {
			e, mock, p, w := newObservedBitgetExecutor(t)
			e.config.Provider = provider
			in := observedBitgetIntent(time.Now().Unix())
			hash := "0x" + strings.Repeat("cd", 32)
			p.states["confirmed-order"] = &bitget.OrderStatus{ID: "confirmed-order", Status: "SUCCESS", Hash: hash}
			expectObservedBitgetJobs(t, mock, in, hash, observedBitgetJob{id: 11, order: "confirmed-order", in: in})
			pool := bitgetReceiptPool(t, in, hash, 103, testBlockHash, nil)
			bound, err := e.AuthorizeGaslessObserved(t.Context(), pool, in.Chain, hash, in.From)
			require.NoError(t, err)
			require.True(t, bound, "a prior signed Bitget order stays observable after changing providers")
			require.Equal(t, []string{"confirmed-order"}, p.reads)
			require.Equal(t, []int64{11}, w.verified)
			require.Equal(t, 1, w.binds)
			assertBitgetObservationCannotSend(t, p, w)
		})
	}
}

func TestBitgetExpiredUnknownOrdersCannotPermanentlySuppressOutflowIncident(t *testing.T) {
	for _, name := range []string{"task id", "no hash", "provider unavailable", "malformed status"} {
		t.Run(name, func(t *testing.T) {
			e, mock, p, w := newObservedBitgetExecutor(t)
			in := observedBitgetIntent(time.Now().Add(-11 * time.Minute).Unix())
			hash := "0x" + strings.Repeat("ab", 32)
			status := &bitget.OrderStatus{ID: "expired-order", Status: "PROCESSING"}
			switch name {
			case "task id":
				status.TxID = "getgas_task_offline"
			case "provider unavailable":
				p.errors[status.ID] = bitget.ErrUnavailable
			case "malformed status":
				status.Status = "UNRECOGNIZED"
			}
			p.states[status.ID] = status
			expectObservedBitgetJobs(t, mock, in, hash, observedBitgetJob{id: 12, order: status.ID, in: in})
			bound, err := e.AuthorizeGaslessObserved(t.Context(), nil, in.Chain, hash, in.From)
			require.NoError(t, err, "expired unverifiable evidence must allow the caller to lock and alert")
			require.False(t, bound)
			require.Zero(t, w.binds)
			assertBitgetObservationCannotSend(t, p, w)
		})
	}
}

func TestBitgetFreshUnknownOrderWaitsOnlyWhenNoLaterExactMatchExists(t *testing.T) {
	for _, matched := range []bool{false, true} {
		name := "wait for propagation"
		if matched {
			name = "continue to exact match"
		}
		t.Run(name, func(t *testing.T) {
			e, mock, p, w := newObservedBitgetExecutor(t)
			in := observedBitgetIntent(time.Now().Unix())
			hash := "0x" + strings.Repeat("ef", 32)
			p.states["newer-unknown"] = &bitget.OrderStatus{ID: "newer-unknown", Status: "PENDING", TxID: "getgas_task_offline"}
			jobs := []observedBitgetJob{{id: 20, order: "newer-unknown", in: in}}
			if matched {
				p.states["older-exact"] = &bitget.OrderStatus{ID: "older-exact", Status: "SUCCESS", Hash: hash}
				jobs = append(jobs, observedBitgetJob{id: 19, order: "older-exact", in: in})
			}
			expectObservedBitgetJobs(t, mock, in, hash, jobs...)
			pool := bitgetReceiptPool(t, in, hash, 103, testBlockHash, nil)
			bound, err := e.AuthorizeGaslessObserved(t.Context(), pool, in.Chain, hash, in.From)
			if matched {
				require.NoError(t, err)
				require.True(t, bound)
				require.Equal(t, []string{"newer-unknown", "older-exact"}, p.reads)
				require.Equal(t, 1, w.binds)
			} else {
				require.ErrorIs(t, err, bitget.ErrUnavailable)
				require.False(t, bound)
				require.Zero(t, w.binds)
			}
			assertBitgetObservationCannotSend(t, p, w)
		})
	}
}

func TestBitgetExactHashWithExtraOutflowReachesIncidentInsteadOfStoppingCursor(t *testing.T) {
	e, mock, p, w := newObservedBitgetExecutor(t)
	in := observedBitgetIntent(time.Now().Unix())
	hash := "0x" + strings.Repeat("ba", 32)
	p.states["extra-outflow"] = &bitget.OrderStatus{ID: "extra-outflow", Status: "SUCCESS", Hash: hash}
	expectObservedBitgetJobs(t, mock, in, hash, observedBitgetJob{id: 21, order: "extra-outflow", in: in})
	pool := bitgetReceiptPool(t, in, hash, 103, testBlockHash, func(r map[string]any) {
		logs, ok := r["logs"].([]any)
		require.True(t, ok)
		r["logs"] = append(logs, logs[0]) // Third transfer is outside net plus quoted fee.
	})
	bound, err := e.AuthorizeGaslessObserved(t.Context(), pool, in.Chain, hash, in.From)
	require.NoError(t, err, "permanent semantic mismatch must reach the caller's lock and Bark path")
	require.False(t, bound)
	require.Zero(t, w.binds)
	assertBitgetObservationCannotSend(t, p, w)
}

func TestBitgetUnsafeMACOrBindingCannotPermanentlySuppressOutflowIncident(t *testing.T) {
	for _, name := range []string{"MAC mismatch", "binding mismatch", "temporary audit failure"} {
		t.Run(name, func(t *testing.T) {
			e, mock, p, w := newObservedBitgetExecutor(t)
			in := observedBitgetIntent(time.Now().Unix())
			hash := "0x" + strings.Repeat("fa", 32)
			p.states["authenticated-order"] = &bitget.OrderStatus{ID: "authenticated-order", Status: "SUCCESS", Hash: hash}
			switch name {
			case "MAC mismatch":
				w.verifyErrors[22] = errors.Join(errors.New("offline MAC mismatch"), bitget.ErrUnsafeOrder)
			case "binding mismatch":
				w.bindError = bitget.ErrUnsafeOrder
			case "temporary audit failure":
				w.verifyErrors[22] = wallet.ErrAuditUnavailable
			}
			expectObservedBitgetJobs(t, mock, in, hash, observedBitgetJob{id: 22, order: "authenticated-order", in: in})
			pool := bitgetReceiptPool(t, in, hash, 103, testBlockHash, nil)
			bound, err := e.AuthorizeGaslessObserved(t.Context(), pool, in.Chain, hash, in.From)
			if name == "temporary audit failure" {
				require.ErrorIs(t, err, wallet.ErrAuditUnavailable, "temporary database failures must not manufacture a theft incident")
			} else {
				require.NoError(t, err, "unsafe permanent authorization must fall through to the incident path")
			}
			require.False(t, bound)
			if name != "binding mismatch" {
				require.Empty(t, p.reads)
				require.Zero(t, w.binds)
			}
			assertBitgetObservationCannotSend(t, p, w)
		})
	}
}

func TestBitgetInvalidPersistedIntentCannotPermanentlySuppressOutflowIncident(t *testing.T) {
	e, mock, p, w := newObservedBitgetExecutor(t)
	in := observedBitgetIntent(time.Now().Unix())
	hash := "0x" + strings.Repeat("da", 32)
	// This is valid JSONB, but it cannot be an authenticated intent.
	rows := sqlmock.NewRows([]string{"job_id", "order_id", "intent", "bound_tx_hash", "signed_messages"}).
		AddRow(int64(23), "invalid-intent", []byte(`{"chain":7}`), "", "offline-fixture")
	mock.ExpectQuery("SELECT b.job_id,b.order_id,b.intent,b.bound_tx_hash,b.signed_messages").
		WithArgs(in.Chain, in.From, hash).WillReturnRows(rows).RowsWillBeClosed()
	bound, err := e.AuthorizeGaslessObserved(t.Context(), nil, in.Chain, hash, in.From)
	require.NoError(t, err, "unparseable durable authorization must reach the caller's lock and Bark path")
	require.False(t, bound)
	require.Empty(t, w.verified)
	require.Empty(t, p.reads)
	assertBitgetObservationCannotSend(t, p, w)
}
