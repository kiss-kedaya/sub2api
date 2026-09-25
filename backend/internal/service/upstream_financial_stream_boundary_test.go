package service

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUpstreamFinancialInterleavedWriters(t *testing.T) {
	for _, direct := range []bool{false, true} {
		for _, started := range []bool{false, true} {
			ctx, recorder := financialContext("/v1/messages")
			original := &financialHeaderSpy{ResponseWriter: ctx.Writer}
			ctx.Writer = original
			if started {
				ctx.Writer.Flush()
			}
			raw := []byte(`{"errors":[{"details":{"message":"当前额度: 0, 需要额度: 12 sk-secret"}}]}`)
			restore := GuardUpstreamFinancialError(ctx, 403, raw)
			ctx.Writer = &financialHeaderSpy{ResponseWriter: ctx.Writer}
			nested := GuardUpstreamFinancialError(ctx, 403, raw)
			nested()
			restore()
			require.Same(t, original, ctx.Writer)
			require.False(t, ctx.GetBool(upstreamFinancialErrorSentKey))
			restore = GuardUpstreamFinancialError(ctx, 403, raw)
			ctx.Writer = &financialHeaderSpy{ResponseWriter: ctx.Writer}
			nested = GuardUpstreamFinancialError(ctx, 500, raw)
			MarkResponseCommitted(ctx)
			if direct {
				require.True(t, WriteUpstreamFinancialError(ctx, 403, raw))
			} else {
				ctx.JSON(403, gin.H{"error": "sk-secret"})
			}
			require.True(t, WriteUpstreamFinancialError(ctx, 500, raw))
			nested()
			restore()
			require.Equal(t, 1, strings.Count(recorder.Body.String(), UpstreamUnavailableMessage))
			require.NotContains(t, recorder.Body.String(), "sk-secret")
			if started {
				require.Equal(t, 200, recorder.Code)
				require.Empty(t, original.headers)
			} else {
				require.Equal(t, 502, recorder.Code)
				require.Equal(t, []int{502}, original.headers)
			}
		}
	}
}

func TestUpstreamFinancialLocalHelperBoundaries(t *testing.T) {
	message := "当前额度: X, 需要额度: Y"
	for _, family := range []string{"google", "antigravity", "responses", "chat"} {
		ctx, recorder := financialContext("/v1/responses")
		switch family {
		case "google":
			_ = (&GeminiMessagesCompatService{}).writeGoogleError(ctx, 400, message)
		case "antigravity":
			_ = (&AntigravityGatewayService{}).writeGoogleError(ctx, 400, message)
		case "responses":
			writeResponsesError(ctx, 400, "invalid_request_error", message)
		case "chat":
			writeGatewayCCError(ctx, 400, "invalid_request_error", message)
		}
		require.Equal(t, 400, recorder.Code, family)
		require.Contains(t, recorder.Body.String(), message, family)
		require.False(t, ctx.GetBool(upstreamFinancialErrorSentKey))
	}
}

func TestUpstreamFinancialMappedErrorAfterFlush(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/v1/chat/completions"} {
		ctx, recorder := financialContext(path)
		spy := &financialHeaderSpy{ResponseWriter: ctx.Writer}
		ctx.Writer = spy
		_, _ = ctx.Writer.WriteString(": ping\n\n")
		ctx.Writer.Flush()
		for repeat := 0; repeat < 2; repeat++ {
			if path == "/v1/messages" {
				writeAnthropicError(ctx, 502, "upstream_error", UpstreamUnavailableMessage)
			} else {
				writeChatCompletionsError(ctx, 502, "upstream_error", UpstreamUnavailableMessage)
			}
		}
		require.Equal(t, 200, recorder.Code)
		require.Empty(t, spy.headers)
		require.Equal(t, 1, strings.Count(recorder.Body.String(), UpstreamUnavailableMessage))
	}
}

func TestUpstreamFinancialMultilineNativeBoundaries(t *testing.T) {
	streams := []string{
		"event: error\ndata: {\ndata: \"type\":\"error\",\ndata: \"error\":{\"details\":{\"message\":\"用户额度已用尽 sk-secret\"}}}\n\n",
		"data: {\ndata: \"errors\":[{\"details\":{\"message\":\"当前额度: 0, 需要额度: 12 sk-secret\"}}]}\n\n",
		"{\n\"error\":{\"message\":\"usage cost: $12 sk-secret\"}\n}",
		"[{\"error\":{\"message\":\"扣费明细: 12 sk-secret\"}}]",
	}
	for _, family := range []string{"anthropic", "native-anthropic", "gemini", "images", "antigravity", "antigravity-buffered"} {
		for _, started := range []bool{false, true} {
			for _, stream := range streams {
				t.Run(family+"/"+stream[:8]+"/"+map[bool]string{true: "flushed", false: "unwritten"}[started], func(t *testing.T) {
					path := "/v1/messages"
					if family == "gemini" || strings.HasPrefix(family, "antigravity") {
						path = "/v1beta/models/gemini:streamGenerateContent"
					}
					if family == "images" {
						path = "/v1/images/generations"
					}
					ctx, recorder := financialContext(path)
					spy := &financialHeaderSpy{ResponseWriter: ctx.Writer}
					ctx.Writer = spy
					if started {
						_, _ = ctx.Writer.WriteString(": ping\n\n")
						ctx.Writer.Flush()
					}
					response := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(stream))}
					account := &Account{ID: 1, Type: AccountTypeAPIKey}
					switch family {
					case "antigravity", "antigravity-buffered":
						gateway := &AntigravityGatewayService{settingService: &SettingService{cfg: &config.Config{}}}
						var err error
						if family == "antigravity" {
							_, err = gateway.handleGeminiStreamingResponse(ctx, response, time.Now())
						} else {
							_, err = gateway.handleGeminiStreamToNonStreaming(ctx, response, time.Now())
						}
						require.NoError(t, err)
					case "anthropic":
						_, err := (&GatewayService{}).handleStreamingResponseAnthropicAPIKeyPassthrough(context.Background(), response, ctx, account, time.Now(), "claude")
						require.NoError(t, err)
					case "native-anthropic":
						_, err := (&OpenAIGatewayService{}).handleNativeAnthropicStreamingResponse(context.Background(), response, ctx, account, "claude", "claude", "claude", nil, time.Now())
						require.NoError(t, err)
					case "gemini":
						_, err := (&GeminiMessagesCompatService{}).handleNativeStreamingResponse(ctx, response, time.Now(), false, account, "")
						require.NoError(t, err)
					case "images":
						_, _, _, _, err := (&OpenAIGatewayService{}).handleOpenAIImagesStreamingResponse(response, ctx, time.Now())
						require.Error(t, err)
					}
					require.Equal(t, 1, strings.Count(recorder.Body.String(), UpstreamUnavailableMessage), recorder.Body.String())
					require.NotContains(t, recorder.Body.String(), "sk-secret")
					if started {
						require.Equal(t, 200, recorder.Code)
						require.NotContains(t, spy.headers, 502)
					} else {
						require.Equal(t, 502, recorder.Code)
						require.Equal(t, 502, spy.headers[len(spy.headers)-1])
					}
				})
			}
		}
	}
}

func TestUpstreamFinancialFrameReaderPreservesSuccess(t *testing.T) {
	for _, body := range []string{
		"data: {\"usage\":{\"total_tokens\":12},\"text\":\"用户额度已用尽\"}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"partial_json\":\"sk-secret billing cost: $1.64\"}}\n\n",
		"event: error\ndata: {\ndata: \"error\":{\"message\":\"invalid model\"}}\n\n",
		"data: {\n data: ignored\ndata: \"usage\":{\"total_tokens\":8}}\n\n",
		"{\"usage\":{\"total_tokens\":8},\"output\":[{\"text\":\"余额不足\"}]}",
		"data: {\"text\":\"" + strings.Repeat("x", 10000) + "\"}\r\n\r\n",
	} {
		actual, err := io.ReadAll(newUpstreamErrorFrameReader(strings.NewReader(body), 1<<20))
		require.NoError(t, err)
		require.Equal(t, body, string(actual))
	}
	_, err := io.ReadAll(newUpstreamErrorFrameReader(strings.NewReader("data: "+strings.Repeat("x", 8192)), 4096))
	require.ErrorIs(t, err, bufio.ErrTooLong)
}

func TestUpstreamFinancialFrameReaderDoesNotWaitForNextEvent(t *testing.T) {
	source, sink := io.Pipe()
	defer func() { _ = source.Close() }()
	defer func() { _ = sink.Close() }()
	line := "data: {\"usage\":{\"total_tokens\":8}}\n"
	result := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(newUpstreamErrorFrameReader(source, 1<<20))
		body, _ := reader.ReadString('\n')
		result <- body
	}()
	_, err := sink.Write([]byte(line))
	require.NoError(t, err)
	select {
	case actual := <-result:
		require.Equal(t, line, actual)
	case <-time.After(time.Second):
		t.Fatal("success event waited for the next event")
	}
}

func TestUpstreamFinancialHTTPStreamingProtocols(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "event: error\ndata: {\"type\":\"error\",\"error\":{\"code\":\"insufficient_quota\",\"message\":\"余额不足 vendor-A host.internal $1.64 sk-secret\"}}\n\n")
	}))
	defer upstream.Close()
	for _, path := range []string{"/v1/responses", "/v1/messages", "/v1beta/models/gemini:streamGenerateContent", "/v1/images/generations"} {
		for _, started := range []bool{false, true} {
			t.Run(path+map[bool]string{true: "/flushed", false: "/unwritten"}[started], func(t *testing.T) {
				router := gin.New()
				router.POST("/*path", func(ctx *gin.Context) {
					if started {
						_, _ = ctx.Writer.WriteString(": ping\n\n")
						ctx.Writer.Flush()
					}
					account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
					for attempt := 0; attempt < 2; attempt++ {
						response, err := upstream.Client().Get(upstream.URL)
						if err != nil {
							t.Error(err)
							ctx.Status(500)
							return
						}
						switch path {
						case "/v1/responses":
							_, err = (&OpenAIGatewayService{}).handleStreamingResponse(ctx.Request.Context(), response, ctx, account, time.Now(), "model", "model")
						case "/v1/messages":
							_, err = (&GatewayService{}).handleStreamingResponseAnthropicAPIKeyPassthrough(ctx.Request.Context(), response, ctx, account, time.Now(), "claude")
						case "/v1/images/generations":
							_, _, _, _, err = (&OpenAIGatewayService{}).handleOpenAIImagesStreamingResponse(response, ctx, time.Now())
						default:
							_, err = (&GeminiMessagesCompatService{}).handleNativeStreamingResponse(ctx, response, time.Now(), false, account, "")
						}
						_ = response.Body.Close()
						var failover *UpstreamFailoverError
						if !errors.As(err, &failover) {
							return
						}
						if !strings.Contains(string(failover.ResponseBody), "sk-secret") {
							t.Error("internal failover body was redacted")
						}
						if attempt == 1 {
							WriteUpstreamFinancialError(ctx, failover.StatusCode, failover.ResponseBody)
						}
					}
				})
				gateway := httptest.NewServer(router)
				defer gateway.Close()
				response, err := gateway.Client().Post(gateway.URL+path, "application/json", strings.NewReader("{}"))
				require.NoError(t, err)
				defer func() { _ = response.Body.Close() }()
				body, err := io.ReadAll(response.Body)
				require.NoError(t, err)
				require.Equal(t, 1, strings.Count(string(body), UpstreamUnavailableMessage), string(body))
				for _, secret := range []string{"sk-secret", "host.internal", "vendor-A", "1.64"} {
					require.NotContains(t, string(body), secret)
				}
				if started {
					require.Equal(t, 200, response.StatusCode)
				} else {
					require.Equal(t, 502, response.StatusCode)
				}
			})
		}
	}
}

func TestUpstreamFinancialHTTPSuccessStatusErrorBody(t *testing.T) {
	for _, body := range []string{
		`{"id":"resp_1","object":"response","status":"failed","usage":{"input_tokens":8,"output_tokens":3},"error":{"code":"billing_not_active","message":"vendor-A sk-secret"}}`,
		`{"usage":{"input_tokens":8,"output_tokens":0},"errors":[{"details":{"message":"当前额度: 0, 需要额度: 1.64 sk-secret"}}]}`,
	} {
		for _, family := range []string{"responses", "passthrough", "gemini"} {
			path := "/v1/responses"
			if family == "gemini" {
				path = "/v1beta/models/gemini:generateContent"
			}
			ctx, recorder := financialContext(path)
			response := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			var err error
			switch family {
			case "responses":
				_, err = (&OpenAIGatewayService{}).handleNonStreamingResponse(context.Background(), response, ctx, account, "model", "model")
			case "passthrough":
				_, err = (&OpenAIGatewayService{}).handleNonStreamingResponsePassthrough(context.Background(), response, ctx, account, "model", "model")
			case "gemini":
				_, err = (&GeminiMessagesCompatService{}).handleNativeNonStreamingResponse(ctx, response, false, account, "")
			}
			require.NoError(t, err, family)
			require.Equal(t, 502, recorder.Code, family)
			require.Contains(t, recorder.Body.String(), UpstreamUnavailableMessage, family)
			require.NotContains(t, recorder.Body.String(), "sk-secret", family)
		}
	}
}
