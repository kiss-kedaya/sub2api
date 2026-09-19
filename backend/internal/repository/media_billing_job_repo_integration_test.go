//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type mediaBillingIntegrationFixture struct {
	repo    *usageBillingRepository
	user    *service.User
	group   *service.Group
	account *service.Account
	apiKey  *service.APIKey
}

func newMediaBillingIntegrationFixture(t *testing.T, balance float64) mediaBillingIntegrationFixture {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	user := mustCreateUser(t, integrationEntClient, &service.User{
		Email:   "media-" + suffix + "@example.com",
		Balance: balance,
	})
	group := mustCreateGroup(t, integrationEntClient, &service.Group{
		Name:           "media-group-" + suffix,
		RateMultiplier: 1,
	})
	account := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:     "media-account-" + suffix,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeAPIKey,
	})
	groupID := group.ID
	apiKey := mustCreateApiKey(t, integrationEntClient, &service.APIKey{
		UserID:  user.ID,
		GroupID: &groupID,
		Key:     "sk-media-" + suffix,
		Name:    "media-key-" + suffix,
	})
	return mediaBillingIntegrationFixture{
		repo:    &usageBillingRepository{db: integrationDB},
		user:    user,
		group:   group,
		account: account,
		apiKey:  apiKey,
	}
}

func (f mediaBillingIntegrationFixture) videoJob(id string, reserve float64) *service.MediaBillingJob {
	return &service.MediaBillingJob{
		ID: id, Kind: service.MediaBillingKindGrokVideo,
		UserID: f.user.ID, APIKeyID: f.apiKey.ID, AccountID: f.account.ID, GroupID: f.group.ID,
		Status: service.MediaBillingCreating, ReservedAmount: reserve,
	}
}

func (f mediaBillingIntegrationFixture) intent(requestID string, balanceCost float64) *service.MediaBillingIntent {
	return &service.MediaBillingIntent{
		Command: service.UsageBillingCommand{
			RequestID: requestID, UserID: f.user.ID, APIKeyID: f.apiKey.ID, AccountID: f.account.ID,
			AccountType: service.AccountTypeAPIKey, BillingType: service.BillingTypeBalance,
			BalanceCost: balanceCost,
		},
		RawAmounts: [5]float64{balanceCost},
	}
}

func TestMediaBillingJobIntegration_CreateIsAtomicAndDuplicateDoesNotHoldAgain(t *testing.T) {
	ctx := context.Background()
	fixture := newMediaBillingIntegrationFixture(t, 10)
	job := fixture.videoJob("media-create-"+fmt.Sprint(time.Now().UnixNano()), 2)

	created, err := fixture.repo.CreateMediaBillingJob(ctx, job)
	require.NoError(t, err)
	require.Equal(t, job.ID, created.ID)
	assertMediaBillingWallet(t, ctx, fixture.user.ID, 8, 2)
	assertMediaBillingOutboxDeltas(t, ctx, fixture.user.ID, []float64{-2})

	duplicate, err := fixture.repo.CreateMediaBillingJob(ctx, job)
	require.NoError(t, err)
	require.Equal(t, created.ID, duplicate.ID)
	assertMediaBillingWallet(t, ctx, fixture.user.ID, 8, 2)
	assertMediaBillingOutboxDeltas(t, ctx, fixture.user.ID, []float64{-2})

	insufficient := fixture.videoJob("media-insufficient-"+fmt.Sprint(time.Now().UnixNano()), 20)
	_, err = fixture.repo.CreateMediaBillingJob(ctx, insufficient)
	require.ErrorIs(t, err, service.ErrBalanceWithholdingFailed)
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_billing_jobs WHERE id = $1`, insufficient.ID).Scan(&count))
	require.Zero(t, count)
	assertMediaBillingWallet(t, ctx, fixture.user.ID, 8, 2)
	assertMediaBillingOutboxDeltas(t, ctx, fixture.user.ID, []float64{-2})

	_, err = fixture.repo.FailMediaBillingJob(ctx, job.ID)
	require.NoError(t, err)
	assertMediaBillingWallet(t, ctx, fixture.user.ID, 10, 0)
	assertMediaBillingOutboxDeltas(t, ctx, fixture.user.ID, []float64{-2, 2})
}

func TestMediaBillingJobIntegration_ListLeasesAcrossWorkers(t *testing.T) {
	ctx := context.Background()
	fixture := newMediaBillingIntegrationFixture(t, 10)
	base := fmt.Sprintf("media-lease-%d-", time.Now().UnixNano())
	for i := 0; i < 2; i++ {
		intent := fixture.intent(base+fmt.Sprint(i), 0)
		_, err := fixture.repo.CreateMediaBillingJob(ctx, &service.MediaBillingJob{
			ID: base + fmt.Sprint(i), Kind: service.MediaBillingKindImage,
			UserID: fixture.user.ID, APIKeyID: fixture.apiKey.ID, AccountID: fixture.account.ID, GroupID: fixture.group.ID,
			Status: service.MediaBillingReady, Intent: intent,
		})
		require.NoError(t, err)
	}

	start := make(chan struct{})
	results := make(chan []*service.MediaBillingJob, 2)
	errs := make(chan error, 2)
	var workers sync.WaitGroup
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			jobs, err := fixture.repo.ListMediaBillingJobs(ctx, 1)
			results <- jobs
			errs <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	seen := map[string]struct{}{}
	for jobs := range results {
		require.Len(t, jobs, 1)
		seen[jobs[0].ID] = struct{}{}
	}
	require.Len(t, seen, 2)

	leased, err := fixture.repo.ListMediaBillingJobs(ctx, 32)
	require.NoError(t, err)
	for _, job := range leased {
		require.NotContains(t, seen, job.ID)
	}
}

func TestMediaBillingJobIntegration_ApplyFailureKeepsReadyAndHold(t *testing.T) {
	ctx := context.Background()
	fixture := newMediaBillingIntegrationFixture(t, 10)
	job := fixture.videoJob("media-apply-rollback-"+fmt.Sprint(time.Now().UnixNano()), 2)
	_, err := fixture.repo.CreateMediaBillingJob(ctx, job)
	require.NoError(t, err)
	require.NoError(t, fixture.repo.SubmitMediaBillingJob(ctx, job.ID, "upstream-"+job.ID, []byte(`{"route":"grok"}`)))
	intent := fixture.intent("media-apply:"+job.ID, 1)
	intent.Command.AccountQuotaCost = 1
	_, err = fixture.repo.PrepareMediaBillingIntent(ctx, job.ID, intent)
	require.NoError(t, err)

	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET deleted_at = NOW() WHERE id = $1`, fixture.account.ID)
	require.NoError(t, err)
	_, err = fixture.repo.ApplyMediaBillingJob(ctx, job.ID)
	require.ErrorIs(t, err, service.ErrAccountNotFound)

	persisted, err := fixture.repo.GetMediaBillingJobByID(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, service.MediaBillingReady, persisted.Status)
	assertMediaBillingWallet(t, ctx, fixture.user.ID, 8, 2)
	assertMediaBillingOutboxDeltas(t, ctx, fixture.user.ID, []float64{-2})
}

func assertMediaBillingWallet(t *testing.T, ctx context.Context, userID int64, wantBalance, wantFrozen float64) {
	t.Helper()
	var balance, frozen float64
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT balance, frozen_balance FROM users WHERE id = $1`, userID).Scan(&balance, &frozen))
	require.InDelta(t, wantBalance, balance, 0.00000001)
	require.InDelta(t, wantFrozen, frozen, 0.00000001)
}

func assertMediaBillingOutboxDeltas(t *testing.T, ctx context.Context, userID int64, want []float64) {
	t.Helper()
	rows, err := integrationDB.QueryContext(ctx, `
		SELECT delta
		FROM live_balance_adjustment_outbox
		WHERE user_id = $1
		ORDER BY id
	`, userID)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	got := make([]float64, 0, len(want))
	for rows.Next() {
		var delta float64
		require.NoError(t, rows.Scan(&delta))
		got = append(got, delta)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, want, got)
}
