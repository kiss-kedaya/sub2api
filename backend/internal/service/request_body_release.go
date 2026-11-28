package service

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/gin-gonic/gin"
)

// ReleaseRequestBodyReferences 在请求处理链结束后，清掉挂在 gin.Context 上、会钉住请求体的
// 几个引用：BodyTop 索引（最近两份请求体）、compat 转发保留的上游请求体、调试转储用的入站体。
//
// 为什么要清：gin 把 Context 放回对象池时不重置它，池里的 Context 还指着最后一个请求的
// Keys；计费任务、日志队列也可能带着 Context。不清，已经结束的请求的整份请求体
// （可达数 MB）要等 Context 被复用、或池在两次 GC 后被清空才能回收。
//
// 只能在没有任何后续读取之后调用（请求处理链已经返回）。这些值都是缓存或诊断用途：
// 之后再有人调 RequestBodyTop 只会重建索引，compat/调试读取方在值为空时有各自的回退。
func ReleaseRequestBodyReferences(c *gin.Context) {
	releaseRequestBodyReferences(c, false, false)
}

// ReleaseRequestBodyReferencesForMidStream releases body memos on gin.Context
// during active SSE streaming while preserving debugInboundBodyKey when debug
// body dumping is explicitly enabled and preserving scalar compat token stashes.
func ReleaseRequestBodyReferencesForMidStream(c *gin.Context) {
	releaseRequestBodyReferences(c, DebugDumpEnabled(), true)
}

func releaseRequestBodyReferences(c *gin.Context, preserveDebugInbound bool, preserveCompatTokenStash bool) {
	if c == nil {
		return
	}
	if v, ok := c.Get(bodyTopMemoKey); ok {
		if memo, _ := v.(*bodyTopMemo); memo != nil {
			for i := range memo.slots {
				memo.slots[i].Store(nil)
			}
		}
	}
	if !preserveCompatTokenStash {
		if _, ok := c.Get(compatUpstreamRequestBodyKey); ok {
			c.Set(compatUpstreamRequestBodyKey, compatRequestStash{})
		}
	}
	if !preserveDebugInbound {
		if _, ok := c.Get(debugInboundBodyKey); ok {
			c.Set(debugInboundBodyKey, []byte(nil))
		}
	}
	if _, ok := c.Get(openAINativeSearchSkipFunctionToolsBodyKey); ok {
		c.Set(openAINativeSearchSkipFunctionToolsBodyKey, []byte(nil))
	}
}

// releaseUpstreamRequestPayload clears Body and GetBody on both the caller's
// request and resp.Request immediately after Do/DoWithTLS returns, so net/http
// and transport shallow copies do not pin outbound request body slices during
// streaming responses.
func releaseUpstreamRequestPayload(req *http.Request, resp *http.Response) {
	if req != nil {
		if r, ok := req.Body.(interface{ Release() }); ok {
			r.Release()
		}
		req.Body = nil
		req.GetBody = nil
	}
	if resp != nil && resp.Request != nil && resp.Request != req {
		if r, ok := resp.Request.Body.(interface{ Release() }); ok {
			r.Release()
		}
		resp.Request.Body = nil
		resp.Request.GetBody = nil
	}
}

type requestBodyReleaseHookContextKey struct{}
type preOutputFailoverDisabledContextKey struct{}
type compactFallbackPossibleContextKey struct{}

const requestBodyReleaseHookGinKey = "__sub2api_request_body_release_hook"

type serviceBodyReleaseEntry struct {
	id uint64
	fn func(account *Account)
}

// RequestBodyReleaseHook coordinates idempotent mid-stream release of handler
// and service request body buffers once SSE streaming commits past all retry
// and failover boundaries.
type RequestBodyReleaseHook struct {
	mu                 sync.Mutex
	once               sync.Once
	nextID             uint64
	handlerFn          func(account *Account)
	serviceFns         []serviceBodyReleaseEntry
	inputUsageObserved atomic.Bool
}

// WithRequestBodyReleaseHook attaches a RequestBodyReleaseHook to ctx and c,
// registering handlerFn to run once when TriggerRequestBodyRelease fires.
func WithRequestBodyReleaseHook(ctx context.Context, c *gin.Context, handlerFn func(account *Account)) (context.Context, *RequestBodyReleaseHook) {
	if ctx == nil {
		ctx = context.Background()
	}
	hook := &RequestBodyReleaseHook{handlerFn: handlerFn}
	ctx = context.WithValue(ctx, requestBodyReleaseHookContextKey{}, hook)
	if c != nil {
		c.Set(requestBodyReleaseHookGinKey, hook)
	}
	return ctx, hook
}

// PushServiceRequestBodyRelease registers a service-frame body release callback
// onto the active RequestBodyReleaseHook (creating one if none exists yet) and
// returns a cleanup function that removes only this frame's callback on exit.
func PushServiceRequestBodyRelease(ctx context.Context, c *gin.Context, fn func(account *Account)) (context.Context, func()) {
	if ctx == nil {
		ctx = context.Background()
	}
	if fn == nil {
		return ctx, func() {}
	}
	hook := lookupRequestBodyReleaseHook(ctx, c)
	created := false
	if hook == nil {
		hook = &RequestBodyReleaseHook{}
		ctx = context.WithValue(ctx, requestBodyReleaseHookContextKey{}, hook)
		if c != nil {
			c.Set(requestBodyReleaseHookGinKey, hook)
		}
		created = true
	} else if ctx.Value(requestBodyReleaseHookContextKey{}) == nil {
		ctx = context.WithValue(ctx, requestBodyReleaseHookContextKey{}, hook)
	}

	hook.mu.Lock()
	hook.nextID++
	entryID := hook.nextID
	hook.serviceFns = append(hook.serviceFns, serviceBodyReleaseEntry{id: entryID, fn: fn})
	hook.mu.Unlock()

	return ctx, func() {
		hook.mu.Lock()
		for i := len(hook.serviceFns) - 1; i >= 0; i-- {
			if hook.serviceFns[i].id == entryID {
				hook.serviceFns = append(hook.serviceFns[:i], hook.serviceFns[i+1:]...)
				break
			}
		}
		hook.mu.Unlock()
		if created {
			hook.Clear(c)
		}
	}
}

func lookupRequestBodyReleaseHook(ctx context.Context, c *gin.Context) *RequestBodyReleaseHook {
	if ctx != nil {
		if hook, ok := ctx.Value(requestBodyReleaseHookContextKey{}).(*RequestBodyReleaseHook); ok && hook != nil {
			return hook
		}
	}
	if c != nil {
		if raw, ok := c.Get(requestBodyReleaseHookGinKey); ok {
			if hook, _ := raw.(*RequestBodyReleaseHook); hook != nil {
				return hook
			}
		}
	}
	return nil
}

// MarkStreamInputUsageObserved records that the upstream stream already
// reported input-side usage tokens (for example Anthropic message_start), so
// mid-stream release can skip redundant request-body token estimation.
func MarkStreamInputUsageObserved(ctx context.Context, c *gin.Context) {
	if hook := lookupRequestBodyReleaseHook(ctx, c); hook != nil {
		hook.inputUsageObserved.Store(true)
	}
}

// IsStreamInputUsageObserved reports whether upstream input-side usage was
// already observed before release.
func IsStreamInputUsageObserved(ctx context.Context, c *gin.Context) bool {
	if hook := lookupRequestBodyReleaseHook(ctx, c); hook != nil {
		return hook.inputUsageObserved.Load()
	}
	return false
}

// WithPreOutputFailoverDisabled marks ctx when the handler has no remaining
// pre-output failover budget/attempts for the current forward call.
func WithPreOutputFailoverDisabled(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, preOutputFailoverDisabledContextKey{}, true)
}

func isPreOutputFailoverDisabled(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	disabled, _ := ctx.Value(preOutputFailoverDisabledContextKey{}).(bool)
	return disabled
}

// WithCompactFallbackPossible marks ctx when an explicit /responses/compact
// fallback retry is still armed for the current attempt.
func WithCompactFallbackPossible(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, compactFallbackPossibleContextKey{}, true)
}

func isCompactFallbackPossible(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	possible, _ := ctx.Value(compactFallbackPossibleContextKey{}).(bool)
	return possible
}

// TriggerRequestBodyRelease executes all registered service and handler release
// callbacks at most once, releases c.Request.Body (*httputil.PrereadBody), and
// clears gin.Context body memos for mid-stream GC collection.
func TriggerRequestBodyRelease(ctx context.Context, c *gin.Context, account *Account) {
	if hook := lookupRequestBodyReleaseHook(ctx, c); hook != nil {
		hook.once.Do(func() {
			hook.mu.Lock()
			serviceFns := hook.serviceFns
			handlerFn := hook.handlerFn
			hook.serviceFns = nil
			hook.handlerFn = nil
			hook.mu.Unlock()

			for i := len(serviceFns) - 1; i >= 0; i-- {
				if serviceFns[i].fn != nil {
					serviceFns[i].fn(account)
				}
			}
			if handlerFn != nil {
				handlerFn(account)
			}
		})
	}
	if c != nil && c.Request != nil && c.Request.Body != nil {
		if releaser, ok := c.Request.Body.(interface{ Release() }); ok {
			releaser.Release()
		}
	}
	ReleaseRequestBodyReferencesForMidStream(c)
}

// Clear drops any retained callbacks from the hook and detaches it from c so
// pooled gin.Context instances cannot pin handler closures on early/error exits.
func (h *RequestBodyReleaseHook) Clear(c *gin.Context) {
	if h != nil {
		h.mu.Lock()
		h.handlerFn = nil
		h.serviceFns = nil
		h.mu.Unlock()
	}
	if c != nil {
		if _, ok := c.Get(requestBodyReleaseHookGinKey); ok {
			c.Set(requestBodyReleaseHookGinKey, (*RequestBodyReleaseHook)(nil))
		}
	}
}
