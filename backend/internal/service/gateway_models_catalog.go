package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"
)

const modelsListCatalogMarker = "*"

type groupModelsCatalog struct {
	byPlatform map[string][]string
	platforms  map[string]struct{}
}

func modelsListCatalogCacheKey(groupID *int64) string {
	return modelsListCacheKey(groupID, modelsListCatalogMarker)
}

func (c groupModelsCatalog) modelsFor(platform string) []string {
	if c.byPlatform == nil {
		return nil
	}
	platform = strings.TrimSpace(platform)
	if platform == "" {
		return c.unionModels()
	}
	return c.byPlatform[platform]
}

func (c groupModelsCatalog) unionModels() []string {
	seen := make(map[string]struct{})
	var models []string
	for _, platform := range groupCatalogPlatforms() {
		for _, model := range c.byPlatform[platform] {
			model = strings.TrimSpace(model)
			if model == "" {
				continue
			}
			if _, ok := seen[model]; ok {
				continue
			}
			seen[model] = struct{}{}
			models = append(models, model)
		}
	}
	if len(models) == 0 {
		return nil
	}
	sort.Strings(models)
	return models
}

func storedModelList(models []string) []string {
	if models == nil {
		return []string{}
	}
	return cloneStringSlice(models)
}

func (s *GatewayService) ensureGroupModelsCatalog(ctx context.Context, groupID *int64) groupModelsCatalog {
	if s == nil {
		return groupModelsCatalog{}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	catalogKey := modelsListCatalogCacheKey(groupID)
	if s.modelsListCache != nil {
		if cached, found := s.modelsListCache.Get(catalogKey); found {
			if cat, ok := cached.(groupModelsCatalog); ok {
				return cat
			}
		}
	}

	resultCh := s.modelsListSF.DoChan(catalogKey, func() (any, error) {
		leaderCtx := context.Background()
		if ctx != nil {
			leaderCtx = context.WithoutCancel(ctx)
		}
		leaderCtx, leaderCancel := context.WithTimeout(leaderCtx, 5*time.Second)
		defer leaderCancel()
		if s.modelsListCache != nil {
			if cached, found := s.modelsListCache.Get(catalogKey); found {
				if cat, ok := cached.(groupModelsCatalog); ok {
					return cat, nil
				}
			}
		}
		cat := s.buildGroupModelsCatalog(leaderCtx, groupID)
		s.storeGroupModelsCatalog(groupID, cat)
		return cat, nil
	})

	var result singleflight.Result
	select {
	case result = <-resultCh:
	case <-ctx.Done():
		return groupModelsCatalog{}
	}
	if result.Err != nil {
		return groupModelsCatalog{}
	}
	cat, _ := result.Val.(groupModelsCatalog)
	return cat
}

func (s *GatewayService) storeGroupModelsCatalog(groupID *int64, cat groupModelsCatalog) {
	if s == nil {
		return
	}
	if s.modelsListCache != nil {
		for _, platform := range groupCatalogPlatforms() {
			s.modelsListCache.Set(modelsListCacheKey(groupID, platform), storedModelList(cat.modelsFor(platform)), s.modelsListCacheTTL)
		}
		s.modelsListCache.Set(modelsListCatalogCacheKey(groupID), cat, s.modelsListCacheTTL)
	}
	if s.platformsListCache != nil {
		s.platformsListCache.Set(fmt.Sprintf("%d", derefGroupID(groupID)), clonePlatformSet(cat.platforms), s.platformsListCacheTTL)
	}
}

func (s *GatewayService) buildGroupModelsCatalog(ctx context.Context, groupID *int64) groupModelsCatalog {
	cat := groupModelsCatalog{
		byPlatform: make(map[string][]string, len(groupCatalogPlatforms())),
		platforms:  make(map[string]struct{}),
	}
	if s == nil || s.accountRepo == nil {
		return cat
	}

	addPlatforms := func(accounts []Account) {
		for i := range accounts {
			if platform := strings.TrimSpace(accounts[i].Platform); platform != "" {
				cat.platforms[platform] = struct{}{}
			}
		}
	}

	if s.schedulerSnapshot != nil {
		requestCtx := withSchedulerRequestMode(ctx, s.accountRepo, s.schedulerSnapshot)
		for _, platform := range groupCatalogPlatforms() {
			accounts, _, err := s.schedulerSnapshot.listSchedulableAccountsForRequest(requestCtx, groupID, platform, false)
			if err != nil {
				// Snapshot-authoritative: a cold/failed bucket is an empty
				// platform catalog, not a PostgreSQL scan. The uncached
				// loadAvailableModels path still has the legacy repo fallback.
				continue
			}
			addPlatforms(accounts)
			cat.byPlatform[platform] = modelsFromSchedulableAccounts(accounts, platform)
		}
		return cat
	}

	accounts, err := listSchedulableAccountsFromRepo(ctx, s.accountRepo, groupID)
	if err != nil || len(accounts) == 0 {
		return cat
	}
	addPlatforms(accounts)
	for _, platform := range groupCatalogPlatforms() {
		cat.byPlatform[platform] = modelsFromSchedulableAccounts(accounts, platform)
	}
	return cat
}

func filterAccountsByPlatform(accounts []Account, platform string) []Account {
	platform = strings.TrimSpace(platform)
	if platform == "" || len(accounts) == 0 {
		return accounts
	}
	out := make([]Account, 0, len(accounts))
	for _, account := range accounts {
		if account.Platform == platform {
			out = append(out, account)
		}
	}
	return out
}

func modelsFromSchedulableAccounts(accounts []Account, platform string) []string {
	if platform != "" {
		accounts = filterAccountsByPlatform(accounts, platform)
	}
	if len(accounts) == 0 {
		return nil
	}

	modelSet := make(map[string]struct{})
	for _, acc := range accounts {
		if platform == PlatformOpenAI && acc.IsOpenAIPassthroughEnabled() {
			return nil
		}
		for model := range acc.GetModelMapping() {
			modelSet[model] = struct{}{}
		}
	}
	if len(modelSet) == 0 {
		return nil
	}
	models := make([]string, 0, len(modelSet))
	for model := range modelSet {
		models = append(models, model)
	}
	sort.Strings(models)
	if platform == PlatformOpenAI {
		models = supplementUnmappedOpenAIModels(accounts, models)
	}
	return models
}

func catalogHasRequestedModel(cat groupModelsCatalog, requestedModel string) groupCatalogModelPresence {
	sawCatalog := false
	for _, platform := range groupCatalogPlatforms() {
		models := cat.modelsFor(platform)
		if models == nil {
			continue
		}
		sawCatalog = true
		if modelsAdmitRequestedModel(models, requestedModel) {
			return groupCatalogModelPresent
		}
	}
	if !sawCatalog {
		return groupCatalogModelUnknown
	}
	return groupCatalogModelAbsent
}
