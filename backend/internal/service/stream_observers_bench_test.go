package service

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ssefixture"
	"github.com/gin-gonic/gin"
)

// End-to-end cost of the per-line response observers inside the stream
// handlers: a 1000-event synthetic upstream stream is read, observed and
// written to an in-memory recorder, so the numbers are CPU and allocations of
// the gateway itself, without network.

func streamBenchContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c, rec
}

func streamBenchService() *OpenAIGatewayService {
	return &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
}

func BenchmarkRawChatCompletionsStream(b *testing.B) {
	stream := ssefixture.Join(ssefixture.ChatStream(1000))
	for name, requestBodyLen := range map[string]int{
		"1000_events":               0,
		"1000_events_large_request": 128 << 10,
	} {
		b.Run(name, func(b *testing.B) {
			svc := streamBenchService()
			b.SetBytes(int64(len(stream)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				c, _ := streamBenchContext()
				resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(stream))}
				if _, err := svc.streamRawChatCompletions(c, resp, &Account{}, "m", "m", "m", nil, nil, time.Now(), requestBodyLen); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkResponsesStream(b *testing.B) {
	for name, frames := range map[string][][]byte{
		"1000_events":     ssefixture.ResponsesStream(1000),
		"echo_300_events": ssefixture.ResponsesStreamWithEcho(300, 20, 2000),
	} {
		stream := ssefixture.Join(frames)
		b.Run(name, func(b *testing.B) {
			svc := streamBenchService()
			b.SetBytes(int64(len(stream)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				c, _ := streamBenchContext()
				resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(stream))}
				if _, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "m", "m"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
