package requestcapture

import (
	"crypto/tls"
	"net/http"
	"net/http/httptrace"
	"sync"
)

// Stages reflect transport callbacks, not captured body length. An unread or
// truncated capture alone cannot establish whether a request was sent.
type captureHTTPTrace struct {
	mu    sync.Mutex
	phase string
}

func (t *captureHTTPTrace) set(phase string) { t.mu.Lock(); t.phase = phase; t.mu.Unlock() }
func (t *captureHTTPTrace) stage() string    { t.mu.Lock(); defer t.mu.Unlock(); return t.phase }
func traceCaptureRequest(req *http.Request) *captureHTTPTrace {
	t := &captureHTTPTrace{phase: "transport"}
	trace := &httptrace.ClientTrace{
		GetConn:  func(string) { t.set("connect") },
		DNSStart: func(httptrace.DNSStartInfo) { t.set("dns") },
		DNSDone: func(i httptrace.DNSDoneInfo) {
			if i.Err == nil {
				t.set("connect")
			}
		},
		TLSHandshakeStart: func() { t.set("tls") },
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			if err == nil {
				t.set("connection_ready")
			}
		},
		GotConn: func(httptrace.GotConnInfo) { t.set("request_write") },
		WroteRequest: func(i httptrace.WroteRequestInfo) {
			if i.Err == nil {
				t.set("response_headers")
			}
		},
	}
	*req = *req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	return t
}
