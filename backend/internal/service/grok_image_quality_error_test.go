package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const qualityUnsupportedFixture = `{"error":{"code":"bad_response_status_code","message":"This model only supports the following quality value(s): low, medium, auto.","param":"","type":"bad_response_status_code"}}`

func TestGrokImageQualityUnsupportedClassification(t *testing.T) {
	for _, tc := range []struct {
		name, model, body string
		status            int
		want              bool
	}{
		{"reported response", "grok-imagine-image-2.0", qualityUnsupportedFixture, 400, true},
		{"success body", "grok-imagine-image-2.0", qualityUnsupportedFixture, 200, false},
		{"video", "grok-imagine-video", qualityUnsupportedFixture, 400, false},
		{"other platform model", "gpt-image-2", qualityUnsupportedFixture, 400, false},
		{"malformed body", "grok-imagine-image-2.0", "quality high invalid", 400, false},
		{"invalid prompt", "grok-imagine-image-2.0", `{"error":{"message":"prompt is required"}}`, 400, false},
		{"policy", "grok-imagine-image-2.0", `{"error":{"message":"content policy violation"}}`, 400, false},
		{"no allowed values", "grok-imagine-image-2.0", `{"error":{"message":"This model only supports the following quality value(s):"}}`, 400, false},
		{"extra text", "grok-imagine-image-2.0", `{"error":{"message":"This model only supports the following quality value(s): low. Please retry"}}`, 400, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isGrokImageQualityUnsupported(tc.status, tc.model, []byte(tc.body)))
		})
	}
}

func TestGrokImageQualityErrorDefersResponseAndDoesNotBanAccount(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	resp := &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(qualityUnsupportedFixture))}
	s := &OpenAIGatewayService{}
	_, err := s.handleGrokMediaErrorResponse(context.Background(), resp, c, &Account{ID: 29131, Platform: PlatformGrok, Type: AccountTypeAPIKey}, "test-request", "grok-imagine-image-2.0")
	var failure *UpstreamFailoverError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, GrokImageQualityUnsupportedReason, failure.Reason)
	require.True(t, failure.ShouldRetryNextAccount())
	require.False(t, failure.RetryableOnSameAccount)
	require.False(t, failure.ShouldReportAccountScheduleFailure())
	require.False(t, c.Writer.Written(), "must not commit the 400 before trying another account")
	raw, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := raw.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, "failover", events[0].Kind)
}

func TestGrokImageQualityPreservedForJSONAndMultipart(t *testing.T) {
	for _, quality := range []string{"low", "medium", "high", "auto"} {
		t.Run(quality, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"model": "grok-imagine-image-2.0", "quality": quality, "prompt": "test", "image": map[string]string{"url": "https://images.invalid/in.png"}})
			require.NoError(t, err)
			for _, endpoint := range []GrokMediaEndpoint{GrokMediaEndpointImagesGenerations, GrokMediaEndpointImagesEdits} {
				out, ct, err := prepareGrokMediaForwardBody(endpoint, body, "application/json")
				require.NoError(t, err)
				out, _, err = sanitizeGrokMediaForwardBody(endpoint, out, ct)
				require.NoError(t, err)
				require.Equal(t, quality, gjson.GetBytes(out, "quality").String())
			}
			var buf bytes.Buffer
			writer := multipart.NewWriter(&buf)
			require.NoError(t, writer.WriteField("model", "grok-imagine-image-2.0"))
			require.NoError(t, writer.WriteField("quality", quality))
			require.NoError(t, writer.WriteField("image", "https://images.invalid/in.png"))
			require.NoError(t, writer.Close())
			out, _, err := prepareGrokMediaForwardBody(GrokMediaEndpointImagesEdits, buf.Bytes(), writer.FormDataContentType())
			require.NoError(t, err)
			require.Equal(t, quality, gjson.GetBytes(out, "quality").String(), "multipart edits must not silently drop quality")
		})
	}
}
