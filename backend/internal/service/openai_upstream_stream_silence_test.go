//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/sjson"
)

const silenceResponsesPrelude = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_synthetic\",\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}}\n\n" +
	"data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial answer\"}\n\n"
const silenceChatPrelude = "data: {\"id\":\"chat_synthetic\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial answer\"}}],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":2}}\n\n"

func shortenDeepSeekStreamSilence(t *testing.T, limit time.Duration) {
	t.Helper()
	previous := deepSeekStreamSilenceLimit
	deepSeekStreamSilenceLimit = limit
	t.Cleanup(func() { deepSeekStreamSilenceLimit = previous })
}

type streamSilenceUpstream struct {
	wire, contentType string
	calls             int
	lastContext       context.Context
}

func (u *streamSilenceUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls++
	u.lastContext = req.Context()
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {u.contentType}}, Body: &streamSilenceBlockingBody{prefix: strings.NewReader(u.wire), closed: make(chan struct{})}}, nil
}

func (u *streamSilenceUpstream) DoWithTLS(req *http.Request, proxy string, account int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, account, concurrency)
}

type streamSilenceBlockingBody struct {
	prefix *strings.Reader
	closed chan struct{}
	closes atomic.Int32
}

func (b *streamSilenceBlockingBody) Read(p []byte) (int, error) {
	if b.prefix.Len() > 0 {
		return b.prefix.Read(p)
	}
	<-b.closed
	return 0, errors.New("synthetic body closed")
}

func (b *streamSilenceBlockingBody) Close() error {
	if b.closes.Add(1) == 1 {
		close(b.closed)
	}
	return nil
}

func streamSilenceFixture(t *testing.T, path, wire string) (*OpenAIGatewayService, *gin.Context, *httptest.ResponseRecorder, *Account, *http.Response) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	account := &Account{ID: 42, Name: "synthetic", Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	u := &streamSilenceUpstream{wire: wire, contentType: "text/event-stream"}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: u}
	req := waitLimitRequest(t, WithOpsGinContext(c.Request.Context(), c), HTTPUpstreamProfileOpenAI)
	req.ContentLength = deepSeekLargeRequestBytes + 1
	resp, err := svc.doOpenAIUpstream(req, "", account)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, resp.Body.Close()) })
	return svc, c, recorder, account, resp
}

func requireStreamSilenceError(t *testing.T, err error, account *Account) {
	t.Helper()
	require.ErrorIs(t, err, errOpenAIUpstreamWaitLimit)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.True(t, failover.IsUpstreamWaitLimit())
	require.False(t, failover.ShouldRetryNextAccount())
	require.False(t, failover.ShouldReportAccountScheduleFailure())
	require.False(t, failover.RetryableOnSameAccount)
	require.True(t, account.Schedulable)
	require.Nil(t, account.TempUnschedulableUntil)
	_, _, eligible := classifyOpenAIAPIKeyHealthFailure(err)
	require.False(t, eligible)
	cache := &failedAffinityCacheStub{}
	invalidateUpstreamAffinity(context.Background(), cache, nil, []string{"synthetic_session"}, account, err)
	require.Empty(t, cache.invalidated)
}

func requireOneStreamSilenceEvent(t *testing.T, c *gin.Context) {
	t.Helper()
	raw, exists := c.Get(OpsUpstreamErrorsKey)
	require.True(t, exists)
	events, ok := raw.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, "stream_silence_limit", events[0].Kind)
	require.Equal(t, http.StatusGatewayTimeout, events[0].UpstreamStatusCode)
}

func TestDeepSeekStreamSilenceConsumersAndZeroSettlement(t *testing.T) {
	shortenDeepSeekStreamSilence(t, 80*time.Millisecond)
	for _, route := range []string{"responses", "passthrough", "messages", "chat", "raw_chat", "cc_responses", "cc_messages"} {
		t.Run(route, func(t *testing.T) {
			path, wire := "/v1/responses", silenceResponsesPrelude
			if route == "messages" || route == "cc_messages" {
				path = "/v1/messages"
			}
			if route == "chat" || route == "raw_chat" {
				path = "/v1/chat/completions"
			}
			if strings.HasPrefix(route, "cc_") || route == "raw_chat" {
				wire = silenceChatPrelude
			}
			svc, c, rec, account, resp := streamSilenceFixture(t, path, wire)
			var result *OpenAIForwardResult
			var err error
			switch route {
			case "responses":
				var v *openaiStreamingResult
				v, err = svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "synthetic", "synthetic")
				require.NotNil(t, v)
				result = &OpenAIForwardResult{Usage: *v.usage, NonBillableUpstreamError: v.nonBillableUpstreamError}
			case "passthrough":
				var v *openaiStreamingResultPassthrough
				v, err = svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "synthetic", "synthetic")
				require.NotNil(t, v)
				result = &OpenAIForwardResult{Usage: *v.usage, NonBillableUpstreamError: v.nonBillableUpstreamError}
			case "messages":
				result, err = svc.handleAnthropicStreamingResponse(resp, c, account, "synthetic", "synthetic", "synthetic", time.Now())
			case "chat":
				result, err = svc.handleChatStreamingResponse(resp, c, account, "synthetic", "synthetic", "synthetic", time.Now(), 0)
			case "raw_chat":
				result, err = svc.streamRawChatCompletions(c, resp, account, "synthetic", "synthetic", "synthetic", nil, nil, time.Now(), 0)
			case "cc_responses":
				result, err = svc.streamChatCompletionsAsResponses(c, resp, "synthetic", nil, nil, false, nil, nil, "synthetic", "synthetic", nil, nil, time.Now())
			case "cc_messages":
				result, err = svc.streamChatCompletionsAsAnthropic(c, resp, "synthetic", "synthetic", "synthetic", nil, nil, time.Now())
			}
			requireStreamSilenceError(t, err, account)
			require.NotNil(t, result)
			require.Contains(t, rec.Body.String(), "partial answer")
			require.Contains(t, rec.Body.String(), "Upstream did not respond in time")
			require.NotContains(t, rec.Body.String(), "message_stop")
			require.NotContains(t, rec.Body.String(), "response.completed")
			require.Equal(t, 1000, result.Usage.InputTokens)
			require.Equal(t, 2, result.Usage.OutputTokens)
			assertExplicitUpstreamErrorFree(t, result)
			requireOneStreamSilenceEvent(t, c)
			preserved, preserveErr := preserveOpenAIStreamingResultOnError(result, err)
			require.Same(t, result, preserved)
			require.ErrorIs(t, preserveErr, errOpenAIStreamSilenceLimit)
		})
	}
}

func TestDeepSeekStreamSilenceBufferedConsumers(t *testing.T) {
	shortenDeepSeekStreamSilence(t, 60*time.Millisecond)
	for _, route := range []string{"responses", "passthrough", "messages", "chat", "raw_chat", "cc_responses", "cc_messages"} {
		t.Run(route, func(t *testing.T) {
			path, wire := "/v1/responses", silenceResponsesPrelude
			if route == "messages" || route == "cc_messages" {
				path = "/v1/messages"
			}
			if route == "chat" || route == "raw_chat" {
				path = "/v1/chat/completions"
			}
			if strings.HasPrefix(route, "cc_") || route == "raw_chat" {
				wire = `{"id":"chat_synthetic","choices":[],"usage":{"prompt_tokens":1000,"completion_tokens":2}}`
			}
			svc, c, rec, account, resp := streamSilenceFixture(t, path, wire)
			var result *OpenAIForwardResult
			var err error
			switch route {
			case "responses":
				var v *openaiNonStreamingResult
				v, err = svc.handleNonStreamingResponse(context.Background(), resp, c, account, "synthetic", "synthetic")
				require.NotNil(t, v)
				result = &OpenAIForwardResult{Usage: *v.usage, NonBillableUpstreamError: v.nonBillableUpstreamError}
			case "passthrough":
				var v *openaiNonStreamingResultPassthrough
				v, err = svc.handleNonStreamingResponsePassthrough(context.Background(), resp, c, account, "synthetic", "synthetic")
				require.NotNil(t, v)
				result = &OpenAIForwardResult{Usage: *v.usage, NonBillableUpstreamError: v.nonBillableUpstreamError}
			case "messages":
				result, err = svc.handleAnthropicBufferedStreamingResponse(resp, c, account, "synthetic", "synthetic", "synthetic", time.Now())
			case "chat":
				result, err = svc.handleChatBufferedStreamingResponse(resp, c, account, "synthetic", "synthetic", "synthetic", time.Now())
			case "raw_chat":
				result, err = svc.bufferRawChatCompletions(c, resp, account, "synthetic", "synthetic", "synthetic", nil, nil, time.Now())
			case "cc_responses":
				result, err = svc.bufferChatCompletionsAsResponses(c, resp, "synthetic", nil, nil, false, nil, nil, "synthetic", "synthetic", nil, nil, time.Now())
			case "cc_messages":
				result, err = svc.bufferChatCompletionsAsAnthropic(c, resp, "synthetic", "synthetic", "synthetic", nil, nil, time.Now())
			}
			requireStreamSilenceError(t, err, account)
			require.Equal(t, http.StatusGatewayTimeout, rec.Code)
			require.Contains(t, rec.Body.String(), "Upstream did not respond in time")
			assertExplicitUpstreamErrorFree(t, result)
			requireOneStreamSilenceEvent(t, c)
		})
	}
}

func TestDeepSeekStreamSilenceBody(t *testing.T) {
	t.Run("split heartbeat comments do not refresh activity", func(t *testing.T) {
		shortenDeepSeekStreamSilence(t, 100*time.Millisecond)
		ctx, cancel := context.WithCancelCause(context.Background())
		reader, writer := io.Pipe()
		b := newOpenAIStreamSilenceBody(ctx, cancel, reader, nil)
		defer func() { require.NoError(t, b.Close()) }()
		writerDone := make(chan struct{})
		go func() {
			defer close(writerDone)
			defer func() { _ = writer.Close() }()
			for {
				for _, part := range []string{":", " keep-alive", "\r\n", " \t\n"} {
					if _, err := io.WriteString(writer, part); err != nil {
						return
					}
				}
				timer := time.NewTimer(10 * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}()
		started := time.Now()
		_, err := io.Copy(io.Discard, b)
		require.ErrorIs(t, err, errOpenAIStreamSilenceLimit)
		require.Less(t, time.Since(started), time.Second)
		<-writerDone
	})
	t.Run("ongoing output can outlast several intervals", func(t *testing.T) {
		shortenDeepSeekStreamSilence(t, 200*time.Millisecond)
		ctx, cancel := context.WithCancelCause(context.Background())
		reader, writer := io.Pipe()
		b := newOpenAIStreamSilenceBody(ctx, cancel, reader, nil)
		defer func() { require.NoError(t, b.Close()) }()
		done := make(chan error, 1)
		go func() {
			defer func() { _ = writer.Close() }()
			for i := 0; i < 20; i++ {
				if _, err := io.WriteString(writer, "data: output\n\n"); err != nil {
					done <- err
					return
				}
				time.Sleep(25 * time.Millisecond)
			}
			done <- nil
		}()
		started := time.Now()
		data, err := io.ReadAll(b)
		require.NoError(t, err)
		require.NoError(t, <-done)
		require.Equal(t, 20, strings.Count(string(data), "data: output"))
		require.Greater(t, time.Since(started), 2*b.limit)
		require.NoError(t, ctx.Err())
	})
	t.Run("detached upstream does not turn a departed client into a free timeout", func(t *testing.T) {
		shortenDeepSeekStreamSilence(t, 30*time.Millisecond)
		clientCtx, clientCancel := context.WithCancel(context.Background())
		c := explicitErrorContext()
		c.Request = c.Request.WithContext(clientCtx)
		ctx, cancel := context.WithCancelCause(WithOpsGinContext(context.Background(), c))
		source := &streamSilenceBlockingBody{prefix: strings.NewReader(""), closed: make(chan struct{})}
		b := newOpenAIStreamSilenceBody(ctx, cancel, source, nil)
		clientCancel()
		time.Sleep(60 * time.Millisecond)
		b.mu.Lock()
		expired := b.expired
		b.mu.Unlock()
		require.False(t, expired)
		require.NoError(t, ctx.Err())
		require.NoError(t, b.Close())
	})
	t.Run("Close stops expiry and closes the source once", func(t *testing.T) {
		shortenDeepSeekStreamSilence(t, 40*time.Millisecond)
		ctx, cancel := context.WithCancelCause(context.Background())
		source := &streamSilenceBlockingBody{prefix: strings.NewReader(""), closed: make(chan struct{})}
		var reports atomic.Int32
		b := newOpenAIStreamSilenceBody(ctx, cancel, source, func() { reports.Add(1) })
		done := make(chan error, 1)
		go func() { _, err := b.Read(make([]byte, 8)); done <- err }()
		require.NoError(t, b.Close())
		require.Error(t, <-done)
		time.Sleep(80 * time.Millisecond)
		require.NoError(t, b.Close())
		require.NotErrorIs(t, context.Cause(ctx), errOpenAIUpstreamWaitLimit)
		require.Zero(t, reports.Load())
		require.Equal(t, int32(1), source.closes.Load())
		b.mu.Lock()
		require.False(t, b.expired)
		require.False(t, b.timer.Stop())
		b.mu.Unlock()
	})
}

func TestDeepSeekStreamSilenceScope(t *testing.T) {
	for _, tc := range []struct {
		name     string
		size     int64
		platform string
		profile  HTTPUpstreamProfile
		wrapped  bool
	}{
		{"exactly threshold", deepSeekLargeRequestBytes, PlatformDeepseek, HTTPUpstreamProfileOpenAI, false},
		{"small", 2, PlatformDeepseek, HTTPUpstreamProfileOpenAI, false},
		{"unknown length", -1, PlatformDeepseek, HTTPUpstreamProfileOpenAI, false},
		{"large", deepSeekLargeRequestBytes + 1, PlatformDeepseek, HTTPUpstreamProfileOpenAI, true},
		{"other provider", deepSeekLargeRequestBytes + 1, PlatformOpenAI, HTTPUpstreamProfileOpenAI, false},
		{"native protocol", deepSeekLargeRequestBytes + 1, PlatformDeepseek, HTTPUpstreamProfileDefault, false},
		{"tools", deepSeekLargeRequestBytes + 1, PlatformDeepseek, HTTPUpstreamProfileDeepSeekTools, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := &streamSilenceUpstream{wire: "", contentType: "text/event-stream"}
			svc := &OpenAIGatewayService{httpUpstream: u}
			req := waitLimitRequest(t, context.Background(), tc.profile)
			req.ContentLength = tc.size
			resp, err := svc.doOpenAIUpstream(req, "", &Account{Platform: tc.platform})
			require.NoError(t, err)
			_, wrapped := resp.Body.(*openAIStreamSilenceBody)
			require.Equal(t, tc.wrapped, wrapped)
			require.NoError(t, resp.Body.Close())
		})
	}
}

func TestDeepSeekStreamSilenceFullRawForward(t *testing.T) {
	shortenDeepSeekStreamSilence(t, 100*time.Millisecond)
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			wire, ct := silenceChatPrelude, "text/event-stream"
			if !stream {
				wire, ct = `{"id":"chat_synthetic","choices":[],"usage":{"prompt_tokens":1000,"completion_tokens":2}}`, "application/json"
			}
			body := []byte(fmt.Sprintf(`{"model":"deepseek-synthetic","messages":[{"role":"user","content":"%s"}],"stream":%t}`, strings.Repeat("x", int(deepSeekLargeRequestBytes)+1), stream))
			u := &streamSilenceUpstream{wire: wire, contentType: ct}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: u}
			account := rawChatCompletionsTestAccount()
			account.Platform, account.Schedulable = PlatformDeepseek, true
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
			ctx := WithOpsGinContext(c.Request.Context(), c)
			result, err := svc.forwardAsRawChatCompletions(ctx, c, account, body, "")
			requireStreamSilenceError(t, err, account)
			require.Equal(t, 1, u.calls)
			require.Contains(t, rec.Body.String(), "Upstream did not respond in time")
			if !stream {
				require.Equal(t, http.StatusGatewayTimeout, rec.Code)
			}
			assertExplicitUpstreamErrorFree(t, result)
			requireOneStreamSilenceEvent(t, c)
		})
	}
}

func TestDeepSeekStreamSilenceFullResponsesForward(t *testing.T) {
	shortenDeepSeekStreamSilence(t, 100*time.Millisecond)
	for _, passthrough := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("passthrough=%t/stream=%t", passthrough, stream), func(t *testing.T) {
				body := []byte(fmt.Sprintf(`{"model":"deepseek-synthetic","input":"%s","stream":%t}`, strings.Repeat("x", int(deepSeekLargeRequestBytes)+1), stream))
				c, rec := newOpenAIPartialUsageContext(t, body)
				account := newOpenAIPartialUsageAccount(passthrough)
				account.Platform = PlatformDeepseek
				account.Credentials["api_protocol"] = APIProtocolResponses
				account.Credentials["base_url"] = "http://upstream.example"
				u := &streamSilenceUpstream{wire: silenceResponsesPrelude, contentType: "text/event-stream"}
				svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: u}
				result, err := svc.Forward(WithOpsGinContext(c.Request.Context(), c), c, account, body)
				requireStreamSilenceError(t, err, account)
				require.Equal(t, 1, u.calls)
				require.Contains(t, rec.Body.String(), "Upstream did not respond in time")
				if !stream {
					require.Equal(t, http.StatusGatewayTimeout, rec.Code)
				}
				assertExplicitUpstreamErrorFree(t, result)
				requireOneStreamSilenceEvent(t, c)
			})
		}
	}
}

func TestDeepSeekStreamSilenceClientCancelKeepsExistingBilling(t *testing.T) {
	shortenDeepSeekStreamSilence(t, 300*time.Millisecond)
	svc, c, _, account, resp := streamSilenceFixture(t, "/v1/chat/completions", silenceChatPrelude)
	clientCtx, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(clientCtx)
	time.AfterFunc(50*time.Millisecond, cancel)
	result, err := svc.streamRawChatCompletions(c, resp, account, "synthetic", "synthetic", "synthetic", nil, nil, time.Now(), 0)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	require.False(t, result.NonBillableUpstreamError)
	require.Equal(t, 1000, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	_, hasLimitEvent := c.Get(OpsUpstreamErrorsKey)
	require.False(t, hasLimitEvent)
}

func TestDeepSeekStreamSilenceSuccessfulTerminalKeepsBilling(t *testing.T) {
	shortenDeepSeekStreamSilence(t, 60*time.Millisecond)
	for _, route := range []string{"responses", "passthrough", "raw_chat", "cc_responses", "cc_messages"} {
		t.Run(route, func(t *testing.T) {
			path, wire := "/v1/responses", silenceResponsesPrelude+"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_synthetic\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}}\n\n"
			if route == "raw_chat" {
				path, wire = "/v1/chat/completions", silenceChatPrelude+"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
			}
			if strings.HasPrefix(route, "cc_") {
				wire = silenceChatPrelude + "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"
				if route == "cc_messages" {
					path = "/v1/messages"
				}
			}
			svc, c, rec, account, resp := streamSilenceFixture(t, path, wire)
			var free bool
			var err error
			switch route {
			case "responses":
				var v *openaiStreamingResult
				v, err = svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "synthetic", "synthetic")
				require.NotNil(t, v)
				free = v.nonBillableUpstreamError
			case "passthrough":
				var v *openaiStreamingResultPassthrough
				v, err = svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "synthetic", "synthetic")
				require.NotNil(t, v)
				free = v.nonBillableUpstreamError
			case "cc_responses":
				var v *OpenAIForwardResult
				v, err = svc.streamChatCompletionsAsResponses(c, resp, "synthetic", nil, nil, false, nil, nil, "synthetic", "synthetic", nil, nil, time.Now())
				require.NotNil(t, v)
				free = v.NonBillableUpstreamError
			case "cc_messages":
				var v *OpenAIForwardResult
				v, err = svc.streamChatCompletionsAsAnthropic(c, resp, "synthetic", "synthetic", "synthetic", nil, nil, time.Now())
				require.NotNil(t, v)
				free = v.NonBillableUpstreamError
			case "raw_chat":
				var v *OpenAIForwardResult
				v, err = svc.streamRawChatCompletions(c, resp, account, "synthetic", "synthetic", "synthetic", nil, nil, time.Now(), 0)
				require.NotNil(t, v)
				free = v.NonBillableUpstreamError
			}
			require.NoError(t, err)
			require.False(t, free)
			require.NotContains(t, rec.Body.String(), "Upstream did not respond in time")
			_, hasLimitEvent := c.Get(OpsUpstreamErrorsKey)
			require.False(t, hasLimitEvent)
		})
	}
}

func TestDeepSeekStreamSilenceHostedContinuationIsEntirelyFree(t *testing.T) {
	shortenDeepSeekStreamSilence(t, 100*time.Millisecond)
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			upstream := &hostedSearchUpstream{modelResponses: [][]byte{
				hostedSearchResponse("resp_synthetic_search", 10, 2, hostedSearchCall()),
				hostedSearchResponse("resp_synthetic_summary", 1000, 2, hostedSearchAnswer()),
			}, searchResponse: `{"results":[{"title":"Synthetic","url":"https://example.com/source"}]}`}
			upstream.modelBody = func(index int, response string) io.ReadCloser {
				if index == 1 {
					if stream {
						response = silenceResponsesPrelude
					}
					return &streamSilenceBlockingBody{prefix: strings.NewReader(response), closed: make(chan struct{})}
				}
				return io.NopCloser(strings.NewReader(response))
			}
			svc, c, rec, account, body := hostedSearchFixture(t, upstream, stream, "auto", false)
			account.Credentials["base_url"] = "http://upstream.example"
			svc.cfg = rawChatCompletionsTestConfig()
			var patchErr error
			body, patchErr = sjson.SetBytes(body, "input", strings.Repeat("x", int(deepSeekLargeRequestBytes)+1))
			require.NoError(t, patchErr)
			result, err := svc.Forward(WithOpsGinContext(c.Request.Context(), c), c, account, body)
			requireStreamSilenceError(t, err, account)
			require.NotNil(t, result)
			require.Equal(t, 1, result.SearchCount)
			require.Len(t, upstream.modelRequests, 2)
			require.Contains(t, rec.Body.String(), "Upstream did not respond in time")
			if stream {
				require.Equal(t, 1, strings.Count(rec.Body.String(), "event: response.failed"))
			} else {
				require.Equal(t, http.StatusGatewayTimeout, rec.Code)
			}
			requireDeepSeekHostedBilling(t, result, true)
			requireOneStreamSilenceEvent(t, c)
		})
	}
}

func TestDeepSeekStreamSilenceWebSocketHTTPBridge(t *testing.T) {
	shortenDeepSeekStreamSilence(t, 100*time.Millisecond)
	payload := []byte(fmt.Sprintf(`{"type":"response.create","model":"deepseek-synthetic","input":"%s","stream":true}`, strings.Repeat("x", int(deepSeekLargeRequestBytes)+1)))
	u := &streamSilenceUpstream{wire: silenceResponsesPrelude, contentType: "text/event-stream"}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: u}
	account := rawChatCompletionsTestAccount()
	account.Platform, account.Schedulable = PlatformDeepseek, true
	c, _ := newOpenAIPartialUsageContext(t, payload)
	var frames [][]byte
	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(WithOpsGinContext(c.Request.Context(), c), c, account, "synthetic-token", payload, len(payload), "deepseek-synthetic", "", "", "", "", 1, func(p []byte) error {
		frames = append(frames, append([]byte(nil), p...))
		return nil
	})
	requireStreamSilenceError(t, err, account)
	require.Equal(t, 1, u.calls)
	require.Contains(t, string(bytes.Join(frames, nil)), "Upstream did not respond in time")
	assertExplicitUpstreamErrorFree(t, result)
	requireOneStreamSilenceEvent(t, c)
}

func TestDeepSeekStreamSilenceEmbeddings(t *testing.T) {
	shortenDeepSeekStreamSilence(t, 100*time.Millisecond)
	body := []byte(fmt.Sprintf(`{"model":"deepseek-synthetic","input":"%s"}`, strings.Repeat("x", int(deepSeekLargeRequestBytes)+1)))
	u := &streamSilenceUpstream{wire: `{"usage":{"prompt_tokens":1000,"total_tokens":1000}}`, contentType: "application/json"}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: u}
	account := rawChatCompletionsTestAccount()
	account.Platform, account.Schedulable = PlatformDeepseek, true
	c, rec := newOpenAIPartialUsageContext(t, body)
	c.Request.URL.Path = "/v1/embeddings"
	result, err := svc.ForwardEmbeddings(WithOpsGinContext(c.Request.Context(), c), c, account, body, "")
	requireStreamSilenceError(t, err, account)
	require.Equal(t, http.StatusGatewayTimeout, rec.Code)
	require.Contains(t, rec.Body.String(), "Upstream did not respond in time")
	assertExplicitUpstreamErrorFree(t, result)
	requireOneStreamSilenceEvent(t, c)
}

func TestDeepSeekStreamSilenceAlphaSearch(t *testing.T) {
	shortenDeepSeekStreamSilence(t, 100*time.Millisecond)
	body := []byte(fmt.Sprintf(`{"model":"deepseek-synthetic","commands":[{"query":"%s"}]}`, strings.Repeat("x", int(deepSeekLargeRequestBytes)+1)))
	u := &streamSilenceUpstream{wire: `{"usage":{"input_tokens":1000,"output_tokens":2}}`, contentType: "application/json"}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: u}
	account := rawChatCompletionsTestAccount()
	account.Platform, account.Schedulable = PlatformDeepseek, true
	c, rec := newOpenAIPartialUsageContext(t, body)
	c.Request.URL.Path = "/v1/alpha/search"
	result, err := svc.ForwardAlphaSearch(WithOpsGinContext(c.Request.Context(), c), c, account, body)
	requireStreamSilenceError(t, err, account)
	require.Equal(t, http.StatusGatewayTimeout, rec.Code)
	require.Contains(t, rec.Body.String(), "Upstream did not respond in time")
	assertExplicitUpstreamErrorFree(t, result)
	requireOneStreamSilenceEvent(t, c)
}

// Keep the scanner's callback contract covered without adding parsing to Read.
func TestDeepSeekStreamSilenceScanner(t *testing.T) {
	shortenDeepSeekStreamSilence(t, 60*time.Millisecond)
	svc, c, _, account, resp := streamSilenceFixture(t, "/v1/responses", silenceChatPrelude)
	chunks := 0
	scan := svc.scanCCStream(c, resp, "synthetic", "", time.Now(), func(*apicompat.ChatCompletionsChunk) { chunks++ })
	require.Positive(t, chunks)
	require.True(t, scan.NonBillableUpstreamError)
	requireStreamSilenceError(t, scan.Err, account)
}
