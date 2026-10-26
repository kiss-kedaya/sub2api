package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const requestedModelE2EClientModel = "gpt-5.2"

// Reuse the existing account repository and real HTTP transport fixtures. This
// router enters the production handler and forwarder; it does not install the
// response writer under test itself, so a missing handler hook is observable.
func newRequestedModelHTTPHandler(t *testing.T, upstreamURL string, enabled, passthrough bool, mapping map[string]any, configure ...func(*service.Account)) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	groupID := int64(4251)
	account := service.Account{
		ID: 9951, Name: "requested-model-http", Platform: service.PlatformOpenAI,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
		GroupIDs:    []int64{groupID},
		Credentials: map[string]any{"api_key": "fixture", "base_url": upstreamURL},
		Extra:       map[string]any{"openai_passthrough": passthrough},
	}
	if mapping != nil {
		account.Credentials["model_mapping"] = mapping
	}
	for _, apply := range configure {
		apply(&account)
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.MaxAccountSwitches = 1
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	gateway := service.NewOpenAIGatewayService(
		&openAIWSUsageHandlerAccountRepoStub{account: account}, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, billingCache,
		&openAIWSFirstRefusalHTTPUpstream{client: &http.Client{Timeout: 3 * time.Second}},
		&service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil,
	)
	handler := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billingCache,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	key := &service.APIKey{
		ID: 1851, UserID: 1751, GroupID: &groupID,
		User: &service.User{ID: 1751, Status: service.StatusActive},
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI,
			Status: service.StatusActive, Hydrated: true, ReturnRequestedModel: enabled},
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), key)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.User.ID})
		c.Next()
	})
	router.POST("/openai/v1/responses", handler.Responses)
	router.POST("/openai/v1/chat/completions", handler.ChatCompletions)
	return router
}

func requestedModelE2EResponse(t *testing.T, upstreamModel string) ([]byte, string, string) {
	t.Helper()
	text := `Literal model: ` + upstreamModel + `; JSON {"model":"` + upstreamModel + `"}`
	arguments := `{"model":"` + upstreamModel + `","nested":{"model":"` + upstreamModel + `"}}`
	body, err := json.Marshal(map[string]any{
		"id": "resp_requested_model", "object": "response", "status": "completed", "model": upstreamModel,
		"metadata": map[string]any{"model": upstreamModel},
		"output": []any{
			map[string]any{"id": "msg_requested_model", "type": "message", "role": "assistant", "status": "completed",
				"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}},
			map[string]any{"id": "fc_requested_model", "type": "function_call", "name": "route", "call_id": "call_requested_model",
				"arguments": arguments, "status": "completed"},
		},
		"usage": map[string]any{"input_tokens": 10, "output_tokens": 2, "total_tokens": 12},
	})
	require.NoError(t, err)
	return body, text, arguments
}

func requestedModelE2ESSE(t *testing.T, response []byte, upstreamModel, text, arguments string) string {
	t.Helper()
	frames := []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": "resp_requested_model", "object": "response", "model": upstreamModel, "status": "in_progress", "output": []any{}}},
		{"type": "response.output_text.delta", "item_id": "msg_requested_model", "output_index": 0, "content_index": 0, "delta": text},
		{"type": "response.function_call_arguments.delta", "item_id": "fc_requested_model", "output_index": 1, "delta": arguments},
		{"type": "response.completed", "response": json.RawMessage(response)},
	}
	var body strings.Builder
	for index, frame := range frames {
		frame["sequence_number"] = index
		encoded, err := json.Marshal(frame)
		require.NoError(t, err)
		fmt.Fprintf(&body, "event: %s\ndata: %s\n\n", frame["type"], encoded)
	}
	_, _ = body.WriteString("data: [DONE]\n\n")
	return body.String()
}

func TestRequestedModelFullHTTPHandlerJSONAndSSE(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, fixture := range []struct {
			name        string
			enabled     bool
			mapped      bool
			passthrough bool
			publicModel string
		}{
			{name: "enabled_without_mapping", enabled: true, passthrough: true},
			{name: "disabled_without_mapping", passthrough: true},
			{name: "enabled_without_mapping_native_forwarder", enabled: true},
			{name: "disabled_without_mapping_native_forwarder"},
			{name: "existing_mapping_while_disabled", mapped: true},
			{name: "existing_mapping_while_enabled", mapped: true, enabled: true},
			{name: "public_context_model_precedes_body", enabled: true, passthrough: true, publicModel: "client-public-model"},
		} {
			t.Run(fmt.Sprintf("%s/stream=%t", fixture.name, stream), func(t *testing.T) {
				upstreamModel := "provider-opaque-v3"
				wantOutboundModel := requestedModelE2EClientModel
				var mapping map[string]any
				if fixture.mapped {
					upstreamModel = "gpt-4.1"
					wantOutboundModel = upstreamModel
					mapping = map[string]any{requestedModelE2EClientModel: upstreamModel}
				}
				response, text, arguments := requestedModelE2EResponse(t, upstreamModel)
				sse := requestedModelE2ESSE(t, response, upstreamModel, text, arguments)
				requests := make(chan []byte, 2)
				upstreamErrors := make(chan error, 2)
				var calls atomic.Int32
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					body, err := io.ReadAll(r.Body)
					upstreamErrors <- err
					requests <- body
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, err = io.WriteString(w, sse)
						if flusher, ok := w.(http.Flusher); ok {
							flusher.Flush()
						}
					} else {
						w.Header().Set("Content-Type", "application/json")
						_, err = w.Write(response)
					}
					upstreamErrors <- err
				}))
				defer upstream.Close()
				router := newRequestedModelHTTPHandler(t, upstream.URL, fixture.enabled, fixture.passthrough, mapping)
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, "/openai/v1/responses",
					strings.NewReader(fmt.Sprintf(`{"model":%q,"input":"hello","stream":%t}`, requestedModelE2EClientModel, stream)))
				request.Header.Set("Content-Type", "application/json")
				if fixture.publicModel != "" {
					request = request.WithContext(context.WithValue(request.Context(), ctxkey.RequestedPublicModel, fixture.publicModel))
				}
				router.ServeHTTP(recorder, request)
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				require.Equal(t, int32(1), calls.Load(), "response decoration must not retry or change scheduling")
				require.NoError(t, <-upstreamErrors)
				require.NoError(t, <-upstreamErrors)
				require.Equal(t, wantOutboundModel, gjson.GetBytes(<-requests, "model").String(), "response decoration must not change outbound model selection")
				wantModel := upstreamModel
				if fixture.enabled || fixture.mapped {
					wantModel = requestedModelE2EClientModel
				}
				if fixture.enabled && fixture.publicModel != "" {
					wantModel = fixture.publicModel
				}
				if !stream {
					assertRequestedModelE2EPayload(t, recorder.Body.Bytes(), wantModel, upstreamModel, text, arguments)
					return
				}
				require.Contains(t, recorder.Header().Get("Content-Type"), "text/event-stream")
				seen := map[string]bool{}
				for _, line := range strings.Split(recorder.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data:") {
						continue
					}
					payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
					if payload == "[DONE]" || payload == "" {
						continue
					}
					event := gjson.Get(payload, "type").String()
					seen[event] = true
					switch event {
					case "response.created":
						require.Equal(t, wantModel, gjson.Get(payload, "response.model").String())
					case "response.output_text.delta":
						require.Equal(t, text, gjson.Get(payload, "delta").String())
					case "response.function_call_arguments.delta":
						require.Equal(t, arguments, gjson.Get(payload, "delta").String())
					case "response.completed":
						assertRequestedModelE2EPayload(t, []byte(gjson.Get(payload, "response").Raw), wantModel, upstreamModel, text, arguments)
					}
				}
				for _, event := range []string{"response.created", "response.output_text.delta", "response.function_call_arguments.delta", "response.completed"} {
					require.True(t, seen[event], "missing %s: %s", event, recorder.Body.String())
				}
			})
		}
	}
}

func assertRequestedModelE2EPayload(t *testing.T, body []byte, wantModel, upstreamModel, text, arguments string) {
	t.Helper()
	require.Equal(t, wantModel, gjson.GetBytes(body, "model").String())
	require.Equal(t, "resp_requested_model", gjson.GetBytes(body, "id").String())
	require.Equal(t, upstreamModel, gjson.GetBytes(body, "metadata.model").String(), "nested business metadata is not the protocol model field")
	require.Equal(t, text, gjson.GetBytes(body, "output.0.content.0.text").String())
	require.Equal(t, arguments, gjson.GetBytes(body, "output.1.arguments").String())
	require.Equal(t, upstreamModel, gjson.Get(gjson.GetBytes(body, "output.1.arguments").String(), "nested.model").String())
}

func TestRequestedModelFullChatCompletionsHandlerJSONAndSSE(t *testing.T) {
	const upstreamModel = "provider-chat-v3"
	const text = `Literal {"model":"provider-chat-v3"}`
	const arguments = `{"model":"provider-chat-v3","nested":{"model":"provider-chat-v3"}}`
	for _, stream := range []bool{false, true} {
		for _, fixture := range []struct {
			name, publicModel string
			enabled           bool
		}{
			{name: "disabled"},
			{name: "enabled", enabled: true},
			{name: "public_context_model_precedes_body", enabled: true, publicModel: "client-public-model"},
		} {
			t.Run(fmt.Sprintf("%s/stream=%t", fixture.name, stream), func(t *testing.T) {
				message := map[string]any{"role": "assistant", "content": text, "tool_calls": []any{
					map[string]any{"index": 0, "id": "call_chat_model", "type": "function",
						"function": map[string]any{"name": "route", "arguments": arguments}},
				}}
				response := map[string]any{"id": "chatcmpl_requested_model", "model": upstreamModel,
					"object": "chat.completion", "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12},
					"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": "tool_calls"}}}
				if stream {
					response["object"] = "chat.completion.chunk"
					response["choices"] = []any{map[string]any{"index": 0, "delta": message, "finish_reason": "tool_calls"}}
				}
				encoded, err := json.Marshal(response)
				require.NoError(t, err)
				requests := make(chan []byte, 1)
				paths := make(chan string, 1)
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, readErr := io.ReadAll(r.Body)
					if readErr != nil {
						http.Error(w, readErr.Error(), http.StatusInternalServerError)
						return
					}
					requests <- body
					paths <- r.URL.Path
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", encoded)
						if flusher, ok := w.(http.Flusher); ok {
							flusher.Flush()
						}
					} else {
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write(encoded)
					}
				}))
				defer upstream.Close()
				router := newRequestedModelHTTPHandler(t, upstream.URL, fixture.enabled, false, nil, func(account *service.Account) {
					account.Extra[openai_compat.ExtraKeyResponsesMode] = string(openai_compat.ResponsesSupportModeForceChatCompletions)
				})
				request := httptest.NewRequest(http.MethodPost, "/openai/v1/chat/completions",
					strings.NewReader(fmt.Sprintf(`{"model":%q,"stream":%t,"messages":[{"role":"user","content":"hello"}]}`, requestedModelE2EClientModel, stream)))
				request.Header.Set("Content-Type", "application/json")
				if fixture.publicModel != "" {
					request = request.WithContext(context.WithValue(request.Context(), ctxkey.RequestedPublicModel, fixture.publicModel))
				}
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, request)
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				require.Equal(t, "/v1/chat/completions", <-paths, "exercise the actual native chat forwarder")
				require.Equal(t, requestedModelE2EClientModel, gjson.GetBytes(<-requests, "model").String())
				payload := recorder.Body.String()
				messagePath := "choices.0.message"
				if stream {
					found := false
					for _, line := range strings.Split(payload, "\n") {
						if strings.HasPrefix(line, "data:") {
							candidate := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
							if gjson.Valid(candidate) && gjson.Get(candidate, "choices.0.delta").Exists() {
								payload, found = candidate, true
								break
							}
						}
					}
					require.True(t, found, recorder.Body.String())
					messagePath = "choices.0.delta"
				}
				wantModel := upstreamModel
				if fixture.enabled {
					wantModel = requestedModelE2EClientModel
					if fixture.publicModel != "" {
						wantModel = fixture.publicModel
					}
				}
				require.Equal(t, wantModel, gjson.Get(payload, "model").String())
				require.Equal(t, text, gjson.Get(payload, messagePath+".content").String())
				require.Equal(t, arguments, gjson.Get(payload, messagePath+".tool_calls.0.function.arguments").String())
			})
		}
	}
}

func TestRequestedModelFullHTTPHandlerPreservesErrorStatusAndBody(t *testing.T) {
	const upstreamBody = `{"model":"provider-error-model","error":{"type":"invalid_request_error","code":"invalid_request","message":"keep provider-error-model unchanged","model":"provider-error-model","param":"input"}}`
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, upstreamBody)
			}))
			defer upstream.Close()
			var baseline string
			for _, enabled := range []bool{false, true} {
				router := newRequestedModelHTTPHandler(t, upstream.URL, enabled, true, nil)
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, "/openai/v1/responses",
					strings.NewReader(fmt.Sprintf(`{"model":%q,"input":"hello","stream":%t}`, requestedModelE2EClientModel, stream)))
				request.Header.Set("Content-Type", "application/json")
				router.ServeHTTP(recorder, request)
				require.Equal(t, http.StatusBadRequest, recorder.Code)
				require.Contains(t, recorder.Body.String(), "keep provider-error-model unchanged")
				if !enabled {
					// The existing OpenAI passthrough error policy builds a safe
					// envelope. The new setting must preserve that exact result.
					baseline = recorder.Body.String()
				} else {
					require.Equal(t, baseline, recorder.Body.String(), "the response decorator must preserve the existing error body byte for byte")
				}
			}
			require.Equal(t, int32(2), calls.Load(), "each error request must make exactly one upstream attempt")
		})
	}
}

func TestRequestedModelFullWebSocketHandlerWithoutMapping(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("enabled=%t", enabled), func(t *testing.T) {
			const upstreamModel = "provider-ws-v3"
			response, text, arguments := requestedModelE2EResponse(t, upstreamModel)
			upstreamErrors := make(chan error, 3)
			upstreamRequests := make(chan []byte, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, nil)
				upstreamErrors <- err
				if err != nil {
					return
				}
				defer func() { _ = conn.CloseNow() }()
				_, request, err := conn.Read(r.Context())
				upstreamErrors <- err
				if err != nil {
					return
				}
				upstreamRequests <- request
				event := append([]byte(`{"type":"response.completed","response":`), response...)
				event = append(event, '}')
				upstreamErrors <- conn.Write(r.Context(), coderws.MessageText, event)
				_, _, _ = conn.Read(r.Context())
			}))
			defer upstream.Close()
			harness := newOpenAIWSFirstRefusalHarness(t, service.PlatformOpenAI, upstream.URL, 0)
			harness.apiKey.Group.ReturnRequestedModel = enabled
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client, _, err := coderws.Dial(ctx, harness.url, &coderws.DialOptions{HTTPHeader: harness.headers})
			require.NoError(t, err)
			defer func() { _ = client.CloseNow() }()
			request := fmt.Sprintf(`{"type":"response.create","model":%q,"input":"hello"}`, requestedModelE2EClientModel)
			require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(request)))
			_, event, err := client.Read(ctx)
			require.NoError(t, err)
			require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())
			wantModel := upstreamModel
			if enabled {
				wantModel = requestedModelE2EClientModel
			}
			assertRequestedModelE2EPayload(t, []byte(gjson.GetBytes(event, "response").Raw), wantModel, upstreamModel, text, arguments)
			require.Equal(t, requestedModelE2EClientModel, gjson.GetBytes(<-upstreamRequests, "model").String())
			for index := 0; index < 3; index++ {
				require.NoError(t, <-upstreamErrors)
			}
		})
	}
}
