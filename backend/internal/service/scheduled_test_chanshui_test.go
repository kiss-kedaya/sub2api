package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type chanshuiTestTransport func(*http.Request) (*http.Response, error)

func (f chanshuiTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func chanshuiTestResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

type chanshuiPlanRepoFake struct {
	scheduledTestPlanRepoStub
	state    *ChanshuiAuditState
	allow    bool
	reserves int
	delay    time.Duration
	deferred []time.Time
}

func (r *chanshuiPlanRepoFake) GetChanshuiAudit(context.Context, int64) (*ChanshuiAuditState, error) {
	if r.state == nil {
		return nil, nil
	}
	copy := *r.state
	return &copy, nil
}
func (r *chanshuiPlanRepoFake) SetChanshuiAudit(_ context.Context, _ int64, s *ChanshuiAuditState) error {
	r.state = s
	return nil
}
func (r *chanshuiPlanRepoFake) ReserveChanshuiCreate(context.Context) (bool, error) {
	r.reserves++
	ok := r.allow
	r.allow = false
	return ok, nil
}
func (r *chanshuiPlanRepoFake) DelayChanshuiCreate(_ context.Context, d time.Duration) error {
	r.delay = d
	return nil
}
func (r *chanshuiPlanRepoFake) DeferChanshuiPlan(_ context.Context, _ int64, next time.Time) error {
	r.deferred = append(r.deferred, next)
	return nil
}

func newChanshuiTestRunner(transport chanshuiTestTransport) (*ScheduledTestRunnerService, *chanshuiPlanRepoFake, *scheduledQualityResultRepo, *ScheduledTestPlan, *scheduledQualityAccountRepo) {
	account := &Account{ID: 2, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "fixture-upstream-key", "base_url": "https://upstream.example/v1"}}
	results := &scheduledQualityResultRepo{}
	runner, accounts, _ := scheduledQualityRunner(account, results)
	runner.accountTestSvc.accountRepo = &chanshuiStateAccountFake{scheduledQualityAccountRepo: accounts}
	repo := &chanshuiPlanRepoFake{allow: true}
	runner.planRepo = repo
	runner.scheduledSvc = NewScheduledTestService(repo, results)
	runner.chanshuiClient = newCustomUsageClient()
	runner.chanshuiClient.Transport = transport
	plan := &ScheduledTestPlan{ID: 1, AccountID: 2, ModelID: "gpt-test", Enabled: true, MaxResults: 20, CronExpression: "*/5 * * * *", QualityCheckEnabled: true, QualityProvider: QualityProviderChanshui, QualityConfig: ChanshuiConfig{BaseURL: "https://audit.example", Protocol: "auto", Timeout: 360}}
	return runner, repo, results, plan, accounts
}

func TestChanshuiCreateResumePollAndRedactedReport(t *testing.T) {
	posts, gets := 0, 0
	transport := chanshuiTestTransport(func(req *http.Request) (*http.Response, error) {
		require.Empty(t, req.Header.Get("Authorization"))
		require.Empty(t, req.Header.Get("Cookie"))
		require.Equal(t, "audit.example", req.URL.Host)
		if req.Method == http.MethodPost {
			posts++
			var body map[string]any
			require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
			require.Equal(t, "https://upstream.example/v1", body["base_url"])
			require.Equal(t, "fixture-upstream-key", body["api_key"])
			require.Equal(t, "auto", body["protocol"])
			require.Equal(t, []any{"fingerprint"}, body["sections"])
			require.EqualValues(t, 360, body["timeout"])
			return chanshuiTestResponse(202, fmt.Sprintf(`{"id":"audit-1","status":"queued","poll_url":"/api/v1/audits/audit-1","expires_at":%d,"skipped":[{"id":"thinking","reason":"non-Claude"}]}`, time.Now().Add(24*time.Hour).Unix())), nil
		}
		gets++
		require.Equal(t, "/api/v1/audits/audit-1", req.URL.Path)
		if gets == 1 {
			return chanshuiTestResponse(200, `{"id":"audit-1","status":"running"}`), nil
		}
		return chanshuiTestResponse(200, `{"id":"audit-1","status":"done","probes":[{"id":"Q1","status":"queued"}],"verdict":{"fingerprint":{"top_model":"gpt-test"},"total":{"score":56,"max":100},"integrity":{"score":78},"tools":{"reason":"echo fixture-upstream-key","api_key":"must-not-persist"},"iq":{"score":null,"complete":false}}}`), nil
	})
	runner, repo, results, plan, accounts := newChanshuiTestRunner(transport)
	runner.runOnePlan(context.Background(), plan)
	require.Equal(t, 1, posts)
	require.Empty(t, results.results)
	require.NotNil(t, repo.state)
	require.Len(t, repo.deferred, 1)
	raw, err := json.Marshal(repo.state)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "fixture-upstream-key")
	// A replacement worker resumes the persisted GET, even after credential rotation.
	accounts.account.Credentials["api_key"] = "fixture-rotated-key"
	replacement := &ScheduledTestRunnerService{planRepo: runner.planRepo, scheduledSvc: runner.scheduledSvc, accountTestSvc: runner.accountTestSvc, chanshuiClient: runner.chanshuiClient, rateLimitSvc: runner.rateLimitSvc, qualityCheck: runner.qualityCheck}
	replacement.runOnePlan(context.Background(), plan)
	require.Equal(t, "running", repo.state.Status)
	replacement.runOnePlan(context.Background(), plan)
	require.Equal(t, 1, posts)
	require.Equal(t, 2, gets)
	require.Equal(t, 1, repo.reserves)
	require.Nil(t, repo.state)
	require.Len(t, results.results, 1)
	require.Equal(t, "audit_pass", results.results[0].Status)
	require.Contains(t, results.results[0].ResponseText, `"score":56`)
	require.NotContains(t, results.results[0].ResponseText, "fixture-upstream-key")
	require.NotContains(t, results.results[0].ResponseText, "must-not-persist")
	require.Zero(t, accounts.pauses)
	require.Zero(t, accounts.recoveries)
	require.Zero(t, accounts.clears)
}

func TestChanshuiChangedConfigCannotApplyPendingDecision(t *testing.T) {
	runner, repo, results, plan, accounts := newChanshuiTestRunner(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, req.Method)
		return chanshuiTestResponse(200, `{"id":"pending-fixture","status":"done","verdict":{"fingerprint":{"top_model":"different-model"}}}`), nil
	})
	repo.state = &ChanshuiAuditState{ID: "pending-fixture", PollURL: "https://audit.example/api/v1/audits/pending-fixture", ServiceURL: "https://audit.example", StartedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour).Unix(), DecisionConfigHash: chanshuiDecisionConfigHash(plan)}
	plan.ModelID = "changed-model"
	runner.runOnePlan(context.Background(), plan)
	require.Len(t, results.results, 1)
	require.Equal(t, "unknown", results.results[0].Status)
	require.True(t, accounts.account.Schedulable)
	require.Nil(t, repo.state)
	require.Zero(t, repo.reserves)
}

func TestChanshui429PersistsRetryAfterWithoutImmediateRetry(t *testing.T) {
	require.Equal(t, 48*time.Hour, chanshuiRetryAfter("172800"))
	posts := 0
	runner, repo, results, plan, _ := newChanshuiTestRunner(func(req *http.Request) (*http.Response, error) {
		posts++
		r := chanshuiTestResponse(429, `{"error":"busy"}`)
		r.Header.Set("Retry-After", "137")
		return r, nil
	})
	runner.runOnePlan(context.Background(), plan)
	require.Equal(t, 137*time.Second, repo.delay)
	require.Len(t, repo.deferred, 1)
	require.True(t, repo.deferred[0].After(time.Now().Add(130*time.Second)))
	runner.runOnePlan(context.Background(), plan)
	require.Equal(t, 1, posts)
	require.Nil(t, repo.state)
	require.Empty(t, results.results)
}

func TestChanshuiSelectedSectionsAreSentAsAnArray(t *testing.T) {
	runner, repo, _, plan, _ := newChanshuiTestRunner(func(req *http.Request) (*http.Response, error) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
		require.Equal(t, []any{"tools", "cache"}, body["sections"])
		return chanshuiTestResponse(202, fmt.Sprintf(`{"id":"audit-selected","status":"queued","poll_url":"/api/v1/audits/audit-selected","expires_at":%d}`, time.Now().Add(time.Hour).Unix())), nil
	})
	plan.QualityConfig.Sections = []string{"tools", "cache"}
	plan.QualityConfig.StopCondition = &ChanshuiStopCondition{Match: "any", Rules: []ChanshuiStopRule{{Type: "section_status", Section: "tools", Status: "fail"}}}
	runner.runOnePlan(context.Background(), plan)
	require.NotNil(t, repo.state)
	require.Equal(t, "audit-selected", repo.state.ID)
}

func TestChanshuiInterruptedSubmissionIsNotReposted(t *testing.T) {
	runner, repo, results, plan, _ := newChanshuiTestRunner(func(*http.Request) (*http.Response, error) {
		t.Fatal("must not issue HTTP after an ambiguous POST")
		return nil, nil
	})
	repo.state = &ChanshuiAuditState{Status: "submitting", ServiceURL: "https://audit.example", StartedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour).Unix()}
	runner.runOnePlan(context.Background(), plan)
	require.Zero(t, repo.reserves)
	require.Nil(t, repo.state)
	require.Len(t, results.results, 1)
	require.Equal(t, "unknown", results.results[0].Status)
}

func TestChanshuiExpiredAndMissingJobsNeverRecreate(t *testing.T) {
	for _, code := range []int{404, 410} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			runner, repo, results, plan, _ := newChanshuiTestRunner(func(req *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, req.Method)
				return chanshuiTestResponse(code, `{"error":"gone"}`), nil
			})
			repo.state = &ChanshuiAuditState{ID: "audit-1", PollURL: "/api/v1/audits/audit-1", ServiceURL: "https://audit.example", StartedAt: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Hour).Unix()}
			runner.runOnePlan(context.Background(), plan)
			require.Zero(t, repo.reserves)
			require.Nil(t, repo.state)
			require.Len(t, results.results, 1)
			require.Equal(t, "unknown", results.results[0].Status)
		})
	}
}

func TestChanshuiConfigurationAndPollOriginValidation(t *testing.T) {
	for _, config := range []ChanshuiConfig{
		{BaseURL: "http://audit.example"}, {BaseURL: "https://127.0.0.1"}, {BaseURL: "https://user:secret@audit.example"}, {BaseURL: "https://audit.example?api_key=secret"}, {Timeout: 601}, {Timeout: 9}, {Protocol: "guess"}, {Sections: []string{"unknown"}}, {Sections: []string{"thinking"}},
	} {
		plan := &ScheduledTestPlan{QualityProvider: QualityProviderChanshui, ModelID: "gpt-test", QualityConfig: config}
		require.Error(t, normalizeScheduledQualityProvider(plan))
	}
	plan := &ScheduledTestPlan{QualityProvider: QualityProviderChanshui, ModelID: "claude-test", QualityConfig: ChanshuiConfig{Sections: []string{"thinking"}, StopCondition: &ChanshuiStopCondition{Match: "any", Rules: []ChanshuiStopRule{{Type: "total_score_below", Threshold: func() *float64 { v := 50.0; return &v }()}}}}}
	require.NoError(t, normalizeScheduledQualityProvider(plan))
	require.Equal(t, DefaultChanshuiBaseURL, plan.QualityConfig.BaseURL)
	for _, poll := range []string{"https://evil.example/api/v1/audits/audit-1", "//evil.example/api/v1/audits/audit-1", "/api/v1/audits/other", "/api/v1/audits/audit-1?token=secret", "http://audit.example/api/v1/audits/audit-1"} {
		_, err := chanshuiPollURL("https://audit.example", poll, "audit-1")
		require.Error(t, err)
	}
	_, err := chanshuiPollURL("https://audit.example", "/api/v1/audits/audit-1", "audit-1")
	require.NoError(t, err)
}

func TestChanshuiCreationFailuresNeverLeakPayloadOrFollowRedirects(t *testing.T) {
	for _, code := range []int{302, 400} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			calls := 0
			runner, _, results, plan, _ := newChanshuiTestRunner(func(req *http.Request) (*http.Response, error) {
				calls++
				r := chanshuiTestResponse(code, `{"error":"fixture-upstream-key"}`)
				r.Header.Set("Location", "https://evil.example")
				return r, nil
			})
			runner.runOnePlan(context.Background(), plan)
			require.Equal(t, 1, calls)
			require.Len(t, results.results, 1)
			require.NotContains(t, results.results[0].ResponseText, "fixture-upstream-key")
			require.NotContains(t, results.results[0].ErrorMessage, "fixture-upstream-key")
		})
	}
}
