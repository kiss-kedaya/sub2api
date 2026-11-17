package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClientErrorPrivacyRelayFingerprintKeepsOpsEvidence(t *testing.T) {
	const message = "bad input (request id: Synthetic123)"
	const original = `{"error":{"type":"new_api_error","message":"bad input (request id: Synthetic123)","code":"invalid_request"}}`
	for _, path := range []string{"/v1/chat/completions", "/v1/messages", "/v1/responses"} {
		for _, stream := range []bool{false, true} {
			t.Run(path+map[bool]string{false: "/json", true: "/sse"}[stream], func(t *testing.T) {
				setupOpsErrorLogTestQueue(t, 4)
				repo := &ingressRejectOpsRepo{}
				ops := service.NewOpsService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
				router := gin.New()
				router.Use(OpsErrorLoggerMiddleware(ops))
				router.POST(path, func(c *gin.Context) {
					service.SetOpsUpstreamError(c, http.StatusBadRequest, message, original)
					if path == "/v1/messages" {
						(&GatewayHandler{}).handleStreamingAwareError(c, http.StatusBadRequest, "invalid_request_error", message, stream)
					} else {
						(&OpenAIGatewayHandler{}).handleStreamingAwareError(c, http.StatusBadRequest, "invalid_request_error", message, stream)
					}
				})
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
				require.NotContains(t, rec.Body.String(), "request id:")
				require.Contains(t, rec.Body.String(), "bad input")
				if !stream {
					require.Equal(t, http.StatusBadRequest, rec.Code)
				}
				require.Equal(t, int64(1), OpsErrorLogQueueLength())
				job := <-opsErrorLogQueue
				require.NotNil(t, job.entry.UpstreamErrorMessage)
				require.Equal(t, message, *job.entry.UpstreamErrorMessage)
				require.NotNil(t, job.entry.UpstreamErrorDetail)
				require.JSONEq(t, original, *job.entry.UpstreamErrorDetail)
				flushOpsErrorLogBatch([]opsErrorLogJob{job})
				require.Len(t, repo.entries, 1)
				require.JSONEq(t, original, *repo.entries[0].UpstreamErrorDetail)
				require.Equal(t, message, *repo.entries[0].UpstreamErrorMessage)
			})
		}
	}
}

func TestClientErrorPrivacyStreamMarkerKeepsRawMessage(t *testing.T) {
	const message = "bad input (request id: Synthetic123)"
	setupOpsErrorLogTestQueue(t, 2)
	ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.Use(OpsErrorLoggerMiddleware(ops))
	router.POST("/v1/responses", func(c *gin.Context) {
		(&OpenAIGatewayHandler{}).handleStreamingAwareError(c, http.StatusBadRequest, "invalid_request_error", message, true)
	})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	require.NotContains(t, rec.Body.String(), "request id:")
	require.Equal(t, int64(1), OpsErrorLogQueueLength())
	job := <-opsErrorLogQueue
	require.NotNil(t, job.entry.UpstreamErrorMessage)
	require.Equal(t, message, *job.entry.UpstreamErrorMessage)
}

func TestClientErrorPrivacyStreamStatusUsesRawMarker(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 2)
	ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.Use(OpsErrorLoggerMiddleware(ops))
	router.POST("/v1/responses", func(c *gin.Context) {
		(&OpenAIGatewayHandler{}).handleStreamingAwareError(c, http.StatusServiceUnavailable, "upstream_error", "temporarily busy (request id: Synthetic123)", true)
	})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, int64(1), OpsErrorLogQueueLength())
	job := <-opsErrorLogQueue
	require.Equal(t, http.StatusServiceUnavailable, job.entry.StatusCode)
}
