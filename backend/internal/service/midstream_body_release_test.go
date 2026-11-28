//go:build unit

package service

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"weak"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// midStreamNonRetainingUpstream executes fn without keeping any reference to
// req or its body slice, faithfully setting resp.Request = req like net/http.
type midStreamNonRetainingUpstream struct {
	fn func(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error)
}

func (u *midStreamNonRetainingUpstream) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	return u.fn(req, proxyURL, accountID, accountConcurrency)
}

func (u *midStreamNonRetainingUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.fn(req, proxyURL, accountID, accountConcurrency)
}

type midStreamNotifyWriter struct {
	gin.ResponseWriter
	mu     sync.Mutex
	buf    bytes.Buffer
	notify chan struct{}
}

func newMidStreamNotifyWriter() *midStreamNotifyWriter {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	return &midStreamNotifyWriter{
		ResponseWriter: c.Writer,
		notify:         make(chan struct{}, 32),
	}
}

func (w *midStreamNotifyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	_, _ = w.buf.Write(p)
	w.mu.Unlock()
	n, err := w.ResponseWriter.Write(p)
	select {
	case w.notify <- struct{}{}:
	default:
	}
	return n, err
}

func (w *midStreamNotifyWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *midStreamNotifyWriter) Flush() {
	w.ResponseWriter.Flush()
	select {
	case w.notify <- struct{}{}:
	default:
	}
}

func (w *midStreamNotifyWriter) Snapshot() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func waitForMidStreamOutput(t *testing.T, w *midStreamNotifyWriter, substr string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if strings.Contains(w.Snapshot(), substr) {
			return
		}
		select {
		case <-w.notify:
		case <-deadline:
			t.Fatalf("timed out waiting for %q in stream output; got:\n%s", substr, w.Snapshot())
		}
	}
}

func TestMidStreamRelease_RespRequestClearedAtDoReturnWithRewrites(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	doReturned := make(chan struct{})
	upstream := &midStreamNonRetainingUpstream{
		fn: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
			if req != nil && req.Body != nil {
				_, _ = io.Copy(io.Discard, req.Body)
				_ = req.Body.Close()
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       pr,
				Request:    req,
			}, nil
		},
	}

	var weakOrig, weakRewritten weak.Pointer[byte]
	var resp *http.Response
	err := func() error {
		orig := []byte(`{"model":"deepseek-r1","stream":true,"reasoning":{"summary":"auto"},"input":[{"id":"rs_123","type":"message","role":"user","content":"` + strings.Repeat("a", 256<<10) + `"}]}`)
		weakOrig = weak.Make(&orig[0])
		rewritten := stripDeepSeekReasoningSummarySwitches(orig)
		rewritten, _, _ = sanitizeOpenAIResponsesInputItemIDs(rewritten)
		// Force distinct slice if helpers returned unchanged slice when test toggles apply.
		if len(rewritten) > 0 && &rewritten[0] == &orig[0] {
			rewritten = append([]byte(nil), rewritten...)
		}
		weakRewritten = weak.Make(&rewritten[0])

		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://api.example.com/v1/responses", bytes.NewReader(rewritten))
		if err != nil {
			return err
		}
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(rewritten)), nil
		}
		resp, err = upstream.Do(req, "", 1, 1)
		releaseUpstreamRequestPayload(req, resp)
		close(doReturned)
		req = nil
		orig = nil
		rewritten = nil
		return err
	}()
	require.NoError(t, err)
	<-doReturned
	require.NotNil(t, resp)
	defer func() { _ = resp.Body.Close() }()
	require.NotNil(t, resp.Request)
	require.Nil(t, resp.Request.Body)
	require.Nil(t, resp.Request.GetBody)

	requireBodyCollected(t, weakOrig, "original request buffer should be collected while resp.Body pipe is still open")
	requireBodyCollected(t, weakRewritten, "rewritten request buffer should be collected while resp.Body pipe is still open")
}

func TestMidStreamRelease_PreOutputFailoverRetainsUntilOutputThenReleasesMidStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pw2Created := make(chan struct{})
	pr2, pw2 := io.Pipe()
	deltaSent := make(chan struct{})
	releaseStream := make(chan struct{})

	var callCount atomic.Int32
	upstream := &midStreamNonRetainingUpstream{
		fn: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
			if req != nil && req.Body != nil {
				_, _ = io.Copy(io.Discard, req.Body)
				_ = req.Body.Close()
			}
			attempt := callCount.Add(1)
			if attempt == 1 {
				// Account 1 emits response.created then a rate_limit_exceeded frame before any text output -> pre-output failover!
				sse := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n" +
					"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\",\"code\":\"rate_limit_exceeded\",\"message\":\"rate limit exceeded\"}}\n\n"
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:       io.NopCloser(strings.NewReader(sse)),
					Request:    req,
				}, nil
			}
			close(pw2Created)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       pr2,
				Request:    req,
			}, nil
		},
	}

	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	svc := NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, NewBillingService(cfg, nil), nil, nil, upstream, &DeferredService{}, nil, nil, nil, nil, nil, nil, nil)

	account1 := &Account{ID: 1, Name: "acc-1", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture-key-1", "base_url": "http://upstream1.test"}, Extra: map[string]any{"openai_passthrough": true}}
	account2 := &Account{ID: 2, Name: "acc-2", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture-key-2", "base_url": "http://upstream2.test"}, Extra: map[string]any{"openai_passthrough": true}}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	nw := newMidStreamNotifyWriter()
	c.Writer = nw
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	c.Request.Header.Set("Content-Type", "application/json")

	var weakBody weak.Pointer[byte]
	done := make(chan struct{})
	var finalResult *OpenAIForwardResult
	var finalErr error
	go func() {
		defer close(done)
		body := []byte(`{"model":"gpt-5","stream":true,"input":"` + strings.Repeat("failover-", 32<<10) + `"}`)
		weakBody = weak.Make(&body[0])
		hookCtx, hook := WithRequestBodyReleaseHook(c.Request.Context(), c, func(_ *Account) {
			body = nil
		})
		defer hook.Clear(c)
		c.Request = c.Request.WithContext(hookCtx)

		_, err := svc.Forward(hookCtx, c, account1, body)
		if err == nil {
			finalErr = err
			_ = pw2.Close()
			return
		}
		var failoverErr *UpstreamFailoverError
		if !errors.As(err, &failoverErr) {
			finalErr = err
			_ = pw2.Close()
			return
		}

		// Failover to account2 must still have intact body.
		finalResult, finalErr = svc.Forward(hookCtx, c, account2, body)
		body = nil
	}()

	<-pw2Created
	_, _ = io.WriteString(pw2, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_2\"}}\n\n")
	// Passthrough buffers response.created until first output so pre-output failover stays possible.
	time.Sleep(30 * time.Millisecond)
	runtime.GC()
	require.NotNil(t, weakBody.Value(), "body must still be alive right after response.created before output")

	_, _ = io.WriteString(pw2, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n")
	close(deltaSent)
	waitForMidStreamOutput(t, nw, "resp_2")
	waitForMidStreamOutput(t, nw, "response.output_text.delta")

	// Prove mid-stream collection WHILE pw2 is still open!
	requireBodyCollected(t, weakBody, "body must be collected mid-stream after first client output while upstream pipe stays open")

	close(releaseStream)
	_, _ = io.WriteString(pw2, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_2\",\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":2,\"total_tokens\":12}}}\n\n")
	_ = pw2.Close()

	<-done
	require.NoError(t, finalErr)
	require.NotNil(t, finalResult)
	require.Equal(t, 10, finalResult.Usage.InputTokens)
}

func TestMidStreamRelease_CompactFallbackStillWorks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var seenModels []string
	var mu sync.Mutex
	upstream := &midStreamNonRetainingUpstream{
		fn: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
			b, _ := io.ReadAll(req.Body)
			_ = req.Body.Close()
			mu.Lock()
			seenModels = append(seenModels, string(b))
			attempt := len(seenModels)
			mu.Unlock()
			if attempt == 1 {
				errSSE := "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"model_not_found\",\"message\":\"The model `gpt-5` does not exist\"}}}\n\n"
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:       io.NopCloser(strings.NewReader(errSSE)),
					Request:    req,
				}, nil
			}
			okSSE := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_compact\",\"status\":\"completed\",\"usage\":{\"input_tokens\":5,\"output_tokens\":2}}}\n\n"
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(okSSE)),
				Request:    req,
			}, nil
		},
	}

	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAICompactModel = "gpt-5-compact-fallback"
	svc := NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, NewBillingService(cfg, nil), nil, nil, upstream, &DeferredService{}, nil, nil, nil, nil, nil, nil, nil)

	account := &Account{
		ID: 11, Name: "compact-acc", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "fixture-key",
			"base_url": "http://upstream-compact.test",
		},
		Extra: map[string]any{"openai_passthrough": true},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	c.Request.Header.Set("Content-Type", "application/json")
	MarkOpenAINativeCompactionV2(c)

	body := []byte(`{"model":"gpt-5","stream":true,"input":[{"type":"compaction_trigger","text":"compact"}]}`)
	// Mark pre-output failover disabled to ensure WithCompactFallbackPossible keeps body until fallback/output!
	ctx := WithPreOutputFailoverDisabled(c.Request.Context())
	hookCtx, hook := WithRequestBodyReleaseHook(ctx, c, func(_ *Account) { body = nil })
	defer hook.Clear(c)
	c.Request = c.Request.WithContext(hookCtx)

	res, err := svc.Forward(hookCtx, c, account, body)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Len(t, seenModels, 2)
	require.Contains(t, seenModels[0], `"gpt-5"`)
	require.Contains(t, seenModels[1], `"gpt-5-compact-fallback"`)
}

func TestMidStreamRelease_DisconnectAfterReleaseBillsPrecomputedInputTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("OpenAI_Passthrough", func(t *testing.T) {
		pr, pw := io.Pipe()
		pipeReady := make(chan struct{})
		upstream := &midStreamNonRetainingUpstream{
			fn: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
				if req != nil && req.Body != nil {
					_, _ = io.Copy(io.Discard, req.Body)
					_ = req.Body.Close()
				}
				close(pipeReady)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:       pr,
					Request:    req,
				}, nil
			},
		}

		cfg := &config.Config{RunMode: config.RunModeSimple}
		cfg.Security.URLAllowlist.Enabled = false
		cfg.Security.URLAllowlist.AllowInsecureHTTP = true
		svc := NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, NewBillingService(cfg, nil), nil, nil, upstream, &DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
		account := &Account{ID: 21, Name: "oa-disc", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture-key", "base_url": "http://upstream-disc.test"}, Extra: map[string]any{"openai_passthrough": true}}

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		nw := newMidStreamNotifyWriter()
		c.Writer = nw
		c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
		c.Request.Header.Set("Content-Type", "application/json")

		origCopy := []byte(`{"model":"gpt-5","stream":true,"input":"` + strings.Repeat("token-word ", 20000) + `"}`)
		expectedInputToks := estimateRequestInputTokens(origCopy)
		require.Greater(t, expectedInputToks, 1000)

		var weakBody weak.Pointer[byte]
		done := make(chan struct{})
		var res *OpenAIForwardResult
		var fwdErr error
		go func() {
			defer close(done)
			body := append([]byte(nil), origCopy...)
			weakBody = weak.Make(&body[0])
			ctx := WithPreOutputFailoverDisabled(c.Request.Context())
			hookCtx, hook := WithRequestBodyReleaseHook(ctx, c, func(_ *Account) { body = nil })
			defer hook.Clear(c)
			c.Request = c.Request.WithContext(hookCtx)
			res, fwdErr = svc.Forward(hookCtx, c, account, body)
			body = nil
		}()

		<-pipeReady
		_, _ = io.WriteString(pw, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial output\"}\n\n")
		waitForMidStreamOutput(t, nw, "partial output")
		requireBodyCollected(t, weakBody, "body should collect mid-stream before disconnect")

		// Simulate abrupt mid-stream disconnect (EOF before response.completed).
		_ = pw.CloseWithError(io.ErrUnexpectedEOF)
		<-done
		require.Error(t, fwdErr)
		require.NotNil(t, res)
		require.Equal(t, expectedInputToks, res.Usage.InputTokens)
	})

	t.Run("Anthropic_Gateway", func(t *testing.T) {
		pr, pw := io.Pipe()
		pipeReady := make(chan struct{})
		upstream := &midStreamNonRetainingUpstream{
			fn: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
				if req != nil && req.Body != nil {
					_, _ = io.Copy(io.Discard, req.Body)
					_ = req.Body.Close()
				}
				close(pipeReady)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:       pr,
					Request:    req,
				}, nil
			},
		}

		cfg := &config.Config{RunMode: config.RunModeSimple}
		cfg.Security.URLAllowlist.Enabled = false
		cfg.Security.URLAllowlist.AllowInsecureHTTP = true
		svc := &GatewayService{
			cfg:              cfg,
			httpUpstream:     upstream,
			billingService:   NewBillingService(cfg, nil),
			rateLimitService: NewRateLimitService(nil, nil, cfg, nil, nil),
		}
		account := &Account{ID: 22, Name: "ant-disc", Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture-key", "base_url": "http://upstream-ant.test"}}

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		nw := newMidStreamNotifyWriter()
		c.Writer = nw
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		c.Request.Header.Set("Content-Type", "application/json")

		origCopy := []byte(`{"model":"claude-sonnet-4","stream":true,"max_tokens":128,"messages":[{"role":"user","content":"` + strings.Repeat("token-word ", 20000) + `"}]}`)
		expectedInputToks := estimateRequestInputTokens(origCopy)
		require.Greater(t, expectedInputToks, 1000)

		var weakBody weak.Pointer[byte]
		done := make(chan struct{})
		var res *ForwardResult
		var fwdErr error
		go func() {
			defer close(done)
			body := append([]byte(nil), origCopy...)
			weakBody = weak.Make(&body[0])
			bodyRef := NewRequestBodyRef(body)
			parsed, err := ParseGatewayRequest(bodyRef, PlatformAnthropic)
			require.NoError(t, err)
			ctx := WithPreOutputFailoverDisabled(c.Request.Context())
			hookCtx, hook := WithRequestBodyReleaseHook(ctx, c, func(_ *Account) {
				body = nil
				bodyRef.Replace(nil)
				parsed.ReleaseBody()
			})
			defer hook.Clear(c)
			c.Request = c.Request.WithContext(hookCtx)
			body = nil
			res, fwdErr = svc.Forward(hookCtx, c, account, parsed)
			parsed = nil
			bodyRef = nil
		}()

		<-pipeReady
		// Emit content_block_delta WITHOUT message_start input_tokens so precomputed estimate is used on abrupt close!
		_, _ = io.WriteString(pw, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n")
		waitForMidStreamOutput(t, nw, "content_block_delta")
		requireBodyCollected(t, weakBody, "anthropic request body should collect mid-stream")

		_ = pw.CloseWithError(io.ErrUnexpectedEOF)
		<-done
		require.Error(t, fwdErr)
		require.NotNil(t, res)
		require.Equal(t, expectedInputToks, res.Usage.InputTokens+res.Usage.CacheReadInputTokens)
	})
}

func TestMidStreamRelease_DebugDumpAndPassthroughRememberInboundBodyGating(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("DebugDumpDisabled_SkipsRememberInboundBody", func(t *testing.T) {
		t.Setenv(DebugDumpDirEnv, "")
		upstream := &midStreamNonRetainingUpstream{
			fn: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
				if req != nil && req.Body != nil {
					_, _ = io.Copy(io.Discard, req.Body)
					_ = req.Body.Close()
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:       io.NopCloser(strings.NewReader("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_dbg\",\"status\":\"completed\",\"usage\":{\"input_tokens\":3,\"output_tokens\":1}}}\n\n")),
					Request:    req,
				}, nil
			},
		}
		cfg := &config.Config{RunMode: config.RunModeSimple}
		cfg.Security.URLAllowlist.Enabled = false
		cfg.Security.URLAllowlist.AllowInsecureHTTP = true
		svc := NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, NewBillingService(cfg, nil), nil, nil, upstream, &DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
		account := &Account{ID: 31, Name: "oa-nodbg", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture-key", "base_url": "http://upstream-nodbg.test"}, Extra: map[string]any{"openai_passthrough": true}}

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
		c.Request.Header.Set("Content-Type", "application/json")

		body := []byte(`{"model":"gpt-5","stream":true,"input":"hello"}`)
		_, err := svc.Forward(c.Request.Context(), c, account, body)
		require.NoError(t, err)
		_, ok := c.Get(debugInboundBodyKey)
		require.False(t, ok, "RememberInboundBody must not set debugInboundBodyKey when debug dump is disabled")
	})

	t.Run("DebugDumpEnabled_PreservesKeyMidStreamAndClearsAtChainEnd", func(t *testing.T) {
		t.Setenv(DebugDumpDirEnv, t.TempDir())
		require.True(t, DebugDumpEnabled())

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		RememberInboundBody(c, []byte(`{"debug":"payload"}`))
		ReleaseRequestBodyReferencesForMidStream(c)
		raw, ok := c.Get(debugInboundBodyKey)
		require.True(t, ok, "mid-stream release must preserve debugInboundBodyKey when DebugDumpEnabled is true")
		require.Equal(t, []byte(`{"debug":"payload"}`), raw)

		ReleaseRequestBodyReferences(c)
		cleared, _ := c.Get(debugInboundBodyKey)
		require.Empty(t, cleared, "end-of-chain cleanup must clear debugInboundBodyKey")
	})
}

func TestMidStreamRelease_OpenAIForwardApplyJSONEditsCollectsMidStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	pipeReady := make(chan struct{})
	capturedCh := make(chan string, 1)
	upstream := &midStreamNonRetainingUpstream{
		fn: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
			if req != nil && req.Body != nil {
				b, _ := io.ReadAll(req.Body)
				_ = req.Body.Close()
				capturedCh <- string(b)
			}
			close(pipeReady)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       pr,
				Request:    req,
			}, nil
		},
	}

	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	svc := NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, NewBillingService(cfg, nil), nil, nil, upstream, &DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	account := &Account{
		ID:       41,
		Name:     "oa-apply-json-edits",
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "fixture-key",
			"base_url": "http://upstream-edits.test",
		},
		Extra: map[string]any{"openai_passthrough": true},
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	nw := newMidStreamNotifyWriter()
	c.Writer = nw
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	c.Request.Header.Set("Content-Type", "application/json")

	var weakOrig weak.Pointer[byte]
	var res *OpenAIForwardResult
	var fwdErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		orig := []byte(`{"model":"deepseek-r1","stream":true,"reasoning":{"effort":"medium","summary":"auto"},"input":[{"id":"bad_msg_id","type":"message","role":"user","content":"` + strings.Repeat("x", 256<<10) + `"}]}`)
		require.GreaterOrEqual(t, len(orig), 256<<10)
		weakOrig = weak.Make(&orig[0])
		hookCtx, hook := WithRequestBodyReleaseHook(c.Request.Context(), c, func(_ *Account) {
			orig = nil
		})
		defer hook.Clear(c)
		c.Request = c.Request.WithContext(hookCtx)

		res, fwdErr = svc.Forward(hookCtx, c, account, orig)
		orig = nil
	}()

	<-pipeReady
	captured := <-capturedCh
	require.True(t, gjson.Valid(captured), "upstream payload must remain valid JSON")
	require.False(t, strings.Contains(captured, "bad_msg_id"), "sanitizeOpenAIResponsesInputItemIDs must strip invalid message item id")
	require.False(t, gjson.Get(captured, "input.0.id").Exists(), "input.0.id must be removed")
	require.False(t, gjson.Get(captured, "reasoning.summary").Exists(), "stripDeepSeekReasoningSummarySwitches must remove reasoning.summary")
	require.Equal(t, "medium", gjson.Get(captured, "reasoning.effort").String(), "reasoning.effort must be preserved")

	_, _ = io.WriteString(pw, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_edits\"}}\n\n")
	time.Sleep(30 * time.Millisecond)
	runtime.GC()
	require.NotNil(t, weakOrig.Value(), "original request buffer must stay alive right after response.created before first semantic output")

	_, _ = io.WriteString(pw, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n")
	waitForMidStreamOutput(t, nw, "resp_edits")
	waitForMidStreamOutput(t, nw, "response.output_text.delta")

	requireBodyCollected(t, weakOrig, "original request buffer must be collected mid-stream after applyJSONEdits and response.output_text.delta while pipe stays open")

	_, _ = io.WriteString(pw, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_edits\",\"status\":\"completed\",\"usage\":{\"input_tokens\":12,\"output_tokens\":3,\"total_tokens\":15}}}\n\n")
	_ = pw.Close()

	<-done
	require.NoError(t, fwdErr)
	require.NotNil(t, res)
	require.Equal(t, 12, res.Usage.InputTokens)
	require.Equal(t, 3, res.Usage.OutputTokens)
}

type midStreamFlushOrderWriter struct {
	gin.ResponseWriter
	mu               sync.Mutex
	buf              bytes.Buffer
	released         *atomic.Bool
	releasedAtFlush  atomic.Bool
	firstFlushLogged atomic.Bool
}

func (w *midStreamFlushOrderWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	_, _ = w.buf.Write(p)
	w.mu.Unlock()
	return w.ResponseWriter.Write(p)
}

func (w *midStreamFlushOrderWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *midStreamFlushOrderWriter) Flush() {
	w.mu.Lock()
	hasDelta := strings.Contains(w.buf.String(), "response.output_text.delta")
	w.mu.Unlock()
	if hasDelta && w.firstFlushLogged.CompareAndSwap(false, true) {
		w.releasedAtFlush.Store(w.released.Load())
	}
	w.ResponseWriter.Flush()
}

func TestMidStreamRelease_TriggerRunsBeforeFirstFlush(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, passthrough := range []bool{false, true} {
		name := "response_handling"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			pr, pw := io.Pipe()
			pipeReady := make(chan struct{})
			upstream := &midStreamNonRetainingUpstream{
				fn: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
					if req != nil && req.Body != nil {
						_, _ = io.Copy(io.Discard, req.Body)
						_ = req.Body.Close()
					}
					close(pipeReady)
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
						Body:       pr,
						Request:    req,
					}, nil
				},
			}

			cfg := &config.Config{RunMode: config.RunModeSimple}
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			svc := NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, NewBillingService(cfg, nil), nil, nil, upstream, &DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
			extra := map[string]any{}
			if passthrough {
				extra["openai_passthrough"] = true
			}
			account := &Account{
				ID:          51,
				Name:        "oa-flush-order",
				Platform:    PlatformOpenAI,
				Type:        AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "fixture-key", "base_url": "http://upstream-order.test"},
				Extra:       extra,
			}

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			var released atomic.Bool
			ow := &midStreamFlushOrderWriter{ResponseWriter: c.Writer, released: &released}
			c.Writer = ow
			c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
			c.Request.Header.Set("Content-Type", "application/json")

			body := []byte(`{"model":"gpt-5","stream":true,"input":"hello"}`)
			hookCtx, hook := WithRequestBodyReleaseHook(c.Request.Context(), c, func(_ *Account) {
				released.Store(true)
				body = nil
			})
			defer hook.Clear(c)
			c.Request = c.Request.WithContext(hookCtx)

			done := make(chan error, 1)
			go func() {
				_, err := svc.Forward(hookCtx, c, account, body)
				done <- err
			}()

			<-pipeReady
			_, _ = io.WriteString(pw, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_order\"}}\n\n")
			_, _ = io.WriteString(pw, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n")
			_, _ = io.WriteString(pw, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_order\",\"status\":\"completed\",\"usage\":{\"input_tokens\":5,\"output_tokens\":1,\"total_tokens\":6}}}\n\n")
			_ = pw.Close()

			require.NoError(t, <-done)
			require.True(t, ow.firstFlushLogged.Load(), "expected first semantic output flush to occur")
			require.True(t, ow.releasedAtFlush.Load(), "TriggerRequestBodyRelease must run before flusher.Flush()")
		})
	}
}
