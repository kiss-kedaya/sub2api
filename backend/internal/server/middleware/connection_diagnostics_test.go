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

func TestConnectionDiagnosticsRecordsFlushedResponsesText(t *testing.T) {
	gin.SetMode(gin.TestMode)
	text := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"
	for _, tc := range []struct {
		name  string
		body  string
		flush bool
		want  bool
	}{
		{"text", text, true, true},
		{"unflushed text", text, false, false},
		{"incomplete SSE frame", strings.TrimSuffix(text, "\n"), true, false},
		{"heartbeat", ": keepalive\n\n", true, false},
		{"empty text", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"\"}\n\n", true, false},
		{"tool arguments", "data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"hello\"}\n\n", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			record := &diagnosticRecorder{}
			r.Use(RequestTiming(record))
			r.POST("/v1/responses", func(c *gin.Context) {
				c.Header("Content-Type", "text/event-stream")
				_, _ = c.Writer.WriteString(": keepalive\n\n")
				c.Writer.Flush()
				_, _ = c.Writer.Write([]byte(tc.body[:len(tc.body)/2]))
				_, _ = c.Writer.WriteString(tc.body[len(tc.body)/2:])
				if tc.flush {
					c.Writer.Flush()
				}
			})
			r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
			if len(record.rows) != 1 {
				t.Fatalf("record count = %d, want 1", len(record.rows))
			}
			events := record.rows[0].Detail.Events
			if _, ok := events["first_output_flush"]; ok != tc.want {
				t.Fatalf("first_output_flush present = %t, want %t", ok, tc.want)
			}
		})
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
