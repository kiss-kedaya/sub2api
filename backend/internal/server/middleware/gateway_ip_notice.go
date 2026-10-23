package middleware

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	coremiddleware "github.com/Wei-Shaw/sub2api/internal/middleware"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ipnotice"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/tidwall/gjson"
)

// GatewayIPNotice applies only to explicitly selected client IPs after normal
// authentication, before routing, billing and upstream requests.
func GatewayIPNotice(settings *service.SettingService, redisClient *redis.Client) gin.HandlerFunc {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := settings.RefreshGatewayIPNotice(ctx); err != nil {
		slog.Warn("gateway IP notice startup refresh failed", "error", err)
	}
	var limiter service.GatewayIPNoticeAllower
	if redisClient != nil {
		limiter = coremiddleware.NewRateLimiter(redisClient)
	}
	return gatewayIPNoticeWithLimiter(settings, limiter)
}

func gatewayIPNoticeWithLimiter(settings *service.SettingService, limiter service.GatewayIPNoticeAllower) gin.HandlerFunc {
	return func(c *gin.Context) {
		policy := settings.GatewayIPNoticeSnapshot()
		clientIP, matched := policy.Match(SecurityClientIP(c))
		if !matched {
			c.Next()
			return
		}
		session := &service.GatewayIPNoticeSession{IP: clientIP, Message: policy.Message, RPM: policy.RPM, Limiter: limiter}
		result, err := session.Allow(c.Request.Context())
		if err != nil || !result.Allowed {
			if result.RetryAfter > 0 {
				c.Header("Retry-After", strconv.FormatInt(int64((result.RetryAfter+time.Second-1)/time.Second), 10))
			}
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": gin.H{
				"type": "ip_notice_rate_limit", "code": "ip_notice_rate_limit", "message": policy.Message,
			}})
			return
		}
		service.SetGatewayIPNoticeSession(c, session)
		protocol := ipnotice.ProtocolForPath(c.Request.URL.Path)
		if c.Request.Method == http.MethodPost && protocol != "" && c.Request.Body != nil {
			body, readErr := pkghttputil.ReadLenientJSONRequestBodyWithPrealloc(c.Request, 64<<20)
			closeErr := c.Request.Body.Close()
			if readErr != nil || closeErr != nil {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "Failed to read request body"}})
				return
			}
			body, err = ipnotice.InjectRequest(body, protocol, policy.Message)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "Failed to add IP notice"}})
				return
			}
			c.Request.Body = io.NopCloser(bytes.NewReader(body))
			c.Request.ContentLength = int64(len(body))
			c.Request.Header.Del("Content-Length")
		}
		if protocol == "" || c.Request.Method != http.MethodPost {
			c.Next()
			return
		}
		writer := newGatewayIPNoticeWriter(c.Writer, protocol, policy.Message)
		c.Writer = writer
		defer func() { c.Writer = writer.ResponseWriter }()
		c.Next()
		if err := writer.finish(); err != nil {
			slog.Debug("gateway IP notice client write failed", "error", err)
		}
	}
}

type gatewayIPNoticeWriter struct {
	gin.ResponseWriter
	protocol ipnotice.Protocol
	message  string
	stream   *ipnotice.EventTransformer
	pending  []byte
	err      error
	bypass   bool
}

const maxGatewayIPNoticeBuffer = 16 << 20

func newGatewayIPNoticeWriter(inner gin.ResponseWriter, protocol ipnotice.Protocol, message string) *gatewayIPNoticeWriter {
	return &gatewayIPNoticeWriter{ResponseWriter: inner, protocol: protocol, message: message, stream: ipnotice.NewEventTransformer(protocol, message)}
}

func (w *gatewayIPNoticeWriter) isSSE() bool {
	return strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream")
}

func (w *gatewayIPNoticeWriter) WriteHeaderNow() {
	w.Header().Del("Content-Length")
	if w.isSSE() || w.Status() >= 400 || w.bypass {
		w.ResponseWriter.WriteHeaderNow()
	}
}

func (w *gatewayIPNoticeWriter) Write(body []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if w.Status() >= 400 || w.bypass || (!w.isSSE() && !strings.Contains(w.Header().Get("Content-Type"), "json")) {
		return w.ResponseWriter.Write(body)
	}
	w.Header().Del("Content-Length")
	w.Header().Del("ETag")
	w.pending = append(w.pending, body...)
	if w.isSSE() {
		w.err = w.drainSSE()
	}
	if len(w.pending) > maxGatewayIPNoticeBuffer {
		// Bound memory even on malformed/truncated events or huge JSON bodies.
		w.bypass = true
		_, w.err = w.ResponseWriter.Write(w.pending)
		w.pending = nil
	}
	if w.err != nil {
		return 0, w.err
	}
	return len(body), nil
}

func (w *gatewayIPNoticeWriter) WriteString(body string) (int, error) { return w.Write([]byte(body)) }

func (w *gatewayIPNoticeWriter) Written() bool {
	return len(w.pending) > 0 || w.ResponseWriter.Written()
}

func (w *gatewayIPNoticeWriter) Size() int {
	size := w.ResponseWriter.Size()
	if size < 0 && len(w.pending) > 0 {
		size = 0
	}
	return size + len(w.pending)
}

func (w *gatewayIPNoticeWriter) Flush() {
	if !w.isSSE() {
		w.err = w.finish()
		w.bypass = true
	}
	w.ResponseWriter.Flush()
}

func (w *gatewayIPNoticeWriter) finish() error {
	if w.err != nil {
		return w.err
	}
	if len(w.pending) == 0 {
		return nil
	}
	body := w.pending
	w.pending = nil
	if !w.isSSE() && w.Status() < 400 {
		body = ipnotice.TransformJSON(body, w.protocol, w.message)
	}
	w.Header().Del("Content-Length")
	_, err := w.ResponseWriter.Write(body)
	return err
}

func (w *gatewayIPNoticeWriter) drainSSE() error {
	for {
		end, delimiter := bytes.Index(w.pending, []byte("\n\n")), 2
		if crlf := bytes.Index(w.pending, []byte("\r\n\r\n")); crlf >= 0 && (end < 0 || crlf < end) {
			end, delimiter = crlf, 4
		}
		if end < 0 {
			return nil
		}
		raw := w.pending[:end+delimiter]
		var data []byte
		var metadata []string
		for _, line := range strings.Split(strings.ReplaceAll(string(raw[:end]), "\r\n", "\n"), "\n") {
			if strings.HasPrefix(line, "data:") {
				if len(data) > 0 {
					data = append(data, '\n')
				}
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")...)
			} else if !strings.HasPrefix(line, "event:") {
				metadata = append(metadata, line)
			}
		}
		frames := w.stream.Transform(data)
		if len(frames) == 1 && bytes.Equal(frames[0], data) {
			if _, err := w.ResponseWriter.Write(raw); err != nil {
				return err
			}
		} else {
			for i, frame := range frames {
				var out strings.Builder
				if i == 0 {
					for _, line := range metadata {
						_, _ = out.WriteString(line + "\n")
					}
				}
				if w.protocol == ipnotice.Anthropic || w.protocol == ipnotice.Responses {
					_, _ = out.WriteString("event: " + gjson.GetBytes(frame, "type").String() + "\n")
				}
				_, _ = out.WriteString("data: " + string(frame) + "\n\n")
				if _, err := w.ResponseWriter.WriteString(out.String()); err != nil {
					return err
				}
			}
		}
		w.pending = w.pending[end+delimiter:]
	}
}
