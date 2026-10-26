package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRewriteRequestedModelResponseUsesProtocolSchema(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, body, path string
	}{
		{"chat", "openai", `{"model":"actual","choices":[{"message":{"content":"actual","model":"body-model"}}],"usage":{"model":"usage-model","total_tokens":9}}`, "model"},
		{"responses", "openai", `{"type":"response.completed","response":{"model":"actual","error":null,"output":[{"model":"body-model"}],"usage":{"model":"usage-model","total_tokens":9}}}`, "response.model"},
		{"messages JSON", "anthropic", `{"model":"actual","content":[{"type":"text","text":"actual","model":"body-model"}],"usage":{"input_tokens":9}}`, "model"},
		{"message start", "anthropic", `{"type":"message_start","message":{"model":"actual","content":[{"model":"body-model"}],"usage":{"input_tokens":9}}}`, "message.model"},
		{"realtime session", "realtime", `{"type":"session.updated","session":{"model":"actual","tools":[{"model":"body-model"}]}}`, "session.model"},
		{"realtime response", "realtime", `{"type":"response.done","response":{"model":"actual","output":[{"model":"body-model"}],"usage":{"total_tokens":9}}}`, "response.model"},
		{"Gemini", "gemini", `{"modelVersion":"actual","candidates":[{"content":{"parts":[{"text":"actual","modelVersion":"body-model"}]}}],"usageMetadata":{"totalTokenCount":9}}`, "modelVersion"},
		{"Gemini JSON stream array", "gemini", `[{"modelVersion":"actual","candidates":[{"modelVersion":"body-model"}]},{"modelVersion":"second"}]`, "0.modelVersion"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := []byte(tc.body)
			got := RewriteRequestedModelResponse(original, tc.protocol, "client-model")
			require.Equal(t, "client-model", gjson.GetBytes(got, tc.path).Str)
			require.Contains(t, string(got), `"body-model"`)
			require.Equal(t, tc.body, string(original), "egress must not mutate the upstream observation buffer")
			if tc.protocol == "gemini" && gjson.ParseBytes(got).IsArray() {
				require.Equal(t, "client-model", gjson.GetBytes(got, "1.modelVersion").Str)
			}
		})
	}
}

func TestRewriteRequestedModelResponseKeepsErrorsAndNonSchemaModels(t *testing.T) {
	for _, body := range []string{
		`{"type":"error","model":"actual","error":{"message":"actual"}}`,
		`{"type":"response.failed","response":{"model":"actual","error":null}}`,
		`{"model":"actual","error":{"message":"actual"}}`,
		`{"type":"response.completed","response":{"model":"actual","error":{"message":"actual"}}}`,
		`{"type":"response.function_call_arguments.delta","delta":"{\"model\":\"actual\"}"}`,
		`{"type":"content_block_delta","delta":{"model":"actual","text":"actual"}}`,
		`{"model":`,
	} {
		require.Equal(t, body, string(RewriteRequestedModelResponse([]byte(body), "openai", "client-model")))
	}
	// A nested message object in an unrelated event is content, not metadata.
	body := `{"type":"content_block_delta","message":{"model":"actual"}}`
	require.Equal(t, body, string(RewriteRequestedModelResponse([]byte(body), "anthropic", "client-model")))
}

func requestedModelWriterTestContext(t *testing.T, protocol string, enabled *bool) (*gin.Context, *httptest.ResponseRecorder, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	finish := InstallRequestedModelResponse(c, protocol, "client-model", func() bool { return *enabled })
	return c, recorder, finish
}

func TestRequestedModelWriterJSONAndLateEffectiveGroup(t *testing.T) {
	enabled := false
	c, recorder, finish := requestedModelWriterTestContext(t, "openai", &enabled)
	defer finish()
	// The actual group is selected after installation, before forwarding.
	enabled = true
	body := `{"model":"actual","choices":[{"message":{"content":"actual"}}],"usage":{"total_tokens":9}}`
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Header("Content-Length", strconv.Itoa(len(body)))
	c.Writer.WriteHeaderNow()
	_, err := c.Writer.WriteString(body[:21])
	require.NoError(t, err)
	_, err = c.Writer.WriteString(body[21:])
	require.NoError(t, err)
	require.Equal(t, "client-model", gjson.Get(recorder.Body.String(), "model").Str)
	require.Equal(t, "actual", gjson.Get(recorder.Body.String(), "choices.0.message.content").Str)
	require.EqualValues(t, 9, gjson.Get(recorder.Body.String(), "usage.total_tokens").Int())
	require.Empty(t, recorder.Result().Header.Get("Content-Length"))
	_, hijacker := c.Writer.(http.Hijacker)
	_, notifier := c.Writer.(interface{ CloseNotify() <-chan bool })
	_, pusher := c.Writer.(http.Pusher)
	require.True(t, hijacker)
	require.True(t, notifier)
	require.True(t, pusher)
	require.NoError(t, http.NewResponseController(c.Writer).Flush())
	require.True(t, recorder.Flushed)
}

func TestRequestedModelWriterKeepsDisabledErrorsAndBinaryVerbatim(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, encoding string
		enabled                     bool
		status                      int
	}{
		{name: "disabled", contentType: "application/json", status: 200},
		{name: "HTTP error", enabled: true, contentType: "application/json", status: 429},
		{name: "binary", enabled: true, contentType: "audio/mpeg", status: 200},
		{name: "encoded", enabled: true, contentType: "application/json", encoding: "gzip", status: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, recorder, finish := requestedModelWriterTestContext(t, "openai", &tc.enabled)
			defer finish()
			c.Header("Content-Type", tc.contentType)
			c.Header("Content-Encoding", tc.encoding)
			c.Status(tc.status)
			body := `{"model":"actual","message":"actual","usage":{"total_tokens":9}}`
			_, err := c.Writer.WriteString(body)
			require.NoError(t, err)
			require.Equal(t, tc.status, recorder.Code)
			require.Equal(t, body, recorder.Body.String())
		})
	}
}

func TestRequestedModelWriterSSEIsIncrementalAndPreservesFraming(t *testing.T) {
	enabled := true
	c, recorder, finish := requestedModelWriterTestContext(t, "openai", &enabled)
	defer finish()
	c.Header("Content-Type", "text/event-stream")
	first := "event: response.created\r\nid: 7\r\ndata: {\r\ndata: \"type\":\"response.created\",\"response\":{\"model\":\"actual\",\"error\":null}\r\ndata: }\r\n\r\n"
	for index := range len(first) {
		_, err := c.Writer.WriteString(first[index : index+1])
		require.NoError(t, err)
		c.Writer.Flush()
	}
	want := strings.Replace(first, `"model":"actual"`, `"model":"client-model"`, 1)
	require.Equal(t, want, recorder.Body.String(), "the first event must arrive before the rest of the stream")
	last := "event: error\ndata: {\"model\":\"actual\",\"message\":\"unchanged\"}\n\ndata: [DONE]\n\n"
	_, err := c.Writer.WriteString(last)
	require.NoError(t, err)
	c.Writer.Flush()
	require.Equal(t, want+last, recorder.Body.String())
}

func TestRequestedModelWriterPreservesUnterminatedAndOversizedInput(t *testing.T) {
	for _, contentType := range []string{"application/json", "text/event-stream"} {
		t.Run(contentType, func(t *testing.T) {
			enabled := true
			c, recorder, finish := requestedModelWriterTestContext(t, "openai", &enabled)
			c.Header("Content-Type", contentType)
			prefix := ""
			if contentType == "text/event-stream" {
				prefix = "data: "
			}
			partial := prefix + strings.Repeat("x", requestedModelPendingLimit+1)
			_, err := c.Writer.WriteString(partial)
			require.NoError(t, err)
			require.Equal(t, partial, recorder.Body.String(), "malformed output cannot grow an unbounded pending buffer")
			if contentType == "text/event-stream" {
				for _, part := range []string{"\r", "\n", "\r\n", "data: {\"model\":\"actual\"}\n\n"} {
					_, err = c.Writer.WriteString(part)
					require.NoError(t, err)
				}
				require.Equal(t, partial+"\r\n\r\ndata: {\"model\":\"client-model\"}\n\n", recorder.Body.String())
			}
			finish()
		})
	}
	enabled := true
	c, recorder, finish := requestedModelWriterTestContext(t, "openai", &enabled)
	c.Header("Content-Type", "text/event-stream")
	_, err := c.Writer.WriteString(`data: {"model":"actual"}`)
	require.NoError(t, err)
	finish()
	require.Equal(t, `data: {"model":"client-model"}`, recorder.Body.String())
}

func TestOpenAIWSPassthroughRequestedSessionAndTurnModelsStaySeparate(t *testing.T) {
	meta := newOpenAIWSPassthroughUsageMeta("turn-A", []byte(`{"model":"turn-A"}`))
	meta.initFromFirstFrame([]byte(`{"model":"upstream-A"}`), "upstream-A")
	meta.updateSessionRequestModel([]byte(`{"type":"session.update","session":{"model":"session-B"}}`))
	session := []byte(`{"type":"session.updated","session":{"model":"upstream-B","instructions":"upstream-B"}}`)
	response := []byte(`{"type":"response.done","response":{"model":"upstream-A","usage":{"total_tokens":9}}}`)
	updatedSession := RewriteRequestedModelResponse(session, "realtime", meta.clientResponseModel(session))
	updatedResponse := RewriteRequestedModelResponse(response, "realtime", meta.clientResponseModel(response))
	require.Equal(t, "session-B", gjson.GetBytes(updatedSession, "session.model").Str)
	require.Equal(t, "upstream-B", gjson.GetBytes(updatedSession, "session.instructions").Str)
	require.Equal(t, "turn-A", gjson.GetBytes(updatedResponse, "response.model").Str)
	requestModel, upstreamModel := meta.turnModels("")
	require.Equal(t, "turn-A", requestModel)
	require.Equal(t, "upstream-A", upstreamModel, "presentation must not change billing metadata")
}

func TestRequestedModelOpenAIWSForwardKeepsUpstreamObservationAndUsage(t *testing.T) {
	for _, tc := range []struct {
		name, mapped, observed, want string
		enabled                      bool
	}{
		{name: "enabled without mapping", enabled: true, observed: "provider-version", want: "gpt-5.5"},
		{name: "disabled without mapping", observed: "provider-version", want: "provider-version"},
		{name: "disabled mapped regression", mapped: "gpt-5.4", observed: "gpt-5.4", want: "gpt-5.5"},
		{name: "enabled mapped different upstream version", enabled: true, mapped: "gpt-5.4", observed: "provider-version", want: "gpt-5.5"},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				c.Set("api_key", &APIKey{Group: &Group{ReturnRequestedModel: tc.enabled}})
				cfg := &config.Config{}
				cfg.Security.URLAllowlist.AllowInsecureHTTP = true
				cfg.Gateway.OpenAIWS.Enabled = true
				cfg.Gateway.OpenAIWS.APIKeyEnabled = true
				cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
				cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
				cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
				cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
				cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
				cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 5
				cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
				upstream := []byte(fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_requested_model","model":%q,"status":"completed","error":null,"output":[{"type":"message","content":[{"type":"output_text","text":"provider-version"}]}],"usage":{"input_tokens":2,"output_tokens":3}}}`, tc.observed))
				capture := &openAIWSCaptureConn{events: [][]byte{upstream}}
				pool := newOpenAIWSConnPool(cfg)
				pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: capture})
				svc := &OpenAIGatewayService{
					cfg: cfg, httpUpstream: &httpUpstreamRecorder{}, cache: &stubGatewayCache{},
					openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(), openaiWSPool: pool,
				}
				credentials := map[string]any{"api_key": "test-fixture-key"}
				if tc.mapped != "" {
					credentials["model_mapping"] = map[string]any{"gpt-5.5": tc.mapped}
				}
				account := &Account{ID: 5983, Name: "requested-model-ws", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
					Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: credentials,
					Extra: map[string]any{"responses_websockets_v2_enabled": true}}
				body := []byte(fmt.Sprintf(`{"model":"gpt-5.5","stream":%t,"input":"hello"}`, stream))
				result, err := svc.Forward(context.Background(), c, account, body)
				require.NoError(t, err)
				require.True(t, result.OpenAIWSMode)
				require.Equal(t, tc.observed, result.UpstreamResponseModel)
				require.EqualValues(t, 2, result.Usage.InputTokens)
				require.EqualValues(t, 3, result.Usage.OutputTokens)
				reply := recorder.Body.String()
				if stream {
					for _, line := range strings.Split(reply, "\n") {
						if strings.HasPrefix(line, "data:") && gjson.Get(strings.TrimSpace(strings.TrimPrefix(line, "data:")), "type").Str == "response.completed" {
							reply = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
							break
						}
					}
					require.Equal(t, tc.want, gjson.Get(reply, "response.model").Str)
				} else {
					require.Equal(t, tc.want, gjson.Get(reply, "model").Str)
				}
				require.Contains(t, reply, `"text":"provider-version"`)
				require.Equal(t, tc.observed, gjson.GetBytes(upstream, "response.model").Str)
			})
		}
	}
}

func TestRequestedModelPassthroughSSEKeepsObservedModelAndUsage(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			upstream := "event: response.created\n" +
				`data: {"type":"response.created","response":{"id":"resp_requested_sse","model":"provider-version","error":null}}` + "\n\n" +
				`data: {"type":"response.output_text.delta","delta":"provider-version"}` + "\n\n" +
				"event: response.completed\n" +
				`data: {"type":"response.completed","response":{"id":"resp_requested_sse","model":"provider-version","error":null,"usage":{"input_tokens":3,"output_tokens":2}}}` + "\n\n"
			var captured *gin.Context
			var finish func()
			result, recorder, writer, err := runPassthroughFlushTest(t, io.NopCloser(strings.NewReader(upstream)), -1, func(c *gin.Context) {
				captured = c
				finish = InstallRequestedModelResponse(c, "openai", "client-model", func() bool { return enabled })
			})
			finish()
			require.NoError(t, err)
			require.Equal(t, 3, result.usage.InputTokens)
			require.Equal(t, 2, result.usage.OutputTokens)
			require.Equal(t, "provider-version", observedUpstreamResponseModel(captured))
			wantModel := "provider-version"
			if enabled {
				wantModel = "client-model"
			}
			for _, line := range strings.Split(recorder.Body.String(), "\n") {
				if !strings.HasPrefix(line, "data:") {
					continue
				}
				data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if gjson.Get(data, "response.model").Exists() {
					require.Equal(t, wantModel, gjson.Get(data, "response.model").Str)
				}
			}
			require.Contains(t, recorder.Body.String(), `"delta":"provider-version"`)
			require.NotEmpty(t, writer.flushBodyLengths, "the forwarder must continue flushing incremental output")
		})
	}
}
