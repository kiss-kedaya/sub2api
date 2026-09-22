//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGrokProductionAccountsPassthroughUnlistedGrok47(t *testing.T) {
	mysandbox := &Account{
		ID: 29088, Platform: PlatformGrok, Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"grok-4.5":       "grok-4.5",
				"grok-4.6":       "grok-4.6",
				"grok-build-0.1": "grok-build-0.1",
			},
		},
	}
	topgo := &Account{
		ID: 29131, Platform: PlatformGrok, Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"grok-4.5": "grok-4.5",
				"grok-4.6": "grok-4.6",
				"grok-4.7": "grok-4.7",
			},
		},
	}
	empty := &Account{ID: 1, Platform: PlatformGrok, Type: AccountTypeAPIKey, Credentials: map[string]any{}}

	require.False(t, mysandbox.IsModelSupported("grok-4.7"))
	require.True(t, topgo.IsModelSupported("grok-4.7"))
	require.True(t, empty.IsModelSupported("grok-4.7"))
	require.Equal(t, "grok-4.7", empty.GetMappedModel("grok-4.7"))
	require.Equal(t, "grok-4.7", topgo.GetMappedModel("grok-4.7"))
	require.False(t, mysandbox.IsModelSupported("gpt-5.6-sol"))
}

func TestForwardGrokResponsesStripsOpenAIFieldsForAPIKeyPing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"grok-4.7","input":"ping","stream":false,"store":false,"service_tier":"priority"}`)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"resp_ping","object":"response","model":"grok-4.7-build","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)),
	}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := grokProtocolAPIKeyAccount(29131)
	account.Credentials["base_url"] = "https://router.91topgo.com/v1"
	account.Credentials["model_mapping"] = map[string]any{"grok-4.7": "grok-4.7"}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Set("api_key", &APIKey{ID: 31896, Group: &Group{ID: 43, Platform: PlatformOpenAI}})
	c.Request = c.Request.WithContext(WithResolvedTargetPlatform(context.Background(), PlatformOpenAI))
	c.Request = c.Request.WithContext(WithOpenAIForwardModel(c.Request.Context(), "grok-4.7", false))

	result, err := svc.forwardGrokResponses(context.Background(), c, account, body, "grok-4.7", false, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, gjson.GetBytes(upstream.lastBody, "store").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "service_tier").Exists())
	require.Equal(t, "grok-4.7", gjson.GetBytes(upstream.lastBody, "model").String())
}
