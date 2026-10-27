// Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later.
package middleware

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type TrafficRecorder interface{ RecordTraffic(service.TrafficEvent) }

const trafficRecoveryFinishKey = "sub2api.user_traffic.recovery_finish"

// Recovery calls this after its error response has passed through the writer.
func finishTrafficAfterRecovery(c *gin.Context) {
	if value, ok := c.Get(trafficRecoveryFinishKey); ok {
		c.Set(trafficRecoveryFinishKey, nil)
		if finish, ok := value.(func()); ok {
			finish()
		}
	}
}

// UserTraffic must precede auth/body pre-reading on gateway routes. Only a
// successfully authenticated identity is attributed, including later failures.
func UserTraffic(recorder TrafficRecorder) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only the external ingress meters a serverless forwarded request. This
		// flag is set by verified HMAC/replay-protected ingress, never a header.
		path := c.FullPath()
		live := strings.Contains(path, "/live") || path == "/backend-api/codex/realtime/calls" || path == "/backend-api/codex/:call_id"
		if recorder == nil || c.GetString("serverless_verified_ip") != "" || live {
			c.Next()
			return
		}
		started := time.Now()
		body := &trafficBody{ReadCloser: c.Request.Body, expected: c.Request.ContentLength}
		if c.Request.Body != nil {
			c.Request.Body = body
		}
		writer := &trafficWriter{ResponseWriter: c.Writer}
		c.Writer = writer
		finish := func(panicked bool) {
			key, ok := GetAPIKeyFromContext(c)
			if !ok || key == nil || key.UserID <= 0 || writer.Status() == http.StatusSwitchingProtocols || writer.hijacked.Load() {
				return
			}
			outcome := "completed"
			if writer.Status() >= 400 {
				outcome = "http_error"
			}
			if c.Request.Context().Err() != nil {
				outcome = "canceled"
			}
			if writer.failed.Load() {
				outcome = "write_error"
			}
			status := writer.Status()
			if panicked {
				outcome = "panic"
				if !writer.Written() {
					status = http.StatusInternalServerError
				}
			}
			// UUID allocation, encoding and persistence belong to the worker.
			e := service.TrafficEvent{UserID: key.UserID, APIKeyID: key.ID,
				Endpoint: connectionMetadata(c.FullPath(), 256), Method: c.Request.Method, StartedAt: started, FinishedAt: time.Now(),
				UploadBytes: body.bytes.Load(), DownloadBytes: writer.bytes.Load(), StatusCode: status, Outcome: outcome,
				RequestComplete: body.complete.Load() || body.expected == 0, MeasurementVersion: 1}
			if key.GroupID != nil {
				e.GroupID = *key.GroupID
			}
			e.RequestID, _ = c.Request.Context().Value(ctxkey.RequestID).(string)
			recorder.RecordTraffic(e)
		}
		defer func() {
			panicked := recover()
			if panicked != nil {
				defer func() { panic(panicked) }()
				if c.GetBool(recoveryActiveKey) {
					c.Set(trafficRecoveryFinishKey, func() { finish(true) })
					return
				}
			}
			finish(panicked != nil)
		}()
		c.Next()
	}
}

type trafficBody struct {
	io.ReadCloser
	expected int64
	bytes    atomic.Int64
	complete atomic.Bool
}

func (b *trafficBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	total := b.bytes.Add(int64(n))
	if err == io.EOF || (b.expected >= 0 && total >= b.expected) {
		b.complete.Store(true)
	}
	return n, err
}

type trafficWriter struct {
	gin.ResponseWriter
	bytes    atomic.Int64
	failed   atomic.Bool
	hijacked atomic.Bool
}

func (w *trafficWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.observe(n, err)
	return n, err
}
func (w *trafficWriter) WriteString(s string) (int, error) {
	n, err := w.ResponseWriter.WriteString(s)
	w.observe(n, err)
	return n, err
}
func (w *trafficWriter) observe(n int, err error) {
	w.bytes.Add(int64(n))
	if err != nil {
		w.failed.Store(true)
	}
}
func (w *trafficWriter) Flush() { _ = w.FlushError() }
func (w *trafficWriter) FlushError() error {
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if err != nil {
		w.failed.Store(true)
	}
	return err
}
func (w *trafficWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *trafficWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := w.ResponseWriter.Hijack()
	if err == nil {
		w.hijacked.Store(true)
	}
	return conn, rw, err
}
