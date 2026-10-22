//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type mediaPollAccountRepo struct{ mediaAccountRepo }

func (mediaPollAccountRepo) UpdateExtra(context.Context, int64, map[string]any) error { return nil }

func TestMediaBillingRecoveryQuarantinesOldVideoWithoutCallingUpstream(t *testing.T) {
	svc, repo, input, pending := newMediaBillingServiceTest(t)
	ctx := context.Background()
	job, err := svc.CreateGrokVideoBillingJob(ctx, input, pending)
	require.NoError(t, err)
	require.NoError(t, svc.SubmitGrokVideoBillingJob(ctx, job, "task_old", pending))
	repo.jobs[job.ID].CreatedAt = time.Now().Add(-grokVideoAutomaticPollMaxAge - time.Minute)
	reservedBalance := repo.balance
	// No account repository or transport is installed: expired polling must
	// terminate before either is needed, including after a process restart.
	svc.recoverMediaBillingJobs(ctx)
	stored, err := repo.GetMediaBillingJobByID(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, MediaBillingReconciliation, stored.Status)
	require.Equal(t, reservedBalance, repo.balance, "an unknown result must neither be charged nor refunded")
	require.Zero(t, repo.applied)
	restarted := &OpenAIGatewayService{mediaBillingJobs: repo}
	restarted.recoverMediaBillingJobs(ctx)
	require.Equal(t, reservedBalance, repo.balance)
	require.NoError(t, svc.SettleGrokVideoBillingJob(ctx, stored, &OpenAIForwardResult{VideoCount: 1, VideoDurationSeconds: 6}))
	require.NoError(t, svc.SettleGrokVideoBillingJob(ctx, stored, &OpenAIForwardResult{VideoCount: 1, VideoDurationSeconds: 6}))
	require.Equal(t, 1, repo.applied, "later evidence of completion still settles exactly once")
}

func TestMediaBillingRecoveryMissingVideoGraceAndTransientErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		age       time.Duration
		attempts  int
		wantState string
		wantCalls int
		wantDelay time.Duration
	}{
		{"recent_not_found", 404, time.Minute, 0, MediaBillingSubmitted, 2, time.Minute},
		{"old_not_found", 404, 10 * time.Minute, 0, MediaBillingReconciliation, 2, 0},
		{"old_gone", 410, 10 * time.Minute, 0, MediaBillingReconciliation, 1, 0},
		{"rate_limited", 429, 10 * time.Minute, 8, MediaBillingSubmitted, 1, 15 * time.Minute},
		{"upstream_unavailable", 503, 10 * time.Minute, 2, MediaBillingSubmitted, 1, 4 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, input, pending := newMediaBillingServiceTest(t)
			ctx := context.Background()
			job, err := svc.CreateGrokVideoBillingJob(ctx, input, pending)
			require.NoError(t, err)
			require.NoError(t, svc.SubmitGrokVideoBillingJob(ctx, job, "task_"+tc.name, pending))
			repo.jobs[job.ID].CreatedAt = time.Now().Add(-tc.age)
			repo.jobs[job.ID].Attempts = tc.attempts
			upstream := &httpUpstreamRecorder{}
			for i := 0; i < tc.wantCalls; i++ {
				upstream.responses = append(upstream.responses, &http.Response{StatusCode: tc.status,
					Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"video unavailable"}}`))})
			}
			account := newOpenAIRejectedFieldTestAccount()
			account.ID, account.Platform = input.Account.ID, PlatformGrok
			account.Credentials["pool_mode"] = true
			svc.accountRepo, svc.httpUpstream = mediaPollAccountRepo{mediaAccountRepo{account: account}}, upstream
			reservedBalance := repo.balance
			started := time.Now()
			svc.recoverMediaBillingJobs(ctx)
			stored, err := repo.GetMediaBillingJobByID(ctx, job.ID)
			require.NoError(t, err)
			require.Len(t, upstream.requests, tc.wantCalls)
			require.Equal(t, tc.wantState, stored.Status)
			require.Equal(t, reservedBalance, repo.balance)
			require.Zero(t, repo.applied)
			if tc.wantDelay > 0 {
				require.WithinDuration(t, started.Add(tc.wantDelay), stored.AvailableAt, 3*time.Second)
			} else {
				svc.recoverMediaBillingJobs(ctx)
				require.Len(t, upstream.requests, tc.wantCalls, "a quarantined job must not be polled again")
			}
		})
	}
}

func TestMediaBillingRecoveryKeepsOldPreparedChargesRecoverable(t *testing.T) {
	svc, repo, input, pending := newMediaBillingServiceTest(t)
	ctx := context.Background()
	job, err := svc.CreateGrokVideoBillingJob(ctx, input, pending)
	require.NoError(t, err)
	require.NoError(t, svc.SubmitGrokVideoBillingJob(ctx, job, "task_old_charge", pending))
	repo.applyErr = errors.New("temporary ledger failure")
	job, err = repo.GetMediaBillingJobByID(ctx, job.ID)
	require.NoError(t, err)
	require.Error(t, svc.SettleGrokVideoBillingJob(ctx, job, &OpenAIForwardResult{VideoCount: 1, VideoDurationSeconds: 6}))
	repo.jobs[job.ID].CreatedAt = time.Now().Add(-2 * grokVideoAutomaticPollMaxAge)
	repo.applyErr = nil
	svc.recoverMediaBillingJobs(ctx)
	stored, err := repo.GetMediaBillingJobByID(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, MediaBillingDone, stored.Status)
	require.Equal(t, 1, repo.applied)
	require.InDelta(t, 96.4, repo.balance, 1e-8)
}

func TestMediaBillingRecoveryKnownFailureAfterReconciliationRefundsOnce(t *testing.T) {
	svc, repo, input, pending := newMediaBillingServiceTest(t)
	ctx := context.Background()
	job, err := svc.CreateGrokVideoBillingJob(ctx, input, pending)
	require.NoError(t, err)
	require.NoError(t, svc.SubmitGrokVideoBillingJob(ctx, job, "task_failed_later", pending))
	require.NoError(t, repo.ReconcileMediaBillingJob(ctx, job.ID))
	job, err = repo.GetMediaBillingJobByID(ctx, job.ID)
	require.NoError(t, err)
	result := &OpenAIForwardResult{VideoStatus: "failed"}
	require.NoError(t, svc.SettleGrokVideoBillingJob(ctx, job, result))
	require.NoError(t, svc.SettleGrokVideoBillingJob(ctx, job, result))
	require.Equal(t, 100.0, repo.balance)
}
