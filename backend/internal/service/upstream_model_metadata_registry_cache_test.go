package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

// countingModelsDevUpstream counts concurrent models.dev fetches for the shared
// registry cache. Every response body is independent so a fetch may be shared by
// several callers.
type countingModelsDevUpstream struct {
	HTTPUpstream

	mu      sync.Mutex
	calls   atomic.Int32
	payload string
	err     error
	// block, when non-nil, keeps a fetch inside the transport until closed so
	// concurrent callers provably overlap.
	block chan struct{}
}

func (u *countingModelsDevUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.DoWithTLS(req, "", 0, 0, nil)
}

func (u *countingModelsDevUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.calls.Add(1)
	if u.block != nil {
		<-u.block
	}
	if u.err != nil {
		return nil, u.err
	}
	u.mu.Lock()
	payload := u.payload
	u.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(payload)),
	}, nil
}

func (u *countingModelsDevUpstream) callCount() int { return int(u.calls.Load()) }

// sharedMetadataRepo routes GetByID/UpdateExtra to the account fixture so one
// AccountTestService can serve several accounts concurrently, as the runner does
// serially in production.
type sharedMetadataRepo struct {
	AccountRepository
	mu    sync.Mutex
	repos map[int64]*metadataResyncRepo
}

func (r *sharedMetadataRepo) repo(id int64) *metadataResyncRepo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.repos[id]
}

func (r *sharedMetadataRepo) GetByID(ctx context.Context, id int64) (*Account, error) {
	return r.repo(id).GetByID(ctx, id)
}

func (r *sharedMetadataRepo) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	return r.repo(id).UpdateExtra(ctx, id, updates)
}

// Scenario: the scheduled runner walks several accounts in one cycle and the
// catalogue is due. The batch must fetch models.dev once and share it, instead
// of issuing one request per account.
func TestRefreshUpstreamModelMetadataSharesOneRegistryFetchPerBatch(t *testing.T) {
	whitelist := metadataResyncModels(3)
	upstream := &countingModelsDevUpstream{
		payload: metadataResyncRegistryPayload("anthropic", "https://api.anthropic.com", whitelist...),
		block:   make(chan struct{}),
	}

	const accounts = 4
	repo := &sharedMetadataRepo{repos: make(map[int64]*metadataResyncRepo, accounts)}
	fixtures := make([]*Account, accounts)
	for i := 0; i < accounts; i++ {
		account := metadataResyncAccount(whitelist, nil)
		account.ID = int64(900 + i)
		repo.repos[account.ID] = &metadataResyncRepo{account: account}
		fixtures[i] = account
	}
	svc := &AccountTestService{
		accountRepo:             repo,
		httpUpstream:            upstream,
		cfg:                     upstreamModelSyncTestConfig(),
		modelMetadataRegistryAt: time.Now().Add(-modelsDevRegistryTTL - time.Hour),
	}
	var wg sync.WaitGroup
	results := make([]UpstreamModelMetadataRefreshResult, accounts)
	errs := make([]error, accounts)
	for i := 0; i < accounts; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = svc.RefreshUpstreamModelMetadata(context.Background(), fixtures[idx], whitelist)
		}(i)
	}

	// Give every goroutine a chance to arrive, then release the single fetch.
	deadline := time.Now().Add(5 * time.Second)
	for upstream.callCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	close(upstream.block)
	wg.Wait()

	for i := 0; i < accounts; i++ {
		require.NoError(t, errs[i])
		require.Equal(t, len(whitelist), results[i].Models)
		require.Empty(t, results[i].RegistryError)
	}
	require.Equal(t, 1, upstream.callCount(), "one runner batch must fetch models.dev once")
}

// Scenario: models.dev is down. The failed attempt must be remembered for the
// retry delay so the next accounts in the same cycle do not each pay a timeout.
func TestRefreshUpstreamModelMetadataBacksOffAfterRegistryFailure(t *testing.T) {
	whitelist := metadataResyncModels(2)
	upstream := &countingModelsDevUpstream{err: errors.New("models.dev unavailable")}
	svc := &AccountTestService{
		httpUpstream:            upstream,
		cfg:                     upstreamModelSyncTestConfig(),
		modelMetadataRegistryAt: time.Now().Add(-modelsDevRegistryTTL - time.Hour),
	}

	for i := 0; i < 3; i++ {
		account := metadataResyncAccount(whitelist, nil)
		account.ID = int64(800 + i)
		repo := &metadataResyncRepo{account: account}
		svc.accountRepo = repo

		result, err := svc.RefreshUpstreamModelMetadata(context.Background(), account, whitelist)

		require.NoError(t, err, "a catalogue outage must never fail the caller")
		require.Len(t, repo.updates, 1, "the whitelist metadata map is still written")
		if i == 0 {
			require.True(t, result.RegistryFetched)
			require.NotEmpty(t, result.RegistryError)
			continue
		}
		require.False(t, result.RegistryFetched, "a recent failure must not be retried per account")
		require.Empty(t, result.RegistryError)
		err = nil
	}
	require.Equal(t, 1, upstream.callCount(), "only the first failure may reach models.dev")
}

// Scenario: the fetch succeeds. The retry delay must be cleared, so once the TTL
// expires again the catalogue is refreshed instead of being pinned to the
// snapshot taken during an earlier outage.
func TestRefreshUpstreamModelMetadataRetriesAfterSuccessClearsBackoff(t *testing.T) {
	whitelist := metadataResyncModels(2)
	upstream := &countingModelsDevUpstream{
		payload: metadataResyncRegistryPayload("anthropic", "https://api.anthropic.com", whitelist...),
	}
	svc := &AccountTestService{
		httpUpstream:            upstream,
		cfg:                     upstreamModelSyncTestConfig(),
		modelMetadataRegistryAt: time.Now().Add(-modelsDevRegistryTTL - time.Hour),
	}
	account := metadataResyncAccount(whitelist, nil)
	svc.accountRepo = &metadataResyncRepo{account: account}

	ok, err := svc.RefreshUpstreamModelMetadata(context.Background(), account, whitelist)
	require.NoError(t, err)
	require.True(t, ok.RegistryFetched)
	require.Equal(t, 1, upstream.callCount())

	// Age the snapshot past the TTL and require one more fetch.
	svc.modelMetadataRegistryMu.Lock()
	svc.modelMetadataRegistryAt = time.Now().Add(-modelsDevRegistryTTL - time.Minute)
	svc.modelMetadataRegistryMu.Unlock()

	again, err := svc.RefreshUpstreamModelMetadata(context.Background(), account, whitelist)
	require.NoError(t, err)
	require.True(t, again.RegistryFetched)
	require.Equal(t, 2, upstream.callCount())
}
