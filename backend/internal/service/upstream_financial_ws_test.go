package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestUpstreamFinancialWSNativeModes(t *testing.T) {
	for _, mode := range []string{OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough} {
		for _, prelude := range []bool{false, true} {
			t.Run(mode+map[bool]string{true: "/error-pair", false: "/failed"}[prelude], func(t *testing.T) {
				ctx, cancel := context.WithCancelCause(context.Background())
				defer cancel(context.Canceled)
				cfg := passthroughLifecycleConfig()
				cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
				cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
				events := [][]byte{[]byte(`{"type":"response.output_text.delta","delta":"safe output"}`)}
				if prelude {
					events = append(events, []byte(`{"type":"error","error":{"code":"insufficient_quota","message":"用户额度已用尽 vendor-A host.internal $1.64 sk-secret"}}`))
				}
				events = append(events, []byte(`{"type":"response.failed","sequence_number":7,"response":{"id":"resp_financial","status":"failed","usage":{"input_tokens":8,"output_tokens":3},"metadata":{"key":"sk-secret"},"error":{"code":"insufficient_quota","message":"当前额度: 0.12, 需要额度: 1.64 vendor-A host.internal sk-secret"}}}`))
				account := passthroughLifecycleAccount()
				account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
				repo := &openAIWSIngressCapacityShedRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{*account}}}
				svc := &OpenAIGatewayService{cfg: cfg, accountRepo: repo, rateLimitService: &RateLimitService{accountRepo: repo}, httpUpstream: &httpUpstreamRecorder{}, cache: &stubGatewayCache{}, openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector()}
				if mode == OpenAIWSIngressModeCtxPool {
					pool := newOpenAIWSConnPool(cfg)
					pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: &openAIWSCaptureConn{events: events}})
					svc.openaiWSPool = pool
				} else {
					upstream := newStagedPassthroughConn()
					for _, event := range events {
						upstream.Send(string(event))
					}
					svc.openaiWSPassthroughDialer = &stagedPassthroughDialer{conn: upstream}
				}
				results := make(chan *OpenAIForwardResult, 1)
				server, finished := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, func(*gin.Context) *OpenAIWSIngressHooks {
					return &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, _ error) { results <- result }}
				})
				defer server.Close()
				client := dialPassthroughLifecycleClient(t, server)
				defer client.CloseNow()
				var frames [][]byte
				for len(frames) < len(events) {
					frame, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
					require.NoError(t, err)
					frames = append(frames, frame)
					if gjson.GetBytes(frame, "type").String() == "response.failed" {
						break
					}
				}
				require.Equal(t, events[0], frames[0])
				require.Equal(t, "response.failed", gjson.GetBytes(frames[len(frames)-1], "type").String())
				for _, frame := range frames[1:] {
					require.Contains(t, string(frame), UpstreamUnavailableMessage)
					for _, secret := range []string{"sk-secret", "host.internal", "vendor-A", "1.64", "insufficient_quota"} {
						require.NotContains(t, string(frame), secret)
					}
				}
				select {
				case result := <-results:
					require.NotNil(t, result)
					require.Equal(t, 8, result.Usage.InputTokens)
					require.Equal(t, 3, result.Usage.OutputTokens)
				case <-time.After(3 * time.Second):
					t.Fatal("terminal usage missing")
				}
				require.Contains(t, string(events[len(events)-1]), "sk-secret")
				_ = client.CloseNow()
				select {
				case <-finished:
				case <-time.After(3 * time.Second):
					t.Fatal("websocket did not close")
				}
			})
		}
	}
}

func TestUpstreamFinancialWSFrameBoundary(t *testing.T) {
	for _, kind := range []coderws.MessageType{coderws.MessageText, coderws.MessageBinary} {
		payloads := []string{
			`{"type":"error","errors":[{"details":{"message":"当前额度: 0, 需要额度: 1.64 sk-secret"}}]}`,
			`{"type":"response.failed","response":{"id":"resp_test","error":{"code":402,"message":"vendor-A sk-secret"},"usage":{"input_tokens":8}}}`,
			`{"type":"response.completed","response":{"usage":{"input_tokens":8},"output":[{"text":"当前额度: 0 sk-secret"}]}}`,
			`{"type":"response.function_call_arguments.delta","delta":"usage cost: $1.64 sk-secret"}`,
			`{"type":"error","error":{"code":"invalid_parameter","message":"invalid usage field"}}`,
		}
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			conn, err := coderws.Accept(writer, request, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()
			adapter := &openAIWSClientFrameConn{conn: conn}
			for _, payload := range payloads {
				if adapter.WriteFrame(request.Context(), kind, []byte(payload)) != nil {
					return
				}
			}
			_ = conn.Close(coderws.StatusNormalClosure, "done")
		}))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
		require.NoError(t, err)
		for index, payload := range payloads {
			actualKind, frame, readErr := client.Read(ctx)
			require.NoError(t, readErr)
			require.Equal(t, kind, actualKind)
			if index < 2 {
				require.Contains(t, string(frame), UpstreamUnavailableMessage)
				require.NotContains(t, string(frame), "sk-secret")
			} else {
				require.Equal(t, payload, string(frame))
			}
		}
		_ = client.CloseNow()
		cancel()
		server.Close()
	}
}

func TestUpstreamFinancialWSHTTPBridge(t *testing.T) {
	for _, bare := range []bool{true, false} {
		body := "data: {\"type\":\"error\",\"error\":{\"code\":\"insufficient_quota\",\"message\":\"用户额度已用尽 sk-secret\"}}\n\n"
		if !bare {
			body += "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_failure\",\"usage\":{\"input_tokens\":8},\"error\":{\"code\":\"insufficient_quota\",\"message\":\"当前额度: 0, 需要额度: 1.64 sk-secret\"}}}\n\n"
		}
		svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}}}
		ctx, _ := financialContext("/v1/responses")
		payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
		var writes [][]byte
		result, _ := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), ctx, &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "sk-test", payload, len(payload), "gpt-5", "", "", "", "", 2, func(frame []byte) error { writes = append(writes, append([]byte(nil), frame...)); return nil })
		require.Len(t, writes, 1)
		require.Contains(t, string(writes[0]), UpstreamUnavailableMessage)
		require.NotContains(t, string(writes[0]), "sk-secret")
		require.Equal(t, "response.failed", gjson.GetBytes(writes[0], "type").String())
		if !bare {
			require.NotNil(t, result)
			require.Equal(t, 8, result.Usage.InputTokens)
		}
	}
}
