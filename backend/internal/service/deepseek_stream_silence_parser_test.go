//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

// Force the same legal frame across many transport reads, independently of the
// scanner's buffer size. No wall-clock wait is needed to prove publication.
type silenceFragmentedReader struct{ io.Reader }

func (r silenceFragmentedReader) Read(p []byte) (int, error) {
	if len(p) > 1024 {
		p = p[:1024]
	}
	return r.Reader.Read(p)
}
func (silenceFragmentedReader) Close() error { return nil }

func TestDeepSeekStreamSilenceParsedProgress(t *testing.T) {
	long := strings.Repeat("x", 96<<10)
	for _, tc := range []struct {
		name, wire string
		output     bool
	}{
		{"long_tool", "data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"" + long + "\"}\n\n", true},
		{"long_image", "data: {\"type\":\"response.image_generation_call.partial_image\",\"partial_image_b64\":\"" + long + "\"}\n\n", true},
		{"concatenated", "data: {\"type\":\"response.in_progress\"}{\"type\":\"response.output_text.delta\",\"delta\":\"answer\"}\n\n", true},
		{"multiline", "event: response.output_text.delta\ndata: {\"delta\":\ndata: \"answer\"}\n\n", true},
		{"text", "data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\n", true},
		{"chat_tool", "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"function\":{\"arguments\":\"args\"}}]}}]}\n\n", true},
		{"chat_image", "data: {\"choices\":[{\"delta\":{\"images\":[{\"type\":\"image_url\",\"image_url\":{\"url\":\"https://example.invalid/image.png\"}}]}}]}\n\n", true},
		{"reasoning", "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"thinking\"}}]}\n\n", true},
		{"named_error_overrides_output_type", "event: error\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"not output\"}\n\n", false},
		{"explicit_failure", "data: {\"type\":\"error\",\"delta\":\"not output\"}\n\n", false},
		{"status_ping_usage", ": ping\n\nevent: ping\ndata: {}\n\ndata: {\"type\":\"response.in_progress\"}\n\ndata: {\"choices\":[],\"usage\":{\"output_tokens\":2}}\n\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shortenDeepSeekStreamSilence(t, time.Hour)
			ctx, cancel := context.WithCancelCause(context.Background())
			b := newOpenAIStreamSilenceBody(ctx, cancel, silenceFragmentedReader{strings.NewReader(tc.wire)}, nil, true)
			defer func() { require.NoError(t, b.Close()) }()
			before := time.Now().Add(-time.Minute)
			b.progress.Store(&openAIStreamSilenceProgress{lastOutput: before})
			svc := &OpenAIGatewayService{}
			scanner := svc.newUpstreamSSEScanner(b)
			for scanner.Scan() {
			}
			require.NoError(t, scanner.Err())
			require.Equal(t, tc.output, b.progress.Load().lastOutput.After(before))
		})
	}
}

func TestDeepSeekStreamSilenceParsedConsumers(t *testing.T) {
	long := strings.Repeat("x", 96<<10)
	for _, route := range []string{"normal", "passthrough", "raw_chat", "chat", "messages", "cc_responses", "cc_messages"} {
		for _, shape := range []string{"long_tool", "long_image", "concatenated", "multiline"} {
			t.Run(route+"/"+shape, func(t *testing.T) {
				shortenDeepSeekStreamSilence(t, time.Hour)
				wire := "data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"" + long + "\"}\n\n"
				switch shape {
				case "long_image":
					wire = "data: {\"type\":\"response.image_generation_call.partial_image\",\"partial_image_b64\":\"" + long + "\"}\n\n"
				case "concatenated":
					wire = "data: {\"type\":\"response.in_progress\"}{\"type\":\"response.output_text.delta\",\"delta\":\"answer\"}\n\n"
				case "multiline":
					wire = "event: response.output_text.delta\ndata: {\"delta\":\ndata: \"answer\"}\n\n"
				}
				// The same upstream Chat frame is valid in the raw and both CC bridges.
				if strings.Contains(route, "cc_") || route == "raw_chat" {
					wire = "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"" + long + "\"}}]}}]}\n\n"
					if shape == "long_image" {
						wire = "data: {\"choices\":[{\"index\":0,\"delta\":{\"images\":[{\"type\":\"image_url\",\"image_url\":{\"url\":\"data:image/png;base64," + long + "\"}}]}}]}\n\n"
					}
					if shape == "concatenated" {
						wire = "data: {\"type\":\"response.in_progress\"}{\"type\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"answer\"}}]}\n\n"
					}
					if shape == "multiline" {
						wire = "data: {\"choices\":[{\"index\":0,\"delta\":{\ndata: \"reasoning_content\":\"thinking\"}}]}\n\n"
					}
				}
				svc, c, _, account, resp := streamSilenceFixture(t, "/v1/responses", "")
				require.NoError(t, resp.Body.Close())
				ctx, cancel := context.WithCancelCause(context.Background())
				b := newOpenAIStreamSilenceBody(ctx, cancel, silenceFragmentedReader{strings.NewReader(wire)}, nil, true)
				defer func() { require.NoError(t, b.Close()) }()
				before := time.Now().Add(-time.Minute)
				b.progress.Store(&openAIStreamSilenceProgress{lastOutput: before})
				resp.Body = b
				switch route {
				case "normal":
					_, _ = svc.handleStreamingResponse(ctx, resp, c, account, time.Now(), "synthetic", "synthetic")
				case "passthrough":
					_, _ = svc.handleStreamingResponsePassthrough(ctx, resp, c, account, time.Now(), "synthetic", "synthetic")
				case "raw_chat":
					_, _ = svc.streamRawChatCompletions(c, resp, account, "synthetic", "synthetic", "synthetic", nil, nil, time.Now(), 0)
				case "chat":
					_, _ = svc.handleChatStreamingResponse(resp, c, account, "synthetic", "synthetic", "synthetic", time.Now(), 0)
				case "messages":
					_, _ = svc.handleAnthropicStreamingResponse(resp, c, account, "synthetic", "synthetic", "synthetic", time.Now())
				case "cc_responses":
					_, _ = svc.streamChatCompletionsAsResponses(c, resp, "synthetic", nil, nil, false, nil, nil, "synthetic", "synthetic", nil, nil, time.Now())
				case "cc_messages":
					_, _ = svc.streamChatCompletionsAsAnthropic(c, resp, "synthetic", "synthetic", "synthetic", nil, nil, time.Now())
				}
				require.NotNil(t, b.progress.Load())
				require.True(t, b.progress.Load().lastOutput.After(before), "consumer did not publish parsed output")
			})
		}
	}
}

type silenceProgressBridgeUpstream struct {
	before time.Time
	body   *openAIStreamSilenceBody
}

func (u *silenceProgressBridgeUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	source := &silenceBridgeDiagnosticBody{Reader: strings.NewReader(silenceResponsesPrelude + "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_synthetic\",\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":2}}}\n\n")}
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: source}
	source.beforeRead = func() {
		if body, ok := resp.Body.(*openAIStreamSilenceBody); ok {
			u.body = body
			u.before = time.Now().Add(-time.Minute)
			u.body.progress.Store(&openAIStreamSilenceProgress{lastOutput: u.before})
		}
	}
	return resp, nil
}
func (u *silenceProgressBridgeUpstream) DoWithTLS(req *http.Request, p string, id int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, p, id, n)
}

type silenceBridgeDiagnosticBody struct {
	*strings.Reader
	beforeRead func()
}

func (b *silenceBridgeDiagnosticBody) Read(p []byte) (int, error) {
	if b.beforeRead != nil {
		fn := b.beforeRead
		b.beforeRead = nil
		fn()
	}
	return b.Reader.Read(p)
}
func (*silenceBridgeDiagnosticBody) Close() error { return nil }

func TestDeepSeekStreamSilenceParsedHTTPBridge(t *testing.T) {
	shortenDeepSeekStreamSilence(t, time.Hour)
	payload := []byte(`{"type":"response.create","model":"deepseek-synthetic","input":"` + strings.Repeat("x", int(deepSeekLargeRequestBytes)+1) + `","stream":true}`)
	upstream := &silenceProgressBridgeUpstream{}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	account := rawChatCompletionsTestAccount()
	account.Platform = PlatformDeepseek
	c, _ := newOpenAIPartialUsageContext(t, payload)
	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(WithOpsGinContext(c.Request.Context(), c), c, account, "synthetic-token", payload, len(payload), "deepseek-synthetic", "", "", "", "", 1, func([]byte) error { return nil })
	require.NoError(t, err)
	require.False(t, result.NonBillableUpstreamError)
	require.NotNil(t, upstream.body)
	require.True(t, upstream.body.progress.Load().lastOutput.After(upstream.before))
}

func TestDeepSeekStreamSilenceBufferedActivity(t *testing.T) {
	shortenDeepSeekStreamSilence(t, time.Hour)
	for _, sse := range []bool{false, true} {
		ctx, cancel := context.WithCancelCause(context.Background())
		b := newOpenAIStreamSilenceBody(ctx, cancel, io.NopCloser(strings.NewReader("data: output\n\n")), nil, sse)
		before := time.Now().Add(-time.Minute)
		b.progress.Store(&openAIStreamSilenceProgress{lastOutput: before})
		_, err := io.ReadAll(b)
		require.NoError(t, err)
		require.True(t, b.progress.Load().lastOutput.After(before))
		require.NoError(t, b.Close())
	}
}

func TestDeepSeekStreamSilencePendingJoinBound(t *testing.T) {
	shortenDeepSeekStreamSilence(t, time.Hour)
	// Each line is legal-sized but the unfinished JSON event exceeds the observer's
	// pending-copy budget. The consumer still gets every byte and later independent
	// compatibility documents can publish output without retaining the bad prefix.
	prefix := "data: {\"delta\":\"" + strings.Repeat("x", 96<<10) + "\n"
	wire := strings.Repeat(prefix, 180) + "data: {\"type\":\"response.output_text.delta\",\"delta\":\"recovered\"}\n\n"
	ctx, cancel := context.WithCancelCause(context.Background())
	b := newOpenAIStreamSilenceBody(ctx, cancel, io.NopCloser(strings.NewReader(wire)), nil, true)
	defer func() { require.NoError(t, b.Close()) }()
	before := time.Now().Add(-time.Minute)
	b.progress.Store(&openAIStreamSilenceProgress{lastOutput: before})
	scanner := (&OpenAIGatewayService{}).newUpstreamSSEScanner(b)
	lines := 0
	for scanner.Scan() {
		lines++
	}
	require.NoError(t, scanner.Err())
	require.Equal(t, 182, lines)
	require.True(t, b.progress.Load().lastOutput.After(before))
}
