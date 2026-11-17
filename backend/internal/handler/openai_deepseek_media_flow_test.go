//go:build unit

package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type deepSeekNativeHandlerUpstream struct {
	client *http.Client
	mu     sync.Mutex
	ids    []int64
}

func (u *deepSeekNativeHandlerUpstream) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.ids = append(u.ids, id)
	u.mu.Unlock()
	return u.client.Do(req)
}

func (u *deepSeekNativeHandlerUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func deepSeekNativeHandler(t *testing.T, upstream *httptest.Server) (*OpenAIGatewayHandler, *deepSeekNativeHandlerUpstream) {
	t.Helper()
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Gateway.MaxAccountSwitches = 1
	group := &service.Group{ID: 36, Platform: service.PlatformDeepseek, Status: service.StatusActive, Hydrated: true, RateMultiplier: .3}
	accounts := make([]service.Account, 2)
	pointers := make([]*service.Account, 2)
	for i := range accounts {
		accounts[i] = service.Account{ID: int64(i + 1), Platform: service.PlatformDeepseek, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Priority: i, GroupIDs: []int64{36},
			Credentials: map[string]any{"base_url": upstream.URL, "api_key": "native-fixture-" + string(rune('1'+i)),
				"model_mapping": map[string]any{"deepseek-fixture": "deepseek-fixture"}}}
		pointers[i] = &accounts[i]
	}
	repo := openAIImagesFailoverAccountRepo{accounts: accounts}
	snapshot := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: pointers}, nil, repo,
		smartRouteHandlerGroups{groups: map[int64]*service.Group{36: group}}, cfg)
	_, err := snapshot.GetGroupByIDLite(context.Background(), 36)
	require.NoError(t, err)
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	transport := &deepSeekNativeHandlerUpstream{client: upstream.Client()}
	gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, snapshot, nil,
		service.NewBillingService(cfg, nil), nil, billing, transport, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	t.Cleanup(gateway.CloseOpenAIWSPool)
	return NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg), transport
}

func TestDeepSeekMediaHandlerSwitchesBeforeResponseOnly(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer native-fixture-1" {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, err := io.WriteString(w, `{"error":{"message":"capacity"}}`)
			assert.NoError(t, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, err := io.WriteString(w, `{"text":"recognized","duration_ms":3200}`)
		assert.NoError(t, err)
	}))
	defer upstream.Close()
	h, transport := deepSeekNativeHandler(t, upstream)
	c, recorder := deepSeekHandlerContext(service.DeepSeekToolsPathASR, "multipart/form-data; boundary=native", "uploaded-file")
	key, ok := middleware2.GetAPIKeyFromContext(c)
	require.True(t, ok)
	key.Group.Status, key.Group.Hydrated = service.StatusActive, true
	h.DeepSeekToolsASR(c)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "recognized")
	transport.mu.Lock()
	defer transport.mu.Unlock()
	require.Equal(t, []int64{1, 2}, transport.ids)
}

func TestDeepSeekTTSHandlerAbortsTruncatedStreamWithoutReplay(t *testing.T) {
	for _, contentLength := range []bool{true, false} {
		t.Run(map[bool]string{true: "content-length", false: "chunked"}[contentLength], func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "audio/wav")
				if contentLength {
					w.Header().Set("Content-Length", "100")
				}
				_, err := io.WriteString(w, "short-audio")
				assert.NoError(t, err)
				flusher, ok := w.(http.Flusher)
				if !assert.True(t, ok) {
					return
				}
				flusher.Flush()
				panic(http.ErrAbortHandler)
			}))
			defer upstream.Close()
			h, transport := deepSeekNativeHandler(t, upstream)
			router := gin.New()
			router.Use(middleware2.Recovery(), func(c *gin.Context) {
				group := &service.Group{ID: 36, Platform: service.PlatformDeepseek, Status: service.StatusActive, Hydrated: true}
				c.Set("api_key", &service.APIKey{ID: 1, Group: group, GroupID: &group.ID, User: &service.User{ID: 2}})
				c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 2})
				c.Next()
			})
			router.POST(service.DeepSeekToolsPathTTS, h.DeepSeekToolsTTS)
			gateway := httptest.NewServer(router)
			defer gateway.Close()
			resp, err := gateway.Client().Post(gateway.URL+service.DeepSeekToolsPathTTS, "application/json", strings.NewReader(`{"text":"hello"}`))
			require.NoError(t, err)
			defer func() { require.NoError(t, resp.Body.Close()) }()
			require.Equal(t, http.StatusOK, resp.StatusCode)
			_, err = io.ReadAll(resp.Body)
			require.Error(t, err, "the caller must observe interrupted audio, not clean EOF")
			transport.mu.Lock()
			defer transport.mu.Unlock()
			require.Equal(t, []int64{1}, transport.ids, "never switch after audio was delivered")
		})
	}
}
