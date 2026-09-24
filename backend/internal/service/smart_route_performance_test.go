package service

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type smartRouteCatalogReadCache struct {
	SchedulerCache
	mu     sync.Mutex
	groups map[int64]int
}

func (c *smartRouteCatalogReadCache) GetSnapshot(ctx context.Context, bucket SchedulerBucket) ([]*Account, bool, error) {
	c.mu.Lock()
	c.groups[bucket.GroupID]++
	c.mu.Unlock()
	return c.SchedulerCache.GetSnapshot(ctx, bucket)
}

func smartRoutePerformanceFixture(count int) (*OpenAIGatewayService, *APIKey, *smartRouteCatalogReadCache) {
	groups := make([]*Group, count)
	accounts := make([]*Account, count)
	for i := range groups {
		id := int64(i + 1)
		groups[i] = &Group{ID: id, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, RateMultiplier: float64(id)}
		accounts[i] = &Account{ID: id, Platform: PlatformOpenAI, GroupIDs: []int64{id}, Credentials: map[string]any{
			"model_mapping": map[string]any{"gpt-route-perf": "gpt-route-perf"},
		}}
	}
	snapshot, cache := newSmartRouteCoreSnapshot(groups, accounts...)
	reads := &smartRouteCatalogReadCache{SchedulerCache: cache, groups: make(map[int64]int)}
	snapshot.cache = reads
	return &OpenAIGatewayService{schedulerSnapshot: snapshot}, smartRouteCoreKey(groups...), reads
}

func TestSmartRouteStopsReadingUnusedGroups(t *testing.T) {
	for _, selected := range []int64{1, 3} {
		t.Run(fmt.Sprintf("selected_%d", selected), func(t *testing.T) {
			svc, key, cache := smartRoutePerformanceFixture(5)
			var attempts []int64
			_, _, routed, err := svc.selectAlongKeyRoutes(context.Background(), key, nil, "gpt-route-perf",
				func(ctx context.Context, gid *int64, _ []string, _ string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
					attempts = append(attempts, *gid)
					if *gid != selected {
						return nil, OpenAIAccountScheduleDecision{}, ErrNoAvailableAccounts
					}
					return &AccountSelectionResult{}, OpenAIAccountScheduleDecision{}, nil
				})
			require.NoError(t, err)
			require.Equal(t, selected, *routed.GroupID)
			require.Equal(t, float64(selected), routed.Group.RateMultiplier)
			require.Len(t, attempts, int(selected))
			cache.mu.Lock()
			defer cache.mu.Unlock()
			for gid := selected + 1; gid <= 5; gid++ {
				require.Zero(t, cache.groups[gid], "successful selection must not read unused group %d", gid)
			}
		})
	}
}

func TestSmartRouteLazyCatalogPreservesSelectionOrder(t *testing.T) {
	for _, tc := range []struct {
		name         string
		models       []string
		customFirst  bool
		inactiveLast bool
		fail         map[int64]bool
		want         []int64
		selected     int64
	}{
		{name: "known_backup_beats_unknown_primary", models: []string{"", "gpt-route-perf", "gpt-route-perf"}, want: []int64{2}, selected: 2},
		{name: "known_support_remains_authoritative_after_failure", models: []string{"", "gpt-route-perf", "", "gpt-route-perf"}, fail: map[int64]bool{2: true}, want: []int64{2, 4}, selected: 4},
		{name: "all_unknown_preserve_key_order", models: []string{"", "", ""}, fail: map[int64]bool{1: true}, want: []int64{1, 2}, selected: 2},
		{name: "unmapped_same_platform_model_preserves_priority", models: []string{"different-model", "", "gpt-route-perf"}, want: []int64{1}, selected: 1},
		{name: "explicit_group_list_keeps_priority", models: []string{"", "gpt-route-perf"}, customFirst: true, want: []int64{1}, selected: 1},
		{name: "inactive_sibling_does_not_hide_unknown", models: []string{"", "gpt-route-perf"}, inactiveLast: true, want: []int64{1}, selected: 1},
		{name: "failed_known_does_not_enable_unknown", models: []string{"gpt-route-perf", ""}, fail: map[int64]bool{1: true}, want: []int64{1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			groups := make([]*Group, len(tc.models))
			accounts := make([]*Account, len(tc.models))
			for i, model := range tc.models {
				id := int64(i + 1)
				groups[i] = &Group{ID: id, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, RateMultiplier: float64(id)}
				accounts[i] = &Account{ID: id, Platform: PlatformOpenAI, GroupIDs: []int64{id}}
				if model != "" {
					accounts[i].Credentials = map[string]any{"model_mapping": map[string]any{model: model}}
				}
			}
			if tc.customFirst {
				groups[0].ModelAllowlist = GroupModelsListConfig{Enabled: true, Models: []string{"gpt-route-perf"}}
			}
			if tc.inactiveLast {
				groups[len(groups)-1].Status = "inactive"
			}
			snapshot, _ := newSmartRouteCoreSnapshot(groups, accounts...)
			svc := &OpenAIGatewayService{schedulerSnapshot: snapshot}
			key := smartRouteCoreKey(groups...)
			var attempted []int64
			_, _, routed, err := svc.selectAlongKeyRoutes(context.Background(), key, nil, "gpt-route-perf",
				func(_ context.Context, gid *int64, _ []string, model string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
					require.Equal(t, "gpt-route-perf", model)
					attempted = append(attempted, *gid)
					if tc.fail[*gid] {
						return nil, OpenAIAccountScheduleDecision{}, ErrNoAvailableAccounts
					}
					return &AccountSelectionResult{}, OpenAIAccountScheduleDecision{}, nil
				})
			require.Equal(t, tc.want, attempted)
			if tc.selected == 0 {
				require.ErrorIs(t, err, ErrNoAvailableAccounts)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.selected, *routed.GroupID)
				require.Equal(t, float64(tc.selected), routed.Group.RateMultiplier)
			}
			require.Equal(t, int64(1), *key.GroupID, "routing must not mutate the authenticated key")
		})
	}
}

func BenchmarkSmartRouteSelection(b *testing.B) {
	for _, count := range []int{1, 5, 10} {
		for _, last := range []bool{false, true} {
			b.Run(fmt.Sprintf("groups_%d/last_%t", count, last), func(b *testing.B) {
				svc, key, _ := smartRoutePerformanceFixture(count)
				selected := int64(1)
				if last {
					selected = int64(count)
				}
				selectOne := func(_ context.Context, gid *int64, _ []string, _ string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
					if *gid != selected {
						return nil, OpenAIAccountScheduleDecision{}, ErrNoAvailableAccounts
					}
					return &AccountSelectionResult{}, OpenAIAccountScheduleDecision{}, nil
				}
				ctx := context.Background()
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_, _, routed, err := svc.selectAlongKeyRoutes(ctx, key, nil, "gpt-route-perf", selectOne)
					if err != nil || routed.GroupID == nil || *routed.GroupID != selected {
						b.Fatalf("selection changed: %v", err)
					}
				}
			})
		}
	}
}

func TestSmartRouteModelCatalogConcurrentReaders(t *testing.T) {
	accounts := make([]Account, 32)
	for i := range accounts {
		accounts[i] = Account{ID: int64(i + 1), Platform: PlatformOpenAI, Credentials: map[string]any{
			"model_mapping": map[string]any{"gpt-route-perf": "gpt-route-perf"},
		}}
	}
	var wg sync.WaitGroup
	results := make(chan bool, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				models := modelsFromSchedulableAccounts(accounts, PlatformOpenAI)
				if len(models) != 1 || models[0] != "gpt-route-perf" {
					results <- false
					return
				}
			}
			results <- true
		}()
	}
	wg.Wait()
	close(results)
	for ok := range results {
		require.True(t, ok)
	}
}

func BenchmarkSmartRouteModelCatalog(b *testing.B) {
	accounts := make([]Account, 256)
	for i := range accounts {
		accounts[i] = Account{ID: int64(i + 1), Platform: PlatformOpenAI, Credentials: map[string]any{
			"model_mapping": map[string]any{"gpt-route-perf": "gpt-route-perf"},
		}}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if models := modelsFromSchedulableAccounts(accounts, PlatformOpenAI); len(models) != 1 {
			b.Fatalf("unexpected catalog: %v", models)
		}
	}
}
