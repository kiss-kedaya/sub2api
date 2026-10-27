package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestWSHTTPBridgeClientErrorPrivacyPreservesUsageAndSuccessfulContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, terminal := range []string{"response.failed", "bare_error_eof"} {
		t.Run(terminal, func(t *testing.T) {
			const partial = `{"type":"response.output_text.delta","response_id":"resp_privacy","delta":"Source https://public-article.example"}`
			const privateError = `{"type":"invalid_request_error","code":"cyber_policy","status_code":400,"param":"messages.0.content","message":"cyber policy rejected https://private-provider.example api_key=synthetic-private-token","account_name":"synthetic-private-account","credentials":{"api_key":"synthetic-private-credential"}}`
			failure := `{"type":"response.failed","response":{"id":"resp_privacy","status":"failed","error":` + privateError + `,"usage":{"input_tokens":13,"output_tokens":3}}}`
			if terminal == "bare_error_eof" {
				failure = `{"type":"error","error":` + privateError + `,"usage":{"input_tokens":13,"output_tokens":3}}`
			}
			body := "data: " + partial + "\n\ndata: " + failure + "\n\n"
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)),
			}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			account := &Account{ID: 112, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
			var writes [][]byte
			result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, account, "synthetic-token", payload, len(payload), "gpt-5", "", "", "", "", 2, func(message []byte) error {
				writes = append(writes, append([]byte(nil), message...))
				return nil
			})
			if terminal == "bare_error_eof" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.NotNil(t, result)
			require.Equal(t, "response.failed", result.UpstreamTerminalEvent)
			require.Equal(t, 13, result.Usage.InputTokens)
			require.Equal(t, 3, result.Usage.OutputTokens)
			require.Len(t, writes, 2)
			require.JSONEq(t, partial, string(writes[0]), "successful content must not be privacy-filtered")
			require.Equal(t, "response.failed", gjson.GetBytes(writes[1], "type").String())
			require.Equal(t, "cyber_policy", gjson.GetBytes(writes[1], "response.error.code").String())
			require.Equal(t, "invalid_request_error", gjson.GetBytes(writes[1], "response.error.type").String())
			for _, forbidden := range []string{"private-provider", "synthetic-private"} {
				require.NotContains(t, string(writes[1]), forbidden)
			}
			mark := GetOpsCyberPolicy(c)
			require.NotNil(t, mark)
			require.Equal(t, 13, mark.UpstreamInTok)
			require.Equal(t, 3, mark.UpstreamOutTok)
		})
	}
}

func TestForwardWSV2ClientErrorPrivacyAcrossStreamingAndJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		for _, eventType := range []string{"error", "response.failed"} {
			t.Run(fmt.Sprintf("%s_stream_%t", eventType, stream), func(t *testing.T) {
				const privateError = `{"type":"invalid_request_error","code":"cyber_policy","status_code":400,"param":"messages.0.content","message":"cyber policy rejected https://private-provider.example api_key=synthetic-private-token","account_name":"synthetic-private-account","credentials":{"api_key":"synthetic-private-credential"}}`
				event := []byte(`{"type":"response.failed","response":{"id":"resp_privacy_v2","status":"failed","error":` + privateError + `,"usage":{"input_tokens":17,"output_tokens":4}}}`)
				if eventType == "error" {
					event = []byte(`{"type":"error","error":` + privateError + `,"usage":{"input_tokens":17,"output_tokens":4}}`)
				}
				originalEvent := append([]byte(nil), event...)
				captureConn := &openAIWSCaptureConn{events: [][]byte{
					[]byte(`{"type":"response.output_text.delta","response_id":"resp_privacy_v2","delta":"Source https://public-article.example"}`), event,
				}}
				cfg := newOpenAIWSV2TestConfig()
				cfg.Security.URLAllowlist.Enabled = false
				cfg.Security.URLAllowlist.AllowInsecureHTTP = true
				cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
				dialer := &openAIWSCaptureDialer{conn: captureConn}
				pool := newOpenAIWSConnPool(cfg)
				pool.setClientDialerForTest(dialer)
				svc := &OpenAIGatewayService{
					cfg: cfg, httpUpstream: &httpUpstreamRecorder{}, cache: &stubGatewayCache{},
					openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(), openaiWSPool: pool,
				}
				account := &Account{
					ID: 5884, Name: "ws-privacy-fixture", Platform: PlatformOpenAI,
					Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
					Credentials: map[string]any{"api_key": "synthetic-token"},
					Extra:       map[string]any{"responses_websockets_v2_enabled": true},
				}
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				body := []byte(fmt.Sprintf(`{"model":"gpt-5.5","stream":%t,"input":"hello"}`, stream))
				result, err := svc.Forward(context.Background(), c, account, body)
				if eventType == "error" {
					require.Error(t, err)
					require.Nil(t, result)
					if !stream {
						require.Equal(t, http.StatusBadRequest, recorder.Code)
						require.Equal(t, "invalid_request_error", gjson.Get(recorder.Body.String(), "error.type").String())
					}
				} else {
					require.NoError(t, err)
					require.NotNil(t, result)
					require.True(t, result.OpenAIWSMode)
					require.Equal(t, 17, result.Usage.InputTokens)
					require.Equal(t, 4, result.Usage.OutputTokens)
				}
				for _, forbidden := range []string{"private-provider", "synthetic-private"} {
					require.NotContains(t, recorder.Body.String(), forbidden)
				}
				if stream {
					require.Contains(t, recorder.Body.String(), "https://public-article.example")
					require.Contains(t, recorder.Body.String(), `"code":"cyber_policy"`)
				}
				require.True(t, bytes.Equal(originalEvent, event), "write-side redaction must not mutate upstream event bytes")
				require.Equal(t, 1, dialer.DialCount(), "privacy filtering must not introduce a replay")
				mark := GetOpsCyberPolicy(c)
				require.NotNil(t, mark)
				require.Equal(t, 17, mark.UpstreamInTok)
				require.Equal(t, 4, mark.UpstreamOutTok)
			})
		}
	}
}
