//go:build unit

package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestChatFallbackUsageDetailsReachResponsesClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const usage = `{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150,"prompt_tokens_details":{"cached_tokens":60,"cache_write_tokens":10,"audio_tokens":4},"completion_tokens_details":{"reasoning_tokens":30,"audio_tokens":2,"accepted_prediction_tokens":10,"rejected_prediction_tokens":3}}`
	for _, mode := range []string{"buffered", "stream"} {
		t.Run(mode, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			payload := `{"id":"chatcmpl-review","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":` + usage + `}`
			stream := mode == "stream"
			if stream {
				payload = "data: " + `{"id":"chatcmpl-review","choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}],"usage":null}` + "\n\ndata: " + `{"choices":[],"usage":` + usage + `}` + "\n\ndata: [DONE]\n\n"
			}
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload))}
			defer func() { require.NoError(t, resp.Body.Close()) }()
			svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
			var result *OpenAIForwardResult
			var err error
			if stream {
				result, err = svc.streamChatCompletionsAsResponses(c, resp, "review-model", nil, nil, false, nil, nil, "review-model", "review-model", nil, nil, time.Now())
			} else {
				result, err = svc.bufferChatCompletionsAsResponses(c, resp, "review-model", nil, nil, false, nil, nil, "review-model", "review-model", nil, nil, time.Now())
			}
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, rec.Code)
			require.Equal(t, OpenAIUsage{InputTokens: 100, OutputTokens: 50, CacheReadInputTokens: 60, CacheCreationInputTokens: 10}, result.Usage)
			wire := cacheReviewWireUsage(rec.Body.String(), stream)
			require.JSONEq(t, `{"input_tokens":100,"output_tokens":50,"total_tokens":150,"cache_creation_input_tokens":10,"input_tokens_details":{"cached_tokens":60,"cache_write_tokens":10,"audio_tokens":4},"output_tokens_details":{"reasoning_tokens":30,"audio_tokens":2,"accepted_prediction_tokens":10,"rejected_prediction_tokens":3}}`, wire.Raw)
		})
	}
}
