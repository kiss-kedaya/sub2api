//go:build unit

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGrokNativeResponsesForwardKeepsStreamAndFlushesBeforeCompletion(t *testing.T) {
	for _, tools := range []string{`[]`, `[{"type":"custom","name":"apply_patch"}]`} {
		t.Run(tools, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			first := "data: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_native\"}}\n\n" +
				"data: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_native\",\"output_index\":0,\"content_index\":0,\"delta\":\"hello\"}\n\n"
			terminal := "data: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"id\":\"resp_native\",\"status\":\"completed\",\"usage\":{\"input_tokens\":128,\"output_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":96}}}}\n\n"
			resp, _, release := delayedGrokSSEResponse(t, first, terminal)
			source := resp.Body
			upstream := &httpUpstreamRecorder{resp: resp}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream, toolCorrector: NewCodexToolCorrector()}
			request := []byte(`{"model":"grok-4.6","stream":true,"input":"hello","tools":` + tools + `}`)
			recorder := newOpenAIResponseFlushRecorder()
			c, _ := gin.CreateTestContext(recorder)
			ctx, cancel := context.WithCancel(context.Background())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(request))).WithContext(ctx)
			finished := make(chan struct{})
			var result *OpenAIForwardResult
			var forwardErr error
			t.Cleanup(func() {
				cancel()
				assert.NoError(t, source.Close())
				release()
				select {
				case <-finished:
				case <-time.After(3 * time.Second):
					t.Error("native Responses forwarding did not stop")
				}
			})
			go func() {
				defer close(finished)
				result, forwardErr = svc.Forward(ctx, c, grokProtocolAPIKeyAccount(7200), request)
			}()
			waitOpenAIResponseFlushCount(t, recorder, 1)
			body, _ := recorder.snapshot()
			require.Contains(t, body, `"delta":"hello"`)
			require.NotContains(t, body, "response.completed")
			select {
			case <-finished:
				t.Fatalf("request ended while completion was withheld: %v", forwardErr)
			default:
			}
			release()
			select {
			case <-finished:
			case <-time.After(3 * time.Second):
				t.Fatal("request waited for EOF after completion")
			}
			require.NoError(t, forwardErr)
			require.NotNil(t, result)
			require.True(t, result.Stream)
			require.Equal(t, "/v1/responses", upstream.lastReq.URL.Path)
			require.Equal(t, gjson.True, gjson.GetBytes(upstream.lastBody, "stream").Type)
			require.Equal(t, 96, result.Usage.CacheReadInputTokens)
			require.NotNil(t, result.FirstTokenMs)
		})
	}
}
