//go:build unit

package openai_ws_v2

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

// closeNotifyFrameConn reports the moment the relay closes it, so a test can tell
// "closed on the spot" from "left open to drain".
type closeNotifyFrameConn struct {
	*passthroughTestFrameConn
	closed chan struct{}
	once   sync.Once
}

func newCloseNotifyFrameConn() *closeNotifyFrameConn {
	return &closeNotifyFrameConn{
		passthroughTestFrameConn: newPassthroughTestFrameConn(nil, false),
		closed:                   make(chan struct{}),
	}
}

func (c *closeNotifyFrameConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.passthroughTestFrameConn.Close()
}

// push hands a frame to whoever reads this connection next.
func (c *passthroughTestFrameConn) push(payload string) {
	c.readCh <- passthroughTestFrame{msgType: coderws.MessageText, payload: []byte(payload)}
}

// failingWriteFrameConn stands in for a client connection whose writes fail.
type failingWriteFrameConn struct {
	*passthroughTestFrameConn
	err error
}

func (c *failingWriteFrameConn) WriteFrame(context.Context, coderws.MessageType, []byte) error {
	return c.err
}

type relayRun struct {
	result RelayResult
	exit   *RelayExit
}

type turnRecorder struct {
	mu    sync.Mutex
	turns []RelayTurnResult
}

func (r *turnRecorder) record(turn RelayTurnResult) {
	r.mu.Lock()
	r.turns = append(r.turns, turn)
	r.mu.Unlock()
}

func (r *turnRecorder) snapshot() []RelayTurnResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]RelayTurnResult(nil), r.turns...)
}

const (
	goneFirstPayload = `{"type":"response.create","model":"gpt-5.4","input":"hello"}`
	goneCreated      = `{"type":"response.created","response":{"id":"resp_gone","model":"gpt-5.4"}}`
	goneDelta        = `{"type":"response.output_text.delta","delta":"partial answer"}`
	goneCompleted    = `{"type":"response.completed","response":{"id":"resp_gone","model":"gpt-5.4","usage":{"input_tokens":6,"output_tokens":4}}}`
)

func startGoneRelay(t *testing.T, client, upstream FrameConn, recorder *turnRecorder) <-chan relayRun {
	t.Helper()
	done := make(chan relayRun, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		result, exit := Relay(ctx, client, upstream, []byte(goneFirstPayload), RelayOptions{
			FirstMessageSent:                true,
			StartClientAfterFirstDownstream: true,
			OnTurnComplete:                  recorder.record,
		})
		done <- relayRun{result: result, exit: exit}
	}()
	return done
}

func awaitWrites(t *testing.T, conn *passthroughTestFrameConn, n int) {
	t.Helper()
	require.Eventually(t, func() bool { return len(conn.Writes()) >= n }, 3*time.Second, 5*time.Millisecond)
}

func awaitRelay(t *testing.T, done <-chan relayRun, within time.Duration) relayRun {
	t.Helper()
	select {
	case run := <-done:
		return run
	case <-time.After(within):
		t.Fatal("the relay did not return")
		return relayRun{}
	}
}

// The client leaves in the middle of a turn. The upstream connection is closed at
// once (it still has frames to send and a terminal event that would never be read),
// and the turn in flight is reported as abandoned with what was observed so far.
func TestRelay_ClientLeavesMidTurnClosesUpstreamAndReportsAbandonedTurn(t *testing.T) {
	t.Parallel()

	client := newPassthroughTestFrameConn(nil, false)
	upstream := newCloseNotifyFrameConn()
	recorder := &turnRecorder{}
	done := startGoneRelay(t, client, upstream, recorder)

	upstream.push(goneCreated)
	upstream.push(goneDelta)
	awaitWrites(t, client, 2)
	leftAt := time.Now()
	require.NoError(t, client.Close())

	run := awaitRelay(t, done, 2*time.Second)
	require.Less(t, time.Since(leftAt), time.Second, "no drain window")
	select {
	case <-upstream.closed:
	default:
		t.Fatal("the upstream connection was left open after the client left")
	}
	require.Nil(t, run.exit, "a client that left is not a relay failure")

	turns := recorder.snapshot()
	require.Len(t, turns, 1)
	turn := turns[0]
	require.True(t, turn.Abandoned)
	require.Equal(t, "resp_gone", turn.RequestID)
	require.Equal(t, "gpt-5.4", turn.RequestModel)
	require.Empty(t, turn.TerminalEventType)
	require.Equal(t, Usage{}, turn.Usage, "nothing the upstream reported is invented")
	require.NotNil(t, turn.Output)
	require.True(t, turn.Output.HasContent())
	require.Equal(t, "partial answer", turn.Output.BuildOutput()[0].Content[0].Text)
	require.NotNil(t, turn.FirstTokenMs)
	require.Equal(t, int64(2), run.result.UpstreamToClientFrames)
}

// A client that leaves between two turns has nothing in flight: no turn is reported
// as abandoned, so nothing is estimated.
func TestRelay_ClientLeavesBetweenTurnsReportsNoAbandonedTurn(t *testing.T) {
	t.Parallel()

	client := newPassthroughTestFrameConn(nil, false)
	upstream := newCloseNotifyFrameConn()
	recorder := &turnRecorder{}
	done := startGoneRelay(t, client, upstream, recorder)

	upstream.push(goneCreated)
	upstream.push(goneDelta)
	upstream.push(goneCompleted)
	awaitWrites(t, client, 3)
	require.NoError(t, client.Close())

	run := awaitRelay(t, done, 2*time.Second)
	select {
	case <-upstream.closed:
	default:
		t.Fatal("the upstream connection was left open after the client left")
	}
	require.Nil(t, run.exit)
	turns := recorder.snapshot()
	require.Len(t, turns, 1, "the finished turn is settled once and nothing else is added")
	require.False(t, turns[0].Abandoned)
	require.Equal(t, "response.completed", turns[0].TerminalEventType)
	require.Equal(t, Usage{InputTokens: 6, OutputTokens: 4}, turns[0].Usage)
}

// A finished turn keeps its settlement and the later turn that is cut off is reported
// on its own, with only its own content.
func TestRelay_ClientLeavesDuringLaterTurnKeepsEarlierTurnSettled(t *testing.T) {
	t.Parallel()

	client := newPassthroughTestFrameConn(nil, false)
	upstream := newCloseNotifyFrameConn()
	recorder := &turnRecorder{}
	done := startGoneRelay(t, client, upstream, recorder)

	upstream.push(goneCreated)
	upstream.push(goneDelta)
	upstream.push(goneCompleted)
	awaitWrites(t, client, 3)

	client.push(`{"type":"response.create","model":"gpt-5.4","input":"second"}`)
	awaitWrites(t, upstream.passthroughTestFrameConn, 1)
	upstream.push(`{"type":"response.created","response":{"id":"resp_second","model":"gpt-5.4"}}`)
	upstream.push(`{"type":"response.output_text.delta","delta":"second turn text"}`)
	awaitWrites(t, client, 5)
	require.NoError(t, client.Close())

	run := awaitRelay(t, done, 2*time.Second)
	require.Nil(t, run.exit)
	turns := recorder.snapshot()
	require.Len(t, turns, 2)
	require.False(t, turns[0].Abandoned)
	require.Equal(t, "resp_gone", turns[0].RequestID)
	require.Equal(t, Usage{InputTokens: 6, OutputTokens: 4}, turns[0].Usage)
	require.True(t, turns[1].Abandoned)
	require.Equal(t, "resp_second", turns[1].RequestID)
	require.Equal(t, Usage{}, turns[1].Usage)
	require.NotNil(t, turns[1].Output)
	require.Equal(t, "second turn text", turns[1].Output.BuildOutput()[0].Content[0].Text,
		"the abandoned turn carries its own content, not the finished turn's")
}

// A write to the client that fails the way a closed connection does means the client
// left, even when its reader had not noticed yet.
func TestRelay_ClientWriteFailingLikeADisconnectAbandonsTheTurn(t *testing.T) {
	t.Parallel()

	client := &failingWriteFrameConn{
		passthroughTestFrameConn: newPassthroughTestFrameConn(nil, false),
		err:                      errors.New("write: broken pipe"),
	}
	upstream := newCloseNotifyFrameConn()
	recorder := &turnRecorder{}
	done := startGoneRelay(t, client, upstream, recorder)

	upstream.push(goneCreated)
	run := awaitRelay(t, done, 2*time.Second)
	select {
	case <-upstream.closed:
	default:
		t.Fatal("the upstream connection was left open after the client left")
	}
	require.Nil(t, run.exit)
	turns := recorder.snapshot()
	require.Len(t, turns, 1)
	require.True(t, turns[0].Abandoned)
	require.Equal(t, "resp_gone", turns[0].RequestID)
}

// A write that merely timed out says nothing about the client leaving: it stays a
// relay failure and no turn is reported as abandoned.
func TestRelay_ClientWriteTimeoutIsNotTheClientLeaving(t *testing.T) {
	t.Parallel()

	client := &failingWriteFrameConn{
		passthroughTestFrameConn: newPassthroughTestFrameConn(nil, false),
		err:                      context.DeadlineExceeded,
	}
	upstream := newCloseNotifyFrameConn()
	recorder := &turnRecorder{}
	done := startGoneRelay(t, client, upstream, recorder)

	upstream.push(goneCreated)
	run := awaitRelay(t, done, 2*time.Second)
	require.NotNil(t, run.exit)
	require.Equal(t, "write_client", run.exit.Stage)
	require.Empty(t, recorder.snapshot())
}
