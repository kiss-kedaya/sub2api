//go:build unit

package handler

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type miniMaxH3StubHTTPUpstream struct {
	requests  []*http.Request
	responses []*http.Response
}

func (s *miniMaxH3StubHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	s.requests = append(s.requests, req)
	if len(s.responses) == 0 {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{}`)),
		}, nil
	}
	resp := s.responses[0]
	s.responses = s.responses[1:]
	return resp, nil
}

func (s *miniMaxH3StubHTTPUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(req, proxyURL, accountID, accountConcurrency)
}

func TestMiniMaxH3VideosContentTypeGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &OpenAIGatewayHandler{}

	// Unsupported media type is rejected with 415.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader("plain"))
	c.Request.Header.Set("Content-Type", "text/plain")
	h.MiniMaxH3Videos(c)
	require.Equal(t, http.StatusUnsupportedMediaType, w.Code)

	// multipart/form-data passes the Content-Type gate (reaches auth check, returning 401 instead of 415).
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	require.NoError(t, mw.WriteField("model", "h3-video"))
	require.NoError(t, mw.WriteField("prompt", "ocean waves"))
	require.NoError(t, mw.WriteField("seconds", "30"))
	require.NoError(t, mw.Close())

	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(body.Bytes()))
	c2.Request.Header.Set("Content-Type", mw.FormDataContentType())
	h.MiniMaxH3Videos(c2)
	require.Equal(t, http.StatusUnauthorized, w2.Code)
}

func TestCompositeGroupVideoLookupDelegatesMiniMaxH3(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		RunMode: config.RunModeSimple,
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{Enabled: false, AllowInsecureHTTP: true, AllowPrivateHosts: true},
		},
	}
	account := &service.Account{
		ID:          1115,
		Name:        "minimax-h3-relay",
		Platform:    service.PlatformMiniMax,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
		Concurrency: 10,
		Credentials: map[string]any{
			"api_key":      "test-h3-key",
			"base_url":     "https://relay.example/v1",
			"account_mode": service.AccountModePayG,
		},
	}
	groupID := int64(57)
	user := &service.User{ID: 36, Balance: 100, Status: service.StatusActive}
	group := &service.Group{
		ID:       groupID,
		Name:     "composite-h3",
		Platform: service.PlatformComposite,
		Status:   service.StatusActive,
		VideoModelPrices: map[string]map[string]float64{
			service.MiniMaxH3PriceFamily: {service.MiniMaxH3PerRequest: 0.5},
		},
	}
	apiKey := &service.APIKey{ID: 142, UserID: user.ID, User: user, GroupID: &groupID, Group: group}
	taskKey := service.MiniMaxH3TaskKey("video_42")
	baseCost := 0.5

	upstream := &miniMaxH3StubHTTPUpstream{
		responses: []*http.Response{
			{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"id":"video_42","object":"video","status":"in_progress","progress":50,"seconds":"30"}`)),
			},
			{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"video/mp4"}},
				Body:       io.NopCloser(strings.NewReader("mp4-bytes")),
			},
		},
	}
	cache := &videoRouteBillingCache{
		bindings: map[string]int64{},
		pending:  map[string][]byte{},
		billed:   map[string]bool{},
	}
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	gateway := service.NewOpenAIGatewayService(
		coldVideoLookupAccountRepo{account: account},
		nil, nil, nil, nil, nil,
		cache, cfg, nil, nil,
		service.NewBillingService(cfg, nil),
		nil, billingCache, upstream, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, nil,
	)
	t.Cleanup(gateway.CloseOpenAIWSPool)
	require.NoError(t, gateway.BindGrokMediaVideoRequestAccount(context.Background(), &groupID, taskKey, user.ID, apiKey.ID, account.ID))
	require.NoError(t, gateway.StoreGrokVideoPendingBilling(context.Background(), taskKey, user.ID, apiKey.ID, service.GrokVideoPendingBilling{
		Provider:             service.VideoProviderMiniMaxH3,
		MiniMaxH3BaseCost:    &baseCost,
		GroupID:              groupID,
		BindingGroupID:       groupID,
		Model:                "h3-video",
		BillingModel:         "h3-video",
		UpstreamModel:        "dmad_30",
		VideoResolution:      "768p",
		VideoDurationSeconds: 30,
	}))

	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(testutil.StubConcurrencyCache{}), billingCache, &service.APIKeyService{}, nil, nil, nil, nil, cfg)

	newCtx := func(path string) (*gin.Context, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, path, nil)
		c.Params = gin.Params{{Key: "request_id", Value: "video_42"}}
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: user.ID, Concurrency: 10})
		return c, w
	}

	// 1. GET /v1/videos/video_42 on composite group routes to MiniMax-H3 status endpoint.
	cStatus, wStatus := newCtx("/v1/videos/video_42")
	h.GrokVideoStatus(cStatus)
	require.Equal(t, http.StatusOK, wStatus.Code)
	require.Contains(t, wStatus.Body.String(), `"in_progress"`)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "https://relay.example/v1/videos/video_42", upstream.requests[0].URL.String())

	// 2. GET /v1/videos/video_42/content on composite group routes to MiniMax-H3 content endpoint.
	cContent, wContent := newCtx("/v1/videos/video_42/content")
	h.GrokVideoContent(cContent)
	require.Equal(t, http.StatusOK, wContent.Code)
	require.Equal(t, "mp4-bytes", wContent.Body.String())
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "https://relay.example/v1/videos/video_42/content", upstream.requests[1].URL.String())
}
