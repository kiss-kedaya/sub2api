//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSeedanceDurableBillingSurvivesRestartAndNoClientPoll(t *testing.T) {
	svc, repo, input, pending := newMediaBillingServiceTest(t)
	input.Account.Platform = PlatformOpenAI
	input.Result.Model, input.Result.BillingModel = "seedance-test", "seedance-test"
	input.APIKey.Group.Platform = PlatformOpenAI
	svc.resolver = newOpenAITokenImageChannelPricingResolverForTest(t, pending.GroupID, "seedance-test")
	pending.Provider, pending.Model, pending.BillingModel = VideoProviderSeedance, "seedance-test", "seedance-test"
	ctx := context.Background()
	job, err := svc.CreateGrokVideoBillingJob(ctx, input, pending)
	require.NoError(t, err)
	require.Zero(t, job.ReservedAmount, "unknown token usage must not be guessed from video duration")
	require.NoError(t, svc.SubmitGrokVideoBillingJob(ctx, job, SeedanceTaskKey("background"), pending))
	account := seedanceTestAccount()
	account.ID = input.Account.ID
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"id":"background","status":"succeeded","usage":{"completion_tokens":10000},"content":{"video_url":"https://cdn.example/output.mp4"}}`)}
	// No pricing service is installed in the restarted process. Settlement must
	// use the submission-time price, group multiplier and billing identity.
	restarted := &OpenAIGatewayService{cfg: svc.cfg, mediaBillingJobs: repo, usageLogRepo: svc.usageLogRepo,
		accountRepo: mediaAccountRepo{account: account}, httpUpstream: upstream}
	restarted.recoverMediaBillingJobs(ctx)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, 1, repo.applied)
	stored, err := repo.GetMediaBillingJobByID(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, MediaBillingDone, stored.Status)
	require.Equal(t, "grok-video:seedance:background", stored.Intent.Command.RequestID)
	require.Equal(t, 10000, stored.Intent.Usage.OutputTokens)
	require.Equal(t, string(BillingModeToken), *stored.Intent.Usage.BillingMode)
	require.InDelta(t, 0.15, stored.Intent.Usage.TotalCost, 1e-8)
	require.InDelta(t, 0.30, stored.Intent.Usage.ActualCost, 1e-8)
	require.InDelta(t, 99.70, repo.balance, 1e-8)
	require.NotContains(t, string(stored.Snapshot), "cdn.example")
	require.NoError(t, restarted.SettleGrokVideoBillingJob(ctx, stored, &OpenAIForwardResult{VideoCount: 1}))
	require.Equal(t, 1, repo.applied)
}

func TestSeedanceDoesNotSettleMissingUsage(t *testing.T) {
	for _, usage := range []string{`{}`, `{"completion_tokens":-1}`, `{"completion_tokens":1.5}`, `{"completion_tokens":"100"}`} {
		result := &OpenAIForwardResult{}
		err := parseSeedanceCompletion([]byte(`{"status":"succeeded","usage":`+usage+`}`), result)
		require.Error(t, err)
		require.Zero(t, result.VideoCount)
	}
	result := &OpenAIForwardResult{}
	require.NoError(t, parseSeedanceCompletion([]byte(`{"status":"succeeded","usage":{"completion_tokens":0}}`), result))
	require.Equal(t, 1, result.VideoCount, "explicit zero usage is distinct from absent usage")
}

func TestMiniMaxH3DurableBillingFreezesRequestedSeconds(t *testing.T) {
	svc, repo, input, pending := newMediaBillingServiceTest(t)
	input.Account.Platform, input.APIKey.Group.Platform = PlatformMiniMax, PlatformMiniMax
	input.Result.Model, input.Result.BillingModel = "MiniMax-H3", "MiniMax-H3"
	base := 0.08
	pending.Provider, pending.Model, pending.BillingModel = VideoProviderMiniMaxH3, "MiniMax-H3", "MiniMax-H3"
	pending.MiniMaxH3BaseCost, pending.VideoDurationSeconds = &base, 5
	ctx := context.Background()
	job, err := svc.CreateGrokVideoBillingJob(ctx, input, pending)
	require.NoError(t, err)
	require.InDelta(t, 0.24, job.ReservedAmount, 1e-8, "retain the site's independent media multiplier")
	require.NoError(t, svc.SubmitGrokVideoBillingJob(ctx, job, MiniMaxH3TaskKey("fixed"), pending))
	job, err = repo.GetMediaBillingJob(ctx, input.User.ID, input.APIKey.ID, MiniMaxH3TaskKey("fixed"))
	require.NoError(t, err)
	require.NoError(t, svc.SettleGrokVideoBillingJob(ctx, job, &OpenAIForwardResult{VideoCount: 1, VideoDurationSeconds: 15}))
	stored, err := repo.GetMediaBillingJobByID(ctx, job.ID)
	require.NoError(t, err)
	require.InDelta(t, 0.24, stored.Intent.Usage.ActualCost, 1e-8)
	require.Equal(t, 5, *stored.Intent.Usage.VideoDurationSeconds)
	require.Equal(t, 1, repo.applied)
}

func TestVideoProviderNeverPublishesTaskBeforeDurableAccept(t *testing.T) {
	for _, provider := range []string{VideoProviderSeedance, VideoProviderMiniMaxH3} {
		t.Run(provider, func(t *testing.T) {
			upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"id":"accepted"}`)}
			svc := &OpenAIGatewayService{httpUpstream: upstream}
			started := false
			ctx := ContextWithGrokVideoBindOwner(context.Background(), GrokVideoBindOwner{
				UserID: 1, APIKeyID: 2, PendingBilling: &GrokVideoPendingBilling{Provider: provider},
				OnRequestStarted: func() { started = true },
				OnAccepted:       func(context.Context, string, GrokVideoPendingBilling) error { return errors.New("ledger unavailable") },
			})
			c, recorder := grokMediaContentTestContext(http.MethodPost, "/v1/videos", nil)
			var err error
			if provider == VideoProviderSeedance {
				_, err = svc.ForwardSeedance(ctx, c, seedanceTestAccount(), SeedanceEndpointCreate, "", []byte(`{"model":"video","content":[{"type":"text","text":"waves"}]}`))
			} else {
				_, err = svc.ForwardMiniMaxH3(ctx, c, miniMaxH3TestAccount(), MiniMaxH3EndpointCreate, "", []byte(`{"model":"MiniMax-H3","prompt":"waves","seconds":"1"}`))
			}
			require.True(t, started)
			require.ErrorIs(t, err, ErrBillingServiceUnavailable)
			require.Empty(t, recorder.Body.String(), "accepted task must not leak before the durable snapshot")
			require.Len(t, upstream.requests, 1, "ambiguous creates are never replayed")
		})
	}
}
