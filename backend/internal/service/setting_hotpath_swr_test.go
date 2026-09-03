package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// hotSettingSWRRepo deliberately blocks GetValue.  If a snapshot-only getter
// ever performs the repository read inline, the call below would not return
// until release is closed; the timeout assertion therefore protects the
// request-path zero-DB contract without relying on timing a fast fake query.
type hotSettingSWRRepo struct {
	SettingRepository
	values  map[string]string
	err     error
	calls   atomic.Int64
	entered chan struct{}
	release chan struct{}
}

func (r *hotSettingSWRRepo) GetValue(ctx context.Context, key string) (string, error) {
	r.calls.Add(1)
	if r.entered != nil {
		select {
		case r.entered <- struct{}{}:
		default:
		}
	}
	if r.release != nil {
		select {
		case <-r.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if r.err != nil {
		return "", r.err
	}
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", ErrSettingNotFound
}

func snapshotOnlyContextForHotSettingTest() context.Context {
	return WithSchedulerSnapshotOnly(context.Background())
}

func assertSnapshotGetterReturnsWithoutWaiting[T any](
	t *testing.T,
	getter func(context.Context) T,
	want T,
) {
	t.Helper()
	done := make(chan T, 1)
	go func() { done <- getter(snapshotOnlyContextForHotSettingTest()) }()
	select {
	case got := <-done:
		require.Equal(t, want, got)
	case <-time.After(250 * time.Millisecond):
		t.Fatal("snapshot-only getter synchronously waited for settingRepo")
	}
}

func TestSnapshotOnlyHotSettingGettersNeverBlockOnColdRepository(t *testing.T) {
	t.Parallel()

	var betaJSON string
	betaRaw, err := json.Marshal(&BetaPolicySettings{Rules: []BetaPolicyRule{{BetaToken: "x", Action: BetaPolicyActionBlock, Scope: BetaPolicyScopeAll}}})
	require.NoError(t, err)
	betaJSON = string(betaRaw)
	fastRaw, err := json.Marshal(&OpenAIFastPolicySettings{Rules: []OpenAIFastPolicyRule{{ServiceTier: OpenAIFastTierPriority, Action: BetaPolicyActionFilter, Scope: BetaPolicyScopeAll}}})
	require.NoError(t, err)
	fastJSON := string(fastRaw)
	rectifierRaw, err := json.Marshal(&RectifierSettings{Enabled: false, ThinkingSignatureEnabled: false})
	require.NoError(t, err)
	rectifierJSON := string(rectifierRaw)

	tests := []struct {
		name  string
		key   string
		value string
		call  func(*SettingService, context.Context) any
		want  any
	}{
		{
			name:  "grok mode",
			key:   SettingKeyGrokDefaultBaseURLMode,
			value: GrokDefaultBaseURLModeUSWest2,
			call: func(s *SettingService, ctx context.Context) any {
				return s.GetGrokDefaultBaseURLMode(ctx)
			},
			want: GrokDefaultBaseURLModeCLI,
		},
		{
			name:  "beta policy",
			key:   SettingKeyBetaPolicySettings,
			value: betaJSON,
			call: func(s *SettingService, ctx context.Context) any {
				value, _ := s.GetBetaPolicySettings(ctx)
				return value
			},
			want: DefaultBetaPolicySettings(),
		},
		{
			name:  "openai fast policy",
			key:   SettingKeyOpenAIFastPolicySettings,
			value: fastJSON,
			call: func(s *SettingService, ctx context.Context) any {
				value, _ := s.GetOpenAIFastPolicySettings(ctx)
				return value
			},
			want: DefaultOpenAIFastPolicySettings(),
		},
		{
			name:  "rectifier",
			key:   SettingKeyRectifierSettings,
			value: rectifierJSON,
			call: func(s *SettingService, ctx context.Context) any {
				value, _ := s.GetRectifierSettings(ctx)
				return value
			},
			want: DefaultRectifierSettings(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &hotSettingSWRRepo{
				values:  map[string]string{tt.key: tt.value},
				entered: make(chan struct{}, 1),
				release: make(chan struct{}),
			}
			svc := NewSettingService(repo, nil)

			assertSnapshotGetterReturnsWithoutWaiting(t, func(ctx context.Context) any {
				return tt.call(svc, ctx)
			}, tt.want)

			select {
			case <-repo.entered:
			case <-time.After(time.Second):
				t.Fatal("expected asynchronous refresh to reach settingRepo")
			}
			close(repo.release)

			require.Eventually(t, func() bool {
				got := tt.call(svc, snapshotOnlyContextForHotSettingTest())
				switch value := got.(type) {
				case string:
					return value == tt.value
				case *BetaPolicySettings:
					return len(value.Rules) == 1 && value.Rules[0].BetaToken == "x"
				case *OpenAIFastPolicySettings:
					return len(value.Rules) == 1 && value.Rules[0].ServiceTier == OpenAIFastTierPriority
				case *RectifierSettings:
					return value.Enabled == false && value.ThinkingSignatureEnabled == false
				default:
					return false
				}
			}, time.Second, time.Millisecond*5, "background refresh should publish the setting")
		})
	}
}

func TestSnapshotOnlyHotSettingServesStaleValueWhileRefreshing(t *testing.T) {
	repo := &hotSettingSWRRepo{
		values:  map[string]string{SettingKeyBetaPolicySettings: `{"rules":[{"beta_token":"fresh","action":"pass","scope":"all"}]}`},
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	svc := NewSettingService(repo, nil)
	stale := &BetaPolicySettings{Rules: []BetaPolicyRule{{BetaToken: "stale", Action: BetaPolicyActionFilter, Scope: BetaPolicyScopeAll}}}
	svc.betaPolicyCache.Store(&cachedBetaPolicySettings{settings: stale, expiresAt: time.Now().Add(-time.Second).UnixNano()})

	done := make(chan *BetaPolicySettings, 1)
	go func() {
		value, _ := svc.GetBetaPolicySettings(snapshotOnlyContextForHotSettingTest())
		done <- value
	}()
	select {
	case got := <-done:
		require.Equal(t, "stale", got.Rules[0].BetaToken)
	case <-time.After(250 * time.Millisecond):
		t.Fatal("stale snapshot getter waited for refresh")
	}
	select {
	case <-repo.entered:
	case <-time.After(time.Second):
		t.Fatal("expected stale refresh to start asynchronously")
	}
	close(repo.release)
	require.Eventually(t, func() bool {
		got, _ := svc.GetBetaPolicySettings(snapshotOnlyContextForHotSettingTest())
		return len(got.Rules) == 1 && got.Rules[0].BetaToken == "fresh"
	}, time.Second, time.Millisecond*5)
}

func TestSnapshotOnlyHotSettingRefreshBackoffSuppressesRetryStorm(t *testing.T) {
	repo := &hotSettingSWRRepo{err: errors.New("database unavailable")}
	svc := NewSettingService(repo, nil)
	ctx := snapshotOnlyContextForHotSettingTest()
	for i := 0; i < 20; i++ {
		_, _ = svc.GetRectifierSettings(ctx)
	}
	require.Eventually(t, func() bool { return repo.calls.Load() == 1 }, time.Second, time.Millisecond*5)
	for i := 0; i < 20; i++ {
		_, _ = svc.GetRectifierSettings(ctx)
	}
	time.Sleep(25 * time.Millisecond)
	require.Equal(t, int64(1), repo.calls.Load(), "failed refresh must be protected by backoff")
}

func TestSnapshotOnlyUngroupedKeySettingDoesNotWaitForRepository(t *testing.T) {
	repo := &hotSettingSWRRepo{
		values:  map[string]string{SettingKeyAllowUngroupedKeyScheduling: "true"},
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	svc := NewSettingService(repo, nil)

	done := make(chan bool, 1)
	go func() {
		done <- svc.IsUngroupedKeySchedulingAllowed(snapshotOnlyContextForHotSettingTest())
	}()
	select {
	case got := <-done:
		require.False(t, got, "cold snapshot should fail closed until the setting is loaded")
	case <-time.After(250 * time.Millisecond):
		t.Fatal("snapshot-only ungrouped-key getter waited for settingRepo")
	}
	select {
	case <-repo.entered:
	case <-time.After(time.Second):
		t.Fatal("expected asynchronous ungrouped-key refresh")
	}
	close(repo.release)
	require.Eventually(t, func() bool {
		return svc.IsUngroupedKeySchedulingAllowed(snapshotOnlyContextForHotSettingTest())
	}, time.Second, time.Millisecond*5)
	require.Equal(t, int64(1), repo.calls.Load())
}

func TestNonSnapshotHotSettingGettersKeepSynchronousRepositorySemantics(t *testing.T) {
	repo := &hotSettingSWRRepo{values: map[string]string{
		SettingKeyGrokDefaultBaseURLMode:   GrokDefaultBaseURLModeAPI,
		SettingKeyBetaPolicySettings:       `{"rules":[]}`,
		SettingKeyOpenAIFastPolicySettings: `{"rules":[]}`,
		SettingKeyRectifierSettings:        `{"enabled":false,"thinking_signature_enabled":false,"thinking_budget_enabled":false}`,
	}}
	svc := NewSettingService(repo, nil)
	require.Equal(t, GrokDefaultBaseURLModeAPI, svc.GetGrokDefaultBaseURLMode(context.Background()))
	beta, err := svc.GetBetaPolicySettings(context.Background())
	require.NoError(t, err)
	require.Empty(t, beta.Rules)
	fast, err := svc.GetOpenAIFastPolicySettings(context.Background())
	require.NoError(t, err)
	require.Empty(t, fast.Rules)
	rectifier, err := svc.GetRectifierSettings(context.Background())
	require.NoError(t, err)
	require.False(t, rectifier.Enabled)
	require.Equal(t, int64(4), repo.calls.Load(), "non-marker callers must keep the synchronous repository path")
}
