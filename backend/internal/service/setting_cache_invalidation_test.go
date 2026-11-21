//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// inMemorySettingRepo is a minimal shared settings store. Two SettingService
// instances pointed at the same repo model two replicas writing/reading the same
// database.
type inMemorySettingRepo struct {
	mu     sync.RWMutex
	values map[string]string
}

func newInMemorySettingRepo() *inMemorySettingRepo {
	return &inMemorySettingRepo{values: map[string]string{}}
}

func (r *inMemorySettingRepo) Get(_ context.Context, key string) (*Setting, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.values[key]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return &Setting{Key: key, Value: value}, nil
}

func (r *inMemorySettingRepo) GetValue(_ context.Context, key string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *inMemorySettingRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[key] = value
	return nil
}

func (r *inMemorySettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (r *inMemorySettingRepo) SetMultiple(_ context.Context, settings map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, value := range settings {
		r.values[key] = value
	}
	return nil
}

func (r *inMemorySettingRepo) GetAll(_ context.Context) (map[string]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(r.values))
	for key, value := range r.values {
		out[key] = value
	}
	return out, nil
}

func (r *inMemorySettingRepo) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.values, key)
	return nil
}

// settingInvalidationHub is a synchronous in-memory stand-in for the Redis
// pub/sub transport. A publish fans out to every subscriber before returning,
// which keeps the test deterministic (no sleeps needed to observe a broadcast).
type settingInvalidationHub struct {
	mu          sync.Mutex
	subscribers []func()
	publishErr  error
	published   int
}

func (h *settingInvalidationHub) PublishSettingsChanged(_ context.Context) error {
	h.mu.Lock()
	h.published++
	err := h.publishErr
	subs := append([]func(){}, h.subscribers...)
	h.mu.Unlock()
	if err != nil {
		return err
	}
	for _, notify := range subs {
		notify()
	}
	return nil
}

func (h *settingInvalidationHub) SubscribeSettingsChanged(ctx context.Context, handler func()) error {
	if handler == nil {
		return nil
	}
	h.mu.Lock()
	h.subscribers = append(h.subscribers, handler)
	h.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

func (h *settingInvalidationHub) subscriberCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subscribers)
}

func (h *settingInvalidationHub) setPublishErr(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.publishErr = err
}

func (h *settingInvalidationHub) publishCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.published
}

func startPeerSettingService(t *testing.T, repo SettingRepository, hub *settingInvalidationHub) *SettingService {
	t.Helper()
	svc := NewSettingService(repo, &config.Config{})
	svc.SetSettingsCacheInvalidationBus(hub)
	svc.StartSettingsCacheInvalidationSubscriber(context.Background())
	t.Cleanup(svc.StopSettingsCacheInvalidationSubscriber)
	return svc
}

func injectedSiteName(t *testing.T, svc *SettingService) string {
	t.Helper()
	settings, err := svc.GetPublicSettingsForInjection(context.Background())
	require.NoError(t, err)
	payload, ok := settings.(*PublicSettingsInjectionPayload)
	require.True(t, ok)
	return payload.SiteName
}

// TestSettingsCacheInvalidation_PeerReplicaSeesWrite is the regression test for
// the production bug: a settings write on one replica must invalidate the other
// replica's injected public settings, not only the writer's.
func TestSettingsCacheInvalidation_PeerReplicaSeesWrite(t *testing.T) {
	repo := newInMemorySettingRepo()
	hub := &settingInvalidationHub{}

	writer := startPeerSettingService(t, repo, hub)
	peer := startPeerSettingService(t, repo, hub)

	require.Eventually(t, func() bool { return hub.subscriberCount() == 2 },
		time.Second, 5*time.Millisecond, "both replicas should subscribe")

	require.NotEqual(t, "written-on-writer", injectedSiteName(t, peer))
	versionBefore := peer.SettingsInjectionVersion()

	require.NoError(t, writer.UpdateSettings(context.Background(), &SystemSettings{SiteName: "written-on-writer"}))

	require.Equal(t, 1, hub.publishCount(), "the write must broadcast once")
	require.Greater(t, peer.SettingsInjectionVersion(), versionBefore,
		"the peer must observe the change even without a local write")
	require.Equal(t, "written-on-writer", injectedSiteName(t, peer),
		"the peer must serve the new public settings")
}

// TestSettingsCacheInvalidation_BroadcastFailureDoesNotBlockWrite verifies the
// write path contract: a dead Redis degrades convergence, never the save.
func TestSettingsCacheInvalidation_BroadcastFailureDoesNotBlockWrite(t *testing.T) {
	repo := newInMemorySettingRepo()
	hub := &settingInvalidationHub{}

	writer := startPeerSettingService(t, repo, hub)
	peer := startPeerSettingService(t, repo, hub)
	require.Eventually(t, func() bool { return hub.subscriberCount() == 2 },
		time.Second, 5*time.Millisecond)

	hub.setPublishErr(errors.New("redis unavailable"))
	writerVersionBefore := writer.SettingsInjectionVersion()
	peerVersionBefore := peer.SettingsInjectionVersion()

	err := writer.UpdateSettings(context.Background(), &SystemSettings{SiteName: "saved-despite-broadcast-failure"})
	require.NoError(t, err, "a broadcast failure must not fail the write")

	require.Greater(t, writer.SettingsInjectionVersion(), writerVersionBefore,
		"the writer must still invalidate its own caches")
	require.Equal(t, peerVersionBefore, peer.SettingsInjectionVersion(),
		"the peer cannot have received the failed broadcast")
	require.Equal(t, "saved-despite-broadcast-failure", injectedSiteName(t, peer),
		"the peer reads storage directly, so a fresh read already sees the value; "+
			"only its cached injection keeps the old one until the TTL expires")
}

// TestSettingsCacheInvalidation_LocalOnUpdateRunsOnce covers the local write
// path: onUpdate (HTML/logo cache drop) must run exactly once per write and the
// version must move so the rendered-HTML cache can never be reused.
func TestSettingsCacheInvalidation_LocalOnUpdateRunsOnce(t *testing.T) {
	repo := newInMemorySettingRepo()
	hub := &settingInvalidationHub{}
	svc := NewSettingService(repo, &config.Config{})
	svc.SetSettingsCacheInvalidationBus(hub)

	var onUpdateCalls int
	var mu sync.Mutex
	svc.SetOnUpdateCallback(func() {
		mu.Lock()
		onUpdateCalls++
		mu.Unlock()
	})

	require.NoError(t, svc.UpdateSettings(context.Background(), &SystemSettings{SiteName: "local-change"}))

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 1, onUpdateCalls)
	require.Equal(t, uint64(1), svc.SettingsInjectionVersion())
}

// TestSettingsInjectionCacheTTL_Normalization pins the fallback TTL bounds. A
// zero/absurd value must never reintroduce an effectively permanent cache.
func TestSettingsInjectionCacheTTL_Normalization(t *testing.T) {
	cases := []struct {
		name string
		raw  int
		want time.Duration
	}{
		{name: "zero uses default", raw: 0, want: defaultSettingsInjectionCacheTTL},
		{name: "negative uses default", raw: -10, want: defaultSettingsInjectionCacheTTL},
		{name: "too small is clamped", raw: 1, want: minSettingsInjectionCacheTTL},
		{name: "in range kept", raw: 30, want: 30 * time.Second},
		{name: "too large is clamped", raw: 86400, want: maxSettingsInjectionCacheTTL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Server.SettingsInjectionCacheTTLSeconds = tc.raw
			svc := NewSettingService(nil, cfg)
			require.Equal(t, tc.want, svc.SettingsInjectionCacheTTL())
		})
	}

	// nil cfg / nil receiver must fall back safely instead of returning 0.
	require.Equal(t, defaultSettingsInjectionCacheTTL, NewSettingService(nil, nil).SettingsInjectionCacheTTL())
	var nilSvc *SettingService
	require.Equal(t, defaultSettingsInjectionCacheTTL, nilSvc.SettingsInjectionCacheTTL())
}
