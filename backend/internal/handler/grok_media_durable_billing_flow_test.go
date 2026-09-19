//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type durableMediaHandlerRepo struct {
	service.UsageBillingRepository

	mu          sync.Mutex
	jobs        map[string]*service.MediaBillingJob
	balance     float64
	applyCount  int
	failCount   int
	directApply int
	applyErr    error
}

var _ service.MediaBillingJobRepository = (*durableMediaHandlerRepo)(nil)

func cloneDurableMediaHandlerJob(job *service.MediaBillingJob) *service.MediaBillingJob {
	if job == nil {
		return nil
	}
	payload, _ := json.Marshal(job)
	var cloned service.MediaBillingJob
	_ = json.Unmarshal(payload, &cloned)
	return &cloned
}

func (r *durableMediaHandlerRepo) Apply(context.Context, *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	r.mu.Lock()
	r.directApply++
	r.mu.Unlock()
	return nil, errors.New("unexpected legacy usage billing apply")
}

func (r *durableMediaHandlerRepo) CreateMediaBillingJob(_ context.Context, job *service.MediaBillingJob) (*service.MediaBillingJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.jobs == nil {
		r.jobs = make(map[string]*service.MediaBillingJob)
	}
	if existing := r.jobs[job.ID]; existing != nil {
		return cloneDurableMediaHandlerJob(existing), nil
	}
	if job.SubscriptionID == nil {
		if r.balance < job.ReservedAmount {
			return nil, service.ErrBalanceWithholdingFailed
		}
		r.balance = service.QuantizeUsageBillingAmount(r.balance - job.ReservedAmount)
	}
	r.jobs[job.ID] = cloneDurableMediaHandlerJob(job)
	return cloneDurableMediaHandlerJob(job), nil
}

func (r *durableMediaHandlerRepo) SubmitMediaBillingJob(_ context.Context, id, taskID string, snapshot json.RawMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	job := r.jobs[id]
	if job == nil {
		return service.ErrMediaBillingJobNotFound
	}
	job.UpstreamTaskID = taskID
	job.Snapshot = append(json.RawMessage(nil), snapshot...)
	job.Status = service.MediaBillingSubmitted
	return nil
}

func (r *durableMediaHandlerRepo) GetMediaBillingJob(_ context.Context, userID, keyID int64, taskID string) (*service.MediaBillingJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, job := range r.jobs {
		if job.UserID == userID && job.APIKeyID == keyID && job.UpstreamTaskID == taskID {
			return cloneDurableMediaHandlerJob(job), nil
		}
	}
	return nil, service.ErrMediaBillingJobNotFound
}

func (r *durableMediaHandlerRepo) GetMediaBillingJobByID(_ context.Context, id string) (*service.MediaBillingJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if job := r.jobs[id]; job != nil {
		return cloneDurableMediaHandlerJob(job), nil
	}
	return nil, service.ErrMediaBillingJobNotFound
}

func (r *durableMediaHandlerRepo) PrepareMediaBillingIntent(_ context.Context, id string, intent *service.MediaBillingIntent) (*service.MediaBillingIntent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job := r.jobs[id]
	if job == nil {
		return nil, service.ErrMediaBillingJobNotFound
	}
	if job.Intent == nil {
		payload, _ := json.Marshal(intent)
		var frozen service.MediaBillingIntent
		_ = json.Unmarshal(payload, &frozen)
		job.Intent = &frozen
		job.Status = service.MediaBillingReady
	}
	return cloneDurableMediaHandlerJob(job).Intent, nil
}

func (r *durableMediaHandlerRepo) ApplyMediaBillingJob(_ context.Context, id string) (*service.UsageBillingApplyResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.applyErr != nil {
		return nil, r.applyErr
	}
	job := r.jobs[id]
	if job == nil {
		return nil, service.ErrMediaBillingJobNotFound
	}
	if job.Status == service.MediaBillingBilled || job.Status == service.MediaBillingDone {
		balance := r.balance
		return &service.UsageBillingApplyResult{Applied: false, NewBalance: &balance}, nil
	}
	if job.Intent == nil {
		return nil, errors.New("media intent is missing")
	}
	r.applyCount++
	r.balance = service.QuantizeUsageBillingAmount(r.balance + job.ReservedAmount - job.Intent.Command.BalanceCost)
	job.Status = service.MediaBillingBilled
	balance := r.balance
	return &service.UsageBillingApplyResult{Applied: true, NewBalance: &balance}, nil
}

func (r *durableMediaHandlerRepo) FailMediaBillingJob(_ context.Context, id string) (*service.UsageBillingApplyResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job := r.jobs[id]
	if job == nil {
		return nil, service.ErrMediaBillingJobNotFound
	}
	if job.Status == service.MediaBillingFailed {
		balance := r.balance
		return &service.UsageBillingApplyResult{Applied: false, NewBalance: &balance}, nil
	}
	if job.Intent != nil {
		return nil, service.ErrUsageBillingRequestConflict
	}
	r.failCount++
	r.balance = service.QuantizeUsageBillingAmount(r.balance + job.ReservedAmount)
	job.Status = service.MediaBillingFailed
	balance := r.balance
	return &service.UsageBillingApplyResult{Applied: true, NewBalance: &balance}, nil
}

func (r *durableMediaHandlerRepo) CompleteMediaBillingJob(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	job := r.jobs[id]
	if job == nil {
		return service.ErrMediaBillingJobNotFound
	}
	job.Status = service.MediaBillingDone
	return nil
}

func (r *durableMediaHandlerRepo) ListMediaBillingJobs(_ context.Context, _ int) ([]*service.MediaBillingJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	jobs := make([]*service.MediaBillingJob, 0, len(r.jobs))
	for _, job := range r.jobs {
		if job.Status == service.MediaBillingSubmitted || job.Status == service.MediaBillingReady || job.Status == service.MediaBillingBilled {
			jobs = append(jobs, cloneDurableMediaHandlerJob(job))
		}
	}
	return jobs, nil
}

func (r *durableMediaHandlerRepo) RetryMediaBillingJob(context.Context, string, time.Duration) error {
	return nil
}

func (r *durableMediaHandlerRepo) snapshotByTask(taskID string) (*service.MediaBillingJob, float64, int, int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, job := range r.jobs {
		if job.UpstreamTaskID == taskID {
			return cloneDurableMediaHandlerJob(job), r.balance, r.applyCount, r.failCount, r.directApply
		}
	}
	return nil, r.balance, r.applyCount, r.failCount, r.directApply
}

func (r *durableMediaHandlerRepo) onlyJobSnapshot() (*service.MediaBillingJob, float64, int, int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, job := range r.jobs {
		return cloneDurableMediaHandlerJob(job), r.balance, r.applyCount, r.failCount, r.directApply
	}
	return nil, r.balance, r.applyCount, r.failCount, r.directApply
}

func (r *durableMediaHandlerRepo) setApplyError(err error) {
	r.mu.Lock()
	r.applyErr = err
	r.mu.Unlock()
}

type durableMediaBalanceCache struct {
	service.BillingCache
	repo *durableMediaHandlerRepo

	mu           sync.Mutex
	balanceReads int
}

func (c *durableMediaBalanceCache) GetUserBalance(context.Context, int64) (float64, error) {
	c.mu.Lock()
	c.balanceReads++
	c.mu.Unlock()
	c.repo.mu.Lock()
	defer c.repo.mu.Unlock()
	return c.repo.balance, nil
}

func (c *durableMediaBalanceCache) SetUserBalance(_ context.Context, _ int64, balance float64) error {
	c.repo.mu.Lock()
	c.repo.balance = balance
	c.repo.mu.Unlock()
	return nil
}

func (c *durableMediaBalanceCache) SetUserBalanceIfLower(_ context.Context, _ int64, balance float64) error {
	c.repo.mu.Lock()
	if balance < c.repo.balance {
		c.repo.balance = balance
	}
	c.repo.mu.Unlock()
	return nil
}

func (c *durableMediaBalanceCache) InvalidateUserBalance(context.Context, int64) error { return nil }

func (c *durableMediaBalanceCache) reads() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.balanceReads
}

type durableMediaUsageRepo struct {
	service.UsageLogRepository
	mu     sync.Mutex
	logs   map[string]*service.UsageLog
	writes int
}

func (r *durableMediaUsageRepo) Create(_ context.Context, usage *service.UsageLog) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.logs == nil {
		r.logs = make(map[string]*service.UsageLog)
	}
	r.writes++
	_, exists := r.logs[usage.RequestID]
	copy := *usage
	r.logs[usage.RequestID] = &copy
	return !exists, nil
}

func (r *durableMediaUsageRepo) snapshot() (map[string]*service.UsageLog, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	logs := make(map[string]*service.UsageLog, len(r.logs))
	for key, usage := range r.logs {
		copy := *usage
		logs[key] = &copy
	}
	return logs, r.writes
}

type durableMediaUpstream struct {
	service.HTTPUpstream

	mu           sync.Mutex
	createCalls  int
	createIDs    []int64
	statusCalls  int
	contentCalls int
	createStatus int
	createErr    error
	taskID       string
	content      string
}

func (u *durableMediaUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if req.Method == http.MethodPost {
		u.createCalls++
		u.createIDs = append(u.createIDs, accountID)
		if u.createErr != nil {
			return nil, u.createErr
		}
		if strings.Contains(req.URL.Path, "/images/") {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{"data":[{"url":"https://example.test/private-generated-image.png"}]}`))}, nil
		}
		status := u.createStatus
		if status == 0 {
			status = http.StatusOK
		}
		body := `{"request_id":"` + u.taskID + `","status":"pending"}`
		if status >= 400 {
			body = `{"error":{"message":"explicit rejection"}}`
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
	if req.URL.Host == "vidgen.x.ai" {
		u.contentCalls++
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"video/mp4"}}, Body: io.NopCloser(strings.NewReader(u.content))}, nil
	}
	u.statusCalls++
	body := `{"request_id":"` + u.taskID + `","status":"done","model":"grok-imagine-video","video":{"url":"https://vidgen.x.ai/test.mp4","duration":6}}`
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func (u *durableMediaUpstream) counts() (create, status, content int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.createCalls, u.statusCalls, u.contentCalls
}

func (u *durableMediaUpstream) createAccountIDs() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.createIDs...)
}

type durableMediaHandlerFixture struct {
	repo     *durableMediaHandlerRepo
	cache    *durableMediaBalanceCache
	usage    *durableMediaUsageRepo
	upstream *durableMediaUpstream
	key      *service.APIKey
	handlers []*OpenAIGatewayHandler
	taskID   string
	initial  float64
}

func newDurableMediaHandlerFixture(t *testing.T) *durableMediaHandlerFixture {
	return newDurableMediaHandlerFixtureWithAccounts(t, 1)
}

func newDurableMediaHandlerFixtureWithAccounts(t *testing.T, accountCount int) *durableMediaHandlerFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	primary := &service.Group{ID: 9, Platform: service.PlatformGrok, Status: service.StatusActive, Hydrated: true,
		AllowImageGeneration: true, RateMultiplier: 0.07, VideoRateMultiplier: 0.1, VideoRateIndependent: true,
		VideoModelPrices: map[string]map[string]float64{service.VideoPriceFamilyGrokImagineVideo: {"720p": 0.01}}}
	selected := &service.Group{ID: 17, Platform: service.PlatformGrok, Status: service.StatusActive, Hydrated: true,
		AllowImageGeneration: true, RateMultiplier: 2, VideoRateMultiplier: 3, VideoRateIndependent: true,
		VideoModelPrices: map[string]map[string]float64{service.VideoPriceFamilyGrokImagineVideo: {"720p": 0.2}}}
	accounts := make([]service.Account, accountCount)
	accountPointers := make([]*service.Account, accountCount)
	for i := range accounts {
		accounts[i] = service.Account{ID: 17001 + int64(i), Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{17}, Concurrency: 10,
			Credentials: map[string]any{"api_key": "synthetic-video-key", "model_mapping": map[string]any{"grok-imagine-video": "grok-imagine-video", "grok-imagine-image": "grok-imagine-image"}}}
		accountPointers[i] = &accounts[i]
	}
	accountRepo := openAIImagesFailoverAccountRepo{accounts: accounts}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Default.RateMultiplier = 1
	cfg.Billing.BalancePreauthorizationEnabled = false
	initial := service.QuantizeUsageBillingAmount(0.2 * 6 * 3)
	repo := &durableMediaHandlerRepo{balance: initial, jobs: make(map[string]*service.MediaBillingJob)}
	cache := &durableMediaBalanceCache{repo: repo}
	usage := &durableMediaUsageRepo{logs: make(map[string]*service.UsageLog)}
	taskID := strings.NewReplacer("/", "-", "=", "-").Replace(t.Name())
	upstream := &durableMediaUpstream{taskID: taskID, content: "durable-video-content"}
	gatewayCache := &videoRouteBillingCache{bindings: map[string]int64{}, pending: map[string][]byte{}, billed: map[string]bool{}}
	key := &service.APIKey{ID: 71, UserID: 42, User: &service.User{ID: 42, Status: service.StatusActive, Balance: initial},
		GroupID: &primary.ID, Group: primary, RouteGroupIDs: []int64{9, 17}}

	fixture := &durableMediaHandlerFixture{repo: repo, cache: cache, usage: usage, upstream: upstream, key: key, taskID: taskID, initial: initial}
	for range 2 {
		snapshot := service.NewSchedulerSnapshotService(&smartRouteHandlerSnapshot{fakeSchedulerCache{accounts: accountPointers}}, nil, accountRepo,
			smartRouteHandlerGroups{groups: map[int64]*service.Group{9: primary, 17: selected}}, cfg)
		for _, id := range []int64{9, 17} {
			_, err := snapshot.GetGroupByIDLite(context.Background(), id)
			require.NoError(t, err)
		}
		billingCache := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
		t.Cleanup(billingCache.Stop)
		gateway := service.NewOpenAIGatewayService(accountRepo, usage, repo, nil, nil, nil, gatewayCache, cfg, snapshot, nil,
			service.NewBillingService(cfg, nil), nil, billingCache, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
		t.Cleanup(gateway.CloseOpenAIWSPool)
		concurrency := service.NewConcurrencyService(testutil.StubConcurrencyCache{})
		fixture.handlers = append(fixture.handlers, NewOpenAIGatewayHandler(gateway, concurrency, billingCache, &service.APIKeyService{}, nil, nil, nil, nil, cfg))
	}
	return fixture
}

func (f *durableMediaHandlerFixture) request(handlerIndex int, method, endpoint string, beforeWrite func()) *httptest.ResponseRecorder {
	body, path := "", "/v1/videos/"+f.taskID
	if method == http.MethodPost {
		body, path = `{"model":"grok-imagine-video","prompt":"test","duration":6,"resolution":"720p"}`, "/v1/videos/generations"
	} else if endpoint == "content" {
		path += "/content"
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	if beforeWrite != nil {
		c.Writer = &videoRouteResponseWriter{ResponseWriter: c.Writer, beforeWrite: beforeWrite}
	}
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(service.ContextWithAPIKeyRoute(context.Background(), f.key))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "request_id", Value: f.taskID}}
	c.Set(string(middleware.ContextKeyAPIKey), f.key)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: f.key.UserID, Concurrency: 0})
	if method == http.MethodPost {
		f.handlers[handlerIndex].GrokVideoGeneration(c)
	} else if endpoint == "content" {
		f.handlers[handlerIndex].GrokVideoContent(c)
	} else {
		f.handlers[handlerIndex].GrokVideoStatus(c)
	}
	return recorder
}

func (f *durableMediaHandlerFixture) reorderKey() {
	selected := *f.key
	selected.GroupID = ptrInt64(17)
	selected.Group = nil
	selected.RouteGroupIDs = []int64{17, 9}
	f.key = &selected
}

func TestGrokVideoDurableHandlerFreezesSelectedRouteBeforePublishingTask(t *testing.T) {
	for _, endpoint := range []string{"status", "content"} {
		t.Run(endpoint, func(t *testing.T) {
			f := newDurableMediaHandlerFixture(t)
			var checked sync.Once
			create := f.request(0, http.MethodPost, "create", func() {
				checked.Do(func() {
					job, balance, _, _, direct := f.repo.snapshotByTask(f.taskID)
					require.NotNil(t, job, "OnAccepted must persist the upstream task before task_id is written")
					require.Equal(t, service.MediaBillingSubmitted, job.Status)
					require.Equal(t, int64(17), job.GroupID)
					require.InDelta(t, f.initial, job.ReservedAmount, 1e-8)
					require.Zero(t, balance, "selected-route hold must exhaust the test wallet")
					require.Zero(t, direct, "durable media flow must not call legacy Apply")
				})
			})
			require.Equal(t, http.StatusOK, create.Code, create.Body.String())
			require.Contains(t, create.Body.String(), f.taskID)
			f.reorderKey()
			readsBefore := f.cache.reads()

			result := f.request(1, http.MethodGet, endpoint, nil)
			require.Equal(t, http.StatusOK, result.Code, result.Body.String())
			if endpoint == "content" {
				require.Equal(t, f.upstream.content, result.Body.String())
			}
			require.Equal(t, readsBefore, f.cache.reads(), "owner lookup with a durable hold must bypass zero-balance admission")

			job, balance, applied, failed, direct := f.repo.snapshotByTask(f.taskID)
			require.Equal(t, service.MediaBillingDone, job.Status)
			require.Zero(t, balance)
			require.Equal(t, 1, applied)
			require.Zero(t, failed)
			require.Zero(t, direct)
			require.NotNil(t, job.Intent)
			require.Equal(t, int64(17), *job.Intent.Usage.GroupID)
			require.InDelta(t, f.initial, job.Intent.Command.BalanceCost, 1e-8)
		})
	}
}

func TestGrokVideoDurableHandlerContentDoesNotLeakWhenSettlementFails(t *testing.T) {
	f := newDurableMediaHandlerFixture(t)
	create := f.request(0, http.MethodPost, "create", nil)
	require.Equal(t, http.StatusOK, create.Code, create.Body.String())
	f.repo.setApplyError(errors.New("synthetic ledger outage"))

	content := f.request(1, http.MethodGet, "content", nil)
	require.Equal(t, http.StatusServiceUnavailable, content.Code, content.Body.String())
	require.NotContains(t, content.Body.String(), f.upstream.content)
	_, statusCalls, contentCalls := f.upstream.counts()
	require.Equal(t, 1, statusCalls)
	require.Equal(t, 1, contentCalls, "test must reach the real content response before billing blocks publication")
	_, balance, applied, failed, _ := f.repo.snapshotByTask(f.taskID)
	require.Zero(t, balance, "failed settlement must retain the hold")
	require.Zero(t, applied)
	require.Zero(t, failed)
}

func TestGrokVideoDurableHandlerSharedRepositoryChargesConcurrentPollOnce(t *testing.T) {
	f := newDurableMediaHandlerFixture(t)
	require.Equal(t, http.StatusOK, f.request(0, http.MethodPost, "create", nil).Code)
	f.reorderKey()

	responses := make(chan *httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for instance := range 2 {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			responses <- f.request(index, http.MethodGet, "status", nil)
		}(instance)
	}
	wg.Wait()
	close(responses)
	for response := range responses {
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	}

	job, balance, applied, failed, direct := f.repo.snapshotByTask(f.taskID)
	require.Equal(t, service.MediaBillingDone, job.Status)
	require.Zero(t, balance)
	require.Equal(t, 1, applied)
	require.Zero(t, failed)
	require.Zero(t, direct)
	logs, _ := f.usage.snapshot()
	require.Len(t, logs, 1)
}

func TestGrokVideoDurableHandlerCreateFailureSemantics(t *testing.T) {
	t.Run("explicit upstream 400 refunds hold", func(t *testing.T) {
		f := newDurableMediaHandlerFixture(t)
		f.upstream.createStatus = http.StatusBadRequest
		response := f.request(0, http.MethodPost, "create", nil)
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		job, balance, applied, failed, direct := f.repo.onlyJobSnapshot()
		require.NotNil(t, job)
		require.Equal(t, service.MediaBillingFailed, job.Status)
		require.InDelta(t, f.initial, balance, 1e-8)
		require.Zero(t, applied)
		require.Equal(t, 1, failed)
		require.Zero(t, direct)
		create, _, _ := f.upstream.counts()
		require.Equal(t, 1, create)
	})

	t.Run("transport uncertainty keeps hold and does not resubmit", func(t *testing.T) {
		f := newDurableMediaHandlerFixtureWithAccounts(t, 2)
		f.upstream.createErr = errors.New("synthetic connection reset after write")
		response := f.request(0, http.MethodPost, "create", nil)
		require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "do not resubmit")
		require.NotContains(t, response.Body.String(), f.taskID)
		job, balance, applied, failed, direct := f.repo.onlyJobSnapshot()
		require.NotNil(t, job)
		require.Equal(t, service.MediaBillingCreating, job.Status)
		require.Zero(t, balance, "uncertain submission must retain the hold")
		require.Zero(t, applied)
		require.Zero(t, failed)
		require.Zero(t, direct)
		require.Len(t, f.upstream.createAccountIDs(), 1, "uncertain submission must not fail over to the spare account")
	})
}

func TestGrokAsyncImagePublishesOnlyAfterDurableSettlement(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(fmt.Sprint("pending=", pending), func(t *testing.T) {
			f := newDurableMediaHandlerFixture(t)
			if pending {
				f.repo.setApplyError(errors.New("synthetic ledger outage"))
			}
			store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
			tasks := service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute)
			owner := service.ImageTaskOwner{UserID: f.key.UserID, APIKeyID: f.key.ID}
			task, err := tasks.Create(context.Background(), owner)
			require.NoError(t, err)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			body := []byte(`{"model":"grok-imagine-image","prompt":"test"}`)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", strings.NewReader(string(body)))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(string(middleware.ContextKeyAPIKey), f.key)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: f.key.UserID})
			taskContext, recorder, cancel := newAsyncImageContext(c, body, time.Minute, task.ID)
			h := NewAsyncImageHandler(tasks, f.handlers[0])
			h.run(task.ID, service.PlatformGrok, taskContext, recorder, cancel)

			stored, err := store.Get(context.Background(), task.ID)
			require.NoError(t, err)
			public, err := tasks.Get(context.Background(), owner, task.ID)
			require.NoError(t, err)
			job, err := f.repo.GetMediaBillingJobByID(context.Background(), "image:"+task.ID)
			require.NoError(t, err)
			require.Equal(t, int64(17), job.GroupID)
			if pending {
				require.Equal(t, service.MediaBillingReady, job.Status)
				require.Equal(t, service.ImageTaskStatusBilling, public.Status)
				require.Empty(t, public.Result)
				require.Empty(t, public.ImageURL)
				require.Empty(t, stored.Result)
				require.Contains(t, string(stored.PendingResult), "private-generated-image.png")
			} else {
				require.Equal(t, service.MediaBillingDone, job.Status)
				require.Equal(t, service.ImageTaskStatusCompleted, public.Status)
				require.Contains(t, public.ImageURL, "private-generated-image.png")
			}
		})
	}
}
