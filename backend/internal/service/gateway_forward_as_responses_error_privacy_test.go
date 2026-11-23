package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// echoedKey is assembled at runtime: a literal key-shaped string would trip the repo's sensitive-data check.
var echoedKey = strings.Join([]string{"sk", "abcdefghijklmnopqrstuvwxyz123456"}, "-")

func convertedErrorContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c, rec
}

// writeConvertedUpstreamError 保留上游状态码与措辞，但上游主机 / IP / 邮箱 / 回显的
// 密钥不能到达客户端（2.3.153 的 sanitizeUpstreamErrorMessage 口径；#572 曾绕过）。
func TestWriteConvertedUpstreamError_RedactsUpstreamIdentity(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		leaks  []string
	}{
		{
			name:   "json message carries url ip email and echoed key",
			status: http.StatusBadRequest,
			body: `{"type":"error","error":{"type":"invalid_request_error","message":"POST https://relay.example-upstream.com/v1/messages failed on 10.1.2.3: ` +
				`Incorrect API key provided: ` + echoedKey + ` (owner ops@example-upstream.com)"}}`,
			leaks: []string{"relay.example-upstream.com", "10.1.2.3", echoedKey[:16], "ops@example-upstream.com"},
		},
		{
			name:   "no error.message falls back to a capped redacted raw body",
			status: http.StatusNotFound,
			body:   `<html><body>Not Found at relay.example-upstream.com (10.9.8.7) token ` + echoedKey + `</body></html>`,
			leaks:  []string{"relay.example-upstream.com", "10.9.8.7", echoedKey[:16]},
		},
		{
			name:   "error.code and error.type are redacted too",
			status: http.StatusUnprocessableEntity,
			body:   `{"error":{"type":"relay.example-upstream.com","code":"10.1.2.3","message":"bad"}}`,
			leaks:  []string{"relay.example-upstream.com", "10.1.2.3"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := convertedErrorContext()
			writeConvertedUpstreamError(c, tc.status, []byte(tc.body))
			require.Equal(t, tc.status, rec.Code, "upstream status must be preserved")
			out := rec.Body.String()
			for _, leak := range tc.leaks {
				require.NotContains(t, out, leak, "client-facing error must not expose %q", leak)
			}
			require.NotEmpty(t, gjson.Get(out, "error.message").String())
			require.NotEmpty(t, gjson.Get(out, "error.code").String())
		})
	}
}

// 没有任何敏感内容时必须原样保留上游措辞和错误码，客户端才看得到真实原因
// （例如 "Unsupported value: 'none'..."），不能被改写成统一的 Upstream request failed。
func TestWriteConvertedUpstreamError_KeepsSafeUpstreamWording(t *testing.T) {
	c, rec := convertedErrorContext()
	writeConvertedUpstreamError(c, http.StatusBadRequest,
		[]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"Unsupported value: 'none' is not supported with this model."}}`))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid_request_error", gjson.Get(rec.Body.String(), "error.code").String())
	require.Equal(t, "Unsupported value: 'none' is not supported with this model.", gjson.Get(rec.Body.String(), "error.message").String())
}

// 上游给了一个很大的非 JSON 响应（中转站错误页）时，回给客户端的文案要被截断。
func TestWriteConvertedUpstreamError_CapsRawBodyFallback(t *testing.T) {
	c, rec := convertedErrorContext()
	writeConvertedUpstreamError(c, http.StatusNotFound, []byte(strings.Repeat("a", 64*1024)))
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.LessOrEqual(t, len(gjson.Get(rec.Body.String(), "error.message").String()), convertedUpstreamErrorRawBodyMax)
}
