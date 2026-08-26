package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newOpenAIPartialUsageContext(t *testing.T, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	return c, recorder
}

func newOpenAIPartialUsageAccount(passthrough bool) *Account {
	extra := map[string]any{"openai_responses_supported": true}
	if passthrough {
		extra["openai_passthrough"] = true
	}
	return &Account{
		ID: 811, Name: "openai-partial-usage", Platform: PlatformOpenAI,
		Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra:       extra, Status: StatusActive, Schedulable: true,
	}
}

func newOpenAIPartialUsageService(upstream *httpUpstreamRecorder) *OpenAIGatewayService {
	return &OpenAIGatewayService{
		cfg:          &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
		httpUpstream: upstream,
	}
}

func partialOpenAIResponsesSSE(withOutput bool) string {
	lines := []string{
		`event: response.created`,
		`data: {"type":"response.created","response":{"id":"resp_partial","model":"gpt-5.4","status":"in_progress","output":[],"usage":{"input_tokens":13,"output_tokens":0}}}`,
		"",
	}
	if withOutput {
		lines = append(lines,
			`event: response.output_text.delta`,
			`data: {"type":"response.output_text.delta","response_id":"resp_partial","delta":"partial","usage":{"input_tokens":13,"output_tokens":4}}`,
			"",
		)
	}
	return strings.Join(lines, "\n")
}

func TestOpenAIGatewayService_Forward_StreamErrorPreservesPartialUsage(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","instructions":"answer","input":"hello","stream":true}`)
	c, recorder := newOpenAIPartialUsageContext(t, body)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"X-Request-Id": []string{"rid-native-partial"},
		},
		Body: io.NopCloser(strings.NewReader(partialOpenAIResponsesSSE(true))),
	}}

	result, err := newOpenAIPartialUsageService(upstream).Forward(
		context.Background(), c, newOpenAIPartialUsageAccount(false), body,
	)

	require.Error(t, err)
	require.Contains(t, err.Error(), "missing terminal event")
	require.NotNil(t, result)
	require.Equal(t, 13, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.Equal(t, "rid-native-partial", result.RequestID)
	require.Contains(t, recorder.Body.String(), "partial")
}

func TestOpenAIGatewayService_Forward_PreOutputFailoverDropsPartialUsage(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","instructions":"answer","input":"hello","stream":true}`)
	c, recorder := newOpenAIPartialUsageContext(t, body)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(partialOpenAIResponsesSSE(false))),
	}}

	result, err := newOpenAIPartialUsageService(upstream).Forward(
		context.Background(), c, newOpenAIPartialUsageAccount(false), body,
	)

	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Nil(t, result, "a replayable attempt must not expose partial usage")
	require.Empty(t, recorder.Body.String())
}

func TestOpenAIGatewayService_PassthroughStreamErrorPreservesPartialUsage(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","instructions":"answer","input":"hello","stream":true}`)
	c, _ := newOpenAIPartialUsageContext(t, body)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"X-Request-Id": []string{"rid-passthrough-partial"},
		},
		Body: io.NopCloser(strings.NewReader(partialOpenAIResponsesSSE(true))),
	}}

	result, err := newOpenAIPartialUsageService(upstream).Forward(
		context.Background(), c, newOpenAIPartialUsageAccount(true), body,
	)

	require.Error(t, err)
	require.Contains(t, err.Error(), "missing terminal event")
	require.NotNil(t, result)
	require.Equal(t, 13, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.Equal(t, "rid-passthrough-partial", result.RequestID)
}

func TestOpenAIGatewayService_GrokStreamErrorPreservesPartialUsage(t *testing.T) {
	body := []byte(`{"model":"grok-4.1","input":"hello","stream":true}`)
	c, _ := newOpenAIPartialUsageContext(t, body)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":   []string{"text/event-stream"},
			"Xai-Request-Id": []string{"rid-grok-partial"},
		},
		Body: io.NopCloser(strings.NewReader(partialOpenAIResponsesSSE(true))),
	}}
	account := &Account{
		ID: 812, Name: "grok-partial-usage", Platform: PlatformGrok,
		Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "xai-test"},
		Status:      StatusActive, Schedulable: true,
	}

	result, err := newOpenAIPartialUsageService(upstream).Forward(context.Background(), c, account, body)

	require.Error(t, err)
	require.Contains(t, err.Error(), "missing terminal event")
	require.NotNil(t, result)
	require.Equal(t, 13, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.Equal(t, "rid-grok-partial", result.RequestID)
}

func TestPreserveOpenAIStreamingResultOnError_DropsFailoverAndZeroUsage(t *testing.T) {
	result := &OpenAIForwardResult{Usage: OpenAIUsage{InputTokens: 1}}
	kept, err := preserveOpenAIStreamingResultOnError(result, &UpstreamFailoverError{StatusCode: http.StatusBadGateway})
	require.Error(t, err)
	require.Nil(t, kept)

	kept, err = preserveOpenAIStreamingResultOnError(&OpenAIForwardResult{}, errors.New("stream read error"))
	require.Error(t, err)
	require.Nil(t, kept)
}

// TestPreserveOpenAIStreamingResultOnError_KeepsTopUpAbort 证明流式补扣失败主动中止
// （ErrBalanceWithholdingFailed）即使无 observed usage 也保留 result，使请求进入
// 统一计费任务。若上游未返回 usage，actual=0 必须释放预扣。
func TestPreserveOpenAIStreamingResultOnError_KeepsTopUpAbort(t *testing.T) {
	// 无 observed usage（终帧未到）但补扣中止：必须保留，避免免费漏扣。
	result := &OpenAIForwardResult{}
	abortErr := wrapStreamOutputHoldTopUpFailure(ErrBalanceWithholdingFailed)
	kept, err := preserveOpenAIStreamingResultOnError(result, abortErr)
	require.Error(t, err)
	require.Same(t, result, kept, "top-up abort must keep the result for unified zero-usage settlement")

	// result==nil 时无可结算对象，仍返回 nil（不构造伪结果）。
	kept, err = preserveOpenAIStreamingResultOnError(nil, abortErr)
	require.Error(t, err)
	require.Nil(t, kept)

	// failover 仍优先于补扣哨兵：可重放，绝不暴露部分结果（防重复计费）。
	kept, err = preserveOpenAIStreamingResultOnError(&OpenAIForwardResult{}, &UpstreamFailoverError{StatusCode: http.StatusBadGateway})
	require.Error(t, err)
	require.Nil(t, kept)
}
