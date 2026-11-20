package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type diagnosticRecorder struct{ rows []service.ConnectionTrace }

func (r *diagnosticRecorder) RecordConnectionTrace(v service.ConnectionTrace) {
	r.rows = append(r.rows, v)
}

func TestConnectionDiagnosticsGeminiGeneration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name            string
		path            string
		wantPath        string
		stream          bool
		auth            bool
		upstreamStarted bool
	}{
		{"native stream", "/v1beta/models/route-value:streamGenerateContent?alt=sse&key=query-value", "/v1beta/models/:model:streamGenerateContent", true, true, true},
		{"native JSON slash alias", "/v1beta/models/route-value/generateContent", "/v1beta/models/:model:generateContent", false, true, true},
		{"Antigravity stream before dispatch", "/antigravity/v1beta/models/route-value:streamGenerateContent", "/antigravity/v1beta/models/:model:streamGenerateContent", true, true, false},
		{"unrelated action", "/v1beta/models/route-value:countTokens", "", false, true, false},
		{"rejected key", "/v1beta/models/route-value:streamGenerateContent", "", true, false, false},
		{"unmatched route", "/other/models/route-value:streamGenerateContent", "", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			record := &diagnosticRecorder{}
			r.Use(RequestTiming(record))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			handler := func(c *gin.Context) {
				if !tc.auth {
					c.Status(http.StatusUnauthorized)
					return
				}
				c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: 11, UserID: 21})
				requesttiming.Observe(c.Request.Context(), "gemini_user_slot")()
				requesttiming.Observe(c.Request.Context(), "gemini_account_selection")()
				if tc.upstreamStarted {
					up := httptest.NewRequest(http.MethodPost, "https://example.invalid/v1beta/models/test:generateContent", nil).WithContext(c.Request.Context())
					_, attempt := requesttiming.StartAttempt(up, 15, 0)
					attempt.Response(nil, context.Canceled)
				}
				cancel()
				c.Status(499)
			}
			r.POST("/v1beta/models/*modelAction", handler)
			r.POST("/antigravity/v1beta/models/*modelAction", handler)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, tc.path, nil).WithContext(ctx))
			if tc.wantPath == "" {
				if len(record.rows) != 0 {
					t.Fatalf("unexpected trace count = %d", len(record.rows))
				}
				return
			}
			if w.Code != 499 || len(record.rows) != 1 {
				t.Fatalf("HTTP=%d records=%d", w.Code, len(record.rows))
			}
			trace := record.rows[0]
			if trace.Path != tc.wantPath || trace.Stream != tc.stream || trace.APIKeyID != 11 || trace.UserID != 21 {
				t.Fatalf("incorrect Gemini trace metadata: %#v", trace)
			}
			d := trace.Detail
			if !d.Canceled || d.DownstreamBytes != 0 || len(d.Spans) != 2 || d.Spans[0].Name != "gemini_user_slot" || d.Spans[1].Name != "gemini_account_selection" {
				t.Fatalf("incorrect recorded stages: %#v", d)
			}
			if tc.upstreamStarted {
				if len(d.Attempts) != 1 || d.Attempts[0].Status != 0 || d.Attempts[0].Error != "canceled" {
					t.Fatalf("expected cancellation before upstream headers, got %#v", d.Attempts)
				}
			} else if len(d.Attempts) != 0 {
				t.Fatalf("unexpected upstream attempt: %#v", d.Attempts)
			}
		})
	}
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
