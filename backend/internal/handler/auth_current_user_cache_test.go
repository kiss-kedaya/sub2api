//go:build unit

package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newCurrentUserContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 31})
	return c, recorder
}

// The endpoint is the busiest API route in the product: a second poll inside the
// reuse window must not reach the database again.
func TestGetCurrentUserReusesPayloadInsideTheWindow(t *testing.T) {
	repo := &userHandlerRepoStub{
		user: &service.User{
			ID:       31,
			Email:    "me@example.com",
			Username: "linuxdo-handle",
			Role:     service.RoleUser,
			Status:   service.StatusActive,
		},
	}
	handler := &AuthHandler{userService: service.NewUserService(repo, nil, nil, nil)}
	now := time.Date(2026, 11, 18, 10, 30, 0, 0, time.UTC)
	handler.currentUserCache.now = func() time.Time { return now }

	c1, r1 := newCurrentUserContext(t)
	handler.GetCurrentUser(c1)
	require.Equal(t, http.StatusOK, r1.Code)
	firstCalls := repo.getByIDCalls

	now = now.Add(time.Second)
	c2, r2 := newCurrentUserContext(t)
	handler.GetCurrentUser(c2)
	require.Equal(t, http.StatusOK, r2.Code)

	require.Equal(t, firstCalls, repo.getByIDCalls, "a poll inside the TTL must not query the database again")
	require.JSONEq(t, r1.Body.String(), r2.Body.String(), "cached payload must match the fresh one")
}

// Once the window closes the handler must go back to the database so a balance
// change is visible again.
func TestGetCurrentUserRefreshesAfterTheWindow(t *testing.T) {
	repo := &userHandlerRepoStub{
		user: &service.User{
			ID:       31,
			Email:    "me@example.com",
			Username: "linuxdo-handle",
			Role:     service.RoleUser,
			Status:   service.StatusActive,
		},
	}
	handler := &AuthHandler{userService: service.NewUserService(repo, nil, nil, nil)}
	now := time.Date(2026, 11, 18, 10, 30, 0, 0, time.UTC)
	handler.currentUserCache.now = func() time.Time { return now }

	c1, _ := newCurrentUserContext(t)
	handler.GetCurrentUser(c1)
	firstCalls := repo.getByIDCalls

	now = now.Add(authCurrentUserCacheTTL + time.Second)
	c2, _ := newCurrentUserContext(t)
	handler.GetCurrentUser(c2)

	require.Greater(t, repo.getByIDCalls, firstCalls, "an expired entry must be refetched")
}

func TestCurrentUserCacheKillSwitch(t *testing.T) {
	t.Setenv(authCurrentUserCacheOffEnv, "true")
	cache := &authCurrentUserCache{}
	cache.put(31, &currentUserResponse{RunMode: "standard"})
	if _, ok := cache.get(31); ok {
		t.Fatalf("cache must stay disabled when %s is true", authCurrentUserCacheOffEnv)
	}
}

func TestCurrentUserCacheDropsExpiredEntriesWhenFull(t *testing.T) {
	cache := &authCurrentUserCache{}
	now := time.Date(2026, 11, 18, 10, 30, 0, 0, time.UTC)
	cache.now = func() time.Time { return now }

	// Fill with expired entries plus one live one.
	now = now.Add(authCurrentUserCacheTTL * 2)
	for i := int64(1); i <= 5000; i++ {
		cache.entriesSet(t, i, now.Add(-time.Minute))
	}
	cache.put(9999, &currentUserResponse{RunMode: "standard"})

	if _, ok := cache.get(1); ok {
		t.Fatal("expired entry should have been swept")
	}
	if _, ok := cache.get(9999); !ok {
		t.Fatal("live entry must survive the sweep")
	}
}

// entriesSet inserts an entry with an explicit expiry, keeping the production
// map shape so the sweep path is exercised.
func (c *authCurrentUserCache) entriesSet(t *testing.T, userID int64, expires time.Time) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[int64]authCurrentUserCacheEntry, 64)
	}
	c.entries[userID] = authCurrentUserCacheEntry{
		value:   &currentUserResponse{RunMode: "standard"},
		expires: expires,
	}
}

// Guard the JSON contract: the cached payload keeps the run_mode field.
func TestCurrentUserResponseKeepsRunMode(t *testing.T) {
	raw, err := json.Marshal(currentUserResponse{RunMode: "standard"})
	require.NoError(t, err)
	require.Contains(t, string(raw), `"run_mode":"standard"`)
}
