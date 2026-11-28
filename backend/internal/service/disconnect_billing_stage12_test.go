//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestOpenAIHeaderCancel_RealTransportDialCancelIsNotBilled verifies that when a
// real net/http.Transport is canceled during TCP/TLS dial (before WroteRequest),
// the request is marked errOpenAIRequestNotSent and never billed.
func TestOpenAIHeaderCancel_RealTransportDialCancelIsNotBilled(t *testing.T) {
	dialStarted := make(chan struct{})
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			close(dialStarted)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	t.Cleanup(tr.CloseIdleConnections)
	client := &http.Client{Transport: tr}

	svc := &OpenAIGatewayService{
		cfg: &config.Config{},
		httpUpstream: headerCancelUpstreamFunc(func(req *http.Request) (*http.Response, error) {
			return client.Do(req)
		}),
	}
	clientCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1/v1/responses", strings.NewReader(`{"model":"gpt-5.4","input":"hello"}`))
	req.RequestURI = ""

	errCh := make(chan error, 1)
	go func() {
		_, err := svc.doOpenAIUpstreamWithHeaderCancel(clientCtx, req, "", &Account{ID: 1})
		errCh <- err
	}()
	select {
	case <-dialStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("dial never started")
	}
	cancel()

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorIs(t, err, errOpenAIRequestNotSent, "cancel before WroteRequest must not be billed")
	case <-time.After(2 * time.Second):
		t.Fatal("doOpenAIUpstreamWithHeaderCancel did not return after dial cancel")
	}
}

// TestOpenAIHeaderCancel_RaceWithUpstreamErrorStatusIsNotBilled verifies that if
// client cancellation races with an upstream >=400 rejection, the body is closed
// and errOpenAIRequestNotSent is returned so rejected requests are never billed.
func TestOpenAIHeaderCancel_RaceWithUpstreamErrorStatusIsNotBilled(t *testing.T) {
	clientCtx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	resp, body, writer := newLifecycleTrackingResponse()
	resp.StatusCode = http.StatusServiceUnavailable
	defer func() { _ = body.Close() }()
	defer func() { _ = writer.Close() }()

	svc := &OpenAIGatewayService{
		cfg: &config.Config{},
		httpUpstream: headerCancelUpstreamFunc(func(req *http.Request) (*http.Response, error) {
			cancel(context.Canceled)
			select {
			case <-req.Context().Done():
				return resp, nil
			case <-time.After(2 * time.Second):
				return resp, errors.New("upstream cancel not delivered")
			}
		}),
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	_, err := svc.doOpenAIUpstreamWithHeaderCancel(clientCtx, req, "", &Account{ID: 1})
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, errOpenAIRequestNotSent, "upstream 5xx race must not bill")
	select {
	case <-body.closed:
	default:
		t.Fatal("discarded error response body was not closed")
	}
}

// TestResponsesStreamEventMayContributeToOutput_MatchesPassthrough checks Stage 1B
// parity between non-passthrough and passthrough output-event detection.
func TestResponsesStreamEventMayContributeToOutput_MatchesPassthrough(t *testing.T) {
	for _, ev := range []struct {
		typ  string
		want bool
	}{
		{"response.output_text.delta", true},
		{"response.reasoning_text.delta", true},
		{"response.refusal.delta", true},
		{"response.custom_tool_call_input.delta", true},
		{"response.output_item.done", true},
		{"response.function_call_arguments.done", true},
		{"response.custom_tool_call_input.done", true},
		{"response.created", false},
		{"response.in_progress", false},
		{"response.completed", false},
	} {
		require.Equal(t, ev.want, responsesStreamEventMayContributeToOutput(ev.typ), "event=%s", ev.typ)
		require.Equal(t, passthroughStreamEventContributesOutput(ev.typ), responsesStreamEventMayContributeToOutput(ev.typ), "parity for %s", ev.typ)
	}
}

func stage12HeldUpstream(t *testing.T, handler http.HandlerFunc) (*headerCancelHTTPUpstream, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
	})
	target, err := url.Parse(server.URL)
	require.NoError(t, err)
	return &headerCancelHTTPUpstream{client: server.Client(), target: target}, server
}

func stage12OpenAICompatAccount(mode openai_compat.ResponsesSupportMode) *Account {
	acc := rawChatCompletionsTestAccount()
	acc.Extra = map[string]any{
		openai_compat.ExtraKeyResponsesMode: string(mode),
	}
	return acc
}

func stage12GrokOAuthAccount() *Account {
	return &Account{
		ID:          7901,
		Name:        "grok-stage12-test",
		Platform:    PlatformGrok,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  "test-access-token",
			"refresh_token": "test-refresh-token",
			"expires_at":    time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
			"base_url":      xai.DefaultCLIBaseURL,
		},
	}
}

// TestOpenAICompatPaths_ClientLeavesBeforeHeadersBillsInputAndClosesUpstream verifies
// Stage 1A across all OpenAI/Grok/Anthropic-native compat paths: canceling while the
// upstream holds headers cancels upstream promptly (no drain), avoids failover, and
// bills estimated input at the 0% default cache-read share; pre-canceled clients cost 0.
func TestOpenAICompatPaths_ClientLeavesBeforeHeadersBillsInputAndClosesUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prompt := strings.Repeat("hello gateway disconnect billing ", 12)
	chatBody := func(stream bool) []byte {
		return []byte(fmt.Sprintf(`{"model":"gpt-5.1","messages":[{"role":"user","content":%q}],"stream":%t}`, prompt, stream))
	}
	anthropicBody := func(stream bool) []byte {
		return []byte(fmt.Sprintf(`{"model":"gpt-5.1","max_tokens":64,"messages":[{"role":"user","content":%q}],"stream":%t}`, prompt, stream))
	}
	responsesBody := func(stream bool) []byte {
		return []byte(fmt.Sprintf(`{"model":"gpt-5.1","input":%q,"stream":%t}`, prompt, stream))
	}
	grokChatBody := []byte(fmt.Sprintf(`{"model":"grok-4.6","prompt_cache_key":"cache-stage12","messages":[{"role":"user","content":%q}],"stream":true}`, prompt))

	cases := []struct {
		name    string
		url     string
		body    []byte
		account func() *Account
		run     func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, acc *Account, body []byte) (*OpenAIForwardResult, error)
	}{
		{
			name:    "chat_over_responses_stream",
			url:     "/v1/chat/completions",
			body:    chatBody(true),
			account: rawChatCompletionsTestAccount,
			run: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, acc *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsChatCompletions(ctx, c, acc, body, "", "gpt-5.1")
			},
		},
		{
			name:    "chat_over_responses_sync",
			url:     "/v1/chat/completions",
			body:    chatBody(false),
			account: rawChatCompletionsTestAccount,
			run: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, acc *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsChatCompletions(ctx, c, acc, body, "", "gpt-5.1")
			},
		},
		{
			name:    "messages_over_responses_stream",
			url:     "/v1/messages",
			body:    anthropicBody(true),
			account: rawChatCompletionsTestAccount,
			run: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, acc *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsAnthropic(ctx, c, acc, body, "", "")
			},
		},
		{
			name:    "raw_chat_detached_ctx_stream",
			url:     "/v1/chat/completions",
			body:    chatBody(true),
			account: rawChatCompletionsTestAccount,
			run: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, acc *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.forwardAsRawChatCompletions(context.WithoutCancel(ctx), c, acc, body, "")
			},
		},
		{
			name:    "responses_over_chat_fallback",
			url:     "/v1/responses",
			body:    responsesBody(true),
			account: forceChatResponsesFallbackAccount,
			run: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, acc *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.Forward(ctx, c, acc, body)
			},
		},
		{
			name: "messages_over_chat_fallback",
			url:  "/v1/messages",
			body: anthropicBody(true),
			account: func() *Account {
				return stage12OpenAICompatAccount(openai_compat.ResponsesSupportModeForceChatCompletions)
			},
			run: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, acc *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsAnthropic(ctx, c, acc, body, "", "")
			},
		},
		{
			name:    "messages_over_native_anthropic",
			url:     "/v1/messages",
			body:    anthropicBody(true),
			account: nativeAnthropicTestAccount,
			run: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, acc *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsAnthropic(ctx, c, acc, body, "", "")
			},
		},
		{
			name:    "chat_over_native_anthropic_stream",
			url:     "/v1/chat/completions",
			body:    chatBody(true),
			account: nativeAnthropicTestAccount,
			run: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, acc *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsChatCompletions(ctx, c, acc, body, "", "")
			},
		},
		{
			name:    "responses_over_native_anthropic_stream",
			url:     "/v1/responses",
			body:    responsesBody(true),
			account: nativeAnthropicTestAccount,
			run: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, acc *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.Forward(ctx, c, acc, body)
			},
		},
		{
			name:    "grok_chat_bridge_stream",
			url:     "/v1/chat/completions",
			body:    grokChatBody,
			account: stage12GrokOAuthAccount,
			run: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, acc *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.forwardGrokChatCompletionsViaResponses(ctx, c, acc, body, "cache-stage12", "")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name+"/dispatched_then_canceled", func(t *testing.T) {
			arrived, closed := make(chan struct{}), make(chan struct{})
			upstream, _ := stage12HeldUpstream(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				close(arrived)
				<-r.Context().Done()
				close(closed)
			})
			cfg := rawChatCompletionsTestConfig()
			cfg.Gateway.StreamDataIntervalTimeout = 1
			svc := &OpenAIGatewayService{
				cfg:               cfg,
				httpUpstream:      upstream,
				grokTokenProvider: NewGrokTokenProvider(nil, nil),
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			rec := newOpenAIResponseFlushRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, tc.url, bytes.NewReader(tc.body)).WithContext(ctx)
			c.Request.Header.Set("Content-Type", "application/json")
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

			done := make(chan headerCancelForwardResult, 1)
			go func() {
				res, err := tc.run(svc, ctx, c, tc.account(), tc.body)
				done <- headerCancelForwardResult{res, err}
			}()

			select {
			case <-arrived:
			case <-time.After(2 * time.Second):
				t.Fatal("upstream never received request")
			}
			canceledAt := time.Now()
			cancel()
			got := awaitClientGone(t, done, 2*time.Second)

			require.Less(t, time.Since(canceledAt), 800*time.Millisecond, "must cancel promptly without draining")
			requireClientGoneNotFailover(t, got.err)
			require.Equal(t, int32(1), upstream.calls.Load())
			require.NotNil(t, got.result, "dispatched request must bill input estimate")
			require.True(t, got.result.ClientDisconnect)
			require.Greater(t, got.result.Usage.InputTokens, 0)
			require.Zero(t, got.result.Usage.CacheReadInputTokens, "default 0%% cache read bills plain input")
			require.Zero(t, got.result.Usage.OutputTokens)
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("upstream connection was not closed after client disconnect")
			}
		})

		t.Run(tc.name+"/pre_canceled_not_billed", func(t *testing.T) {
			upstream, _ := stage12HeldUpstream(t, func(w http.ResponseWriter, r *http.Request) {
				t.Error("pre-canceled request must not reach upstream")
			})
			cfg := rawChatCompletionsTestConfig()
			svc := &OpenAIGatewayService{
				cfg:               cfg,
				httpUpstream:      upstream,
				grokTokenProvider: NewGrokTokenProvider(nil, nil),
			}

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			rec := newOpenAIResponseFlushRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, tc.url, bytes.NewReader(tc.body)).WithContext(ctx)
			c.Request.Header.Set("Content-Type", "application/json")
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

			res, err := tc.run(svc, ctx, c, tc.account(), tc.body)
			requireClientGoneNotFailover(t, err)
			require.Nil(t, res)
			require.Zero(t, upstream.calls.Load())
		})
	}
}

// TestOpenAICompatMidStreamDisconnect_NativeAnthropicAndMessagesChatFallback verifies
// that mid-stream and buffered disconnects on Messages->Chat, Chat->NativeAnthropic,
// and Responses->NativeAnthropic close upstream immediately (no drain) and bill estimates.
func TestOpenAICompatMidStreamDisconnect_NativeAnthropicAndMessagesChatFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prompt := strings.Repeat("mid stream disconnect billing test ", 12)
	anthChunk := "event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Once upon a time in Go."}}` + "\n\n"
	ccChunk := compatCCRoleFrame + compatCCDeltaFrame("Once upon a time in Go.")

	cases := []struct {
		name     string
		url      string
		body     []byte
		account  func() *Account
		sseFrame string
		stream   bool
		run      func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, acc *Account, body []byte) (*OpenAIForwardResult, error)
	}{
		{
			name: "messages_over_chat_stream",
			url:  "/v1/messages",
			body: []byte(fmt.Sprintf(`{"model":"gpt-5.1","max_tokens":64,"messages":[{"role":"user","content":%q}],"stream":true}`, prompt)),
			account: func() *Account {
				return stage12OpenAICompatAccount(openai_compat.ResponsesSupportModeForceChatCompletions)
			},
			sseFrame: ccChunk,
			stream:   true,
			run: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, acc *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsAnthropic(ctx, c, acc, body, "", "")
			},
		},
		{
			name:     "chat_over_native_anthropic_stream",
			url:      "/v1/chat/completions",
			body:     []byte(fmt.Sprintf(`{"model":"gpt-5.1","messages":[{"role":"user","content":%q}],"stream":true}`, prompt)),
			account:  nativeAnthropicTestAccount,
			sseFrame: anthChunk,
			stream:   true,
			run: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, acc *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsChatCompletions(ctx, c, acc, body, "", "")
			},
		},
		{
			name:     "chat_over_native_anthropic_buffered",
			url:      "/v1/chat/completions",
			body:     []byte(fmt.Sprintf(`{"model":"gpt-5.1","messages":[{"role":"user","content":%q}],"stream":false}`, prompt)),
			account:  nativeAnthropicTestAccount,
			sseFrame: anthChunk,
			stream:   false,
			run: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, acc *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsChatCompletions(ctx, c, acc, body, "", "")
			},
		},
		{
			name:     "responses_over_native_anthropic_stream",
			url:      "/v1/responses",
			body:     []byte(fmt.Sprintf(`{"model":"gpt-5.1","input":%q,"stream":true}`, prompt)),
			account:  nativeAnthropicTestAccount,
			sseFrame: anthChunk,
			stream:   true,
			run: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, acc *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.Forward(ctx, c, acc, body)
			},
		},
		{
			name:     "responses_over_native_anthropic_buffered",
			url:      "/v1/responses",
			body:     []byte(fmt.Sprintf(`{"model":"gpt-5.1","input":%q,"stream":false}`, prompt)),
			account:  nativeAnthropicTestAccount,
			sseFrame: anthChunk,
			stream:   false,
			run: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, acc *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.Forward(ctx, c, acc, body)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sent, closed := make(chan struct{}), make(chan struct{})
			upstream, _ := stage12HeldUpstream(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("X-Request-Id", "rid-stage12-mid")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, tc.sseFrame)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				close(sent)
				<-r.Context().Done()
				close(closed)
			})
			cfg := rawChatCompletionsTestConfig()
			cfg.Gateway.StreamDataIntervalTimeout = 30
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			rec := newOpenAIResponseFlushRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, tc.url, bytes.NewReader(tc.body)).WithContext(ctx)
			c.Request.Header.Set("Content-Type", "application/json")
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

			done := make(chan headerCancelForwardResult, 1)
			go func() {
				res, err := tc.run(svc, ctx, c, tc.account(), tc.body)
				done <- headerCancelForwardResult{res, err}
			}()

			select {
			case <-sent:
			case <-time.After(2 * time.Second):
				t.Fatal("upstream never flushed first SSE frame")
			}
			if tc.stream {
				require.Eventually(t, func() bool {
					s, _ := rec.snapshot()
					return strings.Contains(s, "Once upon")
				}, 2*time.Second, 5*time.Millisecond, "client should see first delta")
			} else {
				time.Sleep(80 * time.Millisecond)
			}
			canceledAt := time.Now()
			cancel()
			got := awaitClientGone(t, done, 2*time.Second)

			require.Less(t, time.Since(canceledAt), 800*time.Millisecond, "must close upstream without draining")
			require.NoError(t, got.err, "client disconnect mid-stream is not an upstream error")
			require.NotNil(t, got.result)
			require.True(t, got.result.ClientDisconnect)
			require.Greater(t, got.result.Usage.InputTokens, 0, "input estimate must be billed")
			require.Greater(t, got.result.Usage.OutputTokens, 0, "partial output must be billed")
			require.Zero(t, got.result.Usage.CacheReadInputTokens)
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("upstream connection was not closed")
			}
		})
	}
}

type stage12RedirectUpstream struct {
	target    *url.URL
	client    *http.Client
	calls     atomic.Int32
	onReceive func()
}

func (u *stage12RedirectUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls.Add(1)
	if u.onReceive != nil {
		u.onReceive()
	}
	out := req.Clone(req.Context())
	out.URL.Scheme, out.URL.Host, out.Host, out.RequestURI = u.target.Scheme, u.target.Host, u.target.Host, ""
	return u.client.Do(out)
}

func (u *stage12RedirectUpstream) DoWithTLS(req *http.Request, proxyURL string, id int64, conc int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, id, conc)
}

// TestGeminiFamily_ClientLeavesBeforeHeadersBillsInputAndSkipsCountTokens verifies
// Stage 1C + Stage 2 across Gemini Forward, ForwardNative, ForwardAsChatCompletions,
// and forwardGeminiNativeViaOpenAICompat.
func TestGeminiFamily_ClientLeavesBeforeHeadersBillsInputAndSkipsCountTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	claudeBody := []byte(`{"model":"gemini-2.5-flash","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"` + strings.Repeat("abcd", 100) + `"}]}`)
	ccBody := []byte(`{"model":"gemini-2.5-flash","stream":true,"messages":[{"role":"user","content":"` + strings.Repeat("abcd", 100) + `"}]}`)

	openAICompatGeminiAccount := func() *Account {
		return &Account{
			ID: 8109, Name: "gemini-openai-compat", Platform: PlatformGemini, Type: AccountTypeAPIKey, Concurrency: 1,
			Credentials: map[string]any{
				"api_key":      "test-key",
				"base_url":     "http://upstream.example",
				"api_protocol": APIProtocolChatCompletions,
			},
		}
	}

	type geminiCase struct {
		name     string
		wantBill bool
		run      func(svc *GeminiMessagesCompatService, ctx context.Context, c *gin.Context) (*ForwardResult, error)
		url      string
		reqBody  []byte
	}
	cases := []geminiCase{
		{
			name:     "gemini_forward_claude_compat",
			wantBill: true,
			url:      "/v1/messages",
			reqBody:  claudeBody,
			run: func(svc *GeminiMessagesCompatService, ctx context.Context, c *gin.Context) (*ForwardResult, error) {
				return svc.Forward(ctx, c, geminiGoneAPIKeyAccount(), claudeBody)
			},
		},
		{
			name:     "gemini_forward_native_generate",
			wantBill: true,
			url:      "/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse",
			reqBody:  []byte(geminiGoneRequest),
			run: func(svc *GeminiMessagesCompatService, ctx context.Context, c *gin.Context) (*ForwardResult, error) {
				return svc.ForwardNative(ctx, c, geminiGoneAPIKeyAccount(), "gemini-2.5-flash", "streamGenerateContent", true, []byte(geminiGoneRequest))
			},
		},
		{
			name:     "gemini_forward_native_countTokens_unbilled",
			wantBill: false,
			url:      "/v1beta/models/gemini-2.5-flash:countTokens",
			reqBody:  []byte(geminiGoneRequest),
			run: func(svc *GeminiMessagesCompatService, ctx context.Context, c *gin.Context) (*ForwardResult, error) {
				return svc.ForwardNative(ctx, c, geminiGoneAPIKeyAccount(), "gemini-2.5-flash", "countTokens", false, []byte(geminiGoneRequest))
			},
		},
		{
			name:     "gemini_forward_chat_completions",
			wantBill: true,
			url:      "/v1/chat/completions",
			reqBody:  ccBody,
			run: func(svc *GeminiMessagesCompatService, ctx context.Context, c *gin.Context) (*ForwardResult, error) {
				return svc.ForwardAsChatCompletions(ctx, c, geminiGoneAPIKeyAccount(), ccBody)
			},
		},
		{
			name:     "gemini_native_via_openai_compat",
			wantBill: true,
			url:      "/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse",
			reqBody:  []byte(geminiGoneRequest),
			run: func(svc *GeminiMessagesCompatService, ctx context.Context, c *gin.Context) (*ForwardResult, error) {
				return svc.forwardGeminiNativeViaOpenAICompat(ctx, c, openAICompatGeminiAccount(), "gemini-2.5-flash", "streamGenerateContent", true, []byte(geminiGoneRequest))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			arrived, closed := make(chan struct{}), make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				close(arrived)
				<-r.Context().Done()
				close(closed)
			}))
			t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
			target, err := url.Parse(srv.URL)
			require.NoError(t, err)

			up := &stage12RedirectUpstream{target: target, client: srv.Client()}
			svc := newGeminiGoneService(up)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, tc.url, bytes.NewReader(tc.reqBody)).WithContext(ctx)

			type outT struct {
				res *ForwardResult
				err error
			}
			done := make(chan outT, 1)
			go func() {
				r, e := tc.run(svc, ctx, c)
				done <- outT{r, e}
			}()

			select {
			case <-arrived:
			case <-time.After(2 * time.Second):
				t.Fatal("upstream never received request")
			}
			cancel()
			select {
			case got := <-done:
				require.ErrorIs(t, got.err, context.Canceled)
				require.Equal(t, int32(1), up.calls.Load(), "must not retry after client disconnect")
				if tc.wantBill {
					require.NotNil(t, got.res)
					require.True(t, got.res.ClientDisconnect)
					require.Greater(t, got.res.Usage.InputTokens, 0)
					require.Zero(t, got.res.Usage.CacheReadInputTokens, "default 0%% cache read share")
					require.Zero(t, got.res.Usage.OutputTokens)
				} else {
					require.Nil(t, got.res, "countTokens must never be billed")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Gemini forward did not return after client disconnect")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("upstream connection was not closed")
			}
		})
	}
}

// TestAntigravity_ClientLeavesBeforeHeadersAndMidStream verifies Stage 1C + Stage 2
// across Antigravity Forward (Claude stream & non-stream), ForwardGemini,
// ForwardAsChatCompletions, ForwardAsResponses, and ForwardUpstream.
func TestAntigravity_ClientLeavesBeforeHeadersAndMidStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prompt := strings.Repeat("abcd", 100)
	claudeStreamBody := []byte(`{"model":"claude-sonnet-4-5","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"` + prompt + `"}]}`)
	claudeNonStreamBody := []byte(`{"model":"claude-sonnet-4-5","max_tokens":64,"stream":false,"messages":[{"role":"user","content":"` + prompt + `"}]}`)
	ccBody := []byte(`{"model":"claude-sonnet-4-5","stream":true,"messages":[{"role":"user","content":"` + prompt + `"}]}`)
	respBody := []byte(`{"model":"claude-sonnet-4-5","stream":true,"input":"` + prompt + `"}`)

	newAGAccount := func(models map[string]any) *Account {
		return &Account{
			ID: 8299, Name: "antigravity-stage12", Platform: PlatformAntigravity, Type: AccountTypeOAuth,
			Status: StatusActive, Concurrency: 1,
			Credentials: map[string]any{
				"access_token":  "test-access-token",
				"project_id":    "test-project",
				"model_mapping": models,
			},
		}
	}
	newAGUpstreamAccount := func() *Account {
		return &Account{
			ID: 8298, Name: "antigravity-upstream-stage12", Platform: PlatformAntigravity, Type: AccountTypeUpstream,
			Status: StatusActive, Concurrency: 1,
			Credentials: map[string]any{
				"base_url": "http://upstream.example",
				"api_key":  "test-key",
			},
		}
	}
	newAGService := func(up HTTPUpstream) *AntigravityGatewayService {
		return &AntigravityGatewayService{
			settingService: NewSettingService(&antigravitySettingRepoStub{}, &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}),
			tokenProvider:  &AntigravityTokenProvider{},
			httpUpstream:   up,
		}
	}

	preHeaderCases := []struct {
		name string
		url  string
		body []byte
		run  func(svc *AntigravityGatewayService, ctx context.Context, c *gin.Context) (*ForwardResult, error)
	}{
		{
			name: "claude_stream_before_headers",
			url:  "/v1/messages",
			body: claudeStreamBody,
			run: func(svc *AntigravityGatewayService, ctx context.Context, c *gin.Context) (*ForwardResult, error) {
				acc := newAGAccount(map[string]any{"claude-sonnet-4-5": "claude-sonnet-4-5"})
				return svc.Forward(ctx, c, acc, claudeStreamBody, false)
			},
		},
		{
			name: "gemini_before_headers",
			url:  "/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse",
			body: []byte(geminiGoneRequest),
			run: func(svc *AntigravityGatewayService, ctx context.Context, c *gin.Context) (*ForwardResult, error) {
				return svc.ForwardGemini(ctx, c, antigravityGoneAccount(), "gemini-2.5-flash", "streamGenerateContent", true, []byte(geminiGoneRequest), false)
			},
		},
		{
			name: "compat_chat_before_headers",
			url:  "/v1/chat/completions",
			body: ccBody,
			run: func(svc *AntigravityGatewayService, ctx context.Context, c *gin.Context) (*ForwardResult, error) {
				acc := newAGAccount(map[string]any{"claude-sonnet-4-5": "claude-sonnet-4-5"})
				return svc.ForwardAsChatCompletions(ctx, c, acc, ccBody, nil)
			},
		},
		{
			name: "compat_responses_before_headers",
			url:  "/v1/responses",
			body: respBody,
			run: func(svc *AntigravityGatewayService, ctx context.Context, c *gin.Context) (*ForwardResult, error) {
				acc := newAGAccount(map[string]any{"claude-sonnet-4-5": "claude-sonnet-4-5"})
				return svc.ForwardAsResponses(ctx, c, acc, respBody, nil)
			},
		},
		{
			name: "upstream_passthrough_before_headers",
			url:  "/v1/messages",
			body: claudeStreamBody,
			run: func(svc *AntigravityGatewayService, ctx context.Context, c *gin.Context) (*ForwardResult, error) {
				return svc.ForwardUpstream(ctx, c, newAGUpstreamAccount(), claudeStreamBody)
			},
		},
	}

	for _, tc := range preHeaderCases {
		t.Run(tc.name, func(t *testing.T) {
			arrived, closed := make(chan struct{}), make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				close(arrived)
				<-r.Context().Done()
				close(closed)
			}))
			t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
			target, err := url.Parse(srv.URL)
			require.NoError(t, err)

			up := &stage12RedirectUpstream{target: target, client: srv.Client()}
			svc := newAGService(up)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, tc.url, bytes.NewReader(tc.body)).WithContext(ctx)

			type outT struct {
				res *ForwardResult
				err error
			}
			done := make(chan outT, 1)
			go func() {
				r, e := tc.run(svc, ctx, c)
				done <- outT{r, e}
			}()

			select {
			case <-arrived:
			case <-time.After(2 * time.Second):
				t.Fatal("upstream never received request")
			}
			cancel()
			select {
			case got := <-done:
				require.ErrorIs(t, got.err, context.Canceled)
				require.Equal(t, int32(1), up.calls.Load(), "must not retry when client is gone")
				require.NotNil(t, got.res)
				require.True(t, got.res.ClientDisconnect)
				require.Greater(t, got.res.Usage.InputTokens, 0)
				require.Zero(t, got.res.Usage.CacheReadInputTokens)
				require.Zero(t, got.res.Usage.OutputTokens)
			case <-time.After(2 * time.Second):
				t.Fatal("Antigravity forward did not return after client disconnect")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("upstream connection was not closed")
			}
		})
	}

	// Non-streaming Claude over Antigravity stream-to-nonstream aggregation:
	// client disconnects after partial content arrives -> ClientDisconnect=true + estimated input & output.
	t.Run("claude_nonstream_mid_disconnect_bills_partial_output", func(t *testing.T) {
		sent, closed := make(chan struct{}), make(chan struct{})
		bareEvent := geminiGoneCodeAssist(geminiGoneBareEvent1) + geminiGoneCodeAssist(geminiGoneBareEvent2)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, bareEvent)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			close(sent)
			<-r.Context().Done()
			close(closed)
		}))
		t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
		target, err := url.Parse(srv.URL)
		require.NoError(t, err)

		up := &stage12RedirectUpstream{target: target, client: srv.Client()}
		svc := newAGService(up)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(claudeNonStreamBody)).WithContext(ctx)

		type outT struct {
			res *ForwardResult
			err error
		}
		done := make(chan outT, 1)
		go func() {
			acc := newAGAccount(map[string]any{"claude-sonnet-4-5": "claude-sonnet-4-5"})
			r, e := svc.Forward(ctx, c, acc, claudeNonStreamBody, false)
			done <- outT{r, e}
		}()

		<-sent
		time.Sleep(80 * time.Millisecond)
		cancel()
		select {
		case got := <-done:
			require.NoError(t, got.err)
			require.NotNil(t, got.res)
			require.True(t, got.res.ClientDisconnect)
			require.Greater(t, got.res.Usage.InputTokens, 0)
			require.Zero(t, got.res.Usage.CacheReadInputTokens)
			require.Greater(t, got.res.Usage.OutputTokens, 0, "partial output must be estimated")
		case <-time.After(2 * time.Second):
			t.Fatal("Forward non-stream did not return after client disconnect")
		}
		<-closed
	})

	// Antigravity ForwardAsChatCompletions streaming mid-disconnect: verifies
	// applyIncompleteGeminiUsage fills missing input + partial output.
	t.Run("compat_chat_stream_mid_disconnect_bills_estimates", func(t *testing.T) {
		sent, closed := make(chan struct{}), make(chan struct{})
		bareEvent := geminiGoneCodeAssist(geminiGoneBareEvent1) + geminiGoneCodeAssist(geminiGoneBareEvent2)
		var once sync.Once
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, bareEvent)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			once.Do(func() { close(sent) })
			<-r.Context().Done()
			close(closed)
		}))
		t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
		target, err := url.Parse(srv.URL)
		require.NoError(t, err)

		up := &stage12RedirectUpstream{target: target, client: srv.Client()}
		svc := newAGService(up)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		rec := newOpenAIResponseFlushRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(ccBody)).WithContext(ctx)

		type outT struct {
			res *ForwardResult
			err error
		}
		done := make(chan outT, 1)
		go func() {
			acc := newAGAccount(map[string]any{"claude-sonnet-4-5": "claude-sonnet-4-5"})
			r, e := svc.ForwardAsChatCompletions(ctx, c, acc, ccBody, nil)
			done <- outT{r, e}
		}()

		<-sent
		require.Eventually(t, func() bool {
			s, _ := rec.snapshot()
			return strings.Contains(s, "Once upon")
		}, 2*time.Second, 5*time.Millisecond)
		cancel()
		select {
		case got := <-done:
			require.NoError(t, got.err)
			require.NotNil(t, got.res)
			require.True(t, got.res.ClientDisconnect)
			require.Greater(t, got.res.Usage.InputTokens, 0)
			require.Zero(t, got.res.Usage.CacheReadInputTokens)
			require.Greater(t, got.res.Usage.OutputTokens, 0)
		case <-time.After(2 * time.Second):
			t.Fatal("ForwardAsChatCompletions did not return after client disconnect")
		}
		<-closed
	})
}

// TestReviewFindings_DisconnectBillingEdgeCases covers the P0/P1 edge cases
// identified in the independent billing review:
// - Raw Chat Completions nil-result guard (no panic when body estimates to 0)
// - Antigravity compat non-stream (Chat & Responses) disconnect bills estimates instead of 502
// - Antigravity compat stream disconnect before first token bills input instead of failover
// - Antigravity collectClaudeStreamResponse preserves observed usageMetadata on disconnect
// - Gemini countTokens disconnect while reading 200 body stays unbilled (0)
// - Antigravity Gemini image model disconnect before any image yields ImageCount=0
func TestReviewFindings_DisconnectBillingEdgeCases(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prompt := strings.Repeat("abcd", 100)

	t.Run("raw_chat_unestimable_body_no_panic", func(t *testing.T) {
		arrived := make(chan struct{})
		upstream, _ := stage12HeldUpstream(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			close(arrived)
			<-r.Context().Done()
		})
		svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		body := []byte(`{"model":"gpt-5.1","stream":true}`)
		require.Zero(t, estimateRequestInputTokens(body), "precondition: body must produce 0 estimated input")
		c, _ := gin.CreateTestContext(newOpenAIResponseFlushRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)).WithContext(ctx)
		c.Request.Header.Set("Content-Type", "application/json")

		done := make(chan headerCancelForwardResult, 1)
		go func() {
			res, err := svc.forwardAsRawChatCompletions(context.WithoutCancel(ctx), c, rawChatCompletionsTestAccount(), body, "")
			done <- headerCancelForwardResult{res, err}
		}()
		<-arrived
		cancel()
		got := awaitClientGone(t, done, 2*time.Second)
		require.ErrorIs(t, got.err, context.Canceled)
		require.Nil(t, got.result)
	})

	newAGAccount := func(models map[string]any) *Account {
		return &Account{
			ID: 8301, Name: "antigravity-review-edge", Platform: PlatformAntigravity, Type: AccountTypeOAuth,
			Status: StatusActive, Concurrency: 1,
			Credentials: map[string]any{
				"access_token":  "test-access-token",
				"project_id":    "test-project",
				"model_mapping": models,
			},
		}
	}
	newAGService := func(up HTTPUpstream) *AntigravityGatewayService {
		return &AntigravityGatewayService{
			settingService: NewSettingService(&antigravitySettingRepoStub{}, &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}),
			tokenProvider:  &AntigravityTokenProvider{},
			httpUpstream:   up,
		}
	}

	for _, mode := range []string{"chat_nonstream", "responses_nonstream", "chat_stream_before_first_token"} {
		t.Run("antigravity_compat_"+mode, func(t *testing.T) {
			sent, closed := make(chan struct{}), make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				if mode != "chat_stream_before_first_token" {
					_, _ = io.WriteString(w, geminiGoneCodeAssist(geminiGoneBareEvent1)+geminiGoneCodeAssist(geminiGoneBareEvent2))
				}
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				close(sent)
				<-r.Context().Done()
				close(closed)
			}))
			t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
			target, err := url.Parse(srv.URL)
			require.NoError(t, err)
			svc := newAGService(&stage12RedirectUpstream{target: target, client: srv.Client()})

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c, _ := gin.CreateTestContext(newOpenAIResponseFlushRecorder())
			acc := newAGAccount(map[string]any{"claude-sonnet-4-5": "claude-sonnet-4-5"})

			type outT struct {
				res *ForwardResult
				err error
			}
			done := make(chan outT, 1)
			switch mode {
			case "chat_nonstream":
				body := []byte(`{"model":"claude-sonnet-4-5","stream":false,"messages":[{"role":"user","content":"` + prompt + `"}]}`)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)).WithContext(ctx)
				go func() { r, e := svc.ForwardAsChatCompletions(ctx, c, acc, body, nil); done <- outT{r, e} }()
			case "responses_nonstream":
				body := []byte(`{"model":"claude-sonnet-4-5","stream":false,"input":"` + prompt + `"}`)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body)).WithContext(ctx)
				go func() { r, e := svc.ForwardAsResponses(ctx, c, acc, body, nil); done <- outT{r, e} }()
			default:
				body := []byte(`{"model":"claude-sonnet-4-5","stream":true,"messages":[{"role":"user","content":"` + prompt + `"}]}`)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)).WithContext(ctx)
				go func() { r, e := svc.ForwardAsChatCompletions(ctx, c, acc, body, nil); done <- outT{r, e} }()
			}
			<-sent
			time.Sleep(80 * time.Millisecond)
			cancel()
			select {
			case got := <-done:
				var failover *UpstreamFailoverError
				require.False(t, errors.As(got.err, &failover), "disconnect must never trigger failover")
				require.NoError(t, got.err)
				require.NotNil(t, got.res)
				require.True(t, got.res.ClientDisconnect)
				require.Greater(t, got.res.Usage.InputTokens, 0)
				if mode != "chat_stream_before_first_token" {
					require.Greater(t, got.res.Usage.OutputTokens, 0)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("compat call did not return after disconnect")
			}
			<-closed
		})
	}

	t.Run("antigravity_claude_nonstream_preserves_reported_usage_on_disconnect", func(t *testing.T) {
		sent, closed := make(chan struct{}), make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, geminiGoneCodeAssist(geminiGoneEvent1)+geminiGoneCodeAssist(geminiGoneEvent2))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			close(sent)
			<-r.Context().Done()
			close(closed)
		}))
		t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
		target, err := url.Parse(srv.URL)
		require.NoError(t, err)
		svc := newAGService(&stage12RedirectUpstream{target: target, client: srv.Client()})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		body := []byte(`{"model":"claude-sonnet-4-5","max_tokens":64,"stream":false,"messages":[{"role":"user","content":"` + prompt + `"}]}`)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body)).WithContext(ctx)
		acc := newAGAccount(map[string]any{"claude-sonnet-4-5": "claude-sonnet-4-5"})

		type outT struct {
			res *ForwardResult
			err error
		}
		done := make(chan outT, 1)
		go func() { r, e := svc.Forward(ctx, c, acc, body, false); done <- outT{r, e} }()
		<-sent
		time.Sleep(80 * time.Millisecond)
		cancel()
		got := <-done
		require.NoError(t, got.err)
		require.NotNil(t, got.res)
		require.True(t, got.res.ClientDisconnect)
		require.Equal(t, 120, got.res.Usage.InputTokens, "reported upstream usage must be preserved")
		require.Equal(t, 13, got.res.Usage.OutputTokens)
		<-closed
	})

	t.Run("gemini_countTokens_body_disconnect_stays_unbilled", func(t *testing.T) {
		sent, closed := make(chan struct{}), make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"totalTokens":`)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			close(sent)
			<-r.Context().Done()
			close(closed)
		}))
		t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
		target, err := url.Parse(srv.URL)
		require.NoError(t, err)
		svc := newGeminiGoneService(&stage12RedirectUpstream{target: target, client: srv.Client()})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-flash:countTokens", strings.NewReader(geminiGoneRequest)).WithContext(ctx)

		type outT struct {
			res *ForwardResult
			err error
		}
		done := make(chan outT, 1)
		go func() {
			r, e := svc.ForwardNative(ctx, c, geminiGoneAPIKeyAccount(), "gemini-2.5-flash", "countTokens", false, []byte(geminiGoneRequest))
			done <- outT{r, e}
		}()
		<-sent
		time.Sleep(50 * time.Millisecond)
		cancel()
		got := <-done
		require.ErrorIs(t, got.err, context.Canceled)
		require.Nil(t, got.res, "countTokens must never be billed on body-read disconnect")
		<-closed
	})

	t.Run("antigravity_gemini_image_model_disconnect_zero_images", func(t *testing.T) {
		sent, closed := make(chan struct{}), make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			close(sent)
			<-r.Context().Done()
			close(closed)
		}))
		t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
		target, err := url.Parse(srv.URL)
		require.NoError(t, err)
		svc := newAGService(&stage12RedirectUpstream{target: target, client: srv.Client()})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		imgModel := "gemini-2.5-flash-image"
		acc := newAGAccount(map[string]any{imgModel: imgModel})
		c, _ := gin.CreateTestContext(newOpenAIResponseFlushRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/"+imgModel+":streamGenerateContent?alt=sse", strings.NewReader(geminiGoneRequest)).WithContext(ctx)

		type outT struct {
			res *ForwardResult
			err error
		}
		done := make(chan outT, 1)
		go func() {
			r, e := svc.ForwardGemini(ctx, c, acc, imgModel, "streamGenerateContent", true, []byte(geminiGoneRequest), false)
			done <- outT{r, e}
		}()
		<-sent
		time.Sleep(50 * time.Millisecond)
		cancel()
		got := <-done
		require.NoError(t, got.err)
		require.NotNil(t, got.res)
		require.True(t, got.res.ClientDisconnect)
		require.Zero(t, got.res.ImageCount, "disconnected image model must not charge floor of 1 image")
		<-closed
	})
}
