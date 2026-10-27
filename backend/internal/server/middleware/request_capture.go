package middleware

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/requestcapture"
	"github.com/gin-gonic/gin"
)

// RequestCapture runs after authentication and before model/body rewriting.
func RequestCapture(manager *requestcapture.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if manager == nil {
			c.Next()
			return
		}
		key, ok := GetAPIKeyFromContext(c)
		if !ok || key == nil {
			c.Next()
			return
		}
		group := int64(0)
		if key.GroupID != nil {
			group = *key.GroupID
		}
		rid, _ := c.Request.Context().Value(ctxkey.ClientRequestID).(string)
		session := manager.Begin(requestcapture.Meta{RequestID: rid, ClientRequestID: c.GetHeader("X-Client-Request-ID"), SessionID: c.GetHeader("Session-Id"), ThreadID: c.GetHeader("Thread-Id"), UserID: key.UserID, GroupID: group, Method: c.Request.Method, Path: c.Request.URL.RequestURI(), Protocol: "http"})
		if session == nil {
			c.Next()
			return
		}
		c.Request = c.Request.WithContext(requestcapture.WithSession(c.Request.Context(), session))
		var inputStream *requestcapture.Stream
		if c.Request.Body != nil && !strings.EqualFold(c.GetHeader("Upgrade"), "websocket") {
			inputStream = session.NewStream("client_request", 0, 0, c.GetHeader("Content-Type"), c.Request.Header)
			c.Request.Body = requestcapture.ObserveBody(c.Request.Body, inputStream)
		}
		writer := &captureResponseWriter{ResponseWriter: c.Writer, session: session}
		c.Writer = writer
		defer func() {
			if inputStream != nil {
				_ = inputStream.Close()
			}
			if key, ok := GetAPIKeyFromContext(c); ok && key.GroupID != nil {
				session.SetRoutedGroup(*key.GroupID)
			}
			if writer.stream != nil {
				_ = writer.stream.Close()
			}
			if c.Request.Method == http.MethodPost && !strings.EqualFold(c.GetHeader("Upgrade"), "websocket") {
				session.RequireClientInput()
			}
			session.Finish(c.Writer.Status())
		}()
		c.Next()
	}
}

type captureResponseWriter struct {
	gin.ResponseWriter
	session *requestcapture.Session
	stream  *requestcapture.Stream
}

func (w *captureResponseWriter) observe(p []byte) {
	if strings.EqualFold(w.Header().Get("Upgrade"), "websocket") {
		return
	}
	if w.stream == nil {
		w.stream = w.session.NewStream("client_response", 0, 0, w.Header().Get("Content-Type"), w.Header())
	}
	_, _ = w.stream.Write(p)
}
func (w *captureResponseWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if n > 0 {
		w.observe(p[:n])
	}
	if err != nil {
		w.session.MarkPartial("client_write_failed")
		w.session.MarkError("client_write_failed")
	}
	return n, err
}
func (w *captureResponseWriter) WriteString(p string) (int, error) { return w.Write([]byte(p)) }

var _ http.Flusher = (*captureResponseWriter)(nil)

func (w *captureResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *captureResponseWriter) FlushError() error {
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if err != nil {
		w.session.MarkError("client_flush_failed")
		w.session.MarkPartial("client_flush_failed")
	}
	return err
}
func (w *captureResponseWriter) Flush() { _ = w.FlushError() }
