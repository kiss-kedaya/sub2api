//go:build unit

package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Keep the real handler and settlement path; only provider I/O and storage are stubs.
func r4RunHandler(t *testing.T, protocol, route, wire, marker string, stream, free, failed bool) {
	t.Helper()
	logger.Init(logger.InitOptions{Level: "error", Output: logger.OutputOptions{ToStdout: true}})
	gin.SetMode(gin.TestMode)
	groupID := int64(4001)
	account := service.Account{ID: 9910, Name: "synthetic-stream", Platform: service.PlatformDeepseek, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{groupID},
		Credentials: map[string]any{"api_key": "synthetic", "base_url": "https://example.invalid", "api_protocol": protocol}}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Default.RateMultiplier, cfg.Gateway.MaxAccountSwitches = 1, 1
	admission := *cfg
	admission.RunMode = config.RunModeSimple
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, &admission, nil)
	t.Cleanup(billing.Stop)
	usage, ledger := &openAIWSFirstRefusalUsageRepo{}, &openAIWSFirstRefusalBillingRepo{}
	gateway := service.NewOpenAIGatewayService(&openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{account}}, usage, ledger,
		nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billing,
		&gaps3StreamUpstream{wire: wire}, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	key := &service.APIKey{ID: 55, UserID: 77, GroupID: &groupID, User: &service.User{ID: 77, Balance: 100, Status: service.StatusActive},
		Group: &service.Group{ID: groupID, Platform: service.PlatformDeepseek, Status: service.StatusActive, Hydrated: true, CrossProtocolConversionEnabled: true, RateMultiplier: 1}}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := fmt.Sprintf(`{"model":"deepseek-v4-flash","stream":%t,"input":"hello"}`, stream)
	if route != "responses" {
		body = fmt.Sprintf(`{"model":"deepseek-v4-flash","stream":%t,"max_tokens":128,"messages":[{"role":"user","content":"hello"}]}`, stream)
	}
	if strings.Contains(wire, "custom_tool_call") && route == "responses" {
		body = strings.TrimSuffix(body, "}") + `,"tools":[{"type":"custom","name":"synthetic_tool","description":"Synthetic tool"}]}`
	}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+route, strings.NewReader(body))
	c.Set(string(middleware.ContextKeyAPIKey), key)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 77})
	switch route {
	case "responses":
		h.Responses(c)
	case "chat":
		h.ChatCompletions(c)
	case "messages":
		h.Messages(c)
	}
	rows, commands := usage.snapshot(), ledger.snapshot()
	require.Len(t, rows, 1, rec.Body.String())
	require.Len(t, commands, 1, rec.Body.String())
	t.Logf("R4 cost=%.9f actual=%.9f input=%d output=%d status=%d body=%s", commands[0].BalanceCost, rows[0].ActualCost,
		rows[0].InputTokens, rows[0].OutputTokens, rec.Code, rec.Body.String())
	if free {
		require.Zero(t, commands[0].BalanceCost)
		require.Zero(t, rows[0].ActualCost)
	} else {
		require.Positive(t, commands[0].BalanceCost)
	}
	require.Contains(t, rec.Body.String(), marker)
	if !stream {
		require.True(t, json.Valid(rec.Body.Bytes()), rec.Body.String())
		if failed {
			require.GreaterOrEqual(t, rec.Code, http.StatusBadRequest)
		} else {
			require.Equal(t, http.StatusOK, rec.Code)
		}
	}
	if !failed {
		require.NotContains(t, rec.Body.String(), "upstream_error")
		require.NotContains(t, rec.Body.String(), `"type":"response.failed"`)
	}
}

const r4ResponsesStart = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_synthetic\",\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}}\n\n"
const r4Error = "data: {\"error\":{\"message\":\"synthetic rejection\"}}\n\n"
const r4ResponsesStop = "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_synthetic\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}}\n\n"

func TestOpenAIR4ChatExplicitErrorWithChoices(t *testing.T) {
	for _, marker := range []string{`"type":"error"`, `"type":"response.failed"`, `"status":"failed"`, `"synthetic":true`} {
		for _, route := range []string{"responses", "chat", "messages"} {
			t.Run(marker+"/"+route, func(t *testing.T) {
				eventHeader := ""
				if marker == `"synthetic":true` {
					eventHeader = "event: error\n"
				}
				wire := "data: {\"id\":\"chat_synthetic\",\"choices\":[{\"delta\":{\"role\":\"assistant\"}}],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":2}}\n\n" +
					eventHeader + "data: {" + marker + ",\"choices\":[{\"delta\":{},\"finish_reason\":\"error\"}],\"error\":{\"type\":\"invalid_request_error\",\"message\":\"synthetic rejection\"}}\n\ndata: [DONE]\n\n"
				r4RunHandler(t, service.APIProtocolChatCompletions, route, wire, "synthetic rejection", true, true, true)
			})
		}
	}
}

func r4ReasoningWire(protocol string, recovery bool) string {
	failure := r4Error
	if !recovery {
		failure = ""
	}
	switch protocol {
	case service.APIProtocolChatCompletions:
		return "data: {\"id\":\"chat_synthetic\",\"choices\":[{\"delta\":{\"role\":\"assistant\"}}],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":2}}\n\n" + failure +
			"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"synthetic thinking\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n"
	case service.APIProtocolAnthropic:
		if recovery {
			failure = "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"synthetic rejection\"}}\n\n"
		}
		return "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_synthetic\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"deepseek-v4-flash\",\"content\":[],\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}}\n\n" + failure +
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"synthetic thinking\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"},\"usage\":{\"output_tokens\":2}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	default:
		return r4ResponsesStart + failure + "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"reasoning\",\"id\":\"rs_synthetic\"}}\n\ndata: {\"type\":\"response.reasoning_text.delta\",\"output_index\":0,\"item_id\":\"rs_synthetic\",\"delta\":\"synthetic thinking\"}\n\n" + r4ResponsesStop
	}
}

func TestOpenAIR4ReasoningSuccess(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		for _, protocol := range []string{service.APIProtocolResponses, service.APIProtocolChatCompletions, service.APIProtocolAnthropic} {
			for _, route := range []string{"responses", "chat", "messages"} {
				t.Run(fmt.Sprintf("recovery=%t/%s/%s", recovery, protocol, route), func(t *testing.T) {
					r4RunHandler(t, protocol, route, r4ReasoningWire(protocol, recovery), "synthetic thinking", true, false, false)
				})
			}
		}
	}
}

func TestOpenAIR4BufferedCompletedSideNote(t *testing.T) {
	wire := r4ResponsesStart + "data: {\"type\":\"response.completed\",\"error\":{\"message\":\"synthetic relay note\"},\"response\":{\"id\":\"resp_synthetic\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"synthetic real answer\"}]}],\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}}\n\n"
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			r4RunHandler(t, service.APIProtocolResponses, "responses", wire, "synthetic real answer", stream, false, false)
		})
	}
}

func TestOpenAIR4BufferedToolRecovery(t *testing.T) {
	for _, tool := range []string{`{"type":"function_call","call_id":"call_synthetic","name":"synthetic_tool","status":"completed"}`, `{"type":"custom_tool_call","call_id":"call_synthetic","name":"synthetic_tool","input":"","status":"completed"}`} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tool, stream), func(t *testing.T) {
				wire := r4ResponsesStart + r4Error + "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":" + tool + "}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_synthetic\",\"status\":\"completed\",\"output\":[" + tool + "],\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}}\n\n"
				r4RunHandler(t, service.APIProtocolResponses, "responses", wire, "synthetic_tool", stream, false, false)
			})
		}
	}
}

func TestOpenAIR4BufferedExplicitErrorWithChoices(t *testing.T) {
	for _, marker := range []string{`"type":"error",`, `"type":"response.failed",`, `"status":"failed",`, ""} {
		for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
			t.Run(marker+endpoint, func(t *testing.T) {
				rec, rows, commands := runBufferedEnvelopeRequest(t, endpoint,
					`{`+marker+`"choices":[{"message":{"role":"assistant","content":""},"finish_reason":"error"}],"error":{"type":"invalid_request_error","message":"synthetic rejection"},"usage":{"prompt_tokens":1000,"completion_tokens":2}}`)
				require.Len(t, rows, 1)
				require.Len(t, commands, 1)
				require.Zero(t, commands[0].BalanceCost)
				require.Zero(t, rows[0].ActualCost)
				require.GreaterOrEqual(t, rec.Code, http.StatusBadRequest)
				require.True(t, json.Valid(rec.Body.Bytes()), rec.Body.String())
				require.Contains(t, rec.Body.String(), "synthetic rejection")
			})
		}
	}
}
