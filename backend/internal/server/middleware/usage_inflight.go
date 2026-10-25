package middleware

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// proceedAuthenticated 鉴权已经通过。包一层在途记录，再进入后面的处理。
func proceedAuthenticated(c *gin.Context) {
	if c == nil {
		return
	}
	stop := trackUsageInflight(c)
	defer stop()
	c.Next()
}

func trackUsageInflight(c *gin.Context) func() {
	noop := func() {}
	if c == nil || c.Request == nil || c.Request.Method == http.MethodOptions || skipUsageInflightPath(c.Request.URL.Path) {
		return noop
	}
	userID, apiKeyID, groupID, email := usageInflightIdentity(c)
	if userID <= 0 {
		return noop
	}
	requestID, _ := c.Request.Context().Value(ctxkey.ClientRequestID).(string)
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		requestID, _ = c.Request.Context().Value(ctxkey.RequestID).(string)
		requestID = strings.TrimSpace(requestID)
	}
	if requestID == "" {
		return noop
	}
	path := c.Request.URL.Path
	snap := service.UsageInflightSnapshot{
		RequestID:   requestID,
		UserID:      userID,
		APIKeyID:    apiKeyID,
		GroupID:     groupID,
		Email:       email,
		Model:       usageInflightModel(c.Request.Context()),
		Stream:      usageInflightStream(c),
		WebSocket:   strings.EqualFold(c.GetHeader("Upgrade"), "websocket") || strings.Contains(path, "/realtime/"),
		StartedAt:   time.Now(),
		InputTokens: usageInflightInputTokens(c),
	}
	if accountID, ok := c.Request.Context().Value(ctxkey.AccountID).(int64); ok {
		snap.AccountID = accountID
	}
	snap.ReservedAmount = service.InflightReservationFromContext(c.Request.Context()).Amount()
	fillUsageInflightDisplay(c, &snap)

	state := &usageInflightWriteState{ginCtx: c, requestCtx: c.Request.Context(), snap: snap, done: make(chan struct{})}
	c.Writer = &usageInflightWriter{ResponseWriter: c.Writer, state: state}
	// ponytail: 满 1 秒写一次。首字若在这次之后才到，再补写一次。页面自己刷新才变。
	timer := time.AfterFunc(time.Second, func() {
		state.mu.Lock()
		if state.closed {
			state.mu.Unlock()
			return
		}
		state.persisted = true
		state.mu.Unlock()
		state.refresh()
	})
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-state.done:
				return
			case <-ticker.C:
				state.mu.Lock()
				closed := state.closed || !state.persisted
				state.mu.Unlock()
				if !closed {
					state.refresh()
				}
			}
		}
	}()
	reqCtx := c.Request.Context()
	go func() {
		select {
		case <-reqCtx.Done():
			timer.Stop()
			state.mu.Lock()
			state.closed = true
			persisted := state.persisted
			state.gen.Add(1)
			state.mu.Unlock()
			state.doneOnce.Do(func() { close(state.done) })
			if persisted {
				service.DeleteUsageInflight(context.Background(), userID, requestID)
			}
		case <-state.done:
		}
	}()
	return func() {
		timer.Stop()
		state.mu.Lock()
		state.closed = true
		persisted := state.persisted
		state.gen.Add(1)
		state.mu.Unlock()
		state.doneOnce.Do(func() { close(state.done) })
		if persisted {
			go service.DeleteUsageInflight(context.Background(), userID, requestID)
		}
	}
}

func skipUsageInflightPath(path string) bool {
	switch {
	case strings.HasSuffix(path, "/usage"), strings.HasSuffix(path, "/billing"), strings.HasSuffix(path, "/count_tokens"):
		return true
	case strings.Contains(path, "/models"):
		return true
	default:
		return false
	}
}

func usageInflightIdentity(c *gin.Context) (userID, apiKeyID, groupID int64, email string) {
	if c.Request != nil {
		if id, ok := c.Request.Context().Value(ctxkey.UserID).(int64); ok {
			userID = id
		}
		if group, ok := c.Request.Context().Value(ctxkey.Group).(*service.Group); ok && group != nil {
			groupID = group.ID
		}
	}
	if value, exists := c.Get(string(ContextKeyAPIKey)); exists {
		if key, ok := value.(*service.APIKey); ok && key != nil {
			apiKeyID = key.ID
			if userID == 0 {
				userID = key.UserID
			}
			if groupID == 0 && key.GroupID != nil {
				groupID = *key.GroupID
			}
			if key.User != nil {
				email = key.User.Email
			}
		}
	}
	if userID == 0 {
		if value, exists := c.Get(string(ContextKeyUser)); exists {
			if subject, ok := value.(AuthSubject); ok {
				userID = subject.UserID
			}
		}
	}
	return userID, apiKeyID, groupID, email
}

func usageInflightModel(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if model, ok := service.RequestedPublicModelFromContext(ctx); ok {
		return strings.TrimSpace(model)
	}
	model, _ := ctx.Value(ctxkey.Model).(string)
	return strings.TrimSpace(model)
}

func usageInflightStream(c *gin.Context) bool {
	if c.Query("stream") == "true" {
		return true
	}
	return strings.Contains(strings.ToLower(c.GetHeader("Accept")), "text/event-stream")
}

func usageInflightInputTokens(c *gin.Context) int {
	if c.Request != nil && c.Request.ContentLength > 0 {
		return int(c.Request.ContentLength / 4)
	}
	return 0
}

type usageInflightWriteState struct {
	ginCtx     *gin.Context
	requestCtx context.Context
	snap       service.UsageInflightSnapshot
	bytes      int
	first      *int
	done       chan struct{}
	doneOnce   sync.Once
	closed     bool
	persisted  bool
	gen        atomic.Int64
	pending    atomic.Bool
	mu         sync.Mutex
}

// note 只在请求 goroutine 里调。header 也只能在这里读：定时器 goroutine 读它，
// 会和 handler 的 Header().Set 撞成 fatal error: concurrent map read and map write，进程直接重启。
func (s *usageInflightWriteState) note(n int, header http.Header) {
	if s == nil || n <= 0 {
		return
	}
	s.mu.Lock()
	now := time.Now()
	flushFirst := false
	if s.first == nil {
		ms := int(now.Sub(s.snap.StartedAt).Milliseconds())
		if ms < 0 {
			ms = 0
		}
		s.first = &ms
		flushFirst = s.persisted
		if strings.Contains(strings.ToLower(header.Get("Content-Type")), "text/event-stream") {
			s.snap.Stream = true
		}
	}
	s.bytes += n
	s.mu.Unlock()
	if flushFirst {
		s.refresh()
	}
}

func (s *usageInflightWriteState) snapshotLocked() service.UsageInflightSnapshot {
	snap := s.snap
	snap.OutputTokens = s.bytes / 4
	snap.FirstTokenMs = s.first
	ctx := s.requestCtx
	if s.ginCtx != nil {
		if value, ok := s.ginCtx.Get(service.UsageInflightRequestContextKey); ok {
			if current, ok := value.(context.Context); ok {
				ctx = current
			}
		}
	}
	if ctx != nil {
		if model := usageInflightModel(ctx); model != "" {
			snap.Model = model
		}
		if accountID, ok := ctx.Value(ctxkey.AccountID).(int64); ok && accountID > 0 {
			snap.AccountID = accountID
		}
		if amount := service.InflightReservationFromContext(ctx).Amount(); amount > 0 {
			snap.ReservedAmount = amount
		}
	}
	refreshUsageInflightDisplay(s.ginCtx, &snap, ctx)
	snap.UpdatedAt = time.Now()
	s.snap = snap
	return snap
}

func (s *usageInflightWriteState) refresh() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	snap := s.snapshotLocked()
	gen := s.gen.Load()
	s.mu.Unlock()
	s.flush(snap, gen)
}

func (s *usageInflightWriteState) flush(snap service.UsageInflightSnapshot, gen int64) {
	if !s.pending.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer s.pending.Store(false)
		s.mu.Lock()
		closed := s.closed || s.gen.Load() != gen
		s.mu.Unlock()
		if closed {
			return
		}
		service.SaveUsageInflight(context.Background(), snap)
		s.mu.Lock()
		closed = s.closed || s.gen.Load() != gen
		s.mu.Unlock()
		if closed {
			service.DeleteUsageInflight(context.Background(), snap.UserID, snap.RequestID)
		}
	}()
}

type usageInflightWriter struct {
	gin.ResponseWriter
	state *usageInflightWriteState
}

func (w *usageInflightWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *usageInflightWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	w.state.note(n, w.Header())
	return n, err
}

func (w *usageInflightWriter) WriteString(data string) (int, error) {
	n, err := w.ResponseWriter.WriteString(data)
	w.state.note(n, w.Header())
	return n, err
}

// fillUsageInflightDisplay 建快照时在请求 goroutine 里调一次。
// 客户端 IP 要读 Request.Header，只能在这里算：gateway_bedrock 等路径会在请求中途改它。
func fillUsageInflightDisplay(c *gin.Context, snap *service.UsageInflightSnapshot) {
	if c == nil || snap == nil {
		return
	}
	if value, exists := c.Get(string(ContextKeyAPIKey)); exists {
		if key, ok := value.(*service.APIKey); ok && key != nil {
			if name := strings.TrimSpace(key.Name); name != "" {
				snap.APIKeyName = name
			}
		}
	}
	refreshUsageInflightDisplay(c, snap, c.Request.Context())
	if snap.InboundEndpoint == "" && c.Request != nil && c.Request.URL != nil {
		snap.InboundEndpoint = strings.TrimSpace(c.Request.URL.Path)
	}
	if addr := strings.TrimSpace(ip.GetClientIP(c)); addr != "" {
		snap.IPAddress = addr
	}
}

// refreshUsageInflightDisplay 会在定时器 goroutine 里跑，handler 此时还在写 gin.Context。
// 只读带锁的 c.Get 和已发布的 context，不能读取 handler 正在替换的 c.Request。
func refreshUsageInflightDisplay(c *gin.Context, snap *service.UsageInflightSnapshot, ctx context.Context) {
	if c == nil || snap == nil {
		return
	}
	if v, ok := c.Get("_gateway_inbound_endpoint"); ok {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			snap.InboundEndpoint = strings.TrimSpace(s)
		}
	}
	if endpoint := strings.TrimSpace(service.GetActualOpenAIUpstreamEndpoint(c)); endpoint != "" {
		snap.UpstreamEndpoint = endpoint
	} else if v, ok := c.Get("_gateway_actual_upstream_endpoint"); ok {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			snap.UpstreamEndpoint = strings.TrimSpace(s)
		}
	}
	if ctx != nil {
		if group, ok := ctx.Value(ctxkey.Group).(*service.Group); ok && group != nil {
			snap.GroupID = group.ID
			snap.GroupName = strings.TrimSpace(group.Name)
		}
		if accountID, ok := ctx.Value(ctxkey.AccountID).(int64); ok && accountID > 0 {
			// Clear the old name on an ID-only failover selection.
			snap.AccountID = accountID
			snap.AccountName, _ = ctx.Value(ctxkey.AccountName).(string)
		}
		if effort := service.RequestedReasoningEffortFromContext(ctx); effort != nil {
			snap.ReasoningEffort = *effort
		}
	}
}
