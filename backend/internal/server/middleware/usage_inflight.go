package middleware

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
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
		Model:       usageInflightModel(c),
		Stream:      usageInflightStream(c),
		WebSocket:   strings.EqualFold(c.GetHeader("Upgrade"), "websocket") || strings.Contains(path, "/realtime/"),
		StartedAt:   time.Now(),
		InputTokens: usageInflightInputTokens(c),
	}
	if accountID, ok := c.Request.Context().Value(ctxkey.AccountID).(int64); ok {
		snap.AccountID = accountID
	}
	snap.ReservedAmount = service.InflightReservationFromContext(c.Request.Context()).Amount()

	state := &usageInflightWriteState{ginCtx: c, snap: snap, done: make(chan struct{})}
	c.Writer = &usageInflightWriter{ResponseWriter: c.Writer, state: state}
	// ponytail: 1s 内结束的请求不碰 Redis。使用记录页 2s 刷一次，晚 1s 出现无感。
	timer := time.AfterFunc(time.Second, func() {
		state.mu.Lock()
		if state.closed {
			state.mu.Unlock()
			return
		}
		state.persisted = true
		state.mu.Unlock()
		state.refresh()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-state.done:
				return
			case <-ticker.C:
				state.refresh()
			}
		}
	})
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

func usageInflightModel(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	if model, ok := service.RequestedPublicModelFromContext(c.Request.Context()); ok {
		return strings.TrimSpace(model)
	}
	model, _ := c.Request.Context().Value(ctxkey.Model).(string)
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
	ginCtx    *gin.Context
	snap      service.UsageInflightSnapshot
	bytes     int
	first     *int
	done      chan struct{}
	doneOnce  sync.Once
	closed    bool
	persisted bool
	gen       atomic.Int64
	pending   atomic.Bool
	mu        sync.Mutex
}

func (s *usageInflightWriteState) note(n int) {
	if s == nil || n <= 0 {
		return
	}
	s.mu.Lock()
	now := time.Now()
	if s.first == nil {
		ms := int(now.Sub(s.snap.StartedAt).Milliseconds())
		if ms < 0 {
			ms = 0
		}
		s.first = &ms
	}
	s.bytes += n
	s.mu.Unlock()
}

func (s *usageInflightWriteState) snapshotLocked() service.UsageInflightSnapshot {
	snap := s.snap
	snap.OutputTokens = s.bytes / 4
	snap.FirstTokenMs = s.first
	if s.ginCtx != nil && s.ginCtx.Request != nil {
		ctx := s.ginCtx.Request.Context()
		if model := usageInflightModel(s.ginCtx); model != "" {
			snap.Model = model
		}
		if accountID, ok := ctx.Value(ctxkey.AccountID).(int64); ok && accountID > 0 {
			snap.AccountID = accountID
		}
		if amount := service.InflightReservationFromContext(ctx).Amount(); amount > 0 {
			snap.ReservedAmount = amount
		}
		if strings.Contains(strings.ToLower(s.ginCtx.Writer.Header().Get("Content-Type")), "text/event-stream") {
			snap.Stream = true
		}
	}
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
	w.state.note(n)
	return n, err
}

func (w *usageInflightWriter) WriteString(data string) (int, error) {
	n, err := w.ResponseWriter.WriteString(data)
	w.state.note(n)
	return n, err
}
