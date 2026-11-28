//go:build unit

package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"weak"

	"github.com/Wei-Shaw/sub2api/internal/config"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type handlerMidStreamUpstream struct {
	fn func(req *http.Request) (*http.Response, error)
}

func (u *handlerMidStreamUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.fn(req)
}

func (u *handlerMidStreamUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.fn(req)
}

type handlerSSEFlushRecorder struct {
	header http.Header
	mu     sync.Mutex
	code   int
	buf    bytes.Buffer
	notify chan struct{}
}

func newHandlerSSEFlushRecorder() *handlerSSEFlushRecorder {
	return &handlerSSEFlushRecorder{
		header: make(http.Header),
		code:   http.StatusOK,
		notify: make(chan struct{}, 32),
	}
}

func (w *handlerSSEFlushRecorder) Header() http.Header { return w.header }
func (w *handlerSSEFlushRecorder) WriteHeader(code int) {
	w.mu.Lock()
	w.code = code
	w.mu.Unlock()
}
func (w *handlerSSEFlushRecorder) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.buf.Write(p)
	w.mu.Unlock()
	select {
	case w.notify <- struct{}{}:
	default:
	}
	return n, err
}
func (w *handlerSSEFlushRecorder) Flush() {
	select {
	case w.notify <- struct{}{}:
	default:
	}
}
func (w *handlerSSEFlushRecorder) Snapshot() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func TestMidStreamRelease_CyberPolicyMidStreamFailurePreservesPrecomputedPlanAndTurn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	gatewayCache := testutil.NewRedisGatewayCache(t)

	settingRepo := &contentModerationHandlerSettingRepo{values: map[string]string{
		service.SettingKeyRiskControlEnabled:          "true",
		service.SettingKeyCyberSessionBlockEnabled:    "true",
		service.SettingKeyCyberSessionBlockTTLSeconds: "60",
	}}
	moderationRepo := &contentModerationHandlerTestRepo{}
	moderationSvc := service.NewContentModerationService(settingRepo, moderationRepo, nil, nil, nil, nil, nil, nil)
	settingSvc := service.NewSettingService(settingRepo, nil)

	pr, pw := io.Pipe()
	upstreamReady := make(chan struct{})
	upstream := &handlerMidStreamUpstream{
		fn: func(req *http.Request) (*http.Response, error) {
			if req != nil && req.Body != nil {
				_, _ = io.Copy(io.Discard, req.Body)
				_ = req.Body.Close()
			}
			close(upstreamReady)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       pr,
				Request:    req,
			}, nil
		},
	}

	groupID := int64(4399)
	account := service.Account{
		ID:          9999,
		Name:        "cyber-midstream-openai",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		GroupIDs:    []int64{groupID},
		Credentials: map[string]any{"api_key": "fixture-key", "base_url": "https://api.example.com"},
		Extra:       map[string]any{"openai_passthrough": true},
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.MaxAccountSwitches = 1

	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	gatewaySvc := service.NewOpenAIGatewayService(
		&openAIWSUsageHandlerAccountRepoStub{account: account}, nil, nil, nil, nil, nil, gatewayCache, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, billingCache,
		upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, settingSvc, nil,
	)
	h := NewOpenAIGatewayHandler(
		gatewaySvc,
		service.NewConcurrencyService(nil),
		billingCache,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil, nil, moderationSvc, nil, cfg,
	)

	key := &service.APIKey{
		ID:      1899,
		Key:     "test-key-must-not-leak",
		UserID:  1799,
		GroupID: &groupID,
		User:    &service.User{ID: 1799, Email: "cyber@example.com", Status: service.StatusActive},
		Group:   &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true},
	}

	var weakReqBody weak.Pointer[byte]
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), key)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.User.ID})
		c.Next()
	})
	router.Use(InboundEndpointMiddleware())
	router.Use(middleware.RejectConflictingRequestFields())
	router.Use(func(c *gin.Context) {
		if pb, ok := c.Request.Body.(*pkghttputil.PrereadBody); ok && len(pb.Bytes()) > 0 {
			weakReqBody = weak.Make(&pb.Bytes()[0])
		}
		c.Next()
	})
	router.POST("/openai/v1/responses", h.Responses)

	req := httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(`{"model":"gpt-5","stream":true,"input":"unique cyber midstream user turn `+strings.Repeat("z", 64<<10)+`"}`))
	req.Header.Set("Content-Type", "application/json")

	rec := newHandlerSSEFlushRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		router.ServeHTTP(rec, req)
		req = nil
	}()

	<-upstreamReady
	_, _ = io.WriteString(pw, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
	deadline := time.After(5 * time.Second)
	for !strings.Contains(rec.Snapshot(), "response.output_text.delta") {
		select {
		case <-rec.notify:
		case <-deadline:
			t.Fatalf("timed out waiting for delta")
		}
	}

	require.Eventually(t, func() bool {
		runtime.GC()
		return weakReqBody.Value() == nil
	}, 3*time.Second, 5*time.Millisecond, "request body must be collected mid-stream before cyber response.failed arrives")

	// Now emit cyber_policy failure AFTER body collection!
	_, _ = io.WriteString(pw, "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"cyber_policy\",\"message\":\"Blocked by cyber policy\"},\"usage\":{\"input_tokens\":5,\"output_tokens\":2}}}\n\n")
	_ = pw.Close()
	<-done

	require.Eventually(t, func() bool {
		moderationRepo.mu.Lock()
		defer moderationRepo.mu.Unlock()
		return len(moderationRepo.logs) > 0
	}, 3*time.Second, 10*time.Millisecond, "cyber_policy moderation log must still record using precomputed last user turn")

	moderationRepo.mu.Lock()
	logged := moderationRepo.logs[0]
	moderationRepo.mu.Unlock()
	require.Contains(t, logged.InputExcerpt, "unique cyber midstream user turn")

	// Verify session block was recorded using precomputed block keys (second request is blocked).
	followUp := []byte(`{"model":"gpt-5","stream":true,"input":"unique cyber midstream user turn ` + strings.Repeat("z", 64<<10) + `"}`)
	blockedKey := gatewaySvc.FindCyberSessionBlockedForRequest(context.Background(), key.ID, nil, followUp, "192.0.2.1", "")
	require.NotEmpty(t, blockedKey, "cyber session block plan must still persist after mid-stream body release")
}
