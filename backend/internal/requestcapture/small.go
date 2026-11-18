package requestcapture

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	SmallRequestLimit     = 16 << 10
	SmallResponseLimit    = 256 << 10
	SmallHeaderLimit      = 32 << 10
	SmallActiveBytes      = 64 << 20
	SmallActiveLimit      = 8192
	SmallQueueLimit       = 1024
	SmallStoredBytesLimit = 4 << 30
	SmallResponseSamples  = 5
	SmallRecentRequestIDs = 20
)

var smallSkipReasons = [...]string{"request_too_large", "response_too_large", "headers_too_large", "active_limit", "queue_full", "usage_unknown", "input_not_small", "unsupported_body", "storage_error", "expired", "disabled", "quota_full"}

type SmallUsage struct {
	RequestID           string `json:"request_id"`
	UserID              int64  `json:"user_id"`
	APIKeyID            int64  `json:"api_key_id"`
	GroupID             int64  `json:"group_id"`
	AccountID           int64  `json:"account_id"`
	Platform            string `json:"platform"`
	Model               string `json:"model"`
	UpstreamModel       string `json:"upstream_model"`
	Stream              bool   `json:"stream"`
	InputTokens         int    `json:"input_tokens"`
	OutputTokens        int    `json:"output_tokens"`
	CacheReadTokens     int    `json:"cache_read_tokens"`
	CacheCreationTokens int    `json:"cache_creation_tokens"`
	ActualCost          string `json:"actual_cost"`
	CalculatedCost      string `json:"calculated_cost"`
	Settlement          string `json:"settlement"`
	DurationMs          int    `json:"duration_ms"`
}

type SmallExchange struct {
	StartedAt                       time.Time
	Method, Path                    string
	UserID, APIKeyID, GroupID       int64
	RequestHeaders, ResponseHeaders http.Header
	RequestBody, ResponseBody       []byte
	Status                          int
	Usage                           SmallUsage
	Partial                         bool
}

// SmallRecorder bounds retained body capacity and header bytes, including pending
// billing and queued work. Go object overhead is bounded by SmallActiveLimit.
// No disk spool or synchronous persistence fallback is allowed.
type SmallRecorder struct {
	Store            *SmallStore
	enabled          atomic.Bool
	ready            atomic.Bool
	active           atomic.Int64
	activeBytes      atomic.Int64
	quotaFull        atomic.Bool
	generation       atomic.Uint64
	queue            chan *SmallHandle
	mu               sync.Mutex
	handles          map[*SmallHandle]struct{}
	skipped          [len(smallSkipReasons)]atomic.Int64
	credentialHeader func(string) bool
	cancel           context.CancelFunc
	done             chan struct{}
	once             sync.Once
	lastCleanup      time.Time
}

type SmallHandle struct {
	recorder                                        *SmallRecorder
	mu                                              sync.Mutex
	exchange                                        SmallExchange
	generation                                      uint64
	bodySeen, finished, usageSeen, queued, released bool
	finishedAt                                      time.Time
	reason                                          string
	bytes                                           int64
}

type smallContextKey struct{}

func SmallFromContext(ctx context.Context) *SmallHandle {
	if ctx == nil {
		return nil
	}
	h, _ := ctx.Value(smallContextKey{}).(*SmallHandle)
	return h
}
func WithSmallHandle(ctx context.Context, h *SmallHandle) context.Context {
	if h == nil {
		return ctx
	}
	return context.WithValue(ctx, smallContextKey{}, h)
}

func NewSmallRecorder(store *SmallStore, credentialHeader func(string) bool) *SmallRecorder {
	ctx, cancel := context.WithCancel(context.Background())
	r := &SmallRecorder{Store: store, queue: make(chan *SmallHandle, SmallQueueLimit), handles: make(map[*SmallHandle]struct{}), credentialHeader: credentialHeader, cancel: cancel, done: make(chan struct{})}
	// No initial quota observation means no permission to add stored payloads.
	r.quotaFull.Store(true)
	go r.run(ctx)
	return r
}
func (r *SmallRecorder) Done() <-chan struct{} { return r.done }
func (r *SmallRecorder) Enabled() bool         { return r != nil && r.enabled.Load() && r.ready.Load() }
func (r *SmallRecorder) Ready() bool           { return r != nil && r.ready.Load() }
func (r *SmallRecorder) QuotaFull() bool       { return r != nil && r.quotaFull.Load() }
func (r *SmallRecorder) ApplyEnabled(enabled bool) {
	if r != nil && r.enabled.Swap(enabled) && !enabled {
		r.Invalidate()
	}
}
func (r *SmallRecorder) Close() {
	if r != nil {
		r.once.Do(func() {
			r.ApplyEnabled(false)
			r.cancel()
			<-r.done
			r.Invalidate()
		})
	}
}
func (r *SmallRecorder) skip(reason string) {
	for i, name := range smallSkipReasons {
		if reason == name {
			r.skipped[i].Add(1)
			return
		}
	}
}

func SmallTextEndpoint(method, path string) bool {
	if method != http.MethodPost {
		return false
	}
	for _, suffix := range []string{"/messages", "/chat/completions", "/responses", ":generateContent", ":streamGenerateContent"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

func (r *SmallRecorder) Begin(method, path string, userID, apiKeyID, groupID int64) *SmallHandle {
	if !r.Enabled() {
		return nil
	}
	if r.QuotaFull() {
		r.skip("quota_full")
		return nil
	}
	if len(path) > SmallHeaderLimit {
		r.skip("headers_too_large")
		return nil
	}
	if r.active.Add(1) > SmallActiveLimit {
		r.active.Add(-1)
		r.skip("active_limit")
		return nil
	}
	h := &SmallHandle{recorder: r, generation: r.generation.Load(), exchange: SmallExchange{StartedAt: time.Now().UTC(), Method: method, Path: path, UserID: userID, APIKeyID: apiKeyID, GroupID: groupID}}
	r.mu.Lock()
	// Invalidate snapshots under the same lock. A racing Begin must either be
	// in that snapshot or observe the new generation before publishing itself.
	if !r.Enabled() || h.generation != r.generation.Load() {
		r.mu.Unlock()
		r.active.Add(-1)
		return nil
	}
	r.handles[h] = struct{}{}
	r.mu.Unlock()
	return h
}

func smallHeaderBytes(header http.Header) int {
	n := 0
	for k, values := range header {
		n += len(k)
		for _, v := range values {
			n += len(v)
		}
	}
	return n
}

// reserveLocked runs before every copy, including the temporary old+new response
// buffers during growth. CAS never lets simultaneous writers exceed the budget.
func (h *SmallHandle) reserveLocked(n int) bool {
	for {
		used := h.recorder.activeBytes.Load()
		if int64(n) > SmallActiveBytes-used {
			h.dropLocked("active_limit")
			return false
		}
		if h.recorder.activeBytes.CompareAndSwap(used, used+int64(n)) {
			h.bytes += int64(n)
			return true
		}
	}
}
func (h *SmallHandle) dropLocked(reason string) {
	if h.reason != "" || h.released {
		return
	}
	h.reason = reason
	h.recorder.skip(reason)
	h.releaseLocked()
}
func (h *SmallHandle) releaseLocked() {
	if h.released {
		return
	}
	h.released = true
	h.exchange = SmallExchange{}
	h.recorder.activeBytes.Add(-h.bytes)
	h.bytes = 0
	h.recorder.active.Add(-1)
	h.recorder.mu.Lock()
	delete(h.recorder.handles, h)
	h.recorder.mu.Unlock()
}

// ClientRequest receives the decoded, unmodified bytes from the shared reader.
// It never parses JSON or retains the handler's large backing array.
func (h *SmallHandle) ClientRequest(body []byte, headers http.Header) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.bodySeen || h.released {
		return
	}
	if h.generation != h.recorder.generation.Load() || !h.recorder.Enabled() {
		h.dropLocked("disabled")
		return
	}
	h.bodySeen = true
	headerBytes := smallHeaderBytes(headers)
	switch {
	case len(body) > SmallRequestLimit:
		h.dropLocked("request_too_large")
	case headerBytes > SmallHeaderLimit:
		h.dropLocked("headers_too_large")
	default:
		if !h.reserveLocked(len(body) + headerBytes) {
			return
		}
		h.exchange.RequestBody = make([]byte, len(body))
		copy(h.exchange.RequestBody, body)
		h.exchange.RequestHeaders = headers.Clone()
	}
}
func (h *SmallHandle) Response(p []byte, headers http.Header) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.released || h.queued || !h.bodySeen {
		return
	}
	if h.generation != h.recorder.generation.Load() || !h.recorder.Enabled() {
		h.dropLocked("disabled")
		return
	}
	if len(h.exchange.ResponseBody)+len(p) > SmallResponseLimit {
		h.dropLocked("response_too_large")
		return
	}
	if h.exchange.ResponseHeaders == nil {
		n := smallHeaderBytes(headers)
		if n > SmallHeaderLimit {
			h.dropLocked("headers_too_large")
			return
		}
		if !h.reserveLocked(n) {
			return
		}
		h.exchange.ResponseHeaders = headers.Clone()
	}
	if len(h.exchange.ResponseBody)+len(p) > cap(h.exchange.ResponseBody) {
		capacity := min(SmallResponseLimit, max(4096, max(2*cap(h.exchange.ResponseBody), len(h.exchange.ResponseBody)+len(p))))
		if !h.reserveLocked(capacity) {
			return
		}
		oldCapacity := cap(h.exchange.ResponseBody)
		copyBody := make([]byte, len(h.exchange.ResponseBody), capacity)
		copy(copyBody, h.exchange.ResponseBody)
		h.exchange.ResponseBody = copyBody
		h.bytes -= int64(oldCapacity)
		h.recorder.activeBytes.Add(-int64(oldCapacity))
	}
	h.exchange.ResponseBody = append(h.exchange.ResponseBody, p...)
}
func (h *SmallHandle) Finish(status int, headers http.Header, partial bool) {
	if h == nil {
		return
	}
	h.Response(nil, headers)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.released {
		return
	}
	h.finished, h.finishedAt = true, time.Now()
	h.exchange.Status, h.exchange.Partial = status, partial
	if !h.bodySeen {
		h.dropLocked("unsupported_body")
	}
	h.publishLocked()
}
func (h *SmallHandle) Usage(usage SmallUsage) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.released || h.usageSeen {
		return
	}
	h.usageSeen, h.exchange.Usage = true, usage
	switch {
	case usage.InputTokens < 0 || usage.CacheReadTokens < 0 || usage.CacheCreationTokens < 0:
		h.dropLocked("usage_unknown")
	case usage.InputTokens >= 1000 || usage.CacheReadTokens >= 1000 || usage.CacheCreationTokens >= 1000:
		h.dropLocked("input_not_small")
	default:
		total := usage.InputTokens + usage.CacheReadTokens + usage.CacheCreationTokens
		if total == 0 {
			h.dropLocked("usage_unknown")
		} else if total >= 1000 {
			h.dropLocked("input_not_small")
		}
	}
	h.publishLocked()
}
func (h *SmallHandle) publishLocked() {
	if h.released || h.queued {
		return
	}
	if h.generation != h.recorder.generation.Load() || !h.recorder.Enabled() {
		h.dropLocked("disabled")
		return
	}
	if !h.finished || !h.usageSeen {
		return
	}
	select {
	case h.recorder.queue <- h:
		h.queued = true
	default:
		h.dropLocked("queue_full")
	}
}
func (r *SmallRecorder) expirePending(now time.Time) {
	r.mu.Lock()
	handles := make([]*SmallHandle, 0, len(r.handles))
	for h := range r.handles {
		handles = append(handles, h)
	}
	r.mu.Unlock()
	for _, h := range handles {
		h.mu.Lock()
		if !h.queued && !h.released && h.finished && now.Sub(h.finishedAt) > 2*time.Minute {
			h.dropLocked("usage_unknown")
		}
		h.mu.Unlock()
	}
}
func (r *SmallRecorder) run(ctx context.Context) {
	defer close(r.done)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	r.maintenance(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.maintenance(ctx)
		case h := <-r.queue:
			func() {
				h.mu.Lock()
				defer h.mu.Unlock()
				defer h.releaseLocked()
				if h.released {
					return
				}
				if h.generation != r.generation.Load() || !r.Enabled() {
					r.skip("disabled")
					return
				}
				e, reason := prepareSmallExchange(h.exchange, r.credentialHeader)
				if reason != "" {
					r.skip(reason)
					return
				}
				writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				if err := r.Store.Save(writeCtx, e); err != nil {
					r.skip("storage_error")
				}
			}()
		}
	}
}
func (r *SmallRecorder) refreshQuota(ctx context.Context) {
	queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if size, err := r.Store.StoredBytes(queryCtx); err == nil {
		r.quotaFull.Store(size >= SmallStoredBytesLimit)
	}
}
func (r *SmallRecorder) maintenance(ctx context.Context) {
	r.expirePending(time.Now())
	if r.Store == nil || r.Store.DB == nil {
		return
	}
	workCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if !r.Store.Ready(workCtx) {
		r.ready.Store(false)
		return
	}
	for i, reason := range smallSkipReasons {
		if count := r.skipped[i].Swap(0); count > 0 {
			if err := r.Store.AddSkipped(workCtx, time.Now().UTC().Truncate(time.Hour), reason, count); err != nil {
				r.skipped[i].Add(count)
			}
		}
	}
	if time.Since(r.lastCleanup) >= time.Minute {
		r.lastCleanup = time.Now()
		r.refreshQuota(ctx)
		cleanupCtx, stop := context.WithTimeout(ctx, 3*time.Second)
		if err := r.Store.Cleanup(cleanupCtx, time.Now()); err != nil {
			r.skip("storage_error")
		}
		stop()
	}
	r.ready.Store(true)
}

// Invalidate releases pending copies and prevents them repopulating storage.
func (r *SmallRecorder) Invalidate() {
	if r == nil {
		return
	}
	r.mu.Lock()
	generation := r.generation.Add(1)
	handles := make([]*SmallHandle, 0, len(r.handles))
	for h := range r.handles {
		handles = append(handles, h)
	}
	r.mu.Unlock()
	for _, h := range handles {
		h.mu.Lock()
		if h.generation != generation {
			h.dropLocked("disabled")
		}
		h.mu.Unlock()
	}
}
