package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestForwardAsChatCompletions_AgentTerminalToolRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{true, false} {
		name := "buffered"
		if stream {
			name = "streaming"
		}
		t.Run(name, func(t *testing.T) {
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "test-key"},
				Extra:       map[string]any{"openai_responses_mode": "force_responses", "openai_responses_supported": true},
			}
			request := apicompat.ChatCompletionsRequest{
				Model: "gpt-5.6-sol", Stream: stream,
				Messages: []apicompat.ChatMessage{
					{Role: "developer", Content: json.RawMessage(`"Use exec_command for terminal commands."`)},
					{Role: "user", Content: json.RawMessage(`"Print the working directory."`)},
				},
				Tools:      []apicompat.ChatTool{{Type: "function", Function: &apicompat.ChatFunction{Name: "exec_command", Parameters: json.RawMessage(`{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}`)}}},
				ToolChoice: json.RawMessage(`{"type":"function","function":{"name":"exec_command"}}`),
			}
			body, err := json.Marshal(request)
			require.NoError(t, err)
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader("data: " + `{"type":"response.completed","response":{"id":"resp_terminal","model":"gpt-5.6-sol","status":"completed","output":[{"type":"function_call","id":"fc_terminal","call_id":"call_terminal","name":"exec_command","arguments":"{\"cmd\":\"pwd\"}","status":"completed"}],"usage":{"input_tokens":5,"output_tokens":4}}}` + "\n\n")),
			}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
			result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, "/v1/responses", upstream.lastReq.URL.Path)
			require.Equal(t, "/v1/chat/completions", c.Request.URL.Path)
			require.Equal(t, "developer", gjson.GetBytes(upstream.lastBody, "input.0.role").String())
			require.JSONEq(t, `{"type":"function","name":"exec_command"}`, gjson.GetBytes(upstream.lastBody, "tool_choice").Raw)
			var call apicompat.ChatToolCall
			if stream {
				finishes := 0
				outerState := apicompat.NewChatCompletionsToResponsesStreamState(request.Model)
				var outerEvents []apicompat.ResponsesStreamEvent
				for _, line := range strings.Split(rec.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
						continue
					}
					var chunk apicompat.ChatCompletionsChunk
					require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk))
					outerEvents = append(outerEvents, apicompat.ChatCompletionsChunkToResponsesEvents(&chunk, outerState)...)
					for _, choice := range chunk.Choices {
						for _, delta := range choice.Delta.ToolCalls {
							if delta.ID != "" {
								call.ID = delta.ID
							}
							call.Function.Name += delta.Function.Name
							call.Function.Arguments += delta.Function.Arguments
						}
						if choice.FinishReason != nil {
							require.Equal(t, "tool_calls", *choice.FinishReason)
							finishes++
						}
					}
				}
				require.Equal(t, 1, finishes)
				require.Contains(t, rec.Body.String(), "data: [DONE]")
				outerEvents = append(outerEvents, apicompat.FinalizeChatCompletionsResponsesStream(outerState)...)
				var outerResponse *apicompat.ResponsesResponse
				for _, event := range outerEvents {
					if event.Type == "response.completed" {
						outerResponse = event.Response
					}
				}
				require.NotNil(t, outerResponse, "the downstream Responses gateway must receive a complete tool turn")
				require.Len(t, outerResponse.Output, 1)
				require.Equal(t, "function_call", outerResponse.Output[0].Type)
				require.Equal(t, "call_terminal", outerResponse.Output[0].CallID)
				require.Equal(t, "exec_command", outerResponse.Output[0].Name)
				require.JSONEq(t, `{"cmd":"pwd"}`, outerResponse.Output[0].Arguments)
			} else {
				var response apicompat.ChatCompletionsResponse
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
				require.Len(t, response.Choices, 1)
				require.Equal(t, "tool_calls", response.Choices[0].FinishReason)
				require.Len(t, response.Choices[0].Message.ToolCalls, 1)
				call = response.Choices[0].Message.ToolCalls[0]
			}
			require.Equal(t, "call_terminal", call.ID)
			require.Equal(t, "exec_command", call.Function.Name)
			require.JSONEq(t, `{"cmd":"pwd"}`, call.Function.Arguments)
			request.Messages = append(request.Messages,
				apicompat.ChatMessage{Role: "assistant", ToolCalls: []apicompat.ChatToolCall{call}},
				apicompat.ChatMessage{Role: "tool", ToolCallID: call.ID, Content: json.RawMessage(`"/workspace"`)},
			)
			body, err = json.Marshal(request)
			require.NoError(t, err)
			upstream.resp.Body = io.NopCloser(strings.NewReader("data: " + `{"type":"response.completed","response":{"id":"resp_next","model":"gpt-5.6-sol","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"/workspace"}]}],"usage":{"input_tokens":8,"output_tokens":2}}}` + "\n\n"))
			c, _ = gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
			_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
			require.NoError(t, err)
			require.Equal(t, "call_terminal", gjson.GetBytes(upstream.lastBody, "input.2.call_id").String())
			require.Equal(t, "exec_command", gjson.GetBytes(upstream.lastBody, "input.2.name").String())
			require.Equal(t, "function_call_output", gjson.GetBytes(upstream.lastBody, "input.3.type").String())
			require.Equal(t, "call_terminal", gjson.GetBytes(upstream.lastBody, "input.3.call_id").String())
			require.Equal(t, "/workspace", gjson.GetBytes(upstream.lastBody, "input.3.output").String())
		})
	}
}
