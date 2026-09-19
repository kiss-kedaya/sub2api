//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type mediaJobMemoryRepo struct {
	mu        sync.Mutex
	jobs      map[string]*MediaBillingJob
	applied   int
	applyErr  error
	intentErr error
	balance   float64
}

func cloneMediaTestJob(job *MediaBillingJob) *MediaBillingJob {
	data, _ := json.Marshal(job)
	var result MediaBillingJob
	_ = json.Unmarshal(data, &result)
	return &result
}
func (r *mediaJobMemoryRepo) CreateMediaBillingJob(_ context.Context, job *MediaBillingJob) (*MediaBillingJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.jobs == nil {
		r.jobs = make(map[string]*MediaBillingJob)
	}
	if existing := r.jobs[job.ID]; existing != nil {
		return cloneMediaTestJob(existing), nil
	}
	if job.SubscriptionID == nil {
		if r.balance < job.ReservedAmount {
			return nil, ErrBalanceWithholdingFailed
		}
		r.balance -= job.ReservedAmount
	}
	r.jobs[job.ID] = cloneMediaTestJob(job)
	return cloneMediaTestJob(job), nil
}
func (r *mediaJobMemoryRepo) SubmitMediaBillingJob(_ context.Context, id, task string, snapshot json.RawMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	j := r.jobs[id]
	j.UpstreamTaskID, j.Snapshot, j.Status = task, snapshot, MediaBillingSubmitted
	return nil
}
func (r *mediaJobMemoryRepo) GetMediaBillingJob(_ context.Context, user, key int64, task string) (*MediaBillingJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, j := range r.jobs {
		if j.UserID == user && j.APIKeyID == key && j.UpstreamTaskID == task {
			return cloneMediaTestJob(j), nil
		}
	}
	return nil, ErrMediaBillingJobNotFound
}
func (r *mediaJobMemoryRepo) GetMediaBillingJobByID(_ context.Context, id string) (*MediaBillingJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if j := r.jobs[id]; j != nil {
		return cloneMediaTestJob(j), nil
	}
	return nil, ErrMediaBillingJobNotFound
}
func (r *mediaJobMemoryRepo) PrepareMediaBillingIntent(_ context.Context, id string, i *MediaBillingIntent) (*MediaBillingIntent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.intentErr != nil {
		return nil, r.intentErr
	}
	j := r.jobs[id]
	if j.Intent == nil {
		j.Intent = i
		j.Status = MediaBillingReady
	}
	return cloneMediaTestJob(j).Intent, nil
}
func (r *mediaJobMemoryRepo) ApplyMediaBillingJob(_ context.Context, id string) (*UsageBillingApplyResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.applyErr != nil {
		return nil, r.applyErr
	}
	j := r.jobs[id]
	if j.Status == MediaBillingBilled || j.Status == MediaBillingDone {
		return &UsageBillingApplyResult{}, nil
	}
	r.applied++
	r.balance += j.ReservedAmount - j.Intent.Command.BalanceCost
	j.Status = MediaBillingBilled
	return &UsageBillingApplyResult{Applied: true}, nil
}
func (r *mediaJobMemoryRepo) FailMediaBillingJob(_ context.Context, id string) (*UsageBillingApplyResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	j := r.jobs[id]
	if j.Status == MediaBillingFailed {
		return &UsageBillingApplyResult{}, nil
	}
	if j.Intent != nil {
		return nil, ErrUsageBillingRequestConflict
	}
	r.balance += j.ReservedAmount
	j.Status = MediaBillingFailed
	return &UsageBillingApplyResult{Applied: true}, nil
}
func (r *mediaJobMemoryRepo) CompleteMediaBillingJob(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobs[id].Status = MediaBillingDone
	return nil
}
func (r *mediaJobMemoryRepo) ListMediaBillingJobs(_ context.Context, _ int) ([]*MediaBillingJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var jobs []*MediaBillingJob
	for _, j := range r.jobs {
		if j.Status == MediaBillingSubmitted || j.Status == MediaBillingReady || j.Status == MediaBillingBilled {
			jobs = append(jobs, cloneMediaTestJob(j))
		}
	}
	return jobs, nil
}
func (r *mediaJobMemoryRepo) RetryMediaBillingJob(context.Context, string, time.Duration) error {
	return nil
}

type mediaUsageMemoryRepo struct {
	UsageLogRepository
	mu   sync.Mutex
	logs map[string]*UsageLog
}

func (r *mediaUsageMemoryRepo) Create(_ context.Context, log *UsageLog) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.logs == nil {
		r.logs = make(map[string]*UsageLog)
	}
	_, exists := r.logs[log.RequestID]
	copyLog := *log
	r.logs[log.RequestID] = &copyLog
	return !exists, nil
}

func newMediaBillingServiceTest(t *testing.T) (*OpenAIGatewayService, *mediaJobMemoryRepo, *OpenAIRecordUsageInput, GrokVideoPendingBilling) {
	t.Helper()
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Default.RateMultiplier = 1
	repo := &mediaJobMemoryRepo{balance: 100}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(&openAIRecordUsageLogRepoStub{inserted: true}, &openAIRecordUsageBillingRepoStub{}, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	svc.cfg = cfg
	svc.billingService = NewBillingService(cfg, nil)
	svc.mediaBillingJobs = repo
	svc.usageLogRepo = &mediaUsageMemoryRepo{}
	group := &Group{ID: 17, Platform: PlatformGrok, Status: StatusActive, Hydrated: true, RateMultiplier: 2,
		VideoRateIndependent: true, VideoRateMultiplier: 3, VideoModelPrices: map[string]map[string]float64{VideoPriceFamilyGrokImagineVideo: {"720p": 0.2}}}
	key := &APIKey{ID: 71, UserID: 42, User: &User{ID: 42}, GroupID: &group.ID, Group: group}
	input := &OpenAIRecordUsageInput{Result: &OpenAIForwardResult{Model: "grok-imagine-video", BillingModel: "grok-imagine-video"},
		APIKey: key, User: key.User, Account: &Account{ID: 17001, Platform: PlatformGrok, Type: AccountTypeAPIKey}, PricingAt: time.Now()}
	pending := GrokVideoPendingBilling{GroupID: 17, BindingGroupID: 9, Model: "grok-imagine-video", BillingModel: "grok-imagine-video", VideoResolution: "720p", VideoDurationSeconds: 6, CreatedAt: time.Now().Format(time.RFC3339Nano)}
	return svc, repo, input, pending
}

func TestGrokVideoDurableBillingRecoversWithoutClientPolling(t *testing.T) {
	svc, repo, input, pending := newMediaBillingServiceTest(t)
	ctx := context.Background()
	job, err := svc.CreateGrokVideoBillingJob(ctx, input, pending)
	require.NoError(t, err)
	require.InDelta(t, 3.6, job.ReservedAmount, 1e-8)
	require.NoError(t, svc.SubmitGrokVideoBillingJob(ctx, job, "task_background", pending))
	// A separate gateway instance has only durable job data, as after a restart.
	restarted := &OpenAIGatewayService{cfg: svc.cfg, mediaBillingJobs: repo, usageLogRepo: svc.usageLogRepo}
	job, err = repo.GetMediaBillingJob(ctx, 42, 71, "task_background")
	require.NoError(t, err)
	require.NoError(t, restarted.SettleGrokVideoBillingJob(ctx, job, &OpenAIForwardResult{VideoCount: 1, VideoDurationSeconds: 6}))
	require.Equal(t, 1, repo.applied)
	require.InDelta(t, 96.4, repo.balance, 1e-8)
	stored, err := repo.GetMediaBillingJobByID(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, MediaBillingDone, stored.Status)
	require.Equal(t, int64(17), *stored.Intent.Usage.GroupID)
	require.Equal(t, "grok-video:task_background", stored.Intent.Command.RequestID)
	require.NoError(t, restarted.SettleGrokVideoBillingJob(ctx, job, &OpenAIForwardResult{VideoCount: 1, VideoDurationSeconds: 6}))
	require.Equal(t, 1, repo.applied)
}

func TestGrokVideoDurableBillingFreezesPricesAndRetriesFailure(t *testing.T) {
	svc, repo, input, pending := newMediaBillingServiceTest(t)
	ctx := context.Background()
	job, err := svc.CreateGrokVideoBillingJob(ctx, input, pending)
	require.NoError(t, err)
	require.NoError(t, svc.SubmitGrokVideoBillingJob(ctx, job, "task_frozen", pending))
	input.APIKey.Group.VideoModelPrices[VideoPriceFamilyGrokImagineVideo]["720p"] = 0.001
	input.APIKey.Group.VideoRateMultiplier = 0.07
	job, err = repo.GetMediaBillingJob(ctx, 42, 71, "task_frozen")
	require.NoError(t, err)
	repo.applyErr = errors.New("ledger temporarily unavailable")
	require.Error(t, svc.SettleGrokVideoBillingJob(ctx, job, &OpenAIForwardResult{VideoCount: 1, VideoDurationSeconds: 6}))
	stored, err := repo.GetMediaBillingJobByID(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, MediaBillingReady, stored.Status)
	require.InDelta(t, 3.6, stored.Intent.Command.BalanceCost, 1e-8)
	repo.applyErr = nil
	require.NoError(t, svc.settleMediaBillingJob(ctx, stored, nil))
	require.Equal(t, 1, repo.applied)
}

func TestGrokVideoDurableBillingRefundsKnownFailureOnce(t *testing.T) {
	svc, repo, input, pending := newMediaBillingServiceTest(t)
	job, err := svc.CreateGrokVideoBillingJob(context.Background(), input, pending)
	require.NoError(t, err)
	require.NoError(t, svc.FailGrokVideoBillingJob(context.Background(), job))
	require.NoError(t, svc.FailGrokVideoBillingJob(context.Background(), job))
	require.InDelta(t, 100, repo.balance, 1e-8)
}

func TestGrokVideoDoesNotPublishBeforeBilling(t *testing.T) {
	body := []byte(`{"model":"grok-imagine-video"}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"status":"done","video":{"url":"https://vidgen.x.ai/test.mp4","duration":6}}`))}}}
	svc := newOpenAIRejectedFieldTestService(upstream)
	account := newOpenAIRejectedFieldTestAccount()
	account.Platform = PlatformGrok
	c := newOpenAIRejectedFieldTestContext(body)
	ctx := ContextWithGrokMediaBeforePublish(context.Background(), func(context.Context, *OpenAIForwardResult) error { return errors.New("billing failed") })
	_, err := svc.ForwardGrokMedia(ctx, c, account, GrokMediaEndpointVideoStatus, "task_order", nil, "")
	require.Error(t, err)
	require.False(t, c.Writer.Written())
	require.Equal(t, -1, c.Writer.Size())
}

func TestVideoUnitAmountsRoundOnlyAfterMultiplication(t *testing.T) {
	i := MediaBillingIntent{RawAmounts: [5]float64{0.000000015, 0, 0.000000015, 0, 0}}
	result := scaleVideoMediaIntent(i, 6, 6)
	require.Equal(t, 0.00000009, result.Command.BalanceCost)
	require.Equal(t, result.Command.BalanceCost, result.Usage.ActualCost)
}
