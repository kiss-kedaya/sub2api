//go:build unit

package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// openAIImagesBlockingHTTPUpstream never answers: Do returns only when the request
// context ends. It models an upstream that is still generating the image when the
// client leaves.
type openAIImagesBlockingHTTPUpstream struct {
	service.HTTPUpstream
	mu         sync.Mutex
	accountIDs []int64
	started    chan struct{}
}

func (u *openAIImagesBlockingHTTPUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	u.mu.Unlock()
	u.started <- struct{}{}
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func (u *openAIImagesBlockingHTTPUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.accountIDs...)
}

// Owner decision 2026-11-06: a client that leaves closes the upstream at once. The
// request then ends with a plain context error that is not an account fault: the
// handler must not report a scheduler failure, must not fail over to another
// account (which would start a second paid generation for nobody) and must not
// write an error response to a client that is gone.
func TestOpenAIGatewayHandlerImages_ClientLeavesWhileUpstreamWorksIsNotAnAccountFault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(3131)
	accounts := []service.Account{
		{
			ID: 1, Name: "image-account-1", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
			Status: service.StatusActive, Schedulable: true, Concurrency: 0, Priority: 0,
			Credentials: map[string]any{"access_token": "token-1"},
		},
		{
			ID: 2, Name: "image-account-2", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
			Status: service.StatusActive, Schedulable: true, Concurrency: 0, Priority: 1,
			Credentials: map[string]any{"access_token": "token-2"},
		},
	}
	accountRepo := openAIImagesFailoverAccountRepo{accounts: accounts}
	upstream := &openAIImagesBlockingHTTPUpstream{started: make(chan struct{}, 4)}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	gatewayService := service.NewOpenAIGatewayService(
		accountRepo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	billingService := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingService.Stop)
	handler := NewOpenAIGatewayHandler(
		gatewayService,
		service.NewConcurrencyService(nil),
		billingService,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil, nil, nil, nil,
		cfg,
	)
	handler.maxAccountSwitches = 10

	body := []byte(`{"model":"gpt-image-1","prompt":"draw a cat"}`)
	core, observedLogs := observer.New(zap.DebugLevel)
	requestCtx, cancel := context.WithCancel(logger.IntoContext(context.Background(), zap.New(core)))
	t.Cleanup(cancel)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body)).WithContext(requestCtx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID:      99,
		GroupID: &groupID,
		Group: &service.Group{
			ID:                   groupID,
			Platform:             service.PlatformOpenAI,
			Status:               service.StatusActive,
			Hydrated:             true,
			AllowImageGeneration: true,
		},
		User: &service.User{ID: 100},
	})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 100, Concurrency: 0})

	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.Images(c)
	}()
	select {
	case <-upstream.started:
	case <-time.After(3 * time.Second):
		t.Fatal("the request never reached the upstream")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the handler outlived the departed client")
	}

	require.Equal(t, []int64{1}, upstream.calls(), "a departed client must not fail over to another account")
	require.Empty(t, observedLogs.FilterMessage("openai.images.forward_failed").All(),
		"a departed client is not a forward failure")
	require.NotEmpty(t, observedLogs.FilterMessage("openai.images.client_disconnected").All())
	require.Equal(t, statusClientClosedRequest, c.Writer.Status())
	require.Empty(t, rec.Body.String(), "no error body is written to a client that is gone")
}
