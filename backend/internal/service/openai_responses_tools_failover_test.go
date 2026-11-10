//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Sol/Terra tool calls only work through the Responses API. A managed gateway
// must fail over to another account instead of committing the 400 and losing the
// request, and the local mismatch must never be attributed to account health.
func TestResponsesSolToolsCapabilityFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tools := range []struct{ name, fields string }{
		{"function", `"input":"hello","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]`},
		{"custom", `"input":"hello","tools":[{"type":"custom","name":"exec"}]`},
		{"namespace", `"input":"hello","tools":[{"type":"namespace","name":"shell","tools":[{"type":"function","name":"pwd","parameters":{"type":"object"}}]}]`},
		{"additional", `"input":[{"type":"additional_tools","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]},{"role":"user","content":"hello"}]`},
		{"native", `"input":"hello","tools":[{"type":"computer_use_preview","display_width":1024,"display_height":768,"environment":"browser"}]`},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tools.name, stream), func(t *testing.T) {
				body := fmt.Sprintf(`{"model":"public-alias","stream":%t,%s}`, stream, tools.fields)
				c, rec := newSolToolsFallbackContext("/v1/responses", body)
				account := forceChatResponsesFallbackAccount()
				account.Credentials["model_mapping"] = map[string]any{"public-alias": "gpt-6.1-sol"}
				upstream := &httpUpstreamRecorder{}
				svc := &OpenAIGatewayService{
					cfg: rawChatCompletionsTestConfig(), accountRepo: &accountRepoStub{}, httpUpstream: upstream,
				}

				result, err := svc.forwardResponsesViaRawChatCompletions(context.Background(), c, account, []byte(body))

				require.Nil(t, result)
				var failover *UpstreamFailoverError
				require.ErrorAs(t, err, &failover)
				require.Equal(t, OpenAIResponsesToolsUnsupportedReason, failover.Reason)
				require.Equal(t, GatewayFailureStageRequestCompatibility, failover.Stage)
				require.Equal(t, GatewayFailureScopeRequest, failover.Scope)
				require.Equal(t, http.StatusBadRequest, failover.ClientStatusCode)
				require.Contains(t, failover.ClientMessage, "gpt-6.1-sol requires Responses for tool calls")
				require.True(t, failover.ShouldRetryNextAccount())
				require.False(t, failover.RetryableOnSameAccount)
				require.False(t, failover.ShouldReportAccountScheduleFailure())
				_, _, healthFailure := classifyOpenAIAPIKeyHealthFailure(failover)
				require.False(t, healthFailure)
				require.False(t, c.Writer.Written(), "another account must still be able to answer")
				require.Empty(t, rec.Body.String())
				require.Empty(t, upstream.requests, "the incompatible account must never be called")
				require.Equal(t, "gpt-6.1-sol", account.Credentials["model_mapping"].(map[string]any)["public-alias"])
			})
		}
	}
}

// An invalid reasoning effort is a request bug, not an account capability
// mismatch: retrying it on another account cannot help and must keep the 400.
func TestResponsesInvalidReasoningDoesNotFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ model, effort string }{
		{"gpt-6.1-sol", "none"},
		{"gpt-6.1-sol", "minimal"},
	} {
		t.Run(tc.model+"/"+tc.effort, func(t *testing.T) {
			body := fmt.Sprintf(`{"model":"public-alias","input":"hello","reasoning":{"effort":%q},"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`, tc.effort)
			c, rec := newSolToolsFallbackContext("/v1/responses", body)
			account := forceChatResponsesFallbackAccount()
			account.Credentials["model_mapping"] = map[string]any{"public-alias": tc.model}
			upstream := &httpUpstreamRecorder{}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), accountRepo: &accountRepoStub{}, httpUpstream: upstream}

			_, err := svc.forwardResponsesViaRawChatCompletions(context.Background(), c, account, []byte(body))

			var failover *UpstreamFailoverError
			require.ErrorContains(t, err, tc.model+" does not support reasoning effort")
			require.False(t, errors.As(err, &failover))
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Empty(t, upstream.requests)
		})
	}
}

func TestResponsesTerraToolsCapabilityFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := "{\"model\":\"public-alias\",\"input\":\"hello\",\"tools\":[{\"type\":\"function\",\"name\":\"lookup\",\"parameters\":{\"type\":\"object\"}}]}"
	c, rec := newSolToolsFallbackContext("/v1/responses", body)
	account := forceChatResponsesFallbackAccount()
	account.Credentials["model_mapping"] = map[string]any{"public-alias": "gpt-6.1-terra"}
	upstream := &httpUpstreamRecorder{}
	svc := &OpenAIGatewayService{
		cfg: rawChatCompletionsTestConfig(), accountRepo: &accountRepoStub{}, httpUpstream: upstream,
	}

	_, err := svc.forwardResponsesViaRawChatCompletions(context.Background(), c, account, []byte(body))

	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Equal(t, OpenAIResponsesToolsUnsupportedReason, failover.Reason)
	require.True(t, failover.ShouldRetryNextAccount())
	require.False(t, failover.ShouldReportAccountScheduleFailure())
	require.Contains(t, failover.ClientMessage, "gpt-6.1-terra")
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
	require.Empty(t, upstream.requests)
}

// Without a selector there is no account to switch to, so the original direct
// 400 stays byte-for-byte intact.
func TestResponsesSolToolsWithoutSelectorStillReturns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"model":"gpt-6.1-sol","input":"hello","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`
	c, rec := newSolToolsFallbackContext("/v1/responses", body)
	upstream := &httpUpstreamRecorder{}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

	result, err := svc.forwardResponsesViaRawChatCompletions(context.Background(), c, forceChatResponsesFallbackAccount(), []byte(body))

	var failover *UpstreamFailoverError
	require.Nil(t, result)
	require.Error(t, err)
	require.False(t, errors.As(err, &failover))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "gpt-6.1-sol requires Responses for tool calls")
	require.Empty(t, upstream.requests)
}

// /v1/messages on a Chat-Completions-only account takes the direct Anthropic
// bridge; the same capability mismatch must fail over instead of committing the
// Anthropic-format 400.
func TestAnthropicMessagesSolToolsCapabilityFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"model":"public-alias","max_tokens":64,"stream":false,"messages":[{"role":"user","content":"hello"}],"tools":[{"name":"lookup","input_schema":{"type":"object"}}]}`
	c, rec := newSolToolsFallbackContext("/v1/messages", body)
	account := forceChatResponsesFallbackAccount()
	account.Credentials["model_mapping"] = map[string]any{"public-alias": "gpt-6.1-sol"}
	upstream := &httpUpstreamRecorder{}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), accountRepo: &accountRepoStub{}, httpUpstream: upstream}

	result, err := svc.forwardAnthropicViaRawChatCompletions(context.Background(), c, account, []byte(body), "")

	require.Nil(t, result)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Equal(t, OpenAIResponsesToolsUnsupportedReason, failover.Reason)
	require.True(t, failover.ShouldRetryNextAccount())
	require.False(t, failover.ShouldReportAccountScheduleFailure())
	require.Contains(t, failover.ClientMessage, "gpt-6.1-sol")
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
	require.Empty(t, upstream.requests)
}

func TestAnthropicMessagesSolToolsWithoutSelectorStillReturns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"model":"gpt-6.1-sol","max_tokens":64,"stream":false,"messages":[{"role":"user","content":"hello"}],"tools":[{"name":"lookup","input_schema":{"type":"object"}}]}`
	c, rec := newSolToolsFallbackContext("/v1/messages", body)
	upstream := &httpUpstreamRecorder{}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

	_, err := svc.forwardAnthropicViaRawChatCompletions(context.Background(), c, forceChatResponsesFallbackAccount(), []byte(body), "")

	var failover *UpstreamFailoverError
	require.Error(t, err)
	require.False(t, errors.As(err, &failover))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "gpt-6.1-sol requires Responses for tool calls")
	require.Empty(t, upstream.requests)
}

// /v1/chat/completions on a Chat-Completions-only account hits the same guard in
// the raw Chat path; a managed gateway must be able to switch accounts.
func TestChatCompletionsSolToolsCapabilityFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"model":"public-alias","stream":false,"messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`
	c, rec := newSolToolsFallbackContext("/v1/chat/completions", body)
	account := forceChatResponsesFallbackAccount()
	account.Credentials["model_mapping"] = map[string]any{"public-alias": "gpt-6.1-sol"}
	upstream := &httpUpstreamRecorder{}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), accountRepo: &accountRepoStub{}, httpUpstream: upstream}

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, []byte(body), "")

	require.Nil(t, result)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Equal(t, OpenAIResponsesToolsUnsupportedReason, failover.Reason)
	require.True(t, failover.ShouldRetryNextAccount())
	require.False(t, failover.ShouldReportAccountScheduleFailure())
	require.Contains(t, failover.ClientMessage, "gpt-6.1-sol")
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
	require.Empty(t, upstream.requests)
}

func TestChatCompletionsSolToolsWithoutSelectorStillReturns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"model":"gpt-6.1-sol","stream":false,"messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`
	c, rec := newSolToolsFallbackContext("/v1/chat/completions", body)
	upstream := &httpUpstreamRecorder{}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

	_, err := svc.forwardAsRawChatCompletions(context.Background(), c, forceChatResponsesFallbackAccount(), []byte(body), "")

	var failover *UpstreamFailoverError
	require.Error(t, err)
	require.False(t, errors.As(err, &failover))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "gpt-6.1-sol requires Responses for tool calls")
	require.Empty(t, upstream.requests)
}

func newSolToolsFallbackContext(path, body string) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	return c, rec
}
