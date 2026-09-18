package service

import (
	"context"
	"errors"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCodexTicketSnapshotRefreshIsBoundedAndNonblocking(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	var calls atomic.Int64
	settings := NewSettingService(&codexTicketLifecycleSettings{get: func(ctx context.Context, _ string) (string, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return "true", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}}, &config.Config{})
	require.False(t, settings.GetOpenAICodexTicketEnabledSnapshot(true))
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background settings refresh did not start")
	}
	baseline := runtime.NumGoroutine()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for range 1000 {
			settings.GetOpenAICodexTicketEnabledSnapshot(true)
		}
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("gateway waited for settings storage")
	}
	require.Equal(t, int64(1), calls.Load())
	require.Less(t, runtime.NumGoroutine()-baseline, 10, "each candidate must not leave a waiter behind")
	once.Do(func() { close(release) })
	require.Eventually(t, func() bool { return settings.GetOpenAICodexTicketEnabledSnapshot(false) }, time.Second, time.Millisecond)
}

func TestCodexTicketOldReadCannotUndoAdminDisable(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	settings := NewSettingService(&codexTicketLifecycleSettings{get: func(context.Context, string) (string, error) {
		close(started)
		<-release
		return "true", nil
	}}, &config.Config{})
	result := make(chan bool, 1)
	go func() { result <- settings.GetOpenAICodexTicketEnabled(context.Background(), true) }()
	<-started
	settings.publishOpenAICodexTicketSettings(false, "")
	once.Do(func() { close(release) })
	require.False(t, <-result)
	require.False(t, settings.GetOpenAICodexTicketEnabledSnapshot(true))
}

func TestCodexTicketSettingsFailureDoesNotReuseExpiredEnable(t *testing.T) {
	settings := NewSettingService(&codexTicketLifecycleSettings{get: func(context.Context, string) (string, error) {
		return "", errors.New("settings unavailable")
	}}, &config.Config{})
	settings.openAICodexTicketEnabledCache.Store(&cachedOpenAICodexTicketEnabled{value: true})
	require.False(t, settings.GetOpenAICodexTicketEnabled(context.Background(), true))
	require.False(t, settings.GetOpenAICodexTicketEnabledSnapshot(true))
}

func TestCodexTicketEmptyProxyOverridesFileAndInflightRead(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	settings := NewSettingService(&codexTicketLifecycleSettings{get: func(context.Context, string) (string, error) {
		close(started)
		<-release
		return "http://old.example:8080", nil
	}}, &config.Config{})
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{HarvestProxyURL: "http://file.example:8080"}, nil)
	svc.settingService = settings
	result := make(chan string, 1)
	go func() { result <- svc.openAICodexTicketHarvestProxyURL() }()
	<-started
	settings.publishOpenAICodexTicketSettings(true, "")
	once.Do(func() { close(release) })
	require.Empty(t, <-result)
	require.Empty(t, svc.openAICodexTicketHarvestProxyURL())
	settings = NewSettingService(&codexTicketLifecycleSettings{get: func(context.Context, string) (string, error) { return "", nil }}, &config.Config{})
	svc.settingService = settings
	require.Empty(t, svc.openAICodexTicketHarvestProxyURL())
}

func TestCodexTicketProbeDropsResultAfterRemoteDisable(t *testing.T) {
	var enabled atomic.Bool
	enabled.Store(true)
	settings := NewSettingService(&codexTicketLifecycleSettings{get: func(_ context.Context, key string) (string, error) {
		if key == SettingKeyOpenAICodexTicketEnabled {
			if enabled.Load() {
				return "true", nil
			}
			return "false", nil
		}
		return "", ErrSettingNotFound
	}}, &config.Config{})
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	upstream := &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) {
		close(started)
		<-release
		return codexTicketResponse(), nil
	}}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, HarvestProxyURL: "http://proxy.example:8080"}, upstream)
	svc.settingService = settings
	repo := &codexTicketRefreshRepo{}
	svc.accountRepo = repo
	go func() {
		svc.probeOnceOpenAICodexTicket(context.Background(), ticketTestAccount(41), "gpt-6-astra")
		close(done)
	}()
	<-started
	enabled.Store(false)
	once.Do(func() { close(release) })
	<-done
	require.Empty(t, repo.updates)
	require.Nil(t, svc.lookupOpenAICodexTicket(ticketTestAccount(41), "gpt-6-astra"))
}

func TestCodexTicketHarvestConcurrencyAndCacheCleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var active, maximum, calls atomic.Int64
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	upstream := &codexTicketFuncUpstream{do: func(req *http.Request) (*http.Response, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for previous := maximum.Load(); current > previous && !maximum.CompareAndSwap(previous, current); previous = maximum.Load() {
		}
		if calls.Add(1) == 8 {
			close(started)
		}
		select {
		case <-release:
			return codexTicketResponse(), nil
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}}
	repo := &codexTicketRefreshRepo{}
	for id := int64(1); id <= 20; id++ {
		account := ticketTestAccount(id)
		account.Status = StatusActive
		repo.accounts = append(repo.accounts, *account)
	}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, Models: []string{"gpt-6-astra"}, HarvestProxyURL: "http://proxy.example:8080"}, upstream)
	svc.accountRepo = repo
	go func() { svc.refreshOpenAICodexTickets(ctx); close(done) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("bounded collection did not start")
	}
	require.Equal(t, int64(8), active.Load())
	once.Do(func() { close(release) })
	<-done
	require.Equal(t, int64(20), calls.Load())
	require.LessOrEqual(t, maximum.Load(), int64(8))
	require.NotNil(t, svc.lookupOpenAICodexTicket(ticketTestAccount(1), "gpt-6-astra"))
	repo.accounts = nil
	svc.refreshOpenAICodexTickets(ctx)
	require.Nil(t, svc.lookupOpenAICodexTicket(ticketTestAccount(1), "gpt-6-astra"))
	// Expired entries are removed even after the collection proxy is cleared.
	svc.cfg.Gateway.OpenAICodexTicket.HarvestProxyURL = ""
	svc.openaiCodexTickets.Store("expired", &openAICodexTicket{ExpiresAt: time.Now().Add(-time.Second)})
	svc.refreshOpenAICodexTickets(ctx)
	_, exists := svc.openaiCodexTickets.Load("expired")
	require.False(t, exists)
}
