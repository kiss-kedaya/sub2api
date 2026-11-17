package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func openAITransientFallbackAccount(id, groupID int64) *Account {
	return &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 2, Priority: int(id), GroupIDs: []int64{groupID},
		Credentials: map[string]any{"model_mapping": map[string]any{"model-a": "model-a", "model-b": "model-b"}},
		Extra:       map[string]any{"openai_compact_supported": true}}
}

func newOpenAITransientFallbackFixture(advanced, loadBatch bool, accounts ...*Account) (*OpenAIGatewayService, *smartRouteCoreSnapshotCache, context.Context, []*Group) {
	groups := []*Group{
		{ID: 10, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true},
		{ID: 20, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true},
	}
	snapshot, cache := newSmartRouteCoreSnapshot(groups, accounts...)
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = loadBatch
	svc := &OpenAIGatewayService{schedulerSnapshot: snapshot, cfg: cfg,
		cache: &schedulerTestGatewayCache{}, concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
		rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(fmt.Sprint(advanced))}
	// Warm settings before snapshot-only selection: its cold-cache default is
	// intentionally legacy while an asynchronous refresh runs.
	_ = svc.openAIAdvancedSchedulerRuntimeSettings(context.Background())
	return svc, cache, svc.PrepareSchedulerRequestContext(context.Background()), groups
}

func pauseOpenAITransientModel(t *testing.T, svc *OpenAIGatewayService, account *Account) {
	t.Helper()
	for range 3 {
		svc.recordOpenAIAccountModelTransientFailure(account, "model-a", time.Now())
	}
	require.True(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "model-a"))
}

func TestOpenAIModelTransientFallbackScheduler(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		for _, loadBatch := range []bool{false, true} {
			for _, tc := range []struct {
				name      string
				second    bool
				allPaused bool
				wantID    int64
			}{
				{name: "single paused", wantID: 1},
				{name: "healthy alternative", second: true, wantID: 2},
				{name: "all paused", second: true, allPaused: true, wantID: 1},
			} {
				t.Run(fmt.Sprintf("advanced=%t/load=%t/%s", advanced, loadBatch, tc.name), func(t *testing.T) {
					accounts := []*Account{openAITransientFallbackAccount(1, 10)}
					if tc.second {
						accounts = append(accounts, openAITransientFallbackAccount(2, 10))
					}
					svc, cache, ctx, groups := newOpenAITransientFallbackFixture(advanced, loadBatch, accounts...)
					pauseOpenAITransientModel(t, svc, accounts[0])
					if tc.allPaused {
						pauseOpenAITransientModel(t, svc, accounts[1])
					}
					selection, _, err := svc.SelectAccountWithScheduler(ctx, &groups[0].ID, "", "", "model-a", nil, OpenAIUpstreamTransportAny, false)
					require.NoError(t, err)
					if advanced && tc.allPaused {
						require.Contains(t, []int64{1, 2}, selection.Account.ID, "retain the advanced scheduler's weighted ordering")
					} else {
						require.Equal(t, tc.wantID, selection.Account.ID)
					}
					selection.ReleaseFunc()
					require.True(t, svc.isOpenAIAccountModelRuntimeBlocked(accounts[0], "model-a"), "selection must not erase the cooldown")
					if tc.second && !tc.allPaused {
						require.Equal(t, int64(1), cache.reads.Load(), "healthy alternative must win without a retry")
					}
				})
			}
		}
	}
}

func TestOpenAIModelTransientFallbackHardRejections(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		for _, tc := range []struct {
			name     string
			modify   func(*OpenAIGatewayService, *Account)
			excluded map[int64]struct{}
		}{
			{name: "account runtime block", modify: func(s *OpenAIGatewayService, a *Account) {
				until := time.Now().Add(time.Minute)
				a.TempUnschedulableUntil = &until
				s.BlockAccountScheduling(a, until, "auth_error")
			}},
			{name: "persisted rate limit", modify: func(_ *OpenAIGatewayService, a *Account) {
				until := time.Now().Add(time.Minute)
				a.RateLimitResetAt = &until
			}},
			{name: "persisted overload", modify: func(_ *OpenAIGatewayService, a *Account) {
				until := time.Now().Add(time.Minute)
				a.OverloadUntil = &until
			}},
			{name: "excluded", excluded: map[int64]struct{}{1: {}}},
			{name: "unsupported model", modify: func(_ *OpenAIGatewayService, a *Account) {
				a.Credentials = map[string]any{"model_mapping": map[string]any{"model-b": "model-b"}}
			}},
		} {
			t.Run(fmt.Sprintf("advanced=%t/%s", advanced, tc.name), func(t *testing.T) {
				account := openAITransientFallbackAccount(1, 10)
				svc, _, ctx, groups := newOpenAITransientFallbackFixture(advanced, true, account)
				pauseOpenAITransientModel(t, svc, account)
				if tc.modify != nil {
					tc.modify(svc, account)
				}
				selection, _, err := svc.SelectAccountWithScheduler(ctx, &groups[0].ID, "", "", "model-a", tc.excluded, OpenAIUpstreamTransportAny, false)
				require.ErrorIs(t, err, ErrNoAvailableAccounts)
				require.Nil(t, selection)
			})
		}
	}
}

func TestOpenAIModelTransientFallbackCompact(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		for _, supported := range []bool{false, true} {
			t.Run(fmt.Sprintf("advanced=%t/supported=%t", advanced, supported), func(t *testing.T) {
				account := openAITransientFallbackAccount(1, 10)
				account.Extra["openai_compact_supported"] = supported
				svc, _, ctx, groups := newOpenAITransientFallbackFixture(advanced, true, account)
				pauseOpenAITransientModel(t, svc, account)
				selection, _, err := svc.SelectAccountWithScheduler(ctx, &groups[0].ID, "", "", "model-a", nil, OpenAIUpstreamTransportAny, true)
				if !supported {
					require.ErrorIs(t, err, ErrNoAvailableCompactAccounts)
					require.Nil(t, selection)
					return
				}
				require.NoError(t, err)
				require.Equal(t, account.ID, selection.Account.ID)
				selection.ReleaseFunc()
			})
		}
	}
}

func TestOpenAIModelTransientFallbackKeyRoutes(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		for _, allPaused := range []bool{false, true} {
			t.Run(fmt.Sprintf("advanced=%t/allPaused=%t", advanced, allPaused), func(t *testing.T) {
				a, b := openAITransientFallbackAccount(1, 10), openAITransientFallbackAccount(2, 20)
				svc, _, ctx, groups := newOpenAITransientFallbackFixture(advanced, true, a, b)
				pauseOpenAITransientModel(t, svc, a)
				if allPaused {
					pauseOpenAITransientModel(t, svc, b)
				}
				selection, _, routed, err := svc.SelectAccountWithSchedulerForCapabilityAlongKeyRoutes(ctx, smartRouteCoreKey(groups...), "", "", "model-a", nil, OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityResponses, false, false, false)
				require.NoError(t, err)
				wantID, wantGroup := int64(2), int64(20)
				if allPaused {
					wantID, wantGroup = 1, 10
				}
				require.Equal(t, wantID, selection.Account.ID)
				require.Equal(t, wantGroup, *routed.GroupID)
				selection.ReleaseFunc()
			})
		}
	}
}

func TestOpenAIModelTransientFallbackProxyQuarantine(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		t.Run(fmt.Sprint(advanced), func(t *testing.T) {
			account := openAITransientFallbackAccount(1, 10)
			proxyID := int64(7)
			account.ProxyID = &proxyID
			svc, _, ctx, groups := newOpenAITransientFallbackFixture(advanced, true, account)
			pauseOpenAITransientModel(t, svc, account)
			svc.openaiProxyStreamCircuit = newOpenAIProxyStreamCircuit(openAIProxyStreamCircuitSettings{failureThreshold: 1})
			svc.openaiProxyStreamCircuit.recordFailure(proxyID, time.Now())
			require.True(t, svc.isOpenAIProxyStreamQuarantined(ctx, account))
			selection, _, err := svc.SelectAccountWithScheduler(ctx, &groups[0].ID, "", "", "model-a", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.Equal(t, account.ID, selection.Account.ID)
			selection.ReleaseFunc()
		})
	}
}

func TestOpenAIModelTransientFallbackOtherEntrypoints(t *testing.T) {
	for _, entry := range []string{"model", "exclusions", "load", "token count", "capability", "images", "images key routes"} {
		t.Run(entry, func(t *testing.T) {
			account := openAITransientFallbackAccount(1, 10)
			svc, _, ctx, groups := newOpenAITransientFallbackFixture(false, true, account)
			pauseOpenAITransientModel(t, svc, account)
			var selected *Account
			var selection *AccountSelectionResult
			var err error
			switch entry {
			case "model":
				selected, err = svc.SelectAccountForModel(ctx, &groups[0].ID, "", "model-a")
			case "exclusions":
				selected, err = svc.SelectAccountForModelWithExclusions(ctx, &groups[0].ID, "", "model-a", nil)
			case "token count":
				selected, err = svc.SelectAccountForTokenCount(ctx, &groups[0].ID, "", "model-a", OpenAIEndpointCapabilityResponses, PlatformOpenAI)
			case "load":
				selection, err = svc.SelectAccountWithLoadAwareness(ctx, &groups[0].ID, "", "model-a", nil)
			case "capability":
				selection, _, err = svc.SelectAccountWithSchedulerForCapability(ctx, &groups[0].ID, "", "", "model-a", nil, OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityResponses, false, false, false)
			case "images":
				selection, _, err = svc.SelectAccountWithSchedulerForImages(ctx, &groups[0].ID, "", "model-a", nil, OpenAIImagesCapabilityNative)
			case "images key routes":
				selection, _, _, err = svc.SelectAccountWithSchedulerForImagesAlongKeyRoutes(ctx, smartRouteCoreKey(groups[0]), "", "model-a", nil, OpenAIImagesCapabilityNative)
			}
			require.NoError(t, err)
			if selection != nil {
				selected = selection.Account
				selection.ReleaseFunc()
			}
			require.NotNil(t, selected)
			require.Equal(t, account.ID, selected.ID)
		})
	}
}

func TestOpenAIModelTransientFallbackSticky(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		for _, second := range []bool{false, true} {
			t.Run(fmt.Sprintf("advanced=%t/alternative=%t", advanced, second), func(t *testing.T) {
				a := openAITransientFallbackAccount(1, 10)
				accounts := []*Account{a}
				if second {
					accounts = append(accounts, openAITransientFallbackAccount(2, 10))
				}
				svc, _, ctx, groups := newOpenAITransientFallbackFixture(advanced, true, accounts...)
				pauseOpenAITransientModel(t, svc, a)
				require.NoError(t, svc.BindStickySession(ctx, &groups[0].ID, "session-a", a.ID))
				selection, _, err := svc.SelectAccountWithScheduler(ctx, &groups[0].ID, "", "session-a", "model-a", nil, OpenAIUpstreamTransportAny, false)
				require.NoError(t, err)
				want := a.ID
				if second {
					want = 2
				}
				require.Equal(t, want, selection.Account.ID)
				selection.ReleaseFunc()
				bound, err := svc.getStickySessionAccountID(ctx, &groups[0].ID, "session-a")
				require.NoError(t, err)
				require.Equal(t, want, bound)
			})
		}
	}
}

func TestOpenAIModelTransientFallbackPreviousResponse(t *testing.T) {
	for _, direct := range []bool{false, true} {
		for _, withRepo := range []bool{false, true} {
			t.Run(fmt.Sprintf("direct=%t/repo=%t", direct, withRepo), func(t *testing.T) {
				account := openAITransientFallbackAccount(1, 10)
				svc, _, ctx, groups := newOpenAITransientFallbackFixture(true, true, account)
				if withRepo {
					svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{*account}}
				}
				svc.openaiWSStateStore = NewOpenAIWSStateStore(nil)
				require.NoError(t, svc.openaiWSStateStore.BindResponseAccount(ctx, 10, "resp_paused", account.ID, time.Hour))
				pauseOpenAITransientModel(t, svc, account)
				var selection *AccountSelectionResult
				var err error
				if direct {
					selection, err = svc.SelectAccountByPreviousResponseID(ctx, &groups[0].ID, "resp_paused", "model-a", nil, false)
				} else {
					var decision OpenAIAccountScheduleDecision
					selection, decision, err = svc.SelectAccountWithScheduler(ctx, &groups[0].ID, "resp_paused", "", "model-a", nil, OpenAIUpstreamTransportAny, false)
					require.True(t, decision.StickyPreviousHit, "the model retry must retain the continuation binding")
				}
				require.NoError(t, err)
				require.NotNil(t, selection)
				require.Equal(t, account.ID, selection.Account.ID)
				selection.ReleaseFunc()
				bound, err := svc.openaiWSStateStore.GetResponseAccount(ctx, 10, "resp_paused")
				require.NoError(t, err)
				require.Equal(t, account.ID, bound)
			})
		}
	}
}

func TestOpenAIModelTransientFallbackBoundWSKeepsExistingRefreshPolicy(t *testing.T) {
	for _, durable := range []bool{false, true} {
		t.Run(fmt.Sprint(durable), func(t *testing.T) {
			account := openAITransientFallbackAccount(1, 10)
			svc, _, ctx, _ := newOpenAITransientFallbackFixture(false, false, account)
			if durable {
				repo := &schedulerFreshnessRepoStub{projection: map[int64]SchedulerFreshness{
					1: {ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, GroupIDs: []int64{10}},
				}}
				svc.accountRepo = repo
				ctx = withSchedulerFreshness(context.Background(), repo, svc.schedulerSnapshot, account.ID)
			}
			pauseOpenAITransientModel(t, svc, account)
			_, ok := svc.RefreshSchedulerAccountFreshness(ctx, account, "model-a")
			require.Equal(t, !durable, ok, "snapshot-only turns still admit; durable refresh still requests reconnect")
			require.Nil(t, ctx.Value(modelTransientFallbackContextKey{}), "selection bypass never escapes into later WS turns")
		})
	}
}

func TestOpenAIModelTransientFallbackRuntimeBypassKeepsHardGates(t *testing.T) {
	for _, gate := range []string{"model only", "runtime", "ticket"} {
		t.Run(gate, func(t *testing.T) {
			account := openAITransientFallbackAccount(1, 10)
			svc, _, ctx, _ := newOpenAITransientFallbackFixture(false, false, account)
			pauseOpenAITransientModel(t, svc, account)
			switch gate {
			case "runtime":
				until := time.Now().Add(time.Minute)
				account.TempUnschedulableUntil = &until
				svc.BlockAccountScheduling(account, until, "auth_error")
			case "ticket":
				account.Type = AccountTypeOAuth
				svc.cfg.Gateway.OpenAICodexTicket = config.OpenAICodexTicketConfig{Enabled: true, TargetLength: 292, FailClosed: true, Models: []string{"model-a"}}
			}
			ctx = context.WithValue(ctx, modelTransientFallbackContextKey{}, &modelTransientFallbackProbe{bypass: true})
			require.Equal(t, gate != "model only", svc.isOpenAIAccountRequestRuntimeBlocked(ctx, account, "model-a", false))
		})
	}
}

func TestOpenAIModelTransientFallbackOutcome(t *testing.T) {
	account := openAITransientFallbackAccount(1, 10)
	svc, _, ctx, groups := newOpenAITransientFallbackFixture(false, false, account)
	pauseOpenAITransientModel(t, svc, account)
	selection, _, err := svc.SelectAccountWithScheduler(ctx, &groups[0].ID, "", "", "model-a", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	selection.ReleaseFunc()
	state := svc.getOpenAIAccountModelTransientState()
	key, ok := openAIAccountModelTransientKey(account.ID, "model-a")
	require.True(t, ok)
	state.mu.Lock()
	previous := state.entries[key]
	state.mu.Unlock()
	decision := svc.recordOpenAIAccountModelTransientFailure(account, "model-a", time.Now().Add(time.Second))
	require.Equal(t, 4, decision.FailureStreak)
	require.True(t, decision.BlockUntil.After(previous.blockUntil))
	require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "model-b"))
	svc.ReportOpenAIAccountScheduleResult(account, "model-a", true, nil)
	require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "model-a"))
	require.Zero(t, state.size())
}
