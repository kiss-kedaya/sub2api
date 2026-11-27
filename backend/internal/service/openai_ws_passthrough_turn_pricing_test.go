package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// startPassthroughHookRecordingServer 与 startPassthroughLifecycleServer 相同，
// 但把一组会记录调用的 hooks 传给 ingress，用于观察透传路径的 turn 回调。
func startPassthroughHookRecordingServer(
	t *testing.T,
	controlCtx context.Context,
	svc *OpenAIGatewayService,
	account *Account,
	hooks *OpenAIWSIngressHooks,
) (*httptest.Server, <-chan error) {
	t.Helper()
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			serverErr <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()

		msgType, firstMessage, err := ReadOpenAIWSClientMessage(
			controlCtx,
			conn,
			3*time.Second,
			coderws.StatusPolicyViolation,
			"missing first response.create message",
		)
		if err != nil {
			serverErr <- err
			return
		}
		if msgType != coderws.MessageText {
			serverErr <- errors.New("first message was not text")
			return
		}

		recorder := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(recorder)
		req := r.Clone(controlCtx)
		req.Header = req.Header.Clone()
		ginCtx.Request = req
		serverErr <- svc.ProxyResponsesWebSocketFromClient(controlCtx, ginCtx, conn, account, "sk-test", firstMessage, hooks)
	}))
	return server, serverErr
}

// TestPassthroughIngressReportsTurnStartedBeforeAfterTurn verifies that the
// first passthrough turn keeps the handshake admission semantics while
// preserving the turn-start timestamp used by billing. Subsequent turns are
// covered by TestPassthroughIngressRefreshesBeforeTurnAdmission.
func TestPassthroughIngressReportsTurnStartedBeforeAfterTurn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	controlCtx, cancelControl := context.WithCancelCause(context.Background())
	defer cancelControl(context.Canceled)

	upstream := newStagedPassthroughConn()
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_pricing","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`)

	var hooksMu sync.Mutex
	beforeTurnCalls := 0
	expectedTurnStartedAt := time.Date(2026, time.August, 17, 9, 59, 59, 0, time.UTC)
	type hookEvent struct {
		name      string
		turn      int
		startedAt time.Time
	}
	var hookEvents []hookEvent
	hooks := &OpenAIWSIngressHooks{
		InitialTurnStartedAt: expectedTurnStartedAt,
		TurnStarted: func(turn int, startedAt time.Time) {
			hooksMu.Lock()
			hookEvents = append(hookEvents, hookEvent{name: "TurnStarted", turn: turn, startedAt: startedAt})
			hooksMu.Unlock()
		},
		BeforeTurn: func(int) error {
			hooksMu.Lock()
			beforeTurnCalls++
			hooksMu.Unlock()
			return nil
		},
		AfterTurn: func(turn int, _ *OpenAIForwardResult, _ error) {
			hooksMu.Lock()
			hookEvents = append(hookEvents, hookEvent{name: "AfterTurn", turn: turn})
			hooksMu.Unlock()
		},
	}

	server, serverErr := startPassthroughHookRecordingServer(
		t,
		controlCtx,
		newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream),
		passthroughLifecycleAccount(),
		hooks,
	)
	defer server.Close()
	clientConn := dialPassthroughLifecycleClient(t, server)
	defer func() { _ = clientConn.CloseNow() }()

	event, err := readPassthroughLifecycleFrame(t, clientConn, 3*time.Second)
	require.NoError(t, err)
	require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())

	// 等待连接自然结束（inter-turn idle 超时），确保 AfterTurn 已提交。
	_, _ = readPassthroughLifecycleFrame(t, clientConn, 3*time.Second)
	select {
	case <-serverErr:
	case <-time.After(3 * time.Second):
		t.Fatal("passthrough ingress did not exit")
	}

	hooksMu.Lock()
	gotBefore := beforeTurnCalls
	gotEvents := append([]hookEvent(nil), hookEvents...)
	hooksMu.Unlock()

	require.Zero(t, gotBefore, "透传首轮不应重复调用 BeforeTurn")
	require.GreaterOrEqual(t, len(gotEvents), 2, "透传 ingress 应报告 TurnStarted 和 AfterTurn")
	require.Equal(t, "TurnStarted", gotEvents[0].name)
	require.Equal(t, expectedTurnStartedAt, gotEvents[0].startedAt, "TurnStarted 必须携带入口冻结的首轮开始时刻")
	require.Equal(t, "AfterTurn", gotEvents[1].name)
	require.Equal(t, gotEvents[0].turn, gotEvents[1].turn, "TurnStarted 后应提交同一 turn 的 AfterTurn")
}

func TestPassthroughIngressFreezesSubsequentTurnBeforeRequestPolicy(t *testing.T) {
	testPassthroughIngressFreezesSubsequentTurnBeforeRequestPolicy(t, coderws.MessageText)
}

func TestPassthroughIngressFreezesBinarySubsequentTurnBeforeRequestPolicy(t *testing.T) {
	testPassthroughIngressFreezesSubsequentTurnBeforeRequestPolicy(t, coderws.MessageBinary)
}

func testPassthroughIngressFreezesSubsequentTurnBeforeRequestPolicy(t *testing.T, secondMessageType coderws.MessageType) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	controlCtx, cancelControl := context.WithCancelCause(context.Background())
	defer cancelControl(context.Canceled)

	upstream := newStagedPassthroughConn()
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_first","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`)

	type turnStart struct {
		turn      int
		startedAt time.Time
	}
	turnStarts := make(chan turnStart, 2)
	beforeTurnCalls := make(chan int, 1)
	beforeRequestEntered := make(chan time.Time, 1)
	releaseBeforeRequest := make(chan struct{})
	hooks := &OpenAIWSIngressHooks{
		InitialTurnStartedAt: time.Now(),
		TurnStarted: func(turn int, startedAt time.Time) {
			turnStarts <- turnStart{turn: turn, startedAt: startedAt}
		},
		BeforeTurn: func(turn int) error {
			beforeTurnCalls <- turn
			return nil
		},
		BeforeRequest: func(turn int, payload []byte, _ string) ([]byte, error) {
			if turn == 2 {
				beforeRequestEntered <- time.Now()
				<-releaseBeforeRequest
			}
			return payload, nil
		},
	}

	server, serverErr := startPassthroughHookRecordingServer(
		t,
		controlCtx,
		newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream),
		passthroughLifecycleAccount(),
		hooks,
	)
	defer server.Close()
	clientConn := dialPassthroughLifecycleClient(t, server)
	defer func() { _ = clientConn.CloseNow() }()

	require.Equal(t, "response.create", gjson.GetBytes(requirePassthroughUpstreamWrite(t, upstream, 3*time.Second), "type").String())
	firstCompleted, err := readPassthroughLifecycleFrame(t, clientConn, 3*time.Second)
	require.NoError(t, err)
	require.Equal(t, "resp_first", gjson.GetBytes(firstCompleted, "response.id").String())
	select {
	case first := <-turnStarts:
		require.Equal(t, 1, first.turn)
	case <-time.After(time.Second):
		t.Fatal("first turn start was not reported")
	}

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, secondMessageType, []byte(`{"type":"response.create","model":"gpt-5.1","previous_response_id":"resp_first"}`))
	cancelWrite()
	require.NoError(t, err)

	var policyEnteredAt time.Time
	select {
	case turn := <-beforeTurnCalls:
		require.Equal(t, 2, turn)
	case <-time.After(time.Second):
		t.Fatal("second turn did not run BeforeTurn admission")
	}
	select {
	case policyEnteredAt = <-beforeRequestEntered:
	case <-time.After(time.Second):
		t.Fatal("second turn did not enter BeforeRequest")
	}
	close(releaseBeforeRequest)
	require.Equal(t, "response.create", gjson.GetBytes(requirePassthroughUpstreamWrite(t, upstream, 3*time.Second), "type").String())
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_second","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`)
	secondCompleted, err := readPassthroughLifecycleFrame(t, clientConn, 3*time.Second)
	require.NoError(t, err)
	require.Equal(t, "resp_second", gjson.GetBytes(secondCompleted, "response.id").String())

	select {
	case second := <-turnStarts:
		require.Equal(t, 2, second.turn)
		require.False(t, second.startedAt.After(policyEnteredAt), "第二轮开始时刻必须在 BeforeRequest 策略执行前冻结")
	case <-time.After(time.Second):
		t.Fatal("second turn start was not reported")
	}

	_ = clientConn.CloseNow()
	cancelControl(context.Canceled)
	select {
	case <-serverErr:
	case <-time.After(3 * time.Second):
		t.Fatal("passthrough ingress did not exit")
	}
}

func TestPassthroughIngressPrepareTurnPayloadBeforeTurnAndInitialTurnReplay(t *testing.T) {
	t.Run("PrepareTurnPayload provides real turn 2 payload and model before BeforeTurn runs once", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		controlCtx, cancelControl := context.WithCancelCause(context.Background())
		defer cancelControl(context.Canceled)

		upstream := newStagedPassthroughConn()
		upstream.Send(`{"type":"response.completed","response":{"id":"resp_1","model":"gpt-4o-mini","usage":{"input_tokens":1,"output_tokens":1}}}`)

		var mu sync.Mutex
		preparedPayloads := map[int][]byte{}
		preparedModels := map[int]string{}
		beforeTurnSeenModel := ""
		beforeTurnSeenLen := 0
		beforeTurnCount := 0
		turn2Completed := make(chan int, 1)

		hooks := &OpenAIWSIngressHooks{
			InitialRequestModel:  "gpt-4o-mini",
			InitialTurnStartedAt: time.Now(),
			PrepareTurnPayload: func(turn int, payload []byte, originalModel string) {
				mu.Lock()
				preparedPayloads[turn] = append([]byte(nil), payload...)
				preparedModels[turn] = originalModel
				mu.Unlock()
			},
			BeforeTurn: func(turn int) error {
				mu.Lock()
				beforeTurnCount++
				beforeTurnSeenModel = preparedModels[turn]
				beforeTurnSeenLen = len(preparedPayloads[turn])
				mu.Unlock()
				return nil
			},
			BeforeRequest: func(_ int, payload []byte, _ string) ([]byte, error) {
				return payload, nil
			},
			AfterTurn: func(turn int, _ *OpenAIForwardResult, _ error) {
				if turn == 2 {
					turn2Completed <- turn
				}
			},
		}

		server, serverErr := startPassthroughHookRecordingServer(
			t,
			controlCtx,
			newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream),
			passthroughLifecycleAccount(),
			hooks,
		)
		defer server.Close()
		clientConn := dialPassthroughLifecycleClient(t, server)
		defer func() { _ = clientConn.CloseNow() }()

		require.Equal(t, "response.create", gjson.GetBytes(requirePassthroughUpstreamWrite(t, upstream, 3*time.Second), "type").String())
		_, err := readPassthroughLifecycleFrame(t, clientConn, 3*time.Second)
		require.NoError(t, err)

		turn2Payload := `{"type":"response.create","model":"gpt-5.4-pro","max_output_tokens":16000,"input":"expensive turn 2"}`
		writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
		err = clientConn.Write(writeCtx, coderws.MessageText, []byte(turn2Payload))
		cancelWrite()
		require.NoError(t, err)

		require.Equal(t, "response.create", gjson.GetBytes(requirePassthroughUpstreamWrite(t, upstream, 3*time.Second), "type").String())
		upstream.Send(`{"type":"response.completed","response":{"id":"resp_2","model":"gpt-5.4-pro","usage":{"input_tokens":10,"output_tokens":20}}}`)
		_, err = readPassthroughLifecycleFrame(t, clientConn, 3*time.Second)
		require.NoError(t, err)

		select {
		case <-turn2Completed:
		case <-time.After(3 * time.Second):
			t.Fatal("turn 2 did not complete")
		}
		_ = clientConn.CloseNow()
		cancelControl(context.Canceled)
		<-serverErr

		mu.Lock()
		defer mu.Unlock()
		require.Equal(t, 1, beforeTurnCount, "BeforeTurn must run once per subsequent turn (no duplicate call)")
		require.Equal(t, "gpt-5.4-pro", beforeTurnSeenModel, "BeforeTurn must see turn 2's actual model")
		require.Equal(t, len(turn2Payload), beforeTurnSeenLen, "BeforeTurn must see turn 2's actual payload bytes")
	})

	t.Run("InitialTurnNumber > 1 preserves switched turn 2 model, runs hooks before MapRequestModel once, and skips upstream dial on reject", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		controlCtx, cancelControl := context.WithCancelCause(context.Background())
		defer cancelControl(context.Canceled)

		upstream := newStagedPassthroughConn()
		upstream.Send(`{"type":"response.completed","response":{"id":"resp_replay_2","model":"gpt-5.4-pro-upstream","usage":{"input_tokens":5,"output_tokens":5}}}`)

		var mu sync.Mutex
		var calls []string
		var gotTurnNumbers []int
		var prepareModel, beforeReqModel, mapReqModel string
		hooks := &OpenAIWSIngressHooks{
			InitialRequestModel:  "gpt-4o-mini", // Turn 1 cheap model; Turn 2 replay switches to gpt-5.4-pro!
			InitialTurnStartedAt: time.Now(),
			InitialTurnNumber:    2,
			PrepareTurnPayload: func(turn int, payload []byte, originalModel string) {
				mu.Lock()
				calls = append(calls, "PrepareTurnPayload")
				gotTurnNumbers = append(gotTurnNumbers, turn)
				prepareModel = originalModel
				mu.Unlock()
			},
			BeforeTurn: func(turn int) error {
				mu.Lock()
				calls = append(calls, "BeforeTurn")
				gotTurnNumbers = append(gotTurnNumbers, turn)
				mu.Unlock()
				return nil
			},
			BeforeRequest: func(turn int, payload []byte, originalModel string) ([]byte, error) {
				mu.Lock()
				calls = append(calls, "BeforeRequest")
				gotTurnNumbers = append(gotTurnNumbers, turn)
				beforeReqModel = originalModel
				mu.Unlock()
				return payload, nil
			},
			MapRequestModel: func(turn int, originalModel string) (string, error) {
				mu.Lock()
				calls = append(calls, "MapRequestModel")
				gotTurnNumbers = append(gotTurnNumbers, turn)
				mapReqModel = originalModel
				mu.Unlock()
				return originalModel + "-upstream", nil
			},
			AfterTurn: func(turn int, res *OpenAIForwardResult, _ error) {
				if res == nil {
					return
				}
				mu.Lock()
				calls = append(calls, "AfterTurn")
				gotTurnNumbers = append(gotTurnNumbers, turn)
				mu.Unlock()
			},
		}

		server, serverErr := startPassthroughHookRecordingServer(
			t,
			controlCtx,
			newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream),
			passthroughLifecycleAccount(),
			hooks,
		)
		defer server.Close()
		replayPayload := `{"type":"response.create","model":"gpt-5.4-pro","input":"replayed turn 2"}`
		clientConn := dialPassthroughLifecycleClientWithPayload(t, server, replayPayload)
		defer func() { _ = clientConn.CloseNow() }()

		sentFrame := requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
		require.Equal(t, "response.create", gjson.GetBytes(sentFrame, "type").String())
		require.Equal(t, "gpt-5.4-pro-upstream", gjson.GetBytes(sentFrame, "model").String(),
			"upstream must receive mapped turn 2 model, never overwritten by turn 1 InitialRequestModel")
		_, err := readPassthroughLifecycleFrame(t, clientConn, 3*time.Second)
		require.NoError(t, err)
		_, _ = readPassthroughLifecycleFrame(t, clientConn, 3*time.Second)
		select {
		case <-serverErr:
		case <-time.After(3 * time.Second):
			t.Fatal("passthrough replay ingress did not exit")
		}

		mu.Lock()
		require.Equal(t, []string{"PrepareTurnPayload", "BeforeTurn", "BeforeRequest", "MapRequestModel", "AfterTurn"}, calls)
		require.Equal(t, []int{2, 2, 2, 2, 2}, gotTurnNumbers, "failover replay must preserve logical turn number 2 on every hook including MapRequestModel")
		require.Equal(t, "gpt-5.4-pro", prepareModel)
		require.Equal(t, "gpt-5.4-pro", beforeReqModel)
		require.Equal(t, "gpt-5.4-pro", mapReqModel)
		mu.Unlock()

		// Verify BeforeTurn rejection on replay fails BEFORE dialing upstream.
		rejectCtx, cancelReject := context.WithCancelCause(context.Background())
		defer cancelReject(context.Canceled)
		dialCount := 0
		rejectSvc := newPassthroughLifecycleService(passthroughLifecycleConfig(), newStagedPassthroughConn())
		rejectSvc.openaiWSPassthroughDialer = &countingPassthroughDialer{
			onDial: func() { dialCount++ },
			inner:  rejectSvc.openaiWSPassthroughDialer,
		}
		rejectHooks := &OpenAIWSIngressHooks{
			InitialRequestModel:  "gpt-4o-mini",
			InitialTurnStartedAt: time.Now(),
			InitialTurnNumber:    2,
			BeforeTurn: func(turn int) error {
				require.Equal(t, 2, turn)
				return NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "api key disabled", nil)
			},
		}
		rejectSrv, rejectErrCh := startPassthroughHookRecordingServer(t, rejectCtx, rejectSvc, passthroughLifecycleAccount(), rejectHooks)
		defer rejectSrv.Close()
		rejectClient := dialPassthroughLifecycleClientWithPayload(t, rejectSrv, replayPayload)
		defer func() { _ = rejectClient.CloseNow() }()
		select {
		case gotErr := <-rejectErrCh:
			require.Error(t, gotErr)
			require.Contains(t, gotErr.Error(), "api key disabled")
		case <-time.After(3 * time.Second):
			t.Fatal("rejected passthrough replay did not exit")
		}
		require.Equal(t, 0, dialCount, "BeforeTurn rejection on failover replay must fail BEFORE dialing upstream")
	})
}

type countingPassthroughDialer struct {
	onDial func()
	inner  openAIWSClientDialer
}

func (d *countingPassthroughDialer) Dial(ctx context.Context, wsURL string, headers http.Header, proxyURL string) (openAIWSClientConn, int, http.Header, error) {
	if d.onDial != nil {
		d.onDial()
	}
	return d.inner.Dial(ctx, wsURL, headers, proxyURL)
}
