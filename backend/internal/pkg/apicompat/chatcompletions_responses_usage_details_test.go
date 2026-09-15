package apicompat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChatToResponsesUsageDetailsOnWire(t *testing.T) {
	tests := []struct {
		name string
		chat string
		want string
	}{
		{
			name: "all_details_with_cache",
			chat: `,"prompt_tokens_details":{"cached_tokens":60,"cache_write_tokens":10,"audio_tokens":4},"completion_tokens_details":{"reasoning_tokens":30,"audio_tokens":2,"accepted_prediction_tokens":10,"rejected_prediction_tokens":3}`,
			want: `,"cache_creation_input_tokens":10,"input_tokens_details":{"cached_tokens":60,"cache_write_tokens":10,"audio_tokens":4},"output_tokens_details":{"reasoning_tokens":30,"audio_tokens":2,"accepted_prediction_tokens":10,"rejected_prediction_tokens":3}`,
		},
		{
			name: "input_audio_without_cache",
			chat: `,"prompt_tokens_details":{"audio_tokens":4}`,
			want: `,"input_tokens_details":{"audio_tokens":4}`,
		},
		{
			name: "output_audio_only",
			chat: `,"completion_tokens_details":{"audio_tokens":2}`,
			want: `,"output_tokens_details":{"audio_tokens":2}`,
		},
		{
			name: "reasoning_only",
			chat: `,"completion_tokens_details":{"reasoning_tokens":30}`,
			want: `,"output_tokens_details":{"reasoning_tokens":30}`,
		},
		{
			name: "accepted_prediction_only",
			chat: `,"completion_tokens_details":{"accepted_prediction_tokens":10}`,
			want: `,"output_tokens_details":{"accepted_prediction_tokens":10}`,
		},
		{
			name: "rejected_prediction_only",
			chat: `,"completion_tokens_details":{"rejected_prediction_tokens":3}`,
			want: `,"output_tokens_details":{"rejected_prediction_tokens":3}`,
		},
		{
			name: "audio_with_canonical_cache_zero",
			chat: `,"prompt_tokens_details":{"audio_tokens":4,"cached_tokens":0,"cache_write_tokens":0,"cache_creation_tokens":12},"cached_tokens":60,"cache_creation_input_tokens":10`,
			want: `,"input_tokens_details":{"audio_tokens":4}`,
		},
		{
			name: "audio_with_canonical_cache_null",
			chat: `,"prompt_tokens_details":{"audio_tokens":4,"cached_tokens":null,"cache_write_tokens":null,"cache_creation_tokens":12},"cached_tokens":60,"cache_creation_input_tokens":10`,
			want: `,"input_tokens_details":{"audio_tokens":4}`,
		},
		{name: "absent_details"},
		{
			name: "null_details",
			chat: `,"prompt_tokens_details":null,"completion_tokens_details":null`,
		},
		{
			name: "zero_details",
			chat: `,"prompt_tokens_details":{"audio_tokens":0},"completion_tokens_details":{"reasoning_tokens":0,"audio_tokens":0,"accepted_prediction_tokens":0,"rejected_prediction_tokens":0}`,
		},
	}
	for _, tt := range tests {
		for _, mode := range []string{"buffered", "stream_stop", "stream_length"} {
			t.Run(tt.name+"/"+mode, func(t *testing.T) {
				usageJSON := `{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150` + tt.chat + `}`
				var response *ResponsesResponse
				if mode == "buffered" {
					var chat ChatCompletionsResponse
					require.NoError(t, json.Unmarshal([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":`+usageJSON+`}`), &chat))
					response = ChatCompletionsResponseToResponses(&chat, "review-model", nil, nil, false, nil)
				} else {
					state := NewChatCompletionsToResponsesStreamState("review-model")
					finish := strings.TrimPrefix(mode, "stream_")
					for _, payload := range []string{
						`{"choices":[{"delta":{"content":"ok"},"finish_reason":"` + finish + `"}],"usage":null}`,
						`{"choices":[],"usage":` + usageJSON + `}`,
					} {
						var chunk ChatCompletionsChunk
						require.NoError(t, json.Unmarshal([]byte(payload), &chunk))
						ChatCompletionsChunkToResponsesEvents(&chunk, state)
					}
					for _, event := range FinalizeChatCompletionsResponsesStream(state) {
						if event.Response != nil && event.Response.Usage != nil {
							require.Nil(t, response, "usage must be emitted once")
							sse, err := ResponsesEventToSSE(event)
							require.NoError(t, err)
							_, data, ok := strings.Cut(sse, "data: ")
							require.True(t, ok)
							var decoded ResponsesStreamEvent
							require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(data)), &decoded))
							response = decoded.Response
						}
					}
					require.Empty(t, FinalizeChatCompletionsResponsesStream(state))
				}
				require.NotNil(t, response)
				if mode == "stream_length" {
					require.Equal(t, "incomplete", response.Status)
					require.Equal(t, "max_output_tokens", response.IncompleteDetails.Reason)
				} else {
					require.Equal(t, "completed", response.Status)
				}
				wire, err := json.Marshal(response.Usage)
				require.NoError(t, err)
				want := `{"input_tokens":100,"output_tokens":50,"total_tokens":150` + tt.want + `}`
				require.JSONEq(t, want, string(wire))

				chat := ResponsesToChatCompletions(response, "review-model")
				roundTrip, err := json.Marshal(ChatUsageToResponsesUsage(chat.Usage))
				require.NoError(t, err)
				require.JSONEq(t, want, string(roundTrip), "details must survive the return trip")
			})
		}
	}
}
