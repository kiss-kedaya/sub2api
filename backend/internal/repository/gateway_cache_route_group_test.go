package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newRouteGroupTestStore(t *testing.T) (*miniredis.Miniredis, service.GatewayCache, service.RouteGroupBindingCache) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewGatewayCache(client)
	store, ok := cache.(service.RouteGroupBindingCache)
	require.True(t, ok, "the Redis gateway cache must be able to bind a session to a route group")
	return server, cache, store
}

func TestGatewayCacheRouteGroupBindingRoundTrip(t *testing.T) {
	server, _, store := newRouteGroupTestStore(t)
	ctx := context.Background()

	_, err := store.GetRouteGroup(ctx, 7, "claude-opus-5", "sess")
	require.ErrorIs(t, err, service.ErrStickySessionNotFound, "a session never seen is a miss, not a failure")

	require.NoError(t, store.SetRouteGroup(ctx, 7, "claude-opus-5", "sess", 42, time.Hour))
	got, err := store.GetRouteGroup(ctx, 7, "claude-opus-5", "sess")
	require.NoError(t, err)
	require.Equal(t, int64(42), got)
	require.Equal(t, time.Hour, server.TTL(buildRouteGroupKey(7, "claude-opus-5", "sess")))

	// Binding is per API key, per model and per session.
	for name, lookup := range map[string]func() (int64, error){
		"another key":     func() (int64, error) { return store.GetRouteGroup(ctx, 8, "claude-opus-5", "sess") },
		"another model":   func() (int64, error) { return store.GetRouteGroup(ctx, 7, "claude-sonnet-5", "sess") },
		"another session": func() (int64, error) { return store.GetRouteGroup(ctx, 7, "claude-opus-5", "other") },
	} {
		_, err := lookup()
		require.ErrorIs(t, err, service.ErrStickySessionNotFound, name)
	}
}

func TestGatewayCacheRouteGroupRebindReplacesGroupAndRestartsLifetime(t *testing.T) {
	server, _, store := newRouteGroupTestStore(t)
	ctx := context.Background()
	key := buildRouteGroupKey(7, "claude-opus-5", "sess")

	require.NoError(t, store.SetRouteGroup(ctx, 7, "claude-opus-5", "sess", 42, time.Hour))
	server.FastForward(30 * time.Minute)
	require.Equal(t, 30*time.Minute, server.TTL(key))

	require.NoError(t, store.SetRouteGroup(ctx, 7, "claude-opus-5", "sess", 43, time.Hour))
	require.Equal(t, time.Hour, server.TTL(key), "a rebind starts a new lifetime")
	got, err := store.GetRouteGroup(ctx, 7, "claude-opus-5", "sess")
	require.NoError(t, err)
	require.Equal(t, int64(43), got)

	server.FastForward(time.Hour + time.Second)
	_, err = store.GetRouteGroup(ctx, 7, "claude-opus-5", "sess")
	require.ErrorIs(t, err, service.ErrStickySessionNotFound, "an idle binding expires, so a returning session is judged again")
}

func TestGatewayCacheRouteGroupAndAccountStickyDoNotCollide(t *testing.T) {
	_, cache, store := newRouteGroupTestStore(t)
	ctx := context.Background()

	require.NoError(t, cache.SetSessionAccountID(ctx, 1, "sess", 99, time.Hour))
	require.NoError(t, store.SetRouteGroup(ctx, 1, "m", "sess", 5, time.Hour))

	account, err := cache.GetSessionAccountID(ctx, 1, "sess")
	require.NoError(t, err)
	require.Equal(t, int64(99), account)
	group, err := store.GetRouteGroup(ctx, 1, "m", "sess")
	require.NoError(t, err)
	require.Equal(t, int64(5), group)
}

func TestGatewayCacheRouteGroupReportsRedisTroubleAsAnError(t *testing.T) {
	server, _, store := newRouteGroupTestStore(t)
	server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := store.GetRouteGroup(ctx, 7, "claude-opus-5", "sess")
	require.Error(t, err)
	require.NotErrorIs(t, err, service.ErrStickySessionNotFound, "an outage must not look like a new session")
	require.Error(t, store.SetRouteGroup(ctx, 7, "claude-opus-5", "sess", 42, time.Hour))
}

func TestBuildRouteGroupKeyIsShortDigestedAndDistinct(t *testing.T) {
	hostile := strings.Repeat("x", 10_000) + "\r\nDEL *\r\n"
	key := buildRouteGroupKey(7, "Claude-Opus-5\n", hostile)
	require.True(t, strings.HasPrefix(key, routeGroupPrefix+"7:"))
	require.LessOrEqual(t, len(key), 64, "model and session are digested, whatever the client sent")
	require.NotContains(t, key, "\n")
	require.NotContains(t, key, " ")

	require.Equal(t, key, buildRouteGroupKey(7, "Claude-Opus-5\n", hostile), "stable")
	require.NotEqual(t, key, buildRouteGroupKey(7, "Claude-Opus-5\n", hostile+"y"))
	require.NotEqual(t, key, buildRouteGroupKey(7, "claude-opus-5", hostile))
	require.NotEqual(t, key, buildRouteGroupKey(8, "Claude-Opus-5\n", hostile))
}
