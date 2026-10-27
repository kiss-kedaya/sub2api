package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCopilotSDKForwardEnforcesImageGroupGateBeforeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/v1/responses", "/v1/responses/compact"} {
		for _, body := range []string{
			`{"model":"gpt-image-1","input":"draw a duck"}`,
			`{"model":"auto","tools":[{"type":"image_generation"}],"input":"draw a duck"}`,
		} {
			t.Run(path+"/"+gjson.Get(body, "model").String(), func(t *testing.T) {
				upstream := &httpUpstreamRecorder{}
				svc := openAIClientToolsTestService(upstream)
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				c.Set("api_key", &APIKey{ID: 42, Group: &Group{ID: 7, AllowImageGeneration: false}})
				a := &Account{ID: 71, Status: StatusActive, Schedulable: true, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"openai_copilot_sdk": true}}
				_, err := svc.Forward(c.Request.Context(), c, a, []byte(body))
				require.Error(t, err)
				require.Equal(t, http.StatusForbidden, recorder.Code)
				require.Contains(t, recorder.Body.String(), ImageGenerationPermissionMessage())
				require.Empty(t, upstream.requests, "denied images must not create an SDK turn")
			})
		}
	}
}

func TestCopilotSDKForwardEnforcesFastPolicyBeforeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := &OpenAIFastPolicySettings{Rules: []OpenAIFastPolicyRule{{
		ServiceTier: OpenAIFastTierPriority, Action: BetaPolicyActionBlock,
		Scope: BetaPolicyScopeAll, ErrorMessage: "priority unavailable",
		ModelWhitelist: []string{"auto"}, FallbackAction: BetaPolicyActionPass,
	}}}
	for _, path := range []string{"/v1/responses", "/v1/responses/compact"} {
		t.Run(path, func(t *testing.T) {
			body := []byte(`{"model":"auto","service_tier":"fast","input":"hello"}`)
			upstream := &httpUpstreamRecorder{}
			svc := openAIClientToolsTestService(upstream)
			svc.settingService = newOpenAIGatewayServiceWithSettings(t, settings).settingService
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
			c.Set("api_key", &APIKey{ID: 42})
			a := &Account{ID: 71, Status: StatusActive, Schedulable: true, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"openai_copilot_sdk": true}}
			_, err := svc.Forward(c.Request.Context(), c, a, body)
			require.Error(t, err)
			require.Equal(t, http.StatusForbidden, recorder.Code)
			require.Contains(t, recorder.Body.String(), "priority unavailable")
			require.Empty(t, upstream.requests, "blocked priority must not create an SDK turn")
		})
	}
}

func TestCopilotSDKGroupForcedFastPreservesNativeToolAndHistoryBytes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"auto","stream":false,"tools":[ { "type":"custom", "name":"apply_patch", "format":{"type":"grammar","syntax":"lark","definition":"start: /.+/"} } ],"input":[ { "type":"reasoning", "encrypted_content":"opaque" }, {"type":"custom_tool_call","call_id":"s2cp1_signed","input":"patch","opaque_number":9007199254740993}, {"type":"custom_tool_call_output","call_id":"s2cp1_signed","output":"ok"} ]}`)
	response := `{"id":"resp_sdk","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":1}}`
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}}
	svc := openAIClientToolsTestService(upstream)
	svc.settingService = newOpenAIGatewayServiceWithSettings(t, DefaultOpenAIFastPolicySettings()).settingService
	group := &Group{ID: 7, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, ForceOpenAIFast: true}
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body)).WithContext(ctx)
	c.Set("api_key", &APIKey{ID: 42, Group: group})
	a := &Account{ID: 71, Status: StatusActive, Schedulable: true, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "sidecar-key"}, Extra: map[string]any{"openai_copilot_sdk": true}}
	result, err := svc.Forward(ctx, c, a, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, OpenAIFastTierPriority, gjson.GetBytes(upstream.lastBody, "service_tier").String())
	require.Equal(t, gjson.GetBytes(body, "tools").Raw, gjson.GetBytes(upstream.lastBody, "tools").Raw)
	require.Equal(t, gjson.GetBytes(body, "input").Raw, gjson.GetBytes(upstream.lastBody, "input").Raw)
	require.Equal(t, "9007199254740993", gjson.GetBytes(upstream.lastBody, "input.1.opaque_number").Raw)
}
