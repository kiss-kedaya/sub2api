//go:build unit

package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type coldVideoLookupAccountRepo struct {
	service.AccountRepository
	account *service.Account
	err     error
}

func (r coldVideoLookupAccountRepo) GetByID(context.Context, int64) (*service.Account, error) {
	return r.account, r.err
}

func addColdVideoLookupHandler(t *testing.T, f *durableMediaHandlerFixture, repo service.AccountRepository) int {
	t.Helper()
	cfg := f.handlers[0].cfg
	billingCache := service.NewBillingCacheService(f.cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	// A cold replica has no creation-time scheduler snapshot or Redis task cache.
	// Only the shared durable repository can establish ownership and routing.
	cache := &videoRouteBillingCache{bindings: map[string]int64{}, pending: map[string][]byte{}, billed: map[string]bool{}}
	gateway := service.NewOpenAIGatewayService(repo, f.usage, f.repo, nil, nil, nil, cache, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, billingCache, f.upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	t.Cleanup(gateway.CloseOpenAIWSPool)
	concurrency := service.NewConcurrencyService(testutil.StubConcurrencyCache{})
	f.handlers = append(f.handlers, NewOpenAIGatewayHandler(gateway, concurrency, billingCache, &service.APIKeyService{}, nil, nil, nil, nil, cfg))
	return len(f.handlers) - 1
}

func TestGrokVideoDurableKnownTaskSourceFailuresAreNotMissingTasks(t *testing.T) {
	for _, scenario := range []string{"removed", "platform changed", "temporary failure"} {
		t.Run(scenario, func(t *testing.T) {
			f := newDurableMediaHandlerFixture(t)
			require.Equal(t, http.StatusOK, f.request(0, http.MethodPost, "create", nil).Code)
			current := &service.Account{ID: 17001, Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey, Concurrency: 10,
				Credentials: map[string]any{"api_key": "synthetic-video-key"}}
			repo := coldVideoLookupAccountRepo{account: current}
			expected := http.StatusGone
			if scenario == "removed" {
				repo.account, repo.err = nil, service.ErrAccountNotFound
			} else if scenario == "platform changed" {
				current.Platform = service.PlatformOpenAI
			} else {
				repo.account, repo.err = nil, errors.New("synthetic repository outage")
				expected = http.StatusServiceUnavailable
			}
			cold := addColdVideoLookupHandler(t, f, repo)
			for _, endpoint := range []string{"status", "content"} {
				response := f.request(cold, http.MethodGet, endpoint, nil)
				require.Equal(t, expected, response.Code, response.Body.String())
				require.NotContains(t, response.Body.String(), "Video request not found")
				require.NotContains(t, response.Body.String(), "17001")
				if expected == http.StatusGone {
					require.Contains(t, response.Body.String(), `"type":"video_task_unavailable"`)
				} else {
					require.Equal(t, "2", response.Header().Get("Retry-After"))
				}
			}
			job, balance, applied, failed, direct := f.repo.snapshotByTask(f.taskID)
			require.Equal(t, service.MediaBillingSubmitted, job.Status)
			require.Zero(t, balance)
			require.Zero(t, applied)
			require.Zero(t, failed)
			require.Zero(t, direct)
			create, status, content := f.upstream.counts()
			require.Equal(t, 1, create)
			require.Zero(t, status, "never query another account or reuse the stale create-time account")
			require.Zero(t, content)
		})
	}
}

func TestGrokVideoDurableColdLookupRetainsDurableTasksInEveryRecordedState(t *testing.T) {
	for _, state := range []string{service.MediaBillingSubmitted, service.MediaBillingDone, service.MediaBillingReconciliation} {
		t.Run(string(state), func(t *testing.T) {
			f := newDurableMediaHandlerFixture(t)
			require.Equal(t, http.StatusOK, f.request(0, http.MethodPost, "create", nil).Code)
			f.repo.mu.Lock()
			for _, job := range f.repo.jobs {
				job.Status = state
			}
			f.repo.mu.Unlock()
			account := &service.Account{ID: 17001, Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey, Concurrency: 10,
				Credentials: map[string]any{"api_key": "synthetic-video-key"}}
			cold := addColdVideoLookupHandler(t, f, coldVideoLookupAccountRepo{account: account})
			for _, endpoint := range []string{"status", "content"} {
				response := f.request(cold, http.MethodGet, endpoint, nil)
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			}
			_, _, applied, failed, direct := f.repo.snapshotByTask(f.taskID)
			expected := 1
			if state == service.MediaBillingDone {
				expected = 0
			}
			require.Equal(t, expected, applied)
			require.Zero(t, failed)
			require.Zero(t, direct)
		})
	}
}

func TestGrokVideoSourceErrorsRemainGatewayAttributed(t *testing.T) {
	for _, errType := range []string{"video_task_unavailable", "video_task_lookup_unavailable"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		phase, limited, owner, source := classifyOpsErrorLog(c, normalizeOpsErrorType(errType, ""), "Video task source unavailable", "", http.StatusGone)
		require.Contains(t, []string{"routing", "internal"}, phase)
		require.False(t, limited)
		require.Equal(t, "platform", owner)
		require.Equal(t, "gateway", source)
	}
}

func TestGrokVideoDurableColdLookupDoesNotRevealOtherOwners(t *testing.T) {
	for _, route := range []string{"single group", "smart route"} {
		for _, scenario := range []string{"different user", "different key", "different task"} {
			t.Run(route+"/"+scenario, func(t *testing.T) {
				f := newDurableMediaHandlerFixture(t)
				require.Equal(t, http.StatusOK, f.request(0, http.MethodPost, "create", nil).Code)
				cold := addColdVideoLookupHandler(t, f, coldVideoLookupAccountRepo{err: errors.New("unexpected source lookup for an unknown owner")})
				// Keep ordinary admission available so the test reaches ownership.
				f.repo.mu.Lock()
				f.repo.balance = 1
				f.repo.mu.Unlock()
				key := *f.key
				if route == "single group" {
					key.RouteGroupIDs = nil
				}
				taskID := f.taskID
				switch scenario {
				case "different user":
					user := *key.User
					user.ID++
					key.UserID, key.User = user.ID, &user
				case "different key":
					key.ID++
				case "different task":
					taskID += "-unknown"
				}
				for _, content := range []bool{false, true} {
					path := "/v1/videos/" + taskID
					if content {
						path += "/content"
					}
					response := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(response)
					c.Request = httptest.NewRequest(http.MethodGet, path, strings.NewReader(""))
					c.Params = gin.Params{{Key: "request_id", Value: taskID}}
					c.Set(string(middleware.ContextKeyAPIKey), &key)
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.UserID})
					if content {
						f.handlers[cold].GrokVideoContent(c)
					} else {
						f.handlers[cold].GrokVideoStatus(c)
					}
					require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
					_, selected := c.Get(opsAccountIDKey)
					require.False(t, selected)
					require.NotContains(t, response.Body.String(), "17001")
				}
				create, status, content := f.upstream.counts()
				require.Equal(t, 1, create)
				require.Zero(t, status)
				require.Zero(t, content)
			})
		}
	}
}
