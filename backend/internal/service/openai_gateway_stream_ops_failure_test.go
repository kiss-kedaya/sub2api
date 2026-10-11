package service

import (
	"context"
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

func TestOpenAIStream524Classification(t *testing.T) {
	for _, tt := range []struct {
		payload string
		status  int
	}{
		{`{"type":"error","error":{"message":"upstream 524: <!DOCTYPE html> provider.example"}}`, 524},
		{`{"type":"response.failed","response":{"error":{"status_code":524}}}`, 524},
		{`{"type":"response.failed","response":{"error":{"message":"request failed"}},"input":"upstream 524: ignored"}`, 502},
		{`{"type":"error","error":{"message":"upstream 5240: ignored"}}`, 502},
	} {
		require.Equal(t, tt.status, openAIStreamFailureStatus([]byte(tt.payload), ""))
	}
}

func TestOpenAIStreamFinalFailureOps(t *testing.T) {
	for _, passthrough := range []bool{true, false} {
		for _, nested524 := range []bool{true, false} {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, LogUpstreamErrorBody: true}}}
			body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"
			if nested524 {
				body += "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"message\":\"upstream 524: <!DOCTYPE html> provider.example\"}}}\n\n"
			}
			resp := &http.Response{StatusCode: 200, Header: http.Header{"X-Request-Id": []string{"test-upstream-id"}}, Body: io.NopCloser(strings.NewReader(body))}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			var err error
			if passthrough {
				_, err = svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "model", "model")
			} else {
				_, err = svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "model", "model")
			}
			require.Error(t, err)
			marked := GetOpsStreamErrors(c)
			require.NotEmpty(t, marked)
			require.True(t, marked[0].CountTowardsSLA)
			status := 502
			if nested524 {
				status = 524
			}
			require.Equal(t, status, marked[0].IntendedStatus)
			if nested524 {
				require.NotContains(t, c.GetString(OpsUpstreamErrorMessageKey), "provider.example")
				require.Empty(t, c.GetString(OpsUpstreamErrorDetailKey))
			}
		}
	}
}
