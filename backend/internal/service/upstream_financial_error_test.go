package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// NewAPI official 2d8e50bf: service/billing_session.go and relaykit/types/error.go.
// Sub2API 58d2b023d: billing error, subscription and ratelimit/freeze fixtures.
func TestUpstreamFinancialRealVariants(t *testing.T) {
	messages := []string{
		"余额：0.12，费用：1.64", "You have exhausted your API credits", "Your balance is $0.12",
		"用户额度已用尽", "当前额度: 0.12, 需要额度: 1.64", "扣费明细: 总金额 1.64", "usage cost: $1.64", "charged $1.64",
		"用户额度不足, 剩余额度: $0.003", "预扣费额度失败, 用户剩余额度: $0.001, 需要预扣费额度: $1.64",
		"订阅额度不足或未配置订阅: subscription quota insufficient", "令牌额度不足: sk-secret",
		"计费账户已被冻结", "余额不足，请充值", "餘額不足", "Insufficient balance, withholding failed",
		"Billing service temporarily unavailable", "daily usage limit exceeded", "Weekly usage limit reached. Resets in 2 days.",
		"Your credit balance is too low to access the Anthropic API", "You exceeded your current quota, please check your plan and billing details",
		"The usage limit has been reached", "You have reached your usage limit", "Spending limit reached", "You have run out of credits",
		"billing account has been frozen", "remaining quota: $12.45", "Balance = -1.64",
	}
	for _, status := range []int{400, 402, 403, 429, 500} {
		for _, msg := range messages {
			t.Run(http.StatusText(status)+"/"+msg, func(t *testing.T) {
				body, _ := json.Marshal(gin.H{"error": gin.H{"message": msg + " relay.internal:8080 vendor-A sk-secret"}})
				require.True(t, IsUpstreamFinancialError(status, body))
				code, typ, _, text := WrapUpstreamErrorForClient(status, body)
				require.Equal(t, 502, code)
				require.Equal(t, "upstream_error", typ)
				require.Equal(t, UpstreamUnavailableMessage, text)
			})
		}
	}
	for _, body := range []string{
		"insufficient balance", "payment required", "<html>用户额度不足，剩余额度 1.64</html>",
		`{"errors":[{"details":{"message":"用户额度已用尽"}}]}`,
		`{"error":{"details":[{"message":"当前额度: 0.1, 需要额度: 1.64"}]}}`,
		"{\"error\":{\"code\":\"pre_consume_token_quota_failed\"}}",
		"{\"error\":{\"code\":\"INSUFFICIENT_BALANCE\"}}",
		"{\"error\":{\"ref_code\":400901,\"message\":\"blocked\"}}",
		"{\"error\":{\"code\":402,\"message\":\"blocked\"}}",
		"{\"error\":{\"details\":[{\"reason\":\"BILLING_DISABLED\"}]}}",
		"{\"response\":{\"error\":{\"code\":\"usage_limit_reached\"}}}",
		"{\"error\":{\"message\":\"{\\\"error\\\":{\\\"code\\\":\\\"insufficient_quota\\\"}}\"}}",
	} {
		require.True(t, IsUpstreamFinancialError(403, []byte(body)), body)
	}
}

func TestUpstreamFinancialNegative(t *testing.T) {
	for _, body := range []string{
		`{"error":{"message":"Invalid parameter usage: 1; expected an object"}}`,
		`{"error":{"message":"Unknown model balance: 1.64"}}`,
		`{"error":{"message":"参数无效: 当前额度: X, 需要额度: Y"}}`,
		"{\"error\":{\"message\":\"Invalid value for stream_options.include_usage\",\"param\":\"usage\"}}",
		"{\"error\":{\"message\":\"Model billing-model does not exist\",\"code\":\"model_not_found\"}}",
		"{\"error\":{\"message\":\"Unknown parameter amount\"}}",
		"{\"error\":{\"message\":\"Invalid usage field\"}}",
		"{\"error\":{\"message\":\"Quota exceeded for requests per minute\",\"status\":\"RESOURCE_EXHAUSTED\"}}",
		"{\"error\":{\"message\":\"Rate limit exceeded for tokens per minute\",\"code\":\"rate_limit_exceeded\"}}",
		"{\"error\":{\"message\":\"Your input exceeds the context window\"}}",
		"{\"error\":{\"message\":\"参数金额不能为空\"}}",
		"{\"output\":[{\"text\":\"余额不足\"}],\"usage\":{\"total_tokens\":100},\"error\":null}",
		"{\"choices\":[{\"delta\":{\"tool_calls\":[{\"function\":{\"arguments\":\"insufficient balance\"}}]}}],\"usage\":{\"total_tokens\":100}}",
	} {
		require.False(t, IsUpstreamFinancialError(400, []byte(body)), body)
		require.False(t, upstreamFinancialFailureEnvelope([]byte(body)), body)
	}
}

func financialContext(path string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", path, nil)
	return c, rec
}

type financialHeaderSpy struct {
	gin.ResponseWriter
	headers []int
}

func (w *financialHeaderSpy) WriteHeader(status int) {
	w.headers = append(w.headers, status)
	w.ResponseWriter.WriteHeader(status)
}

func TestUpstreamFinancialGuardProtocols(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/responses/compact", "/v1/chat/completions", "/v1/messages", "/v1beta/models/gemini:generateContent", "/v1beta/models/gemini:streamGenerateContent", "/v1/images/generations", "/v1/videos", "/v1/videos/task/content"} {
		for _, started := range []bool{false, true} {
			t.Run(path+"/"+map[bool]string{true: "flushed", false: "json"}[started], func(t *testing.T) {
				c, rec := financialContext(path)
				spy := &financialHeaderSpy{ResponseWriter: c.Writer}
				c.Writer = spy
				if started {
					_, _ = c.Writer.WriteString(": ping\n\n")
					c.Writer.Flush()
				}
				beforeHeaders := len(spy.headers)
				raw := []byte("{\"error\":{\"code\":\"insufficient_quota\",\"message\":\"余额不足 vendor-A http://host.internal:8080 $1.64 sk-secret\"}}")
				original := append([]byte(nil), raw...)
				restore := GuardUpstreamFinancialError(c, 403, raw)
				c.Header("X-Upstream-Diagnostic", "sk-secret")
				c.Header("Content-Length", "999")
				c.JSON(418, gin.H{"error": gin.H{"message": "custom leaked vendor-A $1.64 sk-secret"}})
				c.JSON(500, gin.H{"error": "duplicate"})
				c.Writer.Flush()
				restore()
				require.Equal(t, original, raw)
				require.Same(t, spy, c.Writer)
				require.Equal(t, 1, strings.Count(rec.Body.String(), UpstreamUnavailableMessage))
				for _, secret := range []string{"host.internal", "vendor-A", "1.64", "sk-secret", "insufficient_quota", "duplicate"} {
					require.NotContains(t, rec.Body.String(), secret)
				}
				if started {
					require.Equal(t, 200, rec.Code)
					require.Len(t, spy.headers, beforeHeaders)
					switch {
					case strings.Contains(path, "/responses"):
						require.Contains(t, rec.Body.String(), "event: response.failed")
						require.Contains(t, rec.Body.String(), "created_at")
					case strings.Contains(path, "/messages"):
						require.Contains(t, rec.Body.String(), "event: error")
					case strings.Contains(path, "/v1beta/"):
						require.Contains(t, rec.Body.String(), "UNAVAILABLE")
						require.NotContains(t, rec.Body.String(), "[DONE]")
					}
				} else {
					require.Equal(t, 502, rec.Code)
					require.True(t, json.Valid(rec.Body.Bytes()))
					require.Empty(t, rec.Header().Get("X-Upstream-Diagnostic"))
					require.Empty(t, rec.Header().Get("Content-Length"))
				}
			})
		}
	}
}

func TestUpstreamFinancialGuardNoRetrySideEffects(t *testing.T) {
	c, rec := financialContext("/v1/messages")
	raw := []byte("{\"error\":{\"code\":\"billing\",\"ref_code\":400901,\"message\":\"计费账户已被冻结\"}}")
	original := c.Writer
	restore := GuardUpstreamFinancialError(c, 400, raw)
	failure := &UpstreamFailoverError{StatusCode: 400, ResponseBody: raw, RetryableOnSameAccount: true}
	require.True(t, isUpstreamBillingAccountFrozen(failure.ResponseBody))
	restore()
	require.False(t, c.Writer.Written())
	require.Same(t, original, c.Writer)
	require.Empty(t, rec.Body.String())
	require.False(t, IsResponseCommitted(c))
	c.JSON(200, gin.H{"usage": gin.H{"total_tokens": 42}, "text": "余额不足"})
	require.Equal(t, 200, rec.Code)
	require.Contains(t, rec.Body.String(), "42")
	require.Contains(t, rec.Body.String(), "余额不足")
}

func TestUpstreamFinancialRuleDoesNotBypassRetries(t *testing.T) {
	c, _ := financialContext("/v1/responses")
	svc := &ErrorPassthroughService{}
	svc.setLocalCache([]*model.ErrorPassthroughRule{newNonFailoverPassthroughRule(403, "额度", 418, "custom $1.64 sk-secret")})
	BindErrorPassthroughService(c, svc)
	status, typ, msg, matched := applyErrorPassthroughRule(c, PlatformOpenAI, 403, []byte("{\"error\":{\"message\":\"用户额度不足\"}}"), 502, "upstream_error", "failed")
	require.False(t, matched)
	require.Equal(t, 502, status)
	require.Equal(t, "upstream_error", typ)
	require.Equal(t, UpstreamUnavailableMessage, msg)
	require.False(t, c.Writer.Written())
	svc.setLocalCache([]*model.ErrorPassthroughRule{newNonFailoverPassthroughRule(400, "schema", 418, "remaining quota: $1.64 sk-secret")})
	status, _, msg, matched = applyErrorPassthroughRule(c, PlatformOpenAI, 400, []byte("{\"error\":{\"message\":\"invalid schema\"}}"), 502, "upstream_error", "failed")
	require.True(t, matched)
	require.Equal(t, 502, status)
	require.Equal(t, UpstreamUnavailableMessage, msg)
}

func TestUpstreamFinancialServiceExits(t *testing.T) {
	for _, status := range []int{400, 402, 403, 429, 500} {
		for _, family := range []string{"anthropic", "openai", "gemini", "grok"} {
			t.Run(family+http.StatusText(status), func(t *testing.T) {
				path := "/v1/messages"
				if family == "openai" {
					path = "/v1/responses"
				}
				if family == "gemini" {
					path = "/v1beta/models/gemini:generateContent"
				}
				if family == "grok" {
					path = "/v1/videos"
				}
				c, rec := financialContext(path)
				raw := []byte("{\"error\":{\"message\":\"用户额度不足 vendor-A $1.64 sk-secret\",\"code\":\"insufficient_user_quota\"}}")
				resp := &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(raw))}
				account := &Account{ID: 1, Platform: family, Type: AccountTypeAPIKey}
				var err error
				switch family {
				case "anthropic":
					_, err = (&GatewayService{}).handleErrorResponse(context.Background(), resp, c, account)
				case "openai":
					_, err = (&OpenAIGatewayService{}).handleErrorResponse(context.Background(), resp, c, account, nil)
				case "gemini":
					err = (&GeminiMessagesCompatService{}).writeGeminiNativeUpstreamError(c, account, resp, raw, "", false)
				case "grok":
					_, err = (&OpenAIGatewayService{}).handleGrokMediaErrorResponse(context.Background(), resp, c, account, "", "grok-imagine-video")
				}
				require.Error(t, err)
				var failover *UpstreamFailoverError
				if errors.As(err, &failover) {
					require.Equal(t, raw, failover.ResponseBody)
					require.False(t, c.Writer.Written())
					return
				}
				require.Equal(t, 502, rec.Code)
				require.Contains(t, rec.Body.String(), UpstreamUnavailableMessage)
				require.NotContains(t, rec.Body.String(), "sk-secret")
			})
		}
	}
}

func TestUpstreamFinancialNativeStreams(t *testing.T) {
	raw := "{\"error\":{\"code\":403,\"message\":\"用户额度不足 vendor-A $1.64 sk-secret\"}}"
	for _, started := range []bool{false, true} {
		t.Run(map[bool]string{true: "flushed", false: "first-event"}[started], func(t *testing.T) {
			c, rec := financialContext("/v1beta/models/gemini:streamGenerateContent")
			if started {
				_, _ = c.Writer.WriteString(": ping\n\n")
				c.Writer.Flush()
			}
			resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: " + raw + "\n\ndata: " + raw + "\n\n"))}
			_, err := (&GeminiMessagesCompatService{}).handleNativeStreamingResponse(c, resp, time.Now(), false, &Account{ID: 1, Platform: PlatformGemini}, "")
			require.NoError(t, err)
			require.Equal(t, 1, strings.Count(rec.Body.String(), UpstreamUnavailableMessage))
			require.NotContains(t, rec.Body.String(), "sk-secret")
			if started {
				require.Equal(t, 200, rec.Code)
			} else {
				require.Equal(t, 502, rec.Code)
			}
		})
	}
	c, rec := financialContext("/v1/messages")
	body := "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"用户额度不足 sk-secret\"}}\n\n"
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	_, err := (&GatewayService{}).handleStreamingResponseAnthropicAPIKeyPassthrough(context.Background(), resp, c, &Account{ID: 1}, time.Now(), "claude")
	require.NoError(t, err)
	require.Equal(t, 502, rec.Code)
	require.Equal(t, 1, strings.Count(rec.Body.String(), UpstreamUnavailableMessage))
	require.NotContains(t, rec.Body.String(), "sk-secret")
}

func TestUpstreamFinancialEventSanitizer(t *testing.T) {
	raw := []byte("{\"type\":\"response.failed\",\"sequence_number\":19,\"response\":{\"id\":\"resp_1\",\"created_at\":123,\"error\":{\"code\":\"insufficient_quota\",\"message\":\"sk-secret $1.64\",\"details\":\"host.internal\"},\"usage\":{\"input_tokens\":99}}}")
	safe, changed := sanitizeOpenAIResponseFailedEventForClient(raw, "response.failed", true)
	require.True(t, changed)
	require.True(t, json.Valid(safe))
	require.Equal(t, int64(19), gjson.GetBytes(safe, "sequence_number").Int())
	require.Contains(t, string(safe), UpstreamUnavailableMessage)
	require.NotContains(t, string(safe), "sk-secret")
	require.NotContains(t, string(safe), "host.internal")
	require.Equal(t, int64(99), gjson.GetBytes(raw, "response.usage.input_tokens").Int())
	tool := []byte("{\"type\":\"response.function_call_arguments.delta\",\"delta\":\"余额不足\",\"usage\":{\"total_tokens\":99}}")
	unchanged, changed := sanitizeOpenAIResponseFailedEventForClient(tool, "response.function_call_arguments.delta", true)
	require.False(t, changed)
	require.Equal(t, tool, unchanged)
}

func TestUpstreamFinancialNestedGuardAndRetry(t *testing.T) {
	c, rec := financialContext("/v1/messages")
	raw := []byte("{\"error\":{\"message\":\"余额不足\"}}")
	original := c.Writer
	first := GuardUpstreamFinancialError(c, 403, raw)
	second := GuardUpstreamFinancialError(c, 403, raw)
	second()
	first()
	require.False(t, c.GetBool(upstreamFinancialErrorSentKey))
	require.Same(t, original, c.Writer)
	// The final exhausted attempt is still writable; marking commitment is not a flush.
	MarkResponseCommitted(c)
	require.False(t, c.Writer.Written())
	third := GuardUpstreamFinancialError(c, 403, raw)
	fourth := GuardUpstreamFinancialError(c, 403, raw)
	require.True(t, WriteUpstreamFinancialError(c, 403, raw))
	c.JSON(500, gin.H{"error": "duplicate"})
	fourth()
	third()
	require.Equal(t, 502, rec.Code)
	require.Equal(t, 1, strings.Count(rec.Body.String(), UpstreamUnavailableMessage))
	require.Same(t, original, c.Writer)
}

func TestUpstreamFinancialResponsesStreamEndToEnd(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, platform := range []string{PlatformOpenAI, PlatformGrok} {
			for _, priorOutput := range []bool{false, true} {
				t.Run(platform+map[bool]string{true: "/passthrough", false: "/native"}[passthrough]+map[bool]string{true: "/output", false: "/early"}[priorOutput], func(t *testing.T) {
					svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
					c, rec := financialContext("/v1/responses")
					stream := ""
					if priorOutput {
						stream = "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_fin\"}}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial tool-safe output\"}\n\n"
					}
					stream += "event: error\ndata: {\"type\":\"error\",\"error\":{\"code\":\"usage_limit_reached\",\"message\":\"用户额度已用尽 sk-secret $1.64 host.internal\"}}\n\nevent: response.failed\ndata: {\"type\":\"response.failed\",\"sequence_number\":7,\"response\":{\"id\":\"resp_fin\",\"created_at\":123,\"status\":\"failed\",\"error\":{\"code\":\"usage_limit_reached\",\"message\":\"用户额度已用尽 sk-secret $1.64 host.internal\"},\"usage\":{\"input_tokens\":8,\"output_tokens\":3,\"total_tokens\":11}}}\n\n"
					resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(stream)), Header: http.Header{}}
					account := &Account{ID: 42, Platform: platform, Type: AccountTypeAPIKey}
					var err error
					var tokens int
					if passthrough {
						result, e := svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "model", "model")
						err = e
						if result != nil {
							tokens = result.usage.InputTokens
						}
					} else {
						result, e := svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "model", "model")
						err = e
						if result != nil {
							tokens = result.usage.InputTokens
						}
					}
					require.Error(t, err)
					if !priorOutput {
						var failover *UpstreamFailoverError
						require.True(t, errors.As(err, &failover))
						require.False(t, c.Writer.Written())
						require.Contains(t, string(failover.ResponseBody), "sk-secret")
						return
					}
					require.Equal(t, 200, rec.Code)
					require.Equal(t, 8, tokens)
					require.Equal(t, 1, strings.Count(rec.Body.String(), "event: response.failed"))
					require.Equal(t, 1, strings.Count(rec.Body.String(), UpstreamUnavailableMessage))
					require.NotContains(t, rec.Body.String(), "sk-secret")
					require.NotContains(t, rec.Body.String(), "host.internal")
					require.Contains(t, rec.Body.String(), "partial tool-safe output")
				})
			}
		}
	}
}

func TestUpstreamFinancialSyncMedia(t *testing.T) {
	raw := []byte("{\"error\":{\"message\":\"当前额度: 1.2, 需要额度: 8 sk-secret\"}}")
	c, rec := financialContext("/v1/videos/task")
	writeGrokMediaResponse(c, &http.Response{StatusCode: 200, Header: http.Header{}}, raw, nil)
	require.Equal(t, 502, rec.Code)
	require.NotContains(t, rec.Body.String(), "sk-secret")
	c, rec = financialContext("/v1/images/generations")
	_, _, _, err := (&OpenAIGatewayService{}).handleOpenAIImagesNonStreamingResponse(context.Background(), &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(raw))}, c, &Account{ID: 1}, &OpenAIImagesRequest{})
	require.Error(t, err)
	require.Equal(t, 502, rec.Code)
	require.NotContains(t, rec.Body.String(), "sk-secret")
}

func TestUpstreamFinancialImagesDedicatedErrors(t *testing.T) {
	for _, status := range []int{400, 402, 403, 429, 500} {
		c, rec := financialContext("/v1/images/generations")
		raw := []byte("{\"error\":{\"code\":\"pre_consume_token_quota_failed\",\"message\":\"当前额度: 1.2, 需要额度: 8 sk-secret\"}}")
		resp := &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(raw))}
		_, err := (&OpenAIGatewayService{}).handleOpenAIImagesErrorResponse(context.Background(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey})
		require.Error(t, err)
		var failover *UpstreamFailoverError
		if errors.As(err, &failover) {
			require.False(t, c.Writer.Written())
			require.Equal(t, raw, failover.ResponseBody)
			continue
		}
		require.Equal(t, 502, rec.Code)
		require.Equal(t, 1, strings.Count(rec.Body.String(), UpstreamUnavailableMessage))
		require.NotContains(t, rec.Body.String(), "sk-secret")
	}
}

func TestUpstreamFinancialImagesStream(t *testing.T) {
	for _, started := range []bool{false, true} {
		c, rec := financialContext("/v1/images/generations")
		spy := &financialHeaderSpy{ResponseWriter: c.Writer}
		c.Writer = spy
		if started {
			_, _ = c.Writer.WriteString(": ping\n\n")
			c.Writer.Flush()
		}
		stream := "event: error\ndata: {\"type\":\"error\",\"error\":{\"code\":\"insufficient_quota\",\"message\":\"vendor-A $1.64 sk-secret\"}}\n\n"
		resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(stream))}
		_, _, _, _, err := (&OpenAIGatewayService{}).handleOpenAIImagesStreamingResponse(resp, c, time.Now())
		require.Error(t, err)
		require.Equal(t, 1, strings.Count(rec.Body.String(), UpstreamUnavailableMessage))
		require.NotContains(t, rec.Body.String(), "sk-secret")
		if started {
			require.Equal(t, 200, rec.Code)
			require.Contains(t, rec.Body.String(), "event: error")
			require.NotContains(t, rec.Body.String(), "[DONE]")
		} else {
			require.Equal(t, 502, rec.Code)
		}
	}
}

func TestUpstreamFinancialHeartbeatBoundary(t *testing.T) {
	for _, images := range []bool{false, true} {
		for _, started := range []bool{false, true} {
			path := "/v1/responses/compact"
			if images {
				path = "/v1/images/generations"
			}
			c, rec := financialContext(path)
			spy := &financialHeaderSpy{ResponseWriter: c.Writer}
			c.Writer = spy
			var beat func() bool
			if images {
				defer StartOpenAIImagesJSONKeepalive(c, time.Hour)()
				beat = openAIImagesJSONKeepaliveFromContext(c).beat
			} else {
				MarkOpenAICompactClientStream(c)
				defer StartOpenAICompactSSEKeepalive(c, time.Hour)()
				value, _ := c.Get(openAICompactSSEKeepaliveKey)
				keepalive, ok := value.(*openAICompactSSEKeepalive)
				require.True(t, ok)
				beat = keepalive.beat
			}
			if started {
				require.True(t, beat())
			}
			raw := []byte(`{"error":{"message":"当前额度: 0.12, 需要额度: 1.64 sk-secret"}}`)
			restore := GuardUpstreamFinancialError(c, 403, raw)
			restore()
			require.False(t, c.GetBool(upstreamFinancialErrorSentKey))
			require.True(t, WriteUpstreamFinancialError(c, 403, raw))
			require.False(t, beat())
			require.True(t, WriteUpstreamFinancialError(c, 403, raw))
			require.Equal(t, 1, strings.Count(rec.Body.String(), UpstreamUnavailableMessage))
			require.NotContains(t, rec.Body.String(), "sk-secret")
			if images || !started {
				require.True(t, json.Valid(rec.Body.Bytes()), rec.Body.String())
			}
			if started {
				require.Equal(t, 200, rec.Code)
				require.NotContains(t, spy.headers, 502)
				if !images {
					require.Equal(t, 1, strings.Count(rec.Body.String(), "event: response.failed"))
				}
			} else {
				require.Equal(t, 502, rec.Code)
				require.Equal(t, []int{502}, spy.headers)
			}
		}
	}
}

func TestUpstreamFinancialImagesFinalEventBoundary(t *testing.T) {
	for _, started := range []bool{false, true} {
		c, rec := financialContext("/v1/images/generations")
		spy := &financialHeaderSpy{ResponseWriter: c.Writer}
		c.Writer = spy
		if started {
			_, _ = c.Writer.WriteString(": ping\n\n")
			c.Writer.Flush()
		}
		failure := &OpenAIImagesUpstreamError{StatusCode: 402, Code: "upstream_rejected", Message: "vendor-A sk-secret"}
		body := buildOpenAIImagesStreamErrorBodyFromUpstream(failure)
		svc := &OpenAIGatewayService{}
		require.NoError(t, svc.writeOpenAIImagesStreamEvent(c, c.Writer, "error", body))
		require.NoError(t, svc.writeOpenAIImagesStreamEvent(c, c.Writer, "error", body))
		require.Equal(t, 1, strings.Count(rec.Body.String(), UpstreamUnavailableMessage))
		require.NotContains(t, rec.Body.String(), "sk-secret")
		require.Equal(t, "upstream_rejected", failure.Code)
		if started {
			require.Equal(t, 200, rec.Code)
			require.Empty(t, spy.headers)
			require.Contains(t, rec.Body.String(), "event: error\n")
		} else {
			require.Equal(t, 502, rec.Code)
			require.Equal(t, []int{502}, spy.headers)
		}
	}
}

func TestUpstreamFinancialNestedNativeAdapterAndSuccess(t *testing.T) {
	for _, bad := range []bool{false, true} {
		c, rec := financialContext("/v1/messages")
		stream := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude\",\"content\":[],\"usage\":{\"input_tokens\":8,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool1\",\"name\":\"lookup_balance\",\"input\":{}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"query\\\":\\\"余额不足\\\"}\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"
		if bad {
			stream += "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"余额不足 sk-secret\"}}\n\n"
		} else {
			stream += "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":3}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
		}
		resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(stream))}
		result, err := (&OpenAIGatewayService{}).handleNativeAnthropicStreamingResponse(context.Background(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, "claude", "claude", "claude", nil, time.Now())
		require.NoError(t, err)
		require.Equal(t, 8, result.Usage.InputTokens)
		require.Equal(t, 200, rec.Code)
		require.Contains(t, rec.Body.String(), "lookup_balance")
		require.Contains(t, rec.Body.String(), "partial_json")
		if bad {
			require.Equal(t, 1, strings.Count(rec.Body.String(), UpstreamUnavailableMessage))
			require.NotContains(t, rec.Body.String(), "sk-secret")
		} else {
			require.Equal(t, stream, rec.Body.String())
			require.Equal(t, 3, result.Usage.OutputTokens)
		}
	}
}
