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

// seedanceTariffBillingFixture builds a durable-billing service whose Seedance
// model is priced by a group model-pricing card. The dedicated video-model
// editor only exists for grok groups, so an OpenAI group reaches per-second /
// per-request pricing exclusively through this card (#436).
func seedanceTariffBillingFixture(t *testing.T, mode BillingMode, price float64) (*OpenAIGatewayService, *mediaJobMemoryRepo, *OpenAIRecordUsageInput, GrokVideoPendingBilling) {
	t.Helper()
	svc, repo, input, pending := newMediaBillingServiceTest(t)
	input.Account.Platform = PlatformOpenAI
	input.Result.Model, input.Result.BillingModel = "doubao-seedance-2.5", "doubao-seedance-2.5"
	group := input.APIKey.Group
	group.Platform = PlatformOpenAI
	group.RateMultiplier = 1
	group.VideoRateIndependent = false
	group.VideoRateMultiplier = 1
	group.ModelPricing = []ChannelModelPricing{{
		Platform:        PlatformOpenAI,
		Models:          []string{"doubao-seedance-2.5"},
		BillingMode:     mode,
		PerRequestPrice: &price,
	}}
	svc.resolver = NewModelPricingResolver(&ChannelService{}, svc.billingService)
	pending.Provider, pending.Model, pending.BillingModel = VideoProviderSeedance, "doubao-seedance-2.5", "doubao-seedance-2.5"
	pending.VideoResolution, pending.VideoDurationSeconds = "720p", 12
	return svc, repo, input, pending
}

func TestSeedanceBillingUsesGroupPerRequestCard(t *testing.T) {
	svc, repo, input, pending := seedanceTariffBillingFixture(t, BillingModePerRequest, 10)
	ctx := context.Background()
	job, err := svc.CreateGrokVideoBillingJob(ctx, input, pending)
	require.NoError(t, err, "a per_request card must be accepted at submission")
	require.InDelta(t, 10, job.ReservedAmount, 1e-8)
	require.NoError(t, svc.SubmitGrokVideoBillingJob(ctx, job, SeedanceTaskKey("per-request"), pending))

	stored, err := repo.GetMediaBillingJobByID(ctx, job.ID)
	require.NoError(t, err)
	require.NoError(t, svc.SettleGrokVideoBillingJob(ctx, stored, &OpenAIForwardResult{
		VideoCount: 1, VideoStatus: "succeeded", VideoDurationSeconds: 12,
		VideoTokenBilling: true, Usage: OpenAIUsage{OutputTokens: 12345},
	}))

	stored, err = repo.GetMediaBillingJobByID(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, MediaBillingDone, stored.Status)
	require.Equal(t, string(BillingModePerRequest), *stored.Intent.Usage.BillingMode)
	require.InDelta(t, 10, stored.Intent.Usage.ActualCost, 1e-8,
		"a per-request card must ignore both completion tokens and duration")
	require.InDelta(t, 10, stored.Intent.Usage.TotalCost, 1e-8)
	require.Equal(t, 1, repo.applied)
	require.InDelta(t, 90, repo.balance, 1e-8)
}

func TestSeedanceBillingUsesGroupPerSecondCard(t *testing.T) {
	svc, repo, input, pending := seedanceTariffBillingFixture(t, BillingModeVideo, 0.5)
	ctx := context.Background()
	job, err := svc.CreateGrokVideoBillingJob(ctx, input, pending)
	require.NoError(t, err, "a video (per-second) card must be accepted at submission")
	require.InDelta(t, 6, job.ReservedAmount, 1e-8, "0.5 USD/s x 12 requested seconds")
	require.NoError(t, svc.SubmitGrokVideoBillingJob(ctx, job, SeedanceTaskKey("per-second"), pending))

	stored, err := repo.GetMediaBillingJobByID(ctx, job.ID)
	require.NoError(t, err)
	require.NoError(t, svc.SettleGrokVideoBillingJob(ctx, stored, &OpenAIForwardResult{
		VideoCount: 1, VideoStatus: "succeeded", VideoDurationSeconds: 12,
	}))

	stored, err = repo.GetMediaBillingJobByID(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, MediaBillingDone, stored.Status)
	require.InDelta(t, 6, stored.Intent.Usage.ActualCost, 1e-8)
	require.NotNil(t, stored.Intent.Usage.VideoDurationSeconds)
	require.Equal(t, 12, *stored.Intent.Usage.VideoDurationSeconds)
	require.Equal(t, 1, repo.applied)
}

func TestSeedancePerSecondBillingDurationBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name           string
		requested      int
		statusDuration int
		wantSeconds    int
	}{
		{"observed status duration wins", 12, 20, 20},
		{"requested duration when status omits it", 12, 0, 12},
		{"ark maximum requested duration", 60, 0, 60},
		{"observed status above the Ark ceiling is clamped", 12, 65, SeedanceVideoBillingMaxDurationSeconds},
		{"shared default when both are unknown", 0, 0, VideoBillingDefaultDurationSeconds},
		{"over-long request is clamped to the Ark ceiling", 90, 0, SeedanceVideoBillingMaxDurationSeconds},
		{"negative request is treated as unknown", -5, 0, VideoBillingDefaultDurationSeconds},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, input, pending := seedanceTariffBillingFixture(t, BillingModeVideo, 0.5)
			pending.VideoDurationSeconds = tc.requested
			ctx := context.Background()
			job, err := svc.CreateGrokVideoBillingJob(ctx, input, pending)
			require.NoError(t, err)
			require.NoError(t, svc.SubmitGrokVideoBillingJob(ctx, job, SeedanceTaskKey("boundary"), pending))
			stored, err := repo.GetMediaBillingJobByID(ctx, job.ID)
			require.NoError(t, err)
			require.NoError(t, svc.SettleGrokVideoBillingJob(ctx, stored, &OpenAIForwardResult{
				VideoCount: 1, VideoStatus: "succeeded", VideoDurationSeconds: tc.statusDuration,
			}))
			stored, err = repo.GetMediaBillingJobByID(ctx, job.ID)
			require.NoError(t, err)
			require.NotNil(t, stored.Intent.Usage.VideoDurationSeconds)
			require.Equal(t, tc.wantSeconds, *stored.Intent.Usage.VideoDurationSeconds)
			require.InDelta(t, 0.5*float64(tc.wantSeconds), stored.Intent.Usage.ActualCost, 1e-8)
		})
	}
}

func TestSeedancePerRequestCardWithoutPriceFailsClosed(t *testing.T) {
	for name, mutate := range map[string]func(*ChannelModelPricing){
		"nil price":  func(p *ChannelModelPricing) { p.PerRequestPrice = nil },
		"zero price": func(p *ChannelModelPricing) { zero := 0.0; p.PerRequestPrice = &zero },
	} {
		t.Run(name, func(t *testing.T) {
			svc, _, input, pending := seedanceTariffBillingFixture(t, BillingModePerRequest, 10)
			mutate(&input.APIKey.Group.ModelPricing[0])
			_, err := svc.CreateGrokVideoBillingJob(context.Background(), input, pending)
			require.ErrorContains(t, err, "asynchronous video price must be greater than zero")
		})
	}
}

func TestSeedanceAsyncVideoTokenBilledFollowsTariff(t *testing.T) {
	ctx := context.Background()
	svc, _, input, _ := seedanceTariffBillingFixture(t, BillingModePerRequest, 10)
	require.False(t, svc.AsyncVideoTokenBilled(ctx, VideoProviderSeedance, "doubao-seedance-2.5", input.APIKey))

	svc, _, input, _ = seedanceTariffBillingFixture(t, BillingModeVideo, 0.5)
	require.False(t, svc.AsyncVideoTokenBilled(ctx, VideoProviderSeedance, "doubao-seedance-2.5", input.APIKey))

	svc, _, input, _ = seedanceTariffBillingFixture(t, BillingModeToken, 10)
	require.True(t, svc.AsyncVideoTokenBilled(ctx, VideoProviderSeedance, "doubao-seedance-2.5", input.APIKey))

	// Without any operator card the legacy token default is preserved.
	svc.resolver = nil
	require.True(t, svc.AsyncVideoTokenBilled(ctx, VideoProviderSeedance, "doubao-seedance-2.5", input.APIKey))
	require.False(t, svc.AsyncVideoTokenBilled(ctx, VideoProviderMiniMaxH3, "MiniMax-H3", input.APIKey))
}

func TestSeedanceRepeatedStatusPollsChargeOnce(t *testing.T) {
	svc, repo, input, pending := seedanceTariffBillingFixture(t, BillingModePerRequest, 10)
	ctx := context.Background()
	job, err := svc.CreateGrokVideoBillingJob(ctx, input, pending)
	require.NoError(t, err)
	require.NoError(t, svc.SubmitGrokVideoBillingJob(ctx, job, SeedanceTaskKey("polled"), pending))

	stored, err := repo.GetMediaBillingJobByID(ctx, job.ID)
	require.NoError(t, err)
	// Still processing: not billable, hold stays.
	require.NoError(t, svc.SettleGrokVideoBillingJob(ctx, stored, &OpenAIForwardResult{VideoStatus: "running"}))
	require.Equal(t, 0, repo.applied)
	require.InDelta(t, 90, repo.balance, 1e-8)

	// First successful poll settles once.
	require.NoError(t, svc.SettleGrokVideoBillingJob(ctx, stored, &OpenAIForwardResult{VideoCount: 1, VideoStatus: "succeeded", VideoDurationSeconds: 12}))
	// Every later poll (status or content download) must be a no-op.
	stored, err = repo.GetMediaBillingJobByID(ctx, job.ID)
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		require.NoError(t, svc.SettleGrokVideoBillingJob(ctx, stored, &OpenAIForwardResult{VideoCount: 1, VideoStatus: "succeeded", VideoDurationSeconds: 12}))
	}
	require.Equal(t, 1, repo.applied)
	require.InDelta(t, 90, repo.balance, 1e-8)
}
