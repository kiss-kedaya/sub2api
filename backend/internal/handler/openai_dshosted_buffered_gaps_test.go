//go:build unit

package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type dsHostedBufferedHandlerUpstream struct {
	service.HTTPUpstream
	protocol, wire, kind string
	turns, searches      int
}

func (u *dsHostedBufferedHandlerUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	kind, wire := "application/json", ""
	if strings.HasSuffix(req.URL.Path, "/tools/search") {
		u.searches++
		wire = `{"results":[{"title":"Synthetic result","url":"https://example.invalid/source"}]}`
	} else {
		u.turns++
		if u.turns > 2 {
			return nil, fmt.Errorf("unexpected synthetic turn %d", u.turns)
		}
		kind, wire = u.kind, u.wire
		if u.turns == 1 {
			kind = "application/json"
			switch u.protocol {
			case service.APIProtocolChatCompletions:
				wire = `{"id":"chatcmpl_synthetic_search","model":"deepseek-v4-flash","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_synthetic_search","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"synthetic\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`
			case service.APIProtocolAnthropic:
				kind = "text/event-stream"
				wire = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_synthetic_search\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"deepseek-v4-flash\",\"content\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":2}}}\n\n" +
					"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_synthetic_search\",\"name\":\"web_search\",\"input\":{\"query\":\"synthetic\"}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":2}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
			default:
				wire = `{"id":"resp_synthetic_search","object":"response","status":"completed","model":"deepseek-v4-flash","output":[{"type":"function_call","id":"fc_synthetic_search","call_id":"call_synthetic_search","name":"web_search","arguments":"{\"query\":\"synthetic\"}","status":"completed"}],"usage":{"input_tokens":10,"output_tokens":2}}`
			}
		}
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {kind}}, Body: io.NopCloser(strings.NewReader(wire))}, nil
}

func TestDSHostedBufferedGapsHandlerSettlement(t *testing.T) {
	gin.SetMode(gin.TestMode)
	errorFrame := "data: {\"error\":{\"code\":\"invalid_request_error\",\"message\":\"unknown parameter synthetic\"}}\n\n"
	usageFrame := "data: {\"type\":\"response.created\",\"response\":{\"usage\":{\"input_tokens\":20,\"output_tokens\":3}}}\n\n"
	emptyTerminal := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_synthetic_empty\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":20,\"output_tokens\":3}}}\n\n"
	answerTerminal := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_synthetic_answer\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"Synthetic final answer\"}]}],\"usage\":{\"input_tokens\":20,\"output_tokens\":3}}}\n\n"
	for _, tc := range []struct {
		name, protocol, kind, wire string
		free                       bool
		input, output              int
	}{
		{"responses_json_without_usage", service.APIProtocolResponses, "application/json", `{"error":{"message":"unknown parameter synthetic"}}`, true, 10, 2},
		{"native_json_without_usage", service.APIProtocolAnthropic, "application/json", "{\n\"error\":{\"message\":\"unknown parameter synthetic\"}\n}", true, 10, 2},
		{"chat_json_without_usage", service.APIProtocolChatCompletions, "application/json", `{"error":{"message":"unknown parameter synthetic"}}`, true, 10, 2},
		{"untyped_error_then_incomplete", service.APIProtocolResponses, "text/event-stream", errorFrame + "data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"output\":[],\"usage\":{\"input_tokens\":20,\"output_tokens\":3}}}\n\n", true, 30, 5},
		{"untyped_error_then_done", service.APIProtocolResponses, "text/event-stream", usageFrame + errorFrame + "data: [DONE]\n\n", true, 30, 5},
		{"error_then_empty_completed", service.APIProtocolResponses, "text/event-stream", errorFrame + emptyTerminal, true, 30, 5},
		{"error_then_answer_completed", service.APIProtocolResponses, "text/event-stream", errorFrame + answerTerminal, false, 30, 5},
		{"pure_EOF_keeps_charging", service.APIProtocolResponses, "text/event-stream", usageFrame, false, 30, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			groupID := int64(4101)
			account := service.Account{ID: 9101, Name: "synthetic-hosted", Platform: service.PlatformDeepseek, Type: service.AccountTypeAPIKey,
				Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{groupID},
				Credentials: map[string]any{"api_key": "synthetic-test-credential", "base_url": "https://example.invalid", "api_protocol": tc.protocol}}
			cfg := &config.Config{RunMode: config.RunModeStandard}
			cfg.Default.RateMultiplier, cfg.Gateway.MaxAccountSwitches = 1, 1
			admission := *cfg
			admission.RunMode = config.RunModeSimple
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, &admission, nil)
			t.Cleanup(billing.Stop)
			usage, ledger := &openAIWSFirstRefusalUsageRepo{}, &openAIWSFirstRefusalBillingRepo{}
			upstream := &dsHostedBufferedHandlerUpstream{protocol: tc.protocol, kind: tc.kind, wire: tc.wire}
			gateway := service.NewOpenAIGatewayService(&openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{account}}, usage, ledger,
				nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
			h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
			price := 5.0
			key := &service.APIKey{ID: 5101, UserID: 6101, GroupID: &groupID, User: &service.User{ID: 6101, Balance: 100, Status: service.StatusActive},
				Group: &service.Group{ID: groupID, Platform: service.PlatformDeepseek, Status: service.StatusActive, Hydrated: true, RateMultiplier: 1, SearchPricePer1k: &price, CrossProtocolConversionEnabled: true}}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"deepseek-v4-flash","stream":false,"input":"Search synthetic","tools":[{"type":"web_search"}]}`))
			c.Set(string(middleware.ContextKeyAPIKey), key)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.UserID})
			h.Responses(c)
			logs, commands := usage.snapshot(), ledger.snapshot()
			require.Equal(t, 2, upstream.turns)
			require.Equal(t, 1, upstream.searches)
			require.Len(t, logs, 1)
			require.Len(t, commands, 1)
			require.Equal(t, tc.input, logs[0].InputTokens)
			require.Equal(t, tc.output, logs[0].OutputTokens)
			if tc.free {
				require.Zero(t, logs[0].ActualCost)
				require.Zero(t, logs[0].TotalCost)
				require.Zero(t, commands[0].BalanceCost)
			} else {
				require.Greater(t, commands[0].BalanceCost, 0.005, "successful search surcharge and model tokens must both be billed")
			}
			require.True(t, json.Valid(recorder.Body.Bytes()), recorder.Body.String())
			if tc.name == "error_then_answer_completed" {
				require.Contains(t, recorder.Body.String(), "Synthetic final answer")
			}
		})
	}
}
