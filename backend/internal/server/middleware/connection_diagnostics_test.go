package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type diagnosticRecorder struct{ rows []service.ConnectionTrace }

func (r *diagnosticRecorder) RecordConnectionTrace(v service.ConnectionTrace) {
	r.rows = append(r.rows, v)
}

func TestConnectionDiagnosticsSeparatesHTTP200FromFailedSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	record := &diagnosticRecorder{}
	r.Use(Recovery(), RequestTiming(record))
	r.POST("/v1/responses", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.WriteString("data: {\"type\":\"error\",\"error\":{\"message\":\"failed\"}}\n\n")
		c.Writer.Flush()
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader("{}")))
	if w.Code != 200 || len(record.rows) != 1 {
		t.Fatalf("HTTP=%d records=%d", w.Code, len(record.rows))
	}
	if v := record.rows[0].Detail; v.Status != 200 || v.Terminal != "failed" {
		t.Fatalf("incorrect outcome: %#v", v)
	}
}

func TestConnectionDiagnosticsRecordsRecoveredPanicStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	record := &diagnosticRecorder{}
	r.Use(Recovery(), RequestTiming(record))
	r.POST("/v1/responses", func(c *gin.Context) { panic("fixture") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader("{}")))
	if w.Code != 500 || len(record.rows) != 1 || record.rows[0].Detail.Status != 500 {
		t.Fatalf("HTTP=%d records=%#v", w.Code, record.rows)
	}
}
