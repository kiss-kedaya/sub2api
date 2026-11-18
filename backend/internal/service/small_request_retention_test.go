//go:build unit

package service

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/requestcapture"
	"github.com/stretchr/testify/require"
)

func TestSmallRetentionSettingsDefaultOffAndIndependent(t *testing.T) {
	repo := &gatewayTTLSettingRepo{data: map[string]string{}}
	svc := NewSettingService(repo, nil)
	config, err := svc.GetCaptureSettings(context.Background())
	require.NoError(t, err)
	require.False(t, config.SmallRequestEnabled)
	config.SmallRequestEnabled = true
	require.NoError(t, svc.UpdateCaptureSettings(context.Background(), config))
	loaded, err := svc.GetCaptureSettings(context.Background())
	require.NoError(t, err)
	require.True(t, loaded.SmallRequestEnabled)
	require.False(t, loaded.Enabled)
	require.False(t, loaded.ID1DiagnosticEnabled)
	var stored map[string]any
	require.NoError(t, json.Unmarshal([]byte(repo.data[SettingKeyRequestCapture]), &stored))
	require.Equal(t, true, stored["small_request_enabled"])
}
func TestSmallRetentionDisabledAccountingDoesNotAllocate(t *testing.T) {
	ctx := context.Background()
	row := &UsageLog{InputTokens: 999}
	require.Zero(t, testing.AllocsPerRun(1000, func() {
		observeSmallUsage(ctx, row, nil, nil, false)
		applied, err := applyUsageBilling(ctx, "synthetic-disabled", row, nil, nil, nil)
		require.NoError(t, err)
		require.False(t, applied)
	}))
}

func smallUsageCaptureFixture(t *testing.T, expected smallUsageExpectation) context.Context {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.ExpectQuery("SELECT to_regclass").WillReturnRows(sqlmock.NewRows([]string{"ready"}).AddRow(true))
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"size"}).AddRow(0))
	for i := 0; i < 4; i++ {
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT pg_try_advisory_xact_lock").WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
		mock.ExpectExec("DELETE FROM small_request_").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectCommit()
	}
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock_shared").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT \\$1::timestamptz").WillReturnRows(sqlmock.NewRows([]string{"live"}).AddRow(true))
	mock.ExpectQuery("INSERT INTO small_request_records").WithArgs(
		sqlmock.AnyArg(), sqlmock.AnyArg(), int64(42), int64(7), "text-test", 200, sqlmock.AnyArg(),
		expected.input, expected.output, expected.read, expected.creation, expected.cost, sqlmock.AnyArg(),
		`["synthetic-retention"]`, sqlmock.AnyArg(), sqlmock.AnyArg(), expected,
	).WillReturnRows(sqlmock.NewRows([]string{"id", "repeat_count"}).AddRow(1, 2))
	mock.ExpectExec("UPDATE small_request_responses SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	r := requestcapture.NewSmallRecorder(&requestcapture.SmallStore{DB: db}, nil)
	r.ApplyEnabled(true)
	require.Eventually(t, r.Enabled, time.Second, time.Millisecond)
	h := r.Begin("POST", "/v1/responses", 42, 7, 0)
	require.NotNil(t, h)
	h.ClientRequest([]byte(`{"model":"text-test","input":"hello"}`), http.Header{"Content-Type": {"application/json"}})
	h.Response([]byte(`{"output":"hello"}`), nil)
	h.Finish(200, nil, false)
	t.Cleanup(func() {
		require.Eventually(t, func() bool { return mock.ExpectationsWereMet() == nil }, 2*time.Second, time.Millisecond)
		r.Close()
		require.NoError(t, mock.ExpectationsWereMet())
		mock.ExpectClose()
		require.NoError(t, db.Close())
	})
	return requestcapture.WithSmallHandle(context.Background(), h)
}

type smallUsageExpectation struct {
	input, output, read, creation int
	cost, settlement, platform    string
}

func (e smallUsageExpectation) Match(value driver.Value) bool {
	data, ok := value.(string)
	if !ok {
		return false
	}
	var record requestcapture.SmallRecordData
	if json.Unmarshal([]byte(data), &record) != nil {
		return false
	}
	u := record.FirstUsage
	return u.InputTokens == e.input && u.OutputTokens == e.output && u.CacheReadTokens == e.read && u.CacheCreationTokens == e.creation && u.ActualCost == e.cost && u.Settlement == e.settlement && u.Platform == e.platform
}

func TestSmallRetentionApplyUsageBillingObservesFinalRowAndSettlement(t *testing.T) {
	for _, tc := range []struct {
		name             string
		applied          bool
		err              error
		settlement, cost string
	}{
		{"settled", true, nil, "settled", "0.02000000"},
		{"already_settled", false, nil, "already_settled", "0.00000000"},
		{"failed", false, errors.New("synthetic settlement failure"), "failed", "0.00000000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := smallUsageCaptureFixture(t, smallUsageExpectation{input: 99, output: 5, read: 800, creation: 100, cost: tc.cost, settlement: tc.settlement, platform: "synthetic"})
			row, p, deps := guardedUsageBillingParams(0.02)
			deps.billingCacheService = &BillingCacheService{}
			row.RequestID, row.Model = "synthetic-retention", "text-test"
			row.InputTokens, row.OutputTokens, row.CacheReadTokens, row.CacheCreationTokens = 99, 5, 800, 100
			p.Account.Platform = "synthetic"
			repo := &guardedUsageBillingApplyRepoStub{result: &UsageBillingApplyResult{Applied: tc.applied}, err: tc.err}
			applied, err := applyUsageBilling(ctx, row.RequestID, row, p, deps, repo)
			require.Equal(t, tc.applied, applied)
			require.ErrorIs(t, err, tc.err)
			require.Equal(t, 99, row.InputTokens)
			require.Equal(t, 800, row.CacheReadTokens)
			require.Equal(t, 100, row.CacheCreationTokens)
		})
	}
}

func TestSmallRetentionSimpleModeObservesRow(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			ctx := smallUsageCaptureFixture(t, smallUsageExpectation{input: 1, output: 5, read: 800, creation: 100, cost: "0.00000000", settlement: "not_billed", platform: provider})
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			userRepo := &openAIRecordUsageUserRepoStub{}
			subRepo := &openAIRecordUsageSubRepoStub{}
			account := &Account{ID: 11, Platform: provider}
			if provider == "openai" {
				svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
				svc.cfg.RunMode = config.RunModeSimple
				require.NoError(t, svc.RecordUsage(ctx, &OpenAIRecordUsageInput{
					Result: &OpenAIForwardResult{RequestID: "synthetic-retention", Model: "gpt-5.1", Usage: OpenAIUsage{InputTokens: 901, OutputTokens: 5, CacheReadInputTokens: 800, CacheCreationInputTokens: 100}},
					APIKey: &APIKey{ID: 7}, User: &User{ID: 42}, Account: account, ChannelUsageFields: ChannelUsageFields{OriginalModel: "text-test"},
				}))
			} else {
				svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, subRepo)
				svc.cfg.RunMode = config.RunModeSimple
				require.NoError(t, svc.RecordUsage(ctx, &RecordUsageInput{
					Result: &ForwardResult{RequestID: "synthetic-retention", Model: "claude-sonnet-4", Usage: ClaudeUsage{InputTokens: 1, OutputTokens: 5, CacheReadInputTokens: 800, CacheCreationInputTokens: 100}},
					APIKey: &APIKey{ID: 7}, User: &User{ID: 42}, Account: account, ChannelUsageFields: ChannelUsageFields{OriginalModel: "text-test"},
				}))
			}
			require.Equal(t, 1, usageRepo.calls)
			require.Zero(t, userRepo.deductCalls)
			require.Zero(t, subRepo.incrementCalls)
		})
	}
}
