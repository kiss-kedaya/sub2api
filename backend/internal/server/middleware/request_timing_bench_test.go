//go:build unit

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ssefixture"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type noopTimingRecorder struct{}

func (noopTimingRecorder) RecordConnectionTrace(service.ConnectionTrace) {}

type timingDiscardHTTPWriter struct {
	header http.Header
}

func (d *timingDiscardHTTPWriter) Header() http.Header         { return d.header }
func (d *timingDiscardHTTPWriter) Write(b []byte) (int, error) { return len(b), nil }
func (d *timingDiscardHTTPWriter) WriteHeader(int)             {}
func (d *timingDiscardHTTPWriter) Flush()                      {}

func BenchmarkTimingWriterWriteString(b *testing.B) {
	prevMode := gin.Mode()
	gin.SetMode(gin.ReleaseMode)
	b.Cleanup(func() { gin.SetMode(prevMode) })

	for _, tc := range []struct {
		name     string
		frames   [][]byte
		recorder ConnectionTraceRecorder
	}{
		{"responses_1000/with_recorder", ssefixture.ResponsesStream(1000), noopTimingRecorder{}},
		{"responses_1000/no_recorder", ssefixture.ResponsesStream(1000), nil},
		{"chat_1000/with_recorder", ssefixture.ChatStream(1000), noopTimingRecorder{}},
		{"chat_1000/no_recorder", ssefixture.ChatStream(1000), nil},
	} {
		rawLines := ssefixture.Lines(tc.frames)
		lines := make([]string, len(rawLines))
		total := 0
		for i, line := range rawLines {
			lines[i] = line + "\n"
			total += len(lines[i])
		}
		router := gin.New()
		if tc.recorder != nil {
			router.Use(RequestTiming(tc.recorder))
		} else {
			router.Use(RequestTiming())
		}
		router.POST("/v1/responses", func(c *gin.Context) {
			c.Header("Content-Type", "text/event-stream")
			for _, line := range lines {
				_, _ = c.Writer.WriteString(line)
			}
			c.Writer.Flush()
		})
		dw := &timingDiscardHTTPWriter{header: make(http.Header)}
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		b.Run(tc.name, func(b *testing.B) {
			b.SetBytes(int64(total))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				router.ServeHTTP(dw, req)
			}
		})
	}
}
