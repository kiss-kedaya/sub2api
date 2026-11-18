//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestGeminiR4BridgeErrorsOverrideChoices(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, content := range []string{"", "synthetic answer"} {
			for _, marker := range []string{"error-object", "error-string", "named-error", "named-error-with-type", "type-error", "type-failed", "status-failed", "finish-error"} {
				if !stream && strings.HasPrefix(marker, "named-error") {
					continue
				}
				if !stream && content != "" && (marker == "error-object" || marker == "error-string") {
					continue
				}
				t.Run(fmt.Sprintf("stream=%t/content=%q/%s", stream, content, marker), func(t *testing.T) {
					messageKey, finish, extra, event := "message", "null", `,"error":{"message":"synthetic failure"}`, ""
					if stream {
						messageKey = "delta"
					}
					switch marker {
					case "error-string":
						extra = `,"error":"synthetic failure"`
					case "named-error":
						event = "event: error\n"
					case "named-error-with-type":
						event, extra = "event: error\n", `,"type":"chat.completion.chunk"`
					case "type-error":
						extra = `,"type":"error"`
					case "type-failed":
						extra = `,"type":"response.failed"`
					case "status-failed":
						extra = `,"status":"failed"`
					case "finish-error":
						finish, extra = `"error"`, ""
					}
					body := fmt.Sprintf(`{"choices":[{"index":0,"%s":{"role":"assistant","content":%q},"finish_reason":%s}]%s,"usage":%s}`, messageKey, content, finish, extra, geminiBridgeChatUsage)
					if stream {
						body = event + "data: " + body + "\n\ndata: [DONE]\n\n"
					}
					result, err, output := geminiR4BridgeForward(t, APIProtocolChatCompletions, stream, body)
					require.Error(t, err)
					require.Contains(t, output, `"error"`)
					require.NotContains(t, output, `"finishReason"`)
					requireGeminiBridgeBilling(t, result, geminiBridgeAccount(APIProtocolChatCompletions), true)
				})
			}
		}
	}
}

func geminiR4BridgeForward(t *testing.T, protocol string, stream bool, body string) (*ForwardResult, error, string) {
	t.Helper()
	service := &GeminiMessagesCompatService{httpUpstream: &geminiBridgeUpstream{do: func(*http.Request) (*http.Response, error) {
		return geminiBridgeResponse(body), nil
	}}}
	account := geminiBridgeAccount(protocol)
	account.Credentials["base_url"] = "https://example.invalid/v1"
	c, rec := geminiBridgeContext()
	action := "generateContent"
	if stream {
		action = "streamGenerateContent"
	}
	result, err := service.ForwardNative(context.Background(), c, account, "gemini-2.5-flash", action, stream, []byte(geminiBridgeRequest))
	return result, err, rec.Body.String()
}

func TestGeminiR4BridgeJSONAnswerMetadata(t *testing.T) {
	for name, answer := range map[string]string{
		"text":      `"choices":[{"message":{"content":"synthetic answer"},"finish_reason":"stop"}]`,
		"thought":   `"choices":[{"message":{"reasoning_content":"synthetic thought"},"finish_reason":"stop"}]`,
		"tool":      `"choices":[{"message":{"tool_calls":[{"type":"function","function":{"name":"synthetic_tool"}}]},"finish_reason":"tool_calls"}]`,
		"responses": `"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"synthetic answer"}]}]`,
	} {
		t.Run(name, func(t *testing.T) {
			protocol, usage := APIProtocolChatCompletions, geminiBridgeChatUsage
			if name == "responses" {
				protocol, usage = APIProtocolResponses, geminiBridgeResponsesUsage
			}
			result, err, _ := geminiR4BridgeForward(t, protocol, false, "{"+answer+`,"error":"synthetic relay note","usage":`+usage+"}")
			require.NoError(t, err)
			requireGeminiBridgeBilling(t, result, geminiBridgeAccount(protocol), false)
		})
	}
}

func TestGeminiR4BridgeJSONPlaceholdersAreNotAnswers(t *testing.T) {
	for _, message := range []string{`{}`, `{"role":"assistant"}`, `{"content":""}`, `{"content":[]}`, `{"content":[{}]}`, `{"tool_calls":[{}]}`} {
		t.Run(message, func(t *testing.T) {
			body := `{"choices":[{"message":` + message + `}],"error":{"message":"synthetic failure"},"usage":` + geminiBridgeChatUsage + `}`
			result, err, output := geminiR4BridgeForward(t, APIProtocolChatCompletions, false, body)
			require.Error(t, err)
			require.Contains(t, output, `"error"`)
			requireGeminiBridgeBilling(t, result, geminiBridgeAccount(APIProtocolChatCompletions), true)
		})
	}
}

func TestGeminiR4NativeSuccessfulTerminalBaseline(t *testing.T) {
	cases := []struct{ name, candidates, marker string }{
		{"text", `[{"content":{"parts":[{"text":"synthetic answer"}]},"finishReason":%q}]`, "synthetic answer"},
		{"thought", `[{"content":{"parts":[{"text":"synthetic thought","thought":true}]},"finishReason":%q}]`, "synthetic thought"},
		{"tool", `[{"content":{"parts":[{"functionCall":{"name":"synthetic_tool"}}]},"finishReason":%q}]`, "synthetic_tool"},
		{"image", `[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"c3ludGhldGlj"}}]},"finishReason":%q}]`, "c3ludGhldGlj"},
		{"audio", `[{"content":{"parts":[{"inlineData":{"mimeType":"audio/pcm","data":"c3ludGhldGlj"}}]},"finishReason":%q}]`, "c3ludGhldGlj"},
		{"file", `[{"content":{"parts":[{"fileData":{"mimeType":"image/png","fileUri":"https://example.invalid/generated.png"}}]},"finishReason":%q}]`, "https://example.invalid/generated.png"},
		{"code", `[{"content":{"parts":[{"executableCode":{"language":"PYTHON","code":"print(1)"}}]},"finishReason":%q}]`, "print(1)"},
		{"code-result", `[{"content":{"parts":[{"codeExecutionResult":{"outcome":"OUTCOME_OK","output":"synthetic result"}}]},"finishReason":%q}]`, "synthetic result"},
		{"second-candidate", `[{"index":0,"finishReason":"STOP"},{"index":1,"content":{"parts":[{"text":"synthetic second answer"}]},"finishReason":%q}]`, "synthetic second answer"},
		{"split-candidate", `[{"index":1,"content":{"parts":[{"text":"synthetic second answer"}]},"finishReason":%q}]`, "synthetic second answer"},
	}
	for _, native := range []bool{false, true} {
		for _, mode := range []string{"buffered", "stream", "oauth-collected"} {
			for _, prelude := range []bool{false, true} {
				if prelude && mode == "buffered" {
					continue
				}
				for _, finish := range []string{"STOP", "MAX_TOKENS"} {
					for _, tc := range cases {
						t.Run(fmt.Sprintf("native=%t/%s/prelude=%t/%s/%s", native, mode, prelude, finish, tc.name), func(t *testing.T) {
							frame := `{"candidates":` + fmt.Sprintf(tc.candidates, finish) + `,` + geminiB2Usage + `}`
							body := frame
							if mode != "buffered" {
								frames := []string{}
								if prelude {
									frames = append(frames, geminiGaps3Error)
								}
								if tc.name == "split-candidate" {
									frames = append(frames, geminiGaps3EmptyStop)
								}
								body = geminiB2SSE(mode == "oauth-collected", append(frames, frame)...)
							}
							result, err, _, rec := geminiGaps3Forward(t, native, mode, body, nil)
							require.NoError(t, err, rec.Body.String())
							require.NotNil(t, result)
							require.Equal(t, ClaudeUsage{InputTokens: 10, OutputTokens: 2}, result.Usage)
							if native {
								require.Contains(t, rec.Body.String(), tc.marker)
							}
							require.False(t, result.NonBillableUpstreamError)
							cost := geminiR4ActualCost(t, result)
							require.Positive(t, cost)
							// Messages IDs are random; all other client bytes must match the baseline.
							output := regexp.MustCompile(`"id":"(?:msg_|toolu_)[^"]+"`).ReplaceAllString(rec.Body.String(), `"id":"synthetic-id"`)
							snapshot, marshalErr := json.Marshal([]any{rec.Code, result.Usage, result.NonBillableUpstreamError, cost, output})
							require.NoError(t, marshalErr)
							t.Logf("R4-SNAPSHOT %s", snapshot)
						})
					}
				}
			}
		}
	}
}

func geminiR4ActualCost(t *testing.T, result *ForwardResult) float64 {
	t.Helper()
	logs, settlement := &openAIRecordUsageLogRepoStub{inserted: true}, &openAIRecordUsageBillingRepoStub{}
	billing := NewBillingService(&config.Config{}, &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		result.Model: {InputCostPerToken: 1e-6, OutputCostPerToken: 2e-6},
	}})
	gateway := &GatewayService{usageLogRepo: logs, usageBillingRepo: settlement, billingService: billing,
		billingCacheService: &BillingCacheService{}, deferredService: &DeferredService{}}
	require.NoError(t, gateway.RecordUsage(context.Background(), &RecordUsageInput{
		Result: result, APIKey: &APIKey{ID: 81}, User: &User{ID: 82}, Account: geminiSignalTestAccount(),
	}))
	require.Equal(t, 1, logs.calls)
	require.Equal(t, 1, settlement.calls)
	require.NotNil(t, logs.lastLog)
	require.NotNil(t, settlement.lastCmd)
	require.Equal(t, result.Usage.InputTokens, logs.lastLog.InputTokens)
	require.Equal(t, result.Usage.OutputTokens, logs.lastLog.OutputTokens)
	require.Equal(t, logs.lastLog.ActualCost, settlement.lastCmd.BalanceCost)
	return logs.lastLog.ActualCost
}

func TestGeminiR4EmptySuccessfulTerminalRestoresBilling(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, mode := range []string{"stream", "oauth-collected"} {
			t.Run(fmt.Sprintf("native=%t/%s", native, mode), func(t *testing.T) {
				result, err, _, _ := geminiGaps3Forward(t, native, mode, geminiB2SSE(mode == "oauth-collected", geminiGaps3Error, geminiGaps3EmptyStop), nil)
				require.NoError(t, err)
				requireGeminiSignalBilling(t, result, false)
			})
		}
	}
}

func TestGeminiR4ResponsesTerminalNeedsNoTextDelivery(t *testing.T) {
	body := "event: error\ndata: {\"error\":\"synthetic prelude\"}\n\n" +
		"event: response.completed\ndata: {\"response\":{\"status\":\"completed\",\"output\":[],\"usage\":" + geminiBridgeResponsesUsage + "}}\n\n"
	result, err, output := geminiR4BridgeForward(t, APIProtocolResponses, true, body)
	require.NoError(t, err)
	require.Contains(t, output, `"finishReason":"STOP"`)
	require.False(t, strings.Contains(output, `"error"`))
	requireGeminiBridgeBilling(t, result, geminiBridgeAccount(APIProtocolResponses), false)
}
