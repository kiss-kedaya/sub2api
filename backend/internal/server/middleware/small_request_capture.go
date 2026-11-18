package middleware

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/requestcapture"
	"github.com/gin-gonic/gin"
)

func beginSmallRequestCapture(c *gin.Context, r *requestcapture.SmallRecorder) func() {
	if !requestcapture.SmallTextEndpoint(c.Request.Method, c.Request.URL.Path) || strings.EqualFold(c.GetHeader("Upgrade"), "websocket") || !strings.Contains(strings.ToLower(c.GetHeader("Content-Type")), "application/json") {
		return nil
	}
	key, ok := GetAPIKeyFromContext(c)
	if !ok || key == nil {
		return nil
	}
	var group int64
	if key.GroupID != nil {
		group = *key.GroupID
	}
	h := r.Begin(c.Request.Method, c.Request.URL.RequestURI(), key.UserID, key.ID, group)
	if h == nil {
		return nil
	}
	c.Request = c.Request.WithContext(requestcapture.WithSmallHandle(c.Request.Context(), h))
	writer := &smallCaptureWriter{ResponseWriter: c.Writer, handle: h}
	c.Writer = writer
	return func() { h.Finish(c.Writer.Status(), c.Writer.Header(), writer.partial) }
}

type smallCaptureWriter struct {
	gin.ResponseWriter
	handle  *requestcapture.SmallHandle
	partial bool
}

func (w *smallCaptureWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if n > 0 {
		w.handle.Response(p[:n], w.Header())
	}
	if err != nil || n < len(p) {
		w.partial = true
	}
	return n, err
}
func (w *smallCaptureWriter) WriteString(p string) (int, error) { return w.Write([]byte(p)) }
func (w *smallCaptureWriter) Unwrap() http.ResponseWriter       { return w.ResponseWriter }
func (w *smallCaptureWriter) FlushError() error {
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if err != nil {
		w.partial = true
	}
	return err
}
func (w *smallCaptureWriter) Flush() { _ = w.FlushError() }
