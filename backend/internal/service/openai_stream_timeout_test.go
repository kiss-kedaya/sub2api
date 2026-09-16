package service

import (
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
	"github.com/tidwall/gjson"
)

func TestOpenAIStreamTimeoutTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const preamble = "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_timeout\"}}\n\n"
	const output = "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"
	const timeout = "event: error\ndata: {\"type\":\"error\",\"code\":\"request_timeout\",\"message\":\"stream closed before response.completed\",\"sequence_number\":0}\n\n"
	const generic = "event: error\ndata: {\"type\":\"error\",\"code\":\"provider_error\",\"message\":\"provider failed\"}\n\n"
	const contextLimit = "event: error\ndata: {\"type\":\"error\",\"error\":{\"code\":\"context_length_exceeded\",\"message\":\"context length exceeded\"}}\n\n"
	const failed = "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_timeout\",\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"final upstream failure\"},\"usage\":{\"input_tokens\":9,\"output_tokens\":2,\"input_tokens_details\":{\"cached_tokens\":7}}}}\n\n"
	const completed = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_timeout\",\"status\":\"completed\",\"usage\":{\"input_tokens\":9,\"output_tokens\":2,\"input_tokens_details\":{\"cached_tokens\":7}}}}\n\n"
	for _, passthrough := range []bool{false, true} {
		for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			for _, tt := range []struct {
				name, stream, code, message string
				failover, success, usage    bool
			}{
				{name: "before output", stream: preamble + timeout, failover: true, message: "stream closed before response.completed"},
				{name: "after output", stream: preamble + output + timeout, code: "request_timeout", message: "stream closed before response.completed"},
				{name: "before done", stream: preamble + output + timeout + "data: [DONE]\n\n", code: "request_timeout", message: "stream closed before response.completed"},
				{name: "authoritative failed", stream: preamble + output + timeout + failed, code: "server_error", message: "final upstream failure", usage: true},
				{name: "duplicate failed", stream: preamble + output + timeout + failed + failed, code: "server_error", message: "final upstream failure", usage: true},
				{name: "authoritative success", stream: preamble + output + generic + completed, success: true, usage: true},
				{name: "policy error then success", stream: preamble + contextLimit + completed, success: true, usage: true},
				{name: "policy error then done", stream: preamble + contextLimit + strings.ReplaceAll(completed, "response.completed", "response.done"), success: true, usage: true},
				{name: "nonretryable before output", stream: preamble + generic, code: "provider_error", message: "provider failed"},
			} {
				t.Run(map[bool]string{false: "native", true: "passthrough"}[passthrough]+"/"+accountType+"/"+tt.name, func(t *testing.T) {
					svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, LogUpstreamErrorBody: true}}}
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
					account := &Account{ID: 42, Platform: PlatformOpenAI, Type: accountType}
					svc.recordOpenAIStreamUpstreamError(c, account, passthrough, "previous-attempt", "failover", []byte(`{"error":{"code":"server_is_overloaded"}}`), "previous overload")
					resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Request-Id": {"final-attempt"}}, Body: io.NopCloser(strings.NewReader(tt.stream))}
					var usage *OpenAIUsage
					var nonBillable bool
					var err error
					if passthrough {
						var result *openaiStreamingResultPassthrough
						result, err = svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "model", "model")
						require.NotNil(t, result)
						usage = result.usage
						nonBillable = result.nonBillableUpstreamError
					} else {
						var result *openaiStreamingResult
						result, err = svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "model", "model")
						require.NotNil(t, result)
						usage = result.usage
						nonBillable = result.nonBillableUpstreamError
					}
					if tt.usage {
						require.Equal(t, 9, usage.InputTokens)
						require.Equal(t, 2, usage.OutputTokens)
						require.Equal(t, 7, usage.CacheReadInputTokens)
					}
					raw, _ := c.Get(OpsUpstreamErrorsKey)
					events, ok := raw.([]*OpsUpstreamErrorEvent)
					require.True(t, ok)
					if tt.success {
						require.NoError(t, err)
						require.False(t, nonBillable)
						require.Len(t, events, 1)
						terminal := "response.completed"
						if strings.Contains(tt.stream, `"type":"response.done"`) {
							terminal = "response.done"
						}
						require.Contains(t, rec.Body.String(), `"type":"`+terminal+`"`)
						require.Contains(t, rec.Body.String(), "event: "+terminal+"\n")
						require.NotContains(t, rec.Body.String(), `"type":"response.failed"`)
						return
					}
					require.Error(t, err)
					var failoverErr *UpstreamFailoverError
					require.Equal(t, tt.failover, errors.As(err, &failoverErr))
					if tt.failover {
						require.False(t, c.Writer.Written())
						require.Empty(t, rec.Body.String())
						require.False(t, failoverErr.RetryableOnSameAccount)
					} else {
						require.NotContains(t, rec.Body.String(), `"type":"error"`)
						require.NotContains(t, rec.Body.String(), "event: error")
						require.NotContains(t, rec.Body.String(), `"type":"response.completed"`)
						require.NotContains(t, rec.Body.String(), "[DONE]")
						require.Equal(t, 1, strings.Count(rec.Body.String(), `"type":"response.failed"`))
						require.Contains(t, rec.Body.String(), `"code":"`+tt.code+`"`)
					}
					require.Len(t, events, 2)
					require.Equal(t, "final-attempt", events[1].UpstreamRequestID)
					require.Equal(t, tt.message, events[1].Message)
					require.Equal(t, tt.message, c.GetString(OpsUpstreamErrorMessageKey))
					require.NotContains(t, c.GetString(OpsUpstreamErrorDetailKey), "server_is_overloaded")
				})
			}
		}
	}
}

func TestOpenAIStreamTimeoutFailoverRequiresStructuredCode(t *testing.T) {
	for _, payload := range []string{
		`{"type":"error","code":"request_timeout"}`,
		`{"type":"error","error":{"code":"request_timeout"}}`,
		`{"type":"error","response":{"error":{"code":"request_timeout"}}}`,
	} {
		require.True(t, openAIStreamErrorEventShouldFailover([]byte(payload), "stream closed"))
	}
	require.False(t, openAIStreamErrorEventShouldFailover([]byte(`{"type":"error","code":"input_too_small"}`), "request_timeout"))
}

func TestOpenAIStreamTimeoutCanceledPreservesUsage(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "passthrough"}[passthrough], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
			body := "data: {\"type\":\"error\",\"code\":\"request_timeout\",\"message\":\"stream closed\",\"usage\":{\"input_tokens\":9,\"output_tokens\":2,\"input_tokens_details\":{\"cached_tokens\":7}}}\n\n"
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
			svc := &OpenAIGatewayService{cfg: &config.Config{}}
			account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			var usage *OpenAIUsage
			var disconnected bool
			var err error
			if passthrough {
				var result *openaiStreamingResultPassthrough
				result, err = svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "model", "model")
				require.NotNil(t, result)
				usage, disconnected = result.usage, result.clientDisconnect
			} else {
				var result *openaiStreamingResult
				result, err = svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "model", "model")
				require.NotNil(t, result)
				usage, disconnected = result.usage, result.clientDisconnect
			}
			require.Error(t, err)
			var failover *UpstreamFailoverError
			require.False(t, errors.As(err, &failover))
			require.True(t, disconnected)
			require.Empty(t, rec.Body.String())
			require.Equal(t, 9, usage.InputTokens)
			require.Equal(t, 2, usage.OutputTokens)
			require.Equal(t, 7, usage.CacheReadInputTokens)
		})
	}
}

func TestOpenAISyntheticFailuresCarryRequiredFields(t *testing.T) {
	source := []byte(`{"type":"error","sequence_number":17,"code":"request_timeout","message":"stream closed"}`)
	for _, tt := range []struct {
		name    string
		payload []byte
		seq     int64
	}{
		{"http", []byte(strings.TrimSpace(strings.SplitN(buildOpenAIResponseFailedSSE("resp_test", "model", source, ""), "data: ", 2)[1])), 17},
		{"ws bridge", buildOpenAIWSHTTPBridgeFailedEvent("resp_test", "model", source, ""), 17},
		{"ws disconnect", buildOpenAIWSPassthroughFailureEvent("resp_test", "model"), 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.True(t, gjson.ValidBytes(tt.payload))
			require.True(t, gjson.GetBytes(tt.payload, "sequence_number").Exists())
			require.Equal(t, tt.seq, gjson.GetBytes(tt.payload, "sequence_number").Int())
			require.Greater(t, gjson.GetBytes(tt.payload, "response.created_at").Int(), int64(0))
			require.Equal(t, "failed", gjson.GetBytes(tt.payload, "response.status").String())
			if tt.seq == 17 {
				require.Equal(t, "request_timeout", gjson.GetBytes(tt.payload, "response.error.code").String())
			}
		})
	}
}

func TestOpenAIStreamFinalFailureClearsUnloggedAttemptDetail(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	setOpsUpstreamError(c, 503, "old failure", `{"error":{"code":"input_too_small"}}`)
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	svc.recordOpenAIStreamUpstreamError(c, &Account{ID: 42, Platform: PlatformOpenAI}, false, "final", "stream_failed", []byte(`{"code":"request_timeout"}`), "stream closed")
	require.Empty(t, c.GetString(OpsUpstreamErrorDetailKey))
	require.Equal(t, "stream closed", c.GetString(OpsUpstreamErrorMessageKey))
}

func TestOpenAIStreamCanceledReadErrorKeepsPartialUsage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body: &passthroughFlushTestErrorBody{
			payload: []byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_cancel\",\"usage\":{\"input_tokens\":9,\"input_tokens_details\":{\"cached_tokens\":7}}}}\n\n"),
			err:     io.ErrUnexpectedEOF,
		},
	}
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	result, err := svc.handleStreamingResponsePassthrough(context.Background(), resp, c,
		&Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, time.Now(), "model", "model")
	require.Error(t, err)
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover))
	require.NotNil(t, result)
	require.True(t, result.clientDisconnect)
	require.Equal(t, 9, result.usage.InputTokens)
	require.Equal(t, 7, result.usage.CacheReadInputTokens)
	require.Empty(t, rec.Body.String())
}

func TestOpenAIStreamingPassthroughCanceledEmptyTerminal(t *testing.T) {
	for _, terminal := range []string{"response.completed", "response.done"} {
		t.Run(terminal, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			body := "data: {\"type\":\"" + terminal + "\",\"response\":{\"id\":\"resp_cancel\",\"output\":[]}}\n\n"
			var c *gin.Context
			result, rec, _, err := runPassthroughFlushTest(t, io.NopCloser(strings.NewReader(body)), -1, func(context *gin.Context) {
				c = context
				c.Request = c.Request.WithContext(ctx)
			})
			require.NoError(t, err)
			require.True(t, result.clientDisconnect)
			require.Empty(t, rec.Body.String())
			_, logged := c.Get(OpsUpstreamErrorsKey)
			require.False(t, logged)
		})
	}
}
