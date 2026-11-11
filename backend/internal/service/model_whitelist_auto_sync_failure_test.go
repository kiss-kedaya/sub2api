package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// modelSyncFailureRepo is an account repository that records every model
// mapping write, so a test can assert that none happened.
type modelSyncFailureRepo struct {
	AccountRepository
	mu            sync.Mutex
	account       *Account
	mappingWrites []map[string]string
}

func (r *modelSyncFailureRepo) GetByID(_ context.Context, _ int64) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	clone := *r.account
	clone.Credentials = mergeMap(nil, r.account.Credentials)
	clone.Extra = mergeMap(nil, r.account.Extra)
	return &clone, nil
}

func (r *modelSyncFailureRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.account.Extra == nil {
		r.account.Extra = map[string]any{}
	}
	for key, value := range updates {
		r.account.Extra[key] = value
	}
	return nil
}

func (r *modelSyncFailureRepo) UpdateAccountModelMappingCAS(_ context.Context, _ *Account, _, next map[string]string, _ map[string]any) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mappingWrites = append(r.mappingWrites, next)
	mapping := make(map[string]any, len(next))
	for key, value := range next {
		mapping[key] = value
	}
	r.account.Credentials["model_mapping"] = mapping
	return true, nil
}

type modelSyncScriptedFetcher struct {
	mu      sync.Mutex
	calls   int
	results []modelSyncFetchResult
}

type modelSyncFetchResult struct {
	models []string
	err    error
}

func (f *modelSyncScriptedFetcher) FetchUpstreamSupportedModels(context.Context, *Account) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	index := f.calls
	f.calls++
	if index >= len(f.results) {
		index = len(f.results) - 1
	}
	return f.results[index].models, f.results[index].err
}

func newModelSyncFailureAccount() *Account {
	return &Account{
		ID:       91,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Status:   StatusActive,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://relay.example/v1",
			// The list a previous sync read successfully.
			"model_mapping": map[string]any{"gpt-5": "gpt-5", "gpt-5-mini": "gpt-5-mini"},
		},
		Extra: map[string]any{
			ModelWhitelistAutoSyncExtraKey: map[string]any{"enabled": true, "interval_minutes": 60},
		},
	}
}

func newModelSyncFailureService(repo *modelSyncFailureRepo, fetcher modelWhitelistFetcher) *ModelWhitelistAutoSyncService {
	svc := NewModelWhitelistAutoSyncService(repo, fetcher)
	svc.retryBackoffUnit = time.Millisecond
	svc.now = func() time.Time { return time.Date(2026, time.November, 11, 12, 0, 0, 0, time.UTC) }
	return svc
}

// A failed read says nothing about which models the upstream serves. The list
// from the last successful sync stays; it is not cleared, and the account is
// not opened to every model.
func TestModelWhitelistAutoSyncKeepsModelListWhenUpstreamFails(t *testing.T) {
	account := newModelSyncFailureAccount()
	repo := &modelSyncFailureRepo{account: account}
	fetchErr := errors.New("upstream returned 502")
	fetcher := &modelSyncScriptedFetcher{results: []modelSyncFetchResult{{err: fetchErr}}}
	svc := newModelSyncFailureService(repo, fetcher)

	result, err := svc.syncAccount(context.Background(), account, time.Hour)

	require.ErrorIs(t, err, fetchErr)
	require.Equal(t, 3, fetcher.calls, "the read is still retried before giving up")
	require.Empty(t, repo.mappingWrites, "a failed sync must not write the model list")
	require.Equal(t,
		map[string]any{"gpt-5": "gpt-5", "gpt-5-mini": "gpt-5-mini"},
		repo.account.Credentials["model_mapping"],
		"the previous model list is kept as it was",
	)
	require.False(t, result.Applied)

	// The failure is still recorded so the operator can see it, and the next
	// attempt is scheduled. The stored text is a sanitized code, not the raw
	// upstream error; the returned error carries the detail.
	require.ErrorContains(t, err, "upstream model sync failed after 3 attempts")
	require.NotNil(t, result.State)
	require.Equal(t, 1, result.State.ConsecutiveFailures)
	require.NotEmpty(t, result.State.LastError)
	require.NotNil(t, result.State.NextRunAt)
	stored := decodeModelWhitelistAutoSyncState(repo.account.Extra)
	require.NotNil(t, stored)
	require.Equal(t, 1, stored.ConsecutiveFailures)
}

// The same holds when the request context ends while waiting to retry.
func TestModelWhitelistAutoSyncKeepsModelListWhenCancelledMidRetry(t *testing.T) {
	account := newModelSyncFailureAccount()
	repo := &modelSyncFailureRepo{account: account}
	fetcher := &modelSyncScriptedFetcher{results: []modelSyncFetchResult{{err: errors.New("timeout")}}}
	svc := newModelSyncFailureService(repo, fetcher)
	svc.retryBackoffUnit = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := svc.syncAccount(ctx, account, time.Hour)

	require.Error(t, err)
	require.Empty(t, repo.mappingWrites)
	require.Equal(t,
		map[string]any{"gpt-5": "gpt-5", "gpt-5-mini": "gpt-5-mini"},
		repo.account.Credentials["model_mapping"],
	)
}

// A read that fails and then succeeds on a retry applies the fresh list.
func TestModelWhitelistAutoSyncAppliesListWhenARetrySucceeds(t *testing.T) {
	account := newModelSyncFailureAccount()
	repo := &modelSyncFailureRepo{account: account}
	fetcher := &modelSyncScriptedFetcher{results: []modelSyncFetchResult{
		{err: errors.New("upstream returned 502")},
		{models: []string{"gpt-5", "gpt-6"}},
	}}
	svc := newModelSyncFailureService(repo, fetcher)

	result, err := svc.syncAccount(context.Background(), account, time.Hour)

	require.NoError(t, err)
	require.True(t, result.Applied)
	require.Equal(t, 2, fetcher.calls)
	require.Equal(t, []map[string]string{{"gpt-5": "gpt-5", "gpt-6": "gpt-6"}}, repo.mappingWrites)
}

// An upstream that answers with an empty list is skipped, as before: an empty
// answer is not a reason to drop the list either.
func TestModelWhitelistAutoSyncKeepsModelListWhenUpstreamReturnsNoModels(t *testing.T) {
	account := newModelSyncFailureAccount()
	repo := &modelSyncFailureRepo{account: account}
	fetcher := &modelSyncScriptedFetcher{results: []modelSyncFetchResult{{models: []string{}}}}
	svc := newModelSyncFailureService(repo, fetcher)

	result, err := svc.syncAccount(context.Background(), account, time.Hour)

	require.NoError(t, err)
	require.Equal(t, ModelWhitelistAutoSyncSkipEmptyCatalog, result.SkippedReason)
	require.Empty(t, repo.mappingWrites)
}
