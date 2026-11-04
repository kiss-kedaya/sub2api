package cryptopay

import (
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/stretchr/testify/require"
)

// statusHarness is a Service wired to a mocked database and fake RPC nodes.
// Nothing here touches a real database, a real RPC provider or the network.
type statusHarness struct {
	svc   *Service
	mock  sqlmock.Sqlmock
	clock *fakeClock
	tick  *fakeTicker
}

type cursorRow struct {
	chain   string
	block   int64
	updated time.Time
}

func poolOf(endpoints ...string) *rpc.Pool { return rpc.NewPool(strings.Join(endpoints, ",")) }

func newStatusHarness(t *testing.T, cfg CryptoConfig, pools map[string]*rpc.Pool, nodes map[string]*fakeNode) *statusHarness {
	t.Helper()
	// Exact SQL matching: a read that turns into a write, or a third query,
	// fails the test instead of slipping through a loose regexp.
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	clock, tick := newFakeClock(), &fakeTicker{}
	svc := &Service{db: db, config: cfg, pools: pools}
	svc.chainMon.now = clock.Now
	svc.chainMon.newPool = poolFor(nodes)
	svc.chainMon.newTicker = tick.New
	t.Cleanup(svc.chainMon.stop)
	return &statusHarness{svc: svc, mock: mock, clock: clock, tick: tick}
}

func (h *statusHarness) expectCursors(rows ...cursorRow) {
	r := sqlmock.NewRows([]string{"chain", "last_block", "updated_at"})
	for _, c := range rows {
		r.AddRow(c.chain, c.block, c.updated)
	}
	h.mock.ExpectQuery(chainCursorQuery).WillReturnRows(r)
}

func (h *statusHarness) expectCursorsError(err error) {
	h.mock.ExpectQuery(chainCursorQuery).WillReturnError(err)
}

func (h *statusHarness) expectPending(counts map[string]int64) {
	r := sqlmock.NewRows([]string{"chain", "count"})
	for chain, n := range counts {
		r.AddRow(chain, n)
	}
	h.mock.ExpectQuery(chainPendingQuery).WillReturnRows(r)
}

func (h *statusHarness) expectPendingError(err error) {
	h.mock.ExpectQuery(chainPendingQuery).WillReturnError(err)
}

// byChain indexes a report by chain name.
func byChain(rep *ChainStatusReport) map[string]ChainStatus {
	out := make(map[string]ChainStatus, len(rep.Chains))
	for _, c := range rep.Chains {
		out[c.Chain] = c
	}
	return out
}

func chainNames(rep *ChainStatusReport) []string {
	names := make([]string, 0, len(rep.Chains))
	for _, c := range rep.Chains {
		names = append(names, c.Chain)
	}
	return names
}

// bscOnly is a config with a single enabled chain and its pool.
func bscOnly(endpoints ...string) (CryptoConfig, map[string]*rpc.Pool) {
	cfg := DefaultCryptoConfig()
	cfg.Chains = []string{ChainBSC}
	cfg.RPC = map[string]string{ChainBSC: strings.Join(endpoints, ",")}
	return cfg, map[string]*rpc.Pool{ChainBSC: poolOf(endpoints...)}
}
