//go:build unit

package handler

import (
	"fmt"
	"io"
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

type gaps3StreamUpstream struct {
	service.HTTPUpstream
	wire string
}

func (u *gaps3StreamUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(u.wire))}, nil
}

// Exercise the handler as well as settlement so an already-written failure
// cannot grow a second error during generic handler cleanup.
func TestOpenAIGaps3StreamHandlerBilling(t *testing.T) {
	logger.Init(logger.InitOptions{Level: "error", Output: logger.OutputOptions{ToStdout: true}})
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{service.APIProtocolResponses, service.APIProtocolChatCompletions, service.APIProtocolAnthropic} {
		for _, route := range []string{"responses", "chat", "messages"} {
			for _, mode := range []string{"failure", "empty_success", "answer_success", "disconnect"} {
				t.Run(fmt.Sprintf("%s/%s/%s", protocol, route, mode), func(t *testing.T) {
					groupID := int64(4001)
					account := service.Account{ID: 9910, Name: "synthetic-stream", Platform: service.PlatformDeepseek, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{groupID}, Credentials: map[string]any{"api_key": "synthetic", "base_url": "https://example.invalid", "api_protocol": protocol}}
					cfg := &config.Config{RunMode: config.RunModeStandard}
					cfg.Default.RateMultiplier = 1
					cfg.Gateway.MaxAccountSwitches = 1
					admission := *cfg
					admission.RunMode = config.RunModeSimple
					billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, &admission, nil)
					t.Cleanup(billing.Stop)
					usage := &openAIWSFirstRefusalUsageRepo{}
					ledger := &openAIWSFirstRefusalBillingRepo{}
					wire := gaps3HandlerWire(protocol, mode)
					gateway := service.NewOpenAIGatewayService(&openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{account}}, usage, ledger, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billing, &gaps3StreamUpstream{wire: wire}, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
					h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
					key := &service.APIKey{ID: 55, UserID: 77, GroupID: &groupID, User: &service.User{ID: 77, Balance: 100, Status: service.StatusActive}, Group: &service.Group{ID: groupID, Platform: service.PlatformDeepseek, Status: service.StatusActive, Hydrated: true, CrossProtocolConversionEnabled: true, RateMultiplier: 1}}
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					body := `{"model":"deepseek-v4-flash","stream":true,"input":"hello"}`
					if route != "responses" {
						body = `{"model":"deepseek-v4-flash","stream":true,"max_tokens":128,"messages":[{"role":"user","content":"hello"}]}`
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
					logs, commands := usage.snapshot(), ledger.snapshot()
					require.Len(t, logs, 1, rec.Body.String())
					require.Len(t, commands, 1)
					require.Positive(t, logs[0].InputTokens)
					if mode == "failure" {
						require.Zero(t, commands[0].BalanceCost)
						require.Zero(t, logs[0].ActualCost)
						require.Equal(t, 1, strings.Count(rec.Body.String(), "synthetic rejection"), rec.Body.String())
					} else {
						require.Positive(t, commands[0].BalanceCost)
						if mode != "empty_success" {
							require.Contains(t, rec.Body.String(), "synthetic answer")
						}
					}
				})
			}
		}
	}
}

func gaps3HandlerWire(protocol, mode string) string {
	var start, failure, answer, stop string
	switch protocol {
	case service.APIProtocolAnthropic:
		start = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_synthetic\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"deepseek-v4-flash\",\"content\":[],\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}}\n\n"
		failure = "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"synthetic rejection\"}}\n\n"
		answer = "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"synthetic answer\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"
		stop = "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	case service.APIProtocolChatCompletions:
		start = "data: {\"id\":\"chat_synthetic\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"\"}}],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":2}}\n\n"
		failure = "data: {\"error\":{\"message\":\"synthetic rejection\"}}\n\n"
		answer = "data: {\"choices\":[{\"delta\":{\"content\":\"synthetic answer\"}}]}\n\n"
		stop = "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	default:
		start = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_synthetic\",\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}}\n\n"
		failure = "data: {\"error\":{\"type\":\"invalid_request_error\",\"message\":\"synthetic rejection\"},\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}\n\n"
		answer = "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg_synthetic\",\"role\":\"assistant\",\"content\":[]}}\n\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_synthetic\",\"delta\":\"synthetic answer\"}\n\n"
		stop = "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_synthetic\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}}\n\n"
	}
	wire := start
	if mode != "disconnect" {
		wire += failure
	}
	if mode == "answer_success" || mode == "disconnect" {
		wire += answer
	}
	if mode == "answer_success" || mode == "empty_success" {
		wire += stop
	}
	return wire
}
