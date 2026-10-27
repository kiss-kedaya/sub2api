package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// A malformed hosted tool must fail before either the model or the dedicated
// search provider is contacted. Keep the real Forward entry point in the test
// so handler fallback cannot accidentally append SSE to its complete JSON error.
type deepSeekHostedValidationUpstream struct {
	calls int
}

func (u *deepSeekHostedValidationUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls++
	return nil, errors.New("invalid hosted tool unexpectedly reached upstream")
}

func (u *deepSeekHostedValidationUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func deepSeekHostedValidationFixture(t *testing.T, protocol string, stream, heartbeat bool, option string) (*service.OpenAIGatewayService, *gin.Context, *httptest.ResponseRecorder, *service.Account, []byte, *deepSeekHostedValidationUpstream) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	var c *gin.Context
	var recorder *httptest.ResponseRecorder
	if heartbeat {
		var stop func()
		c, recorder, stop = committedGatewayKeepaliveContext(t, EndpointResponses)
		t.Cleanup(stop)
	} else {
		recorder = httptest.NewRecorder()
		c, _ = gin.CreateTestContext(recorder)
	}
	body := []byte(fmt.Sprintf(`{"model":"deepseek-v4.1-flash","input":"Look up current documentation.","stream":%t,"tools":[{"type":"web_search",%s}],"tool_choice":"auto"}`, stream, option))
	c.Request = httptest.NewRequest(http.MethodPost, EndpointResponses, strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	price, providerKey := 5.0, "hosted-validation-search-fixture"
	group := &service.Group{ID: 36, Platform: service.PlatformDeepseek, SearchPricePer1k: &price, SearchProviderAPIKey: &providerKey}
	c.Set("api_key", &service.APIKey{ID: 1, Group: group, GroupID: &group.ID})
	account := &service.Account{
		ID: 7, Platform: service.PlatformDeepseek, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true,
		Credentials: map[string]any{
			"api_key": "hosted-validation-model-fixture", "api_protocol": protocol,
			"base_url": "https://api.deepseek.com",
		},
	}
	upstream := &deepSeekHostedValidationUpstream{}
	svc := service.NewOpenAIGatewayService(
		nil, nil, nil, nil, nil, nil, nil, &config.Config{}, nil, nil, nil,
		nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	t.Cleanup(svc.StopOpenAICodexTicketHarvester)
	return svc, c, recorder, account, body, upstream
}

func TestOpenAIDeepSeekHostedValidationWritesOneJSONErrorWithoutFallbackSSE(t *testing.T) {
	for _, protocol := range []string{service.APIProtocolResponses, service.APIProtocolChatCompletions} {
		for _, stream := range []bool{false, true} {
			for _, option := range []string{
				`"search_content_types":["video"]`,
				`"search_context_size":"unbounded"`,
				`"unrecognized_search_option":true`,
			} {
				t.Run(fmt.Sprintf("%s/stream=%t/%s", protocol, stream, option), func(t *testing.T) {
					svc, c, recorder, account, body, upstream := deepSeekHostedValidationFixture(t, protocol, stream, false, option)
					result, err := svc.Forward(context.Background(), c, account, body)
					require.Error(t, err)
					require.Nil(t, result)
					require.Zero(t, upstream.calls, "validation must precede model and search calls")
					require.Equal(t, http.StatusBadRequest, recorder.Code)
					require.True(t, service.IsResponseCommitted(c), "the service owns its complete validation response")
					beforeFallback := recorder.Body.String()
					h := &OpenAIGatewayHandler{}
					require.False(t, h.ensureForwardErrorResponse(c, stream))
					require.False(t, h.ensureForwardErrorResponse(c, !stream), "fallback must remain idempotent")
					require.Equal(t, beforeFallback, recorder.Body.String())
					require.Contains(t, recorder.Header().Get("Content-Type"), "application/json")
					require.NotContains(t, beforeFallback, "event:")
					require.NotContains(t, beforeFallback, "data:")
					require.NotContains(t, beforeFallback, "Upstream request failed")
					decoder := json.NewDecoder(strings.NewReader(beforeFallback))
					var payload map[string]json.RawMessage
					require.NoError(t, decoder.Decode(&payload))
					require.ErrorIs(t, decoder.Decode(&payload), io.EOF, "only one JSON document may be returned")
					require.Equal(t, "invalid_request_error", gjson.Get(beforeFallback, "error.type").String())
					require.Equal(t, "tools", gjson.Get(beforeFallback, "error.param").String())
					require.Contains(t, gjson.Get(beforeFallback, "error.message").String(), "deepseek hosted search")
				})
			}
		}
	}
}

func TestOpenAIDeepSeekHostedValidationAfterHeartbeatWritesOneResponseFailed(t *testing.T) {
	for _, protocol := range []string{service.APIProtocolResponses, service.APIProtocolChatCompletions} {
		t.Run(protocol, func(t *testing.T) {
			svc, c, recorder, account, body, upstream := deepSeekHostedValidationFixture(t, protocol, true, true, `"search_context_size":"unbounded"`)
			heartbeat := recorder.Body.String()
			require.NotEmpty(t, heartbeat)
			result, err := svc.Forward(context.Background(), c, account, body)
			require.Error(t, err)
			require.Nil(t, result)
			require.Zero(t, upstream.calls)
			require.True(t, service.IsResponseCommitted(c))
			require.Equal(t, http.StatusOK, recorder.Code, "an earlier heartbeat has already committed HTTP 200")
			streamErr, ok := service.GetOpsStreamError(c)
			require.True(t, ok)
			require.Equal(t, http.StatusBadRequest, streamErr.IntendedStatus)
			require.False(t, streamErr.CountTowardsSLA, "a client validation error is not a provider outage")
			completed := recorder.Body.String()
			require.True(t, strings.HasPrefix(completed, heartbeat), "earlier heartbeat bytes must be preserved")
			require.Equal(t, 1, strings.Count(completed, "event: response.failed\n"))
			require.NotContains(t, completed, "event: error\n")
			require.NotContains(t, completed, "Upstream request failed")
			terminal := strings.TrimPrefix(completed, heartbeat)
			require.True(t, strings.HasSuffix(terminal, "\n\n"))
			frames := strings.Split(strings.TrimSuffix(terminal, "\n\n"), "\n\n")
			require.Len(t, frames, 1, "validation needs one terminal event and no appended JSON document")
			lines := strings.Split(frames[0], "\n")
			require.Len(t, lines, 2)
			require.Equal(t, "event: response.failed", lines[0])
			require.True(t, strings.HasPrefix(lines[1], "data: "))
			raw := strings.TrimPrefix(lines[1], "data: ")
			require.True(t, json.Valid([]byte(raw)), "SSE data must contain one valid JSON object")
			event := gjson.Parse(raw)
			require.Equal(t, "response.failed", event.Get("type").String())
			require.Equal(t, "failed", event.Get("response.status").String())
			require.Equal(t, "invalid_request_error", event.Get("response.error.code").String())
			require.Contains(t, event.Get("response.error.message").String(), "search_context_size")
			h := &OpenAIGatewayHandler{}
			require.False(t, h.ensureForwardErrorResponse(c, true))
			require.False(t, h.ensureForwardErrorResponse(c, false))
			require.Equal(t, completed, recorder.Body.String(), "handler fallback must not append a second terminal event")
		})
	}
}
