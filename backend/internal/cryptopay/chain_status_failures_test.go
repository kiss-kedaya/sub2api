package cryptopay

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChainStatusWhenEveryNodeFails(t *testing.T) {
	a, b, c := "https://rpc.ankr.com/bsc/SECRETKEY", "https://bsc.drpc.org/SECRETKEY", "https://1rpc.io/SECRETKEY"
	cfg, pools := bscOnly(a, b, c)
	h := newStatusHarness(t, cfg, pools, map[string]*fakeNode{
		a: newFakeNode(t, fakeNodeOpts{status: 500}),
		b: newFakeNode(t, fakeNodeOpts{status: 502}),
		c: newFakeNode(t, fakeNodeOpts{status: 429}),
	})
	now := h.clock.Now()
	h.expectCursors(cursorRow{ChainBSC, 500, now.Add(-30 * time.Second)})
	h.expectPending(nil)

	rep, err := h.svc.ChainStatus(context.Background())
	require.NoError(t, err)
	require.NoError(t, h.mock.ExpectationsWereMet())
	bsc := byChain(rep)[ChainBSC]
	require.Equal(t, ChainStateError, bsc.State)
	require.Equal(t, &ChainLastError{Kind: NodeErrHTTP5xx, Stage: ErrStageHead}, bsc.LastError, "two 5xx outvote one 429")
	require.Nil(t, bsc.Head)
	require.Nil(t, bsc.ConfirmedHead)
	require.Nil(t, bsc.Unscanned)
	require.Nil(t, bsc.ProgressPct)
	require.Nil(t, bsc.ETASeconds)
	require.Equal(t, uint64(500), *bsc.Cursor, "the stored cursor is still shown")
	require.Equal(t, int64(30), *bsc.StalledSeconds)
	kinds := []string{*bsc.Nodes[0].ErrorKind, *bsc.Nodes[1].ErrorKind, *bsc.Nodes[2].ErrorKind}
	require.Equal(t, []string{NodeErrHTTP5xx, NodeErrHTTP5xx, NodeErrHTTP429}, kinds)
	require.Equal(t, ChainStatusSummary{Enabled: 1, Error: 1}, rep.Summary)
}

func TestChainStatusCursorReadFailureNeverLeaksTheDriverError(t *testing.T) {
	a := "https://rpc.ankr.com/bsc/SECRETKEY"
	cfg, pools := bscOnly(a)
	h := newStatusHarness(t, cfg, pools, map[string]*fakeNode{a: newFakeNode(t, fakeNodeOpts{chainID: 56, head: 10_000})})
	h.expectCursorsError(errors.New(`pq: password authentication failed for user "payadmin" host=db.internal.example port=5432 password=DBPASSWORD`))
	h.expectPending(nil)

	rep, err := h.svc.ChainStatus(context.Background())
	require.NoError(t, err, "a database failure degrades the rows, it does not fail the page")
	require.NoError(t, h.mock.ExpectationsWereMet())
	bsc := byChain(rep)[ChainBSC]
	require.Equal(t, ChainStateError, bsc.State)
	require.Equal(t, &ChainLastError{Kind: ErrKindDB, Stage: ErrStageCursor}, bsc.LastError)
	require.Nil(t, bsc.Cursor)
	require.Nil(t, bsc.StalledSeconds)
	require.Nil(t, bsc.Unscanned, "no cursor, no backlog figure")
	require.Equal(t, uint64(10_000), *bsc.Head, "the nodes are healthy and still reported")
	require.Equal(t, NodeStatusOK, bsc.Nodes[0].Status)

	raw, err := json.Marshal(rep)
	require.NoError(t, err)
	for _, secret := range []string{"DBPASSWORD", "payadmin", "db.internal.example", "password authentication"} {
		require.NotContains(t, string(raw), secret)
	}
}

func TestChainStatusPendingReadFailureOnlyHidesThePendingCount(t *testing.T) {
	a := "https://rpc.ankr.com/bsc/SECRETKEY"
	cfg, pools := bscOnly(a)
	h := newStatusHarness(t, cfg, pools, map[string]*fakeNode{a: newFakeNode(t, fakeNodeOpts{chainID: 56, head: 10_000})})
	now := h.clock.Now()
	h.expectCursors(cursorRow{ChainBSC, 9_900, now.Add(-5 * time.Second)})
	h.expectPendingError(errors.New("connection reset"))

	rep, err := h.svc.ChainStatus(context.Background())
	require.NoError(t, err)
	bsc := byChain(rep)[ChainBSC]
	require.Equal(t, ChainStateSynced, bsc.State, "scan state does not depend on the pending count")
	require.Nil(t, bsc.PendingDeposits, "unknown is null, never a made-up zero")
	require.Nil(t, bsc.LastError)
	require.Equal(t, uint64(97), *bsc.Unscanned)
}

func TestChainStatusReturnsTheCallersCancellation(t *testing.T) {
	a := "https://rpc.ankr.com/bsc/SECRETKEY"
	cfg, pools := bscOnly(a)
	h := newStatusHarness(t, cfg, pools, map[string]*fakeNode{a: newFakeNode(t, fakeNodeOpts{chainID: 56, head: 1, delay: time.Second})})
	h.mock.MatchExpectationsInOrder(false)
	h.expectCursors()
	h.expectPending(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	rep, err := h.svc.ChainStatus(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Nil(t, rep)
}

func TestChainStatusNilServiceIsAnError(t *testing.T) {
	var s *Service
	rep, err := s.ChainStatus(context.Background())
	require.Error(t, err)
	require.Nil(t, rep)
}
