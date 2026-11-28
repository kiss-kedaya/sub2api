package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIPassthroughOutputEstimator_MatchesBufferedResponseAccumulator(t *testing.T) {
	cases := []struct {
		name   string
		events []struct {
			eventType string
			data      string
		}
	}{
		{
			name: "mixed text refusal reasoning with whitespace boundaries and escapes",
			events: []struct {
				eventType string
				data      string
			}{
				{"response.output_text.delta", `{"type":"response.output_text.delta","delta":"   "}`},
				{"response.output_text.delta", `{"type":"response.output_text.delta","delta":"Hello, "}`},
				{"response.output_text.delta", `{"type":"response.output_text.delta","delta":"world!\n\"quoted\"  "}`},
				{"response.output_text.delta", `{"type":"response.output_text.delta","delta":"   "}`},
				{"response.refusal.delta", `{"type":"response.refusal.delta","delta":"  cannot do that  "}`},
				{"response.reasoning_summary_text.delta", `{"type":"response.reasoning_summary_text.delta","delta":"思考中"}`},
				{"response.reasoning_text.delta", `{"type":"response.reasoning_text.delta","delta":"：一步步分析\u4e2d\u6587 "}`},
			},
		},
		{
			name: "function call deltas replaced by done and custom tool call",
			events: []struct {
				eventType string
				data      string
			}{
				{"response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call_1","name":"get_weather"}}`},
				{"response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"city\":"}`},
				{"response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","output_index":0,"delta":"\"Tokyo\"}"}`},
				{"response.function_call_arguments.done", `{"type":"response.function_call_arguments.done","output_index":0,"arguments":"{\"city\":\"Tokyo\",\"unit\":\"c\"}"}`},
				{"response.output_item.added", `{"type":"response.output_item.added","output_index":1,"item":{"type":"custom_tool_call","call_id":"call_2","name":"apply_patch"}}`},
				{"response.custom_tool_call_input.delta", `{"type":"response.custom_tool_call_input.delta","output_index":1,"delta":"*** Begin Patch\n"}`},
				{"response.output_item.done", `{"type":"response.output_item.done","output_index":1,"item":{"type":"custom_tool_call","call_id":"call_2","name":"apply_patch","input":"*** Begin Patch\n+line\n*** End Patch"}}`},
			},
		},
		{
			name: "fallback event type from SSE event header and non-string deltas ignored",
			events: []struct {
				eventType string
				data      string
			}{
				{"response.output_text.delta", `{"delta":"from event header"}`},
				{"response.output_text.delta", `{"type":"response.output_text.delta","delta":12345}`},
				{"response.output_text.delta", `not-json`},
				{"response.created", `{"type":"response.created","response":{"id":"resp_1"}}`},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			acc := apicompat.NewBufferedResponseAccumulator()
			var est openAIPassthroughOutputEstimator
			for _, ev := range tc.events {
				raw := []byte(ev.data)
				observePassthroughStreamOutput(acc, ev.eventType, raw)
				frame := parseOpenAISSEDataFrame(raw, ev.eventType)
				est.observeFrame(frame, ev.eventType)
			}
			require.Equal(t, estimateIncompleteStreamOutputTokens(acc), est.tokens())
		})
	}
}

func TestObserveResponsesStreamOutputFrame_MatchesJSONUnmarshal(t *testing.T) {
	frames := []string{
		`{"type":"response.output_text.delta","delta":"hello "}`,
		`{"type":"response.output_text.delta","delta":"world\n\"ok\""}`,
		`{"type":"response.refusal.delta","delta":"refused"}`,
		`{"type":"response.reasoning_summary_text.delta","delta":"plan"}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"c1","name":"search"}}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"q\":\"go\"}"}`,
		`{"type":"response.function_call_arguments.done","output_index":0,"arguments":"{\"q\":\"golang\"}"}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"custom_tool_call","call_id":"c2","name":"patch"}}`,
		`{"type":"response.custom_tool_call_input.delta","output_index":1,"delta":"diff"}`,
		`{"type":"response.custom_tool_call_input.done","output_index":1,"input":"final diff"}`,
		`{"type":"response.output_item.done","output_index":2,"item":{"type":"message","content":[{"type":"output_text","text":"ignored"}]}}`,
		`{"type":"response.output_text.delta","delta":42}`,
		`{"delta":"missing type"}`,
	}

	wantAcc := apicompat.NewBufferedResponseAccumulator()
	gotAcc := apicompat.NewBufferedResponseAccumulator()

	for _, rawStr := range frames {
		raw := []byte(rawStr)
		var ev apicompat.ResponsesStreamEvent
		if err := json.Unmarshal(raw, &ev); err == nil {
			wantAcc.ProcessEvent(&ev)
		}
		frame := parseOpenAISSEDataFrame(raw, "")
		observeResponsesStreamOutputFrame(gotAcc, frame)
	}

	require.Equal(t, wantAcc.BuildOutput(), gotAcc.BuildOutput())
}

func TestHandleStreamingResponse_ReusedAckChannelStopsCleanlyOnEarlyCancel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)

	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       pr,
	}
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}

	done := make(chan error, 1)
	go func() {
		_, err := svc.handleStreamingResponse(ctx, resp, c, &Account{ID: 7, Platform: PlatformOpenAI}, time.Now(), "gpt-5", "gpt-5")
		done <- err
	}()

	_, err := io.WriteString(pw, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n")
	require.NoError(t, err)
	cancel()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("handleStreamingResponse deadlocked on early cancel")
	}
}
