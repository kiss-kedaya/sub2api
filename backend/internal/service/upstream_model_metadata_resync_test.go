package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// metadataResyncRepo is the minimal account repository for the post-whitelist
// metadata refresh. It returns a deep copy so the production code cannot mutate
// the fixture, and records every extra update so tests can assert write counts.
type metadataResyncRepo struct {
	AccountRepository
	mu      sync.Mutex
	account *Account
	updates []map[string]any
}

func (r *metadataResyncRepo) GetByID(_ context.Context, _ int64) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	clone := *r.account
	clone.Credentials = mergeMap(r.account.Credentials, nil)
	clone.Extra = mergeMap(r.account.Extra, nil)
	return &clone, nil
}

func (r *metadataResyncRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.account.Extra == nil {
		r.account.Extra = map[string]any{}
	}
	copied := make(map[string]any, len(updates))
	for key, value := range updates {
		r.account.Extra[key] = value
		copied[key] = value
	}
	r.updates = append(r.updates, copied)
	return nil
}

func (r *metadataResyncRepo) snapshot(t *testing.T) UpstreamModelMetadataSnapshot {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	raw, ok := r.account.Extra[UpstreamModelMetadataExtraKey]
	require.True(t, ok, "upstream_model_metadata must be written")
	encoded, err := json.Marshal(raw)
	require.NoError(t, err)
	var snapshot UpstreamModelMetadataSnapshot
	require.NoError(t, json.Unmarshal(encoded, &snapshot))
	return snapshot
}

func metadataResyncRegistryPayload(providerID, api string, modelIDs ...string) string {
	entries := make([]string, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		entries = append(entries, `"`+modelID+`":{"id":"`+modelID+`","name":"`+modelID+` display"}`)
	}
	return `{"` + providerID + `":{"id":"` + providerID + `","name":"` + providerID + `","api":"` + api + `","models":{` + strings.Join(entries, ",") + `}}}`
}

func decodeMetadataResyncRegistry(t *testing.T, payload string) map[string]modelsDevProvider {
	t.Helper()
	var registry map[string]modelsDevProvider
	require.NoError(t, json.Unmarshal([]byte(payload), &registry))
	return registry
}

func metadataResyncPreviousSnapshot(modelIDs []string, syncedAt time.Time) UpstreamModelMetadataSnapshot {
	models := make(map[string]UpstreamModelMetadata, len(modelIDs))
	for _, modelID := range modelIDs {
		models[modelID] = UpstreamModelMetadata{
			ID:              modelID,
			DisplayName:     modelID + " legacy",
			Reasoning:       boolPtr(false),
			InputModalities: []string{"text"},
			ContextWindow:   200000,
		}
	}
	return UpstreamModelMetadataSnapshot{
		Source:   "models.dev",
		SyncedAt: syncedAt.UTC().Format(time.RFC3339),
		Models:   models,
	}
}

func metadataResyncAccount(whitelist []string, previous *UpstreamModelMetadataSnapshot) *Account {
	mapping := make(map[string]any, len(whitelist))
	for _, modelID := range whitelist {
		mapping[modelID] = modelID
	}
	extra := map[string]any{}
	if previous != nil {
		extra[UpstreamModelMetadataExtraKey] = *previous
	}
	return &Account{
		ID:       34786,
		Platform: PlatformAnthropic,
		Type:     AccountTypeAPIKey,
		Status:   StatusActive,
		Credentials: map[string]any{
			"api_key":       "sk-ant-test",
			"base_url":      "https://api.anthropic.com",
			"model_mapping": mapping,
		},
		Extra: extra,
	}
}

func metadataResyncModels(count int) []string {
	models := make([]string, 0, count)
	for i := 0; i < count; i++ {
		models = append(models, fmt.Sprintf("claude-base-%02d", i))
	}
	return models
}

// Scenario: the whitelist grows from 10 to 14 models. The metadata snapshot must
// cover the new models from an already-fresh catalogue without a second upstream
// or registry request, while keeping every existing entry untouched.
func TestRefreshUpstreamModelMetadataCoversGrownWhitelist(t *testing.T) {
	initial := metadataResyncModels(10)
	grown := append(append([]string(nil), initial...), "claude-opus-5-5", "claude-opus-4-7", "claude-sonnet-4-5", "claude-opus-4-5-20251101")
	account := metadataResyncAccount(grown, func() *UpstreamModelMetadataSnapshot {
		snapshot := metadataResyncPreviousSnapshot(initial, time.Now().Add(-time.Hour))
		return &snapshot
	}())
	repo := &metadataResyncRepo{account: account}
	upstream := &httpUpstreamRecorder{err: errors.New("unexpected upstream request")}
	svc := &AccountTestService{
		accountRepo:             repo,
		httpUpstream:            upstream,
		cfg:                     upstreamModelSyncTestConfig(),
		modelMetadataRegistry:   decodeMetadataResyncRegistry(t, metadataResyncRegistryPayload("anthropic", "https://api.anthropic.com", grown...)),
		modelMetadataRegistryAt: time.Now(),
	}

	result, err := svc.RefreshUpstreamModelMetadata(context.Background(), account, grown)

	require.NoError(t, err)
	require.Empty(t, upstream.requests, "a fresh catalogue must be reused without a network request")
	require.False(t, result.RegistryFetched)
	require.Empty(t, result.RegistryError)
	require.Equal(t, len(grown), result.Models)
	require.Equal(t, 4, result.Added)
	require.Len(t, repo.updates, 1)

	snapshot := repo.snapshot(t)
	require.Len(t, snapshot.Models, len(grown))
	require.Equal(t, "models.dev", snapshot.Source)
	require.NotEmpty(t, snapshot.SyncedAt)
	for _, modelID := range initial {
		require.Equal(t, modelID+" legacy", snapshot.Models[modelID].DisplayName,
			"existing metadata must be preserved")
	}
	for _, modelID := range grown[len(initial):] {
		require.Equal(t, modelID+" display", snapshot.Models[modelID].DisplayName,
			"newly whitelisted models must be enriched")
	}
}

// Scenario: models.dev (and the built-in tables) do not know a whitelisted
// model. The key must still exist with an empty entry instead of the model
// vanishing from the snapshot.
func TestRefreshUpstreamModelMetadataKeepsEmptyEntryWhenCatalogMissesModel(t *testing.T) {
	initial := metadataResyncModels(10)
	unknown := "relay-private-model"
	whitelist := append(append([]string(nil), initial...), unknown)
	previous := metadataResyncPreviousSnapshot(initial, time.Now().Add(-time.Hour))
	account := metadataResyncAccount(whitelist, &previous)
	repo := &metadataResyncRepo{account: account}
	upstream := &httpUpstreamRecorder{err: errors.New("unexpected upstream request")}
	svc := &AccountTestService{
		accountRepo:             repo,
		httpUpstream:            upstream,
		cfg:                     upstreamModelSyncTestConfig(),
		modelMetadataRegistry:   decodeMetadataResyncRegistry(t, metadataResyncRegistryPayload("anthropic", "https://api.anthropic.com", initial...)),
		modelMetadataRegistryAt: time.Now(),
	}

	result, err := svc.RefreshUpstreamModelMetadata(context.Background(), account, whitelist)

	require.NoError(t, err)
	require.Empty(t, upstream.requests)
	require.Equal(t, len(whitelist), result.Models)

	snapshot := repo.snapshot(t)
	require.Len(t, snapshot.Models, len(whitelist))
	entry, ok := snapshot.Models[unknown]
	require.True(t, ok, "an unresolved model must keep its key")
	require.Equal(t, unknown, entry.ID)
	require.Empty(t, entry.DisplayName)
	require.Nil(t, entry.Reasoning)
	require.Zero(t, entry.ContextWindow)

	metadata, ok := account.GetUpstreamModelMetadata(unknown)
	require.True(t, ok, "account lookup must find the empty entry")
	require.Equal(t, unknown, metadata.ID)
}

// Scenario: the catalogue refresh fails. The whitelist already applied, so the
// refresh must only be advisory: previous metadata is preserved, newly
// whitelisted models still get keys, and synced_at is left stale so the next
// run retries.
func TestRefreshUpstreamModelMetadataKeepsPreviousMetadataWhenRegistryRefreshFails(t *testing.T) {
	initial := metadataResyncModels(10)
	whitelist := append(append([]string(nil), initial...), "claude-opus-5-5", "claude-opus-4-7", "claude-sonnet-4-5", "claude-opus-4-5-20251101")
	staleAt := time.Now().Add(-7 * time.Hour)
	previous := metadataResyncPreviousSnapshot(initial, staleAt)
	account := metadataResyncAccount(whitelist, &previous)
	repo := &metadataResyncRepo{account: account}
	upstream := &httpUpstreamRecorder{err: errors.New("models.dev unavailable")}
	svc := &AccountTestService{
		accountRepo:  repo,
		httpUpstream: upstream,
		cfg:          upstreamModelSyncTestConfig(),
	}

	result, err := svc.RefreshUpstreamModelMetadata(context.Background(), account, whitelist)

	require.NoError(t, err, "a catalogue failure is advisory, never fatal")
	require.True(t, result.RegistryFetched)
	require.NotEmpty(t, result.RegistryError)
	require.Len(t, upstream.requests, 1)

	snapshot := repo.snapshot(t)
	require.Len(t, snapshot.Models, len(whitelist))
	for _, modelID := range initial {
		require.Equal(t, modelID+" legacy", snapshot.Models[modelID].DisplayName,
			"old metadata must survive a failed refresh")
	}
	for _, modelID := range whitelist[len(initial):] {
		require.Equal(t, modelID, snapshot.Models[modelID].ID)
		require.Empty(t, snapshot.Models[modelID].DisplayName)
	}
	require.Equal(t, staleAt.UTC().Format(time.RFC3339), snapshot.SyncedAt,
		"a failed refresh must leave synced_at stale so the next run retries")
}

// Scenario: whitelist auto-sync runs every hour. Once the snapshot covers the
// whitelist and the catalogue is fresh, a repeat refresh must not issue another
// request or rewrite the account.
func TestRefreshUpstreamModelMetadataIsIdempotent(t *testing.T) {
	initial := metadataResyncModels(10)
	grown := append(append([]string(nil), initial...), "claude-opus-5-5")
	previous := metadataResyncPreviousSnapshot(initial, time.Now().Add(-time.Hour))
	account := metadataResyncAccount(grown, &previous)
	repo := &metadataResyncRepo{account: account}
	upstream := &httpUpstreamRecorder{err: errors.New("unexpected upstream request")}
	svc := &AccountTestService{
		accountRepo:             repo,
		httpUpstream:            upstream,
		cfg:                     upstreamModelSyncTestConfig(),
		modelMetadataRegistry:   decodeMetadataResyncRegistry(t, metadataResyncRegistryPayload("anthropic", "https://api.anthropic.com", grown...)),
		modelMetadataRegistryAt: time.Now(),
	}

	first, err := svc.RefreshUpstreamModelMetadata(context.Background(), account, grown)
	require.NoError(t, err)
	require.Equal(t, len(grown), first.Models)
	require.Len(t, repo.updates, 1)

	second, err := svc.RefreshUpstreamModelMetadata(context.Background(), account, grown)

	require.NoError(t, err)
	require.Equal(t, len(grown), second.Models)
	require.Empty(t, upstream.requests, "a repeat refresh must not hit the network")
	require.Len(t, repo.updates, 1, "a repeat refresh must not rewrite unchanged metadata")
	snapshot := repo.snapshot(t)
	require.Len(t, snapshot.Models, len(grown))
	require.Equal(t, "claude-opus-5-5 display", snapshot.Models["claude-opus-5-5"].DisplayName)
	require.Equal(t, initial[0]+" legacy", snapshot.Models[initial[0]].DisplayName)
}

// Scenario: the in-memory catalogue is older than the TTL. One whitelist sync
// must refresh it exactly once and resolve the newly added model from the live
// registry.
func TestRefreshUpstreamModelMetadataRefreshesStaleCatalogOnce(t *testing.T) {
	initial := metadataResyncModels(10)
	grown := append(append([]string(nil), initial...), "claude-opus-5-5")
	previous := metadataResyncPreviousSnapshot(initial, time.Now().Add(-time.Minute))
	account := metadataResyncAccount(grown, &previous)
	repo := &metadataResyncRepo{account: account}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(metadataResyncRegistryPayload("anthropic", "https://api.anthropic.com", grown...))),
	}}}
	svc := &AccountTestService{
		accountRepo:             repo,
		httpUpstream:            upstream,
		cfg:                     upstreamModelSyncTestConfig(),
		modelMetadataRegistry:   decodeMetadataResyncRegistry(t, metadataResyncRegistryPayload("anthropic", "https://api.anthropic.com", initial...)),
		modelMetadataRegistryAt: time.Now().Add(-modelsDevRegistryTTL - time.Hour),
	}

	first, err := svc.RefreshUpstreamModelMetadata(context.Background(), account, grown)

	require.NoError(t, err)
	require.True(t, first.RegistryFetched)
	require.Empty(t, first.RegistryError)
	require.Len(t, upstream.requests, 1)
	snapshot := repo.snapshot(t)
	require.Len(t, snapshot.Models, len(grown))
	require.Equal(t, "claude-opus-5-5 display", snapshot.Models["claude-opus-5-5"].DisplayName)
	require.Len(t, repo.updates, 1)

	second, err := svc.RefreshUpstreamModelMetadata(context.Background(), account, grown)

	require.NoError(t, err)
	require.False(t, second.RegistryFetched)
	require.Len(t, upstream.requests, 1, "the refreshed catalogue must be reused")
	require.Len(t, repo.updates, 1, "an unchanged snapshot must not be rewritten")
}

// modelWhitelistMetadataSyncFake implements both the fetch and the metadata
// refresh sides of the whitelist runner.
type modelWhitelistMetadataSyncFake struct {
	*modelSyncScriptedFetcher
	mu     sync.Mutex
	calls  int
	models []string
	result UpstreamModelMetadataRefreshResult
	err    error
}

func newModelWhitelistMetadataSyncFake(results []modelSyncFetchResult, refreshErr error) *modelWhitelistMetadataSyncFake {
	return &modelWhitelistMetadataSyncFake{
		modelSyncScriptedFetcher: &modelSyncScriptedFetcher{results: results},
		err:                      refreshErr,
	}
}

func (f *modelWhitelistMetadataSyncFake) RefreshUpstreamModelMetadata(_ context.Context, _ *Account, models []string) (UpstreamModelMetadataRefreshResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.models = append([]string(nil), models...)
	return f.result, f.err
}

// Scenario: after the whitelist is applied, the runner hands the fresh model list
// to the metadata refresher so extra.upstream_model_metadata follows the new
// whitelist instead of waiting for a separate admin sync.
func TestModelWhitelistAutoSyncRefreshesMetadataAfterWhitelistApply(t *testing.T) {
	account := newModelSyncFailureAccount()
	repo := &modelSyncFailureRepo{account: account}
	models := []string{"gpt-5", "gpt-6", "claude-opus-5-5"}
	fetcher := newModelWhitelistMetadataSyncFake([]modelSyncFetchResult{{models: models}}, nil)
	svc := newModelSyncFailureService(repo, fetcher)

	result, err := svc.syncAccount(context.Background(), account, time.Hour)

	require.NoError(t, err)
	require.True(t, result.Applied)
	require.Equal(t, len(models), result.ModelCount)
	require.Equal(t, []map[string]string{{"claude-opus-5-5": "claude-opus-5-5", "gpt-5": "gpt-5", "gpt-6": "gpt-6"}}, repo.mappingWrites)
	require.Equal(t, 1, fetcher.calls)
	require.Equal(t, []string{"claude-opus-5-5", "gpt-5", "gpt-6"}, fetcher.models)
}

// Scenario: metadata enrichment fails after the whitelist write. The runner must
// still report success, keep the whitelist and the schedule, and not surface the
// metadata failure to the admin result.
func TestModelWhitelistAutoSyncAppliesWhitelistWhenMetadataRefreshFails(t *testing.T) {
	account := newModelSyncFailureAccount()
	repo := &modelSyncFailureRepo{account: account}
	models := []string{"gpt-5", "gpt-6"}
	fetcher := newModelWhitelistMetadataSyncFake([]modelSyncFetchResult{{models: models}}, errors.New("metadata write failed"))
	svc := newModelSyncFailureService(repo, fetcher)

	result, err := svc.syncAccount(context.Background(), account, time.Hour)

	require.NoError(t, err)
	require.True(t, result.Applied)
	require.Empty(t, result.Error)
	require.Len(t, repo.mappingWrites, 1, "the whitelist must be applied regardless of metadata")
	require.NotNil(t, result.State)
	require.Equal(t, len(models), result.State.LastAppliedModels)
	require.Empty(t, result.State.LastError)
	require.NotNil(t, result.State.NextRunAt)
	require.Equal(t, 1, fetcher.calls)
}
