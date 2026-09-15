//go:build unit

package service

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const timingReviewTerminal = `{"type":"response.completed","response":{"id":"resp_timing","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":12,"output_tokens":4,"input_tokens_details":{"cached_tokens":3}}}}`

type timingReviewResult struct {
	firstTokenMs *int
	usage        OpenAIUsage
	err          error
}

func setTimingReviewMode(t *testing.T, mode string) {
	t.Helper()
	previous := gatewayForwardingCache.Load()
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{openAITTFTMode: mode})
	t.Cleanup(func() {
		if previous != nil {
			gatewayForwardingCache.Store(previous)
		} else {
			gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{openAITTFTMode: OpenAITTFTModeSemantic})
		}
	})
}

func startTimingReviewStream(t *testing.T, platform, path string, requestBytes, guardSeconds int, recorder *openAIResponseFlushRecorder) (*io.PipeWriter, <-chan timingReviewResult) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+path, nil)
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		OpenAIFirstOutputTimeoutSeconds: guardSeconds,
	}}, toolCorrector: NewCodexToolCorrector()}
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
	account := &Account{ID: 1, Platform: platform, Type: AccountTypeAPIKey}
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: reader}
	if platform == PlatformGrok {
		resp.Body = newGrokResponsesBillingPingFilterBody(resp.Body, account, defaultMaxLineSize)
	}
	resultCh := make(chan timingReviewResult, 1)
	start := time.Now()
	go func() {
		defer func() { _ = resp.Body.Close() }()
		if path == "responses" {
			result, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, account, start, "model", "model")
			out := timingReviewResult{err: err}
			if result != nil {
				out.firstTokenMs, out.usage = result.firstTokenMs, *result.usage
			}
			resultCh <- out
			return
		}
		var result *OpenAIForwardResult
		var err error
		if path == "chat/completions" {
			result, err = svc.handleChatStreamingResponse(resp, c, account, "model", "model", "model", start, requestBytes)
		} else {
			result, err = svc.handleAnthropicStreamingResponse(resp, c, account, "model", "model", "model", start)
		}
		out := timingReviewResult{err: err}
		if result != nil {
			out.firstTokenMs, out.usage = result.FirstTokenMs, result.Usage
		}
		resultCh <- out
	}()
	return writer, resultCh
}

func writeTimingReviewSSE(t *testing.T, writer *io.PipeWriter, payload string) {
	t.Helper()
	_, err := io.WriteString(writer, "data: "+payload+"\n\n")
	require.NoError(t, err)
	synctest.Wait()
}

func requireTimingReviewResult(t *testing.T, result timingReviewResult, wantMS int) {
	t.Helper()
	require.NoError(t, result.err)
	require.NotNil(t, result.firstTokenMs)
	require.Equal(t, wantMS, *result.firstTokenMs)
	require.Equal(t, 12, result.usage.InputTokens)
	require.Equal(t, 4, result.usage.OutputTokens)
	require.Equal(t, 3, result.usage.CacheReadInputTokens)
}

func TestStreamTimingReview_ProgressBeforeTerminal(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformGrok} {
		for _, path := range []string{"responses", "chat/completions", "messages"} {
			for _, mode := range []string{OpenAITTFTModeSemantic, OpenAITTFTModeVisible} {
				for _, requestBytes := range []int{0, 64 * 1024, 128 * 1024} {
					t.Run(fmt.Sprintf("%s/%s/%s/request_%d", platform, path, mode, requestBytes), func(t *testing.T) {
						setTimingReviewMode(t, mode)
						synctest.Test(t, func(t *testing.T) {
							recorder := newOpenAIResponseFlushRecorder()
							writer, results := startTimingReviewStream(t, platform, path, requestBytes, 30, recorder)
							writeTimingReviewSSE(t, writer, `{"type":"response.created","response":{"id":"resp_timing"}}`)
							time.Sleep(time.Second)
							writeTimingReviewSSE(t, writer, `{"type":"response.output_item.added","output_index":0,"item":{"id":"reasoning_1","type":"reasoning","summary":[]}}`)
							time.Sleep(time.Second)
							writeTimingReviewSSE(t, writer, `{"type":"response.output_text.delta","output_index":1,"delta":"hel"}`)
							_, flushes := recorder.snapshot()
							require.NotEmpty(t, flushes)
							require.Contains(t, flushes[len(flushes)-1], `"hel"`, "text must be flushed while terminal is withheld")
							require.NotContains(t, flushes[len(flushes)-1], "response.completed")
							time.Sleep(time.Second)
							writeTimingReviewSSE(t, writer, `{"type":"response.output_text.delta","output_index":1,"delta":"lo"}`)
							_, flushes = recorder.snapshot()
							require.Contains(t, flushes[len(flushes)-1], `"lo"`, "subsequent text must also flush before terminal")
							time.Sleep(6 * time.Second)
							writeTimingReviewSSE(t, writer, timingReviewTerminal)
							require.NoError(t, writer.Close())
							wantMS := 1000
							if mode == OpenAITTFTModeVisible {
								wantMS = 2000
							}
							requireTimingReviewResult(t, <-results, wantMS)
						})
					})
				}
			}
		}
	}
}

func TestStreamTimingReview_GuardedTTFTExcludesBlockedDownstreamFlush(t *testing.T) {
	for _, mode := range []string{OpenAITTFTModeSemantic, OpenAITTFTModeVisible} {
		t.Run(mode, func(t *testing.T) {
			setTimingReviewMode(t, mode)
			synctest.Test(t, func(t *testing.T) {
				release := make(chan struct{})
				recorder := newOpenAIResponseFlushRecorder()
				recorder.blockFlush, recorder.flushBlocked, recorder.releaseFlush = 1, make(chan struct{}), release
				writer, results := startTimingReviewStream(t, PlatformOpenAI, "responses", 0, 30, recorder)
				writeTimingReviewSSE(t, writer, `{"type":"response.created"}`)
				time.Sleep(2 * time.Second)
				writeTimingReviewSSE(t, writer, `{"type":"response.output_text.delta","delta":"hello"}`)
				<-recorder.flushBlocked
				time.Sleep(7 * time.Second)
				close(release)
				synctest.Wait()
				writeTimingReviewSSE(t, writer, timingReviewTerminal)
				require.NoError(t, writer.Close())
				requireTimingReviewResult(t, <-results, 2000)
			})
		})
	}
}

func TestStreamTimingReview_TerminalOnlyText(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformGrok} {
		for _, path := range []string{"responses", "chat/completions", "messages"} {
			t.Run(platform+"/"+path, func(t *testing.T) {
				setTimingReviewMode(t, OpenAITTFTModeSemantic)
				synctest.Test(t, func(t *testing.T) {
					recorder := newOpenAIResponseFlushRecorder()
					writer, results := startTimingReviewStream(t, platform, path, 128*1024, 30, recorder)
					writeTimingReviewSSE(t, writer, `{"type":"response.created","response":{"id":"resp_timing"}}`)
					time.Sleep(9 * time.Second)
					body, _ := recorder.snapshot()
					require.NotContains(t, body, "hello")
					writeTimingReviewSSE(t, writer, timingReviewTerminal)
					require.NoError(t, writer.Close())
					result := <-results
					body, _ = recorder.snapshot()
					require.Contains(t, body, "hello", "terminal-only upstream text must not disappear in conversion")
					requireTimingReviewResult(t, result, 9000)
				})
			})
		}
	}
}

func TestStreamTimingReview_LargePreambleFlushesAtFirstText(t *testing.T) {
	setTimingReviewMode(t, OpenAITTFTModeSemantic)
	synctest.Test(t, func(t *testing.T) {
		recorder := newOpenAIResponseFlushRecorder()
		writer, results := startTimingReviewStream(t, PlatformOpenAI, "responses", 0, 30, recorder)
		writeTimingReviewSSE(t, writer, `{"type":"response.created","padding":"`+strings.Repeat("x", 70*1024)+`"}`)
		body, flushes := recorder.snapshot()
		require.Empty(t, body)
		require.Empty(t, flushes)
		time.Sleep(2 * time.Second)
		writeTimingReviewSSE(t, writer, `{"type":"response.output_text.delta","delta":"hello"}`)
		_, flushes = recorder.snapshot()
		require.Len(t, flushes, 1)
		require.Contains(t, flushes[0], "hello")
		time.Sleep(7 * time.Second)
		writeTimingReviewSSE(t, writer, timingReviewTerminal)
		require.NoError(t, writer.Close())
		requireTimingReviewResult(t, <-results, 2000)
	})
}

func TestStreamTimingReview_DelayedEventBoundary(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformGrok} {
		for _, path := range []string{"responses", "chat/completions", "messages"} {
			t.Run(platform+"/"+path, func(t *testing.T) {
				setTimingReviewMode(t, OpenAITTFTModeSemantic)
				synctest.Test(t, func(t *testing.T) {
					recorder := newOpenAIResponseFlushRecorder()
					writer, results := startTimingReviewStream(t, platform, path, 128*1024, 30, recorder)
					time.Sleep(2 * time.Second)
					_, err := io.WriteString(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n")
					require.NoError(t, err)
					synctest.Wait()
					_, flushes := recorder.snapshot()
					require.Empty(t, flushes, "a data line alone must not dispatch an unfinished SSE event")
					time.Sleep(7 * time.Second)
					_, err = io.WriteString(writer, "\n")
					require.NoError(t, err)
					synctest.Wait()
					_, flushes = recorder.snapshot()
					require.Len(t, flushes, 1)
					require.Contains(t, flushes[0], "hello")
					writeTimingReviewSSE(t, writer, timingReviewTerminal)
					require.NoError(t, writer.Close())
					wantMS := 9000
					if platform == PlatformGrok && path == "responses" {
						// The non-staged native path historically measures the data line.
						wantMS = 2000
					}
					requireTimingReviewResult(t, <-results, wantMS)
				})
			})
		}
	}
}

func TestStreamTimingReview_EventFieldAndTerminalAlias(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformGrok} {
		for _, path := range []string{"responses", "chat/completions", "messages"} {
			t.Run(platform+"/"+path, func(t *testing.T) {
				setTimingReviewMode(t, OpenAITTFTModeSemantic)
				synctest.Test(t, func(t *testing.T) {
					recorder := newOpenAIResponseFlushRecorder()
					writer, results := startTimingReviewStream(t, platform, path, 128*1024, 30, recorder)
					time.Sleep(2 * time.Second)
					_, err := io.WriteString(writer, "event: response.output_text.delta\ndata: {\"delta\":\"hello\"}\n\n")
					require.NoError(t, err)
					synctest.Wait()
					_, flushes := recorder.snapshot()
					require.NotEmpty(t, flushes)
					require.Contains(t, flushes[len(flushes)-1], "hello")
					time.Sleep(7 * time.Second)
					writeTimingReviewSSE(t, writer, strings.Replace(timingReviewTerminal, "response.completed", "response.done", 1))
					require.NoError(t, writer.Close())
					requireTimingReviewResult(t, <-results, 2000)
				})
			})
		}
	}
}

func TestStreamTimingReview_UsageOnlyDoesNotImplyVisibleText(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformGrok} {
		for _, path := range []string{"responses", "chat/completions", "messages"} {
			for _, mode := range []string{OpenAITTFTModeSemantic, OpenAITTFTModeVisible} {
				t.Run(platform+"/"+path+"/"+mode, func(t *testing.T) {
					setTimingReviewMode(t, mode)
					synctest.Test(t, func(t *testing.T) {
						recorder := newOpenAIResponseFlushRecorder()
						writer, results := startTimingReviewStream(t, platform, path, 128*1024, 30, recorder)
						writeTimingReviewSSE(t, writer, `{"type":"response.created"}`)
						time.Sleep(9 * time.Second)
						writeTimingReviewSSE(t, writer, `{"type":"response.completed","response":{"id":"resp_usage","status":"completed","output":[],"usage":{"input_tokens":12,"output_tokens":400}}}`)
						require.NoError(t, writer.Close())
						result := <-results
						require.NoError(t, result.err)
						require.Equal(t, 400, result.usage.OutputTokens)
						if mode == OpenAITTFTModeSemantic {
							require.NotNil(t, result.firstTokenMs)
							require.Equal(t, 9000, *result.firstTokenMs)
						} else {
							require.Nil(t, result.firstTokenMs)
						}
					})
				})
			}
		}
	}
}
