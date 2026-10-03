//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMessagesNativeProtocolSelection(t *testing.T) {
	for _, tc := range []struct {
		name, platform, protocol, accountType, nativeBase string
		want                                              bool
	}{
		{"adaptive CN native", PlatformZhipu, APIProtocolAdaptive, AccountTypeAPIKey, "", true},
		{"explicit native", PlatformKimi, APIProtocolAnthropic, AccountTypeAPIKey, "", true},
		{"explicit chat wins", PlatformKimi, APIProtocolChatCompletions, AccountTypeAPIKey, "https://native.example", false},
		{"explicit responses wins", PlatformDeepseek, APIProtocolResponses, AccountTypeAPIKey, "https://native.example", false},
		{"Gemini without messages endpoint", PlatformGemini, APIProtocolAdaptive, AccountTypeAPIKey, "", false},
		{"Gemini configured messages endpoint", PlatformGemini, APIProtocolAdaptive, AccountTypeAPIKey, "https://native.example", true},
		{"managed OAuth never uses editable native setting", PlatformKimi, APIProtocolAdaptive, AccountTypeOAuth, "https://native.example", false},
		{"OpenAI OAuth keeps Responses", PlatformOpenAI, APIProtocolAnthropic, AccountTypeOAuth, "https://native.example", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := adaptiveProtocolTestAccount(tc.platform, map[string]any{APIProtocolAnthropic: tc.nativeBase})
			a.Type = tc.accountType
			a.Credentials["api_protocol"] = tc.protocol
			require.Equal(t, tc.want, shouldForwardMessagesViaNativeAnthropic(a))
		})
	}
	require.False(t, shouldForwardMessagesViaNativeAnthropic(nil))
}

func TestNativeMessagesURLPreservesConfiguredPrefix(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
	for _, tc := range []struct{ base, want string }{
		{"http://native.example", "http://native.example/v1/messages"},
		{"http://native.example/v1/", "http://native.example/v1/messages"},
		{"http://native.example/anthropic", "http://native.example/anthropic/v1/messages"},
		{"http://native.example/anthropic/v1", "http://native.example/anthropic/v1/messages"},
		{"http://native.example/v1/messages/", "http://native.example/v1/messages"},
	} {
		t.Run(tc.base, func(t *testing.T) {
			a := adaptiveProtocolTestAccount(PlatformKimi, map[string]any{APIProtocolAnthropic: tc.base})
			u, err := svc.nativeAnthropicTargetURL(a)
			require.NoError(t, err)
			require.Equal(t, tc.want, u)
		})
	}
}

func TestNativeMessagesPassthroughPreservesBodyAndRecordsEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"k3","max_tokens":32,"stream":false,"metadata":{"user_id":"test-session"},"system":[{"type":"text","text":"keep","cache_control":{"type":"ephemeral","ttl":"1h"}}],"tools":[{"name":"terminal","input_schema":{"type":"object","properties":{"command":{"type":"string"}}}}],"messages":[{"role":"user","content":"hello"}],"custom_vendor_field":{"keep":true}}`)
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: nativeAnthropicBufferedResponse()}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			a := adaptiveProtocolTestAccount(PlatformKimi, map[string]any{APIProtocolAnthropic: "http://native.example/v1"})
			c := adaptiveProtocolTestContext("/v1/messages", body)
			c.Set("api_key", &APIKey{Group: &Group{CrossProtocolConversionEnabled: enabled}})
			c.Request.Header.Set("User-Agent", "native-test-client")
			c.Request.Header.Set("Anthropic-Beta", "client-beta")
			_, err := svc.ForwardAsAnthropic(context.Background(), c, a, body, "", "")
			require.NoError(t, err)
			require.Equal(t, body, upstream.lastBody)
			require.Equal(t, "http://native.example/v1/messages", upstream.lastReq.URL.String())
			require.Equal(t, "/v1/messages", GetActualOpenAIUpstreamEndpoint(c))
			require.Equal(t, "native-test-client", getHeaderRaw(upstream.lastReq.Header, "user-agent"))
			require.Equal(t, "client-beta", getHeaderRaw(upstream.lastReq.Header, "anthropic-beta"))
		})
	}
}

func TestAdaptiveMessagesWithoutNativeEndpointHonorsConversionSwitch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gemini-test","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`)
	upstream := &httpUpstreamRecorder{}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	a := adaptiveProtocolTestAccount(PlatformGemini, nil)
	a.Credentials["base_url"] = "http://responses.example"
	c := adaptiveProtocolTestContext("/v1/messages", body)
	c.Set("api_key", &APIKey{Group: &Group{CrossProtocolConversionEnabled: false}})
	_, err := svc.ForwardAsAnthropic(context.Background(), c, a, body, "", "")
	require.True(t, IsCrossProtocolConversionDisabled(err))
	require.Nil(t, upstream.lastReq)
	require.Equal(t, http.StatusBadRequest, c.Writer.Status())
}

func TestNativeMessagesErrorsDoNotSilentlyConvert(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, status := range []int{400, 401, 404, 405, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			body := []byte(`{"model":"k3","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`)
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{"error":{"type":"upstream_error","message":"native rejected"}}`)),
			}}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			a := adaptiveProtocolTestAccount(PlatformKimi, map[string]any{APIProtocolAnthropic: "http://native.example"})
			c := adaptiveProtocolTestContext("/v1/messages", body)
			c.Set("api_key", &APIKey{Group: &Group{CrossProtocolConversionEnabled: true}})
			_, err := svc.ForwardAsAnthropic(context.Background(), c, a, body, "", "")
			require.Error(t, err)
			require.Equal(t, "http://native.example/v1/messages", upstream.lastReq.URL.String())
			require.Equal(t, "/v1/messages", GetActualOpenAIUpstreamEndpoint(c))
		})
	}
}

func TestAdaptiveMessagesWithoutNativeEndpointUsesResponsesWhenAllowed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gemini-test","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`)
	upstream := &httpUpstreamRecorder{err: fmt.Errorf("stop after capture")}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	a := adaptiveProtocolTestAccount(PlatformGemini, nil)
	a.Credentials["base_url"] = "http://responses.example"
	c := adaptiveProtocolTestContext("/v1/messages", body)
	c.Set("api_key", &APIKey{Group: &Group{CrossProtocolConversionEnabled: true}})
	_, err := svc.ForwardAsAnthropic(context.Background(), c, a, body, "", "")
	require.Error(t, err)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "http://responses.example/v1/responses", upstream.lastReq.URL.String())
	require.Contains(t, string(upstream.lastBody), `"input"`)
}
