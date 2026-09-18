//go:build unit

package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type videoRouteBillingCache struct {
	testutil.StubGatewayCache
	mu       sync.Mutex
	bindings map[string]int64
	pending  map[string][]byte
	billed   map[string]bool
	loadErr  error
	storeErr error
}

func (s *videoRouteBillingCache) SetSessionAccountID(_ context.Context, groupID int64, key string, accountID int64, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bindings[fmt.Sprintf("%d:%s", groupID, key)] = accountID
	return nil
}

func (s *videoRouteBillingCache) GetSessionAccountID(_ context.Context, groupID int64, key string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.bindings[fmt.Sprintf("%d:%s", groupID, key)]
	if !ok {
		return 0, service.ErrStickySessionNotFound
	}
	return id, nil
}

func (s *videoRouteBillingCache) SetGrokVideoPendingBilling(_ context.Context, key string, payload []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.storeErr != nil {
		return s.storeErr
	}
	s.pending[key] = append([]byte(nil), payload...)
	return nil
}

func (s *videoRouteBillingCache) GetGrokVideoPendingBilling(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.pending[key]...), s.loadErr
}

func (s *videoRouteBillingCache) ClaimGrokVideoBilled(_ context.Context, key string, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.billed[key] {
		return false, nil
	}
	s.billed[key] = true
	return true, nil
}

type videoRouteSettlement struct {
	service.UsageBillingRepository
	commands []*service.UsageBillingCommand
}

func (r *videoRouteSettlement) Apply(_ context.Context, cmd *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	copy := *cmd
	r.commands = append(r.commands, &copy)
	return &service.UsageBillingApplyResult{Applied: true}, nil
}

func TestGrokVideoSmartRouteBillsSelectedGroup(t *testing.T) {
	for _, endpoint := range []string{"status", "content"} {
		for _, independent := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/independent=%t", endpoint, independent), func(t *testing.T) {
				f := newVideoRouteBillingFixture(t, independent)
				require.Equal(t, http.StatusOK, f.request(http.MethodPost, "create").Code)
				require.Empty(t, f.settlement.commands, "creation must not settle an unfinished video")
				require.Empty(t, f.usage.logs)
				for range 2 {
					response := f.request(http.MethodGet, endpoint)
					require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				}
				require.Len(t, f.settlement.commands, 1, "status/content repeat must not charge twice")
				wantRate := 2.0
				if independent {
					wantRate = 3
				}
				wantTotal := 0.2 * 6
				require.InDelta(t, wantTotal*wantRate, f.settlement.commands[0].BalanceCost, 1e-8)
				require.Equal(t, "grok-video:"+f.task, f.settlement.commands[0].RequestID)
				select {
				case usage := <-f.usage.logs:
					require.Equal(t, int64(17), *usage.GroupID)
					require.InDelta(t, wantTotal, usage.TotalCost, 1e-8)
					require.InDelta(t, wantTotal*wantRate, usage.ActualCost, 1e-8)
					require.Equal(t, wantRate, usage.RateMultiplier)
					require.Equal(t, 6, *usage.VideoDurationSeconds)
					require.Equal(t, "720p", *usage.VideoResolution)
				default:
					t.Fatal("missing usage record")
				}
				require.Equal(t, int64(9), *f.key.GroupID, "cached auth key must remain immutable")
				require.Equal(t, int64(9), f.key.Group.ID)
			})
		}
	}
}

func TestGrokVideoSmartRouteMissingBillingGroupDoesNotReleaseVideo(t *testing.T) {
	for _, failure := range []string{"missing", "cache error"} {
		t.Run(failure, func(t *testing.T) {
			f := newVideoRouteBillingFixture(t, false)
			require.Equal(t, http.StatusOK, f.request(http.MethodPost, "create").Code)
			for key := range f.cache.pending {
				switch failure {
				case "missing":
					delete(f.cache.pending, key)
				case "cache error":
					f.cache.loadErr = errors.New("synthetic cache unavailable")
				}
			}
			response := f.request(http.MethodGet, "status")
			require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
			require.NotContains(t, response.Body.String(), "video.url")
			require.Equal(t, 1, f.upstream.calls, "must stop before exposing completed media")
			require.Empty(t, f.settlement.commands)
			require.Empty(t, f.cache.billed, "failed restoration must not consume the billing claim")
		})
	}
}

func TestGrokVideoSmartRouteLegacyTaskUsesBoundAccountGroup(t *testing.T) {
	f := newVideoRouteBillingFixture(t, true)
	require.Equal(t, http.StatusOK, f.request(http.MethodPost, "create").Code)
	for key := range f.cache.pending {
		f.cache.pending[key] = []byte(`{"model":"grok-imagine-video","video_duration_seconds":6,"video_resolution":"720p"}`)
	}
	response := f.request(http.MethodGet, "status")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Len(t, f.settlement.commands, 1)
	require.InDelta(t, 3.6, f.settlement.commands[0].BalanceCost, 1e-8)
	usage := <-f.usage.logs
	require.Equal(t, int64(17), *usage.GroupID)
}

func TestGrokVideoSmartRouteLegacyAmbiguousGroupDoesNotBill(t *testing.T) {
	f := newVideoRouteBillingFixture(t, true)
	require.Equal(t, http.StatusOK, f.request(http.MethodPost, "create").Code)
	for key := range f.cache.pending {
		f.cache.pending[key] = []byte(`{"model":"grok-imagine-video","video_duration_seconds":6,"video_resolution":"720p"}`)
	}
	account, err := f.h.gatewayService.GetGrokMediaBoundAccount(context.Background(), 17001)
	require.NoError(t, err)
	account.GroupIDs = []int64{9, 17}
	response := f.request(http.MethodGet, "status")
	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	require.Empty(t, f.settlement.commands)
	require.Empty(t, f.cache.billed)
}

func TestGrokVideoSmartRoutePreservesBillingAfterKeyReorder(t *testing.T) {
	f := newVideoRouteBillingFixture(t, true)
	require.Equal(t, http.StatusOK, f.request(http.MethodPost, "create").Code)
	reordered := *f.key
	reordered.GroupID = ptrInt64(17)
	reordered.Group = nil
	reordered.RouteGroupIDs = []int64{17, 9}
	f.key = &reordered
	response := f.request(http.MethodGet, "status")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Len(t, f.settlement.commands, 1)
	require.InDelta(t, 3.6, f.settlement.commands[0].BalanceCost, 1e-8)
}

func TestGrokVideoSmartRoutePublishesBillingBeforeTaskID(t *testing.T) {
	f := newVideoRouteBillingFixture(t, false)
	f.beforeWrite = func() {
		require.Len(t, f.cache.pending, 1)
		for _, payload := range f.cache.pending {
			require.Equal(t, int64(17), gjson.GetBytes(payload, "group_id").Int())
			require.Equal(t, int64(9), gjson.GetBytes(payload, "binding_group_id").Int())
		}
	}
	require.Equal(t, http.StatusOK, f.request(http.MethodPost, "create").Code)
}

func TestGrokVideoSmartRouteStoreFailureDoesNotExposeTaskID(t *testing.T) {
	f := newVideoRouteBillingFixture(t, false)
	f.cache.storeErr = errors.New("synthetic store failure")
	response := f.request(http.MethodPost, "create")
	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), f.task)
	require.Equal(t, 1, f.upstream.calls, "accepted tasks must not be regenerated on another account")
	require.Empty(t, f.settlement.commands)
}

type videoRouteBillingFixture struct {
	h           *OpenAIGatewayHandler
	key         *service.APIKey
	cache       *videoRouteBillingCache
	settlement  *videoRouteSettlement
	usage       smartRouteHandlerUsage
	upstream    *grokMediaSlotUpstream
	task        string
	beforeWrite func()
}

type videoRouteResponseWriter struct {
	gin.ResponseWriter
	beforeWrite func()
}

func (w *videoRouteResponseWriter) Write(body []byte) (int, error) {
	if w.beforeWrite != nil {
		w.beforeWrite()
	}
	return w.ResponseWriter.Write(body)
}

func newVideoRouteBillingFixture(t *testing.T, independent bool) *videoRouteBillingFixture {
	t.Helper()
	primary := &service.Group{ID: 9, Platform: service.PlatformGrok, Status: service.StatusActive, Hydrated: true,
		AllowImageGeneration: true, RateMultiplier: 0.07, VideoRateMultiplier: 0.1, VideoRateIndependent: independent,
		VideoModelPrices: map[string]map[string]float64{service.VideoPriceFamilyGrokImagineVideo: {"720p": 0.01}}}
	selected := &service.Group{ID: 17, Platform: service.PlatformGrok, Status: service.StatusActive, Hydrated: true,
		AllowImageGeneration: true, RateMultiplier: 2, VideoRateMultiplier: 3, VideoRateIndependent: independent,
		VideoModelPrices: map[string]map[string]float64{service.VideoPriceFamilyGrokImagineVideo: {"720p": 0.2}}}
	account := service.Account{ID: 17001, Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{17}, Concurrency: 10,
		Credentials: map[string]any{"api_key": "synthetic-video-key", "model_mapping": map[string]any{"grok-imagine-video": "grok-imagine-video"}}}
	accountRepo := openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Default.RateMultiplier = 1
	snapshot := service.NewSchedulerSnapshotService(&smartRouteHandlerSnapshot{fakeSchedulerCache{accounts: []*service.Account{&account}}}, nil, accountRepo,
		smartRouteHandlerGroups{groups: map[int64]*service.Group{9: primary, 17: selected}}, cfg)
	for _, id := range []int64{9, 17} {
		_, err := snapshot.GetGroupByIDLite(context.Background(), id)
		require.NoError(t, err)
	}
	cache := &videoRouteBillingCache{bindings: map[string]int64{}, pending: map[string][]byte{}, billed: map[string]bool{}}
	settlement := &videoRouteSettlement{}
	usage := smartRouteHandlerUsage{logs: make(chan *service.UsageLog, 4)}
	task := strings.NewReplacer("/", "-", "=", "-").Replace(t.Name())
	upstream := &grokMediaSlotUpstream{call: func(req *http.Request, accountID int64) (*http.Response, error) {
		require.Equal(t, account.ID, accountID)
		body, contentType := `{"request_id":"`+task+`","status":"pending"}`, "application/json"
		if req.Method == http.MethodGet {
			body = `{"request_id":"` + task + `","status":"done","model":"grok-imagine-video","video":{"url":"https://vidgen.x.ai/test.mp4","duration":6}}`
			if req.URL.Host == "vidgen.x.ai" {
				body, contentType = "synthetic-video-content", "video/mp4"
			}
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}}
	// Only eligibility storage is bypassed; handlers, routing, media pricing
	// and settlement command construction all use their production code.
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, &config.Config{RunMode: config.RunModeSimple}, nil)
	t.Cleanup(billingCache.Stop)
	gateway := service.NewOpenAIGatewayService(accountRepo, usage, settlement, nil, nil, nil, cache, cfg, snapshot, nil,
		service.NewBillingService(cfg, nil), nil, billingCache, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	concurrency := service.NewConcurrencyService(testutil.StubConcurrencyCache{})
	h := NewOpenAIGatewayHandler(gateway, concurrency, billingCache, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
	key := &service.APIKey{ID: 71, UserID: 42, User: &service.User{ID: 42, Status: service.StatusActive}, GroupID: &primary.ID, Group: primary, RouteGroupIDs: []int64{9, 17}}
	return &videoRouteBillingFixture{h: h, key: key, cache: cache, settlement: settlement, usage: usage, upstream: upstream, task: task}
}

func (f *videoRouteBillingFixture) request(method, endpoint string) *httptest.ResponseRecorder {
	body, path := "", "/v1/videos/"+f.task
	if method == http.MethodPost {
		body, path = `{"model":"grok-imagine-video","prompt":"test","duration":6,"resolution":"720p"}`, "/v1/videos/generations"
	} else if endpoint == "content" {
		path += "/content"
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	if f.beforeWrite != nil {
		c.Writer = &videoRouteResponseWriter{ResponseWriter: c.Writer, beforeWrite: f.beforeWrite}
	}
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(service.ContextWithAPIKeyRoute(context.Background(), f.key))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "request_id", Value: f.task}}
	c.Set(string(middleware.ContextKeyAPIKey), f.key)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: f.key.UserID, Concurrency: 0})
	if method == http.MethodPost {
		f.h.GrokVideoGeneration(c)
	} else if endpoint == "content" {
		f.h.GrokVideoContent(c)
	} else {
		f.h.GrokVideoStatus(c)
	}
	return recorder
}
