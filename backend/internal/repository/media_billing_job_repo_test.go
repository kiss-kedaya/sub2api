//go:build unit

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var mediaBillingTestColumns = []string{
	"id", "kind", "user_id", "api_key_id", "account_id", "group_id", "subscription_id",
	"upstream_task_id", "status", "reserved_amount", "snapshot", "intent",
	"created_at", "updated_at", "available_at", "attempts",
}

func mediaBillingTestRow(job service.MediaBillingJob) *sqlmock.Rows {
	var subscriptionID any
	if job.SubscriptionID != nil {
		subscriptionID = *job.SubscriptionID
	}
	var snapshot any
	if len(job.Snapshot) > 0 {
		snapshot = []byte(job.Snapshot)
	}
	var intent any
	if job.Intent != nil {
		encoded, err := json.Marshal(job.Intent)
		if err != nil {
			panic(err)
		}
		intent = encoded
	}
	now := time.Now().UTC()
	if !job.CreatedAt.IsZero() {
		now = job.CreatedAt
	}
	updatedAt := now
	if !job.UpdatedAt.IsZero() {
		updatedAt = job.UpdatedAt
	}
	availableAt := now
	if !job.AvailableAt.IsZero() {
		availableAt = job.AvailableAt
	}
	return sqlmock.NewRows(mediaBillingTestColumns).AddRow(
		job.ID, job.Kind, job.UserID, job.APIKeyID, job.AccountID, job.GroupID, subscriptionID,
		job.UpstreamTaskID, job.Status, job.ReservedAmount, snapshot, intent,
		now, updatedAt, availableAt, job.Attempts,
	)
}

func newMediaBillingSQLMock(t *testing.T) (*usageBillingRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &usageBillingRepository{db: db}, mock
}

func testMediaIntent(balanceCost float64) *service.MediaBillingIntent {
	return &service.MediaBillingIntent{
		Command: service.UsageBillingCommand{
			RequestID:       "media:req-1",
			UserID:          42,
			APIKeyID:        7,
			AccountID:       9,
			BillingType:     service.BillingTypeBalance,
			BalanceCost:     balanceCost,
			APIKeyQuotaCost: balanceCost,
		},
		RawAmounts: [5]float64{balanceCost, balanceCost / 2},
	}
}

func TestCreateMediaBillingJob_DuplicateReturnsPersistedIntentWithoutRepricing(t *testing.T) {
	repo, mock := newMediaBillingSQLMock(t)
	existing := service.MediaBillingJob{
		ID: "img-duplicate", Kind: service.MediaBillingKindImage,
		UserID: 42, APIKeyID: 7, AccountID: 9, GroupID: 11,
		Status: service.MediaBillingReady, Intent: testMediaIntent(1.25),
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)INSERT INTO media_billing_jobs.*ON CONFLICT \(id\) DO NOTHING.*RETURNING`).
		WillReturnRows(sqlmock.NewRows(mediaBillingTestColumns))
	mock.ExpectQuery(`(?s)SELECT.*FROM media_billing_jobs WHERE id = \$1 FOR UPDATE`).
		WithArgs(existing.ID).
		WillReturnRows(mediaBillingTestRow(existing))
	mock.ExpectCommit()

	requested := existing
	requested.AccountID = 99
	requested.GroupID = 101
	requested.Intent = testMediaIntent(9.99)
	got, err := repo.CreateMediaBillingJob(context.Background(), &requested)
	require.NoError(t, err)
	require.NotNil(t, got.Intent)
	require.Equal(t, 1.25, got.Intent.Command.BalanceCost)
	require.Equal(t, [5]float64{1.25, 0.625}, got.Intent.RawAmounts)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateMediaBillingJob_DuplicateRejectsOwnerKindOrReserveConflict(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*service.MediaBillingJob)
	}{
		{name: "user", mutate: func(job *service.MediaBillingJob) { job.UserID++ }},
		{name: "api key", mutate: func(job *service.MediaBillingJob) { job.APIKeyID++ }},
		{name: "kind", mutate: func(job *service.MediaBillingJob) { job.Kind = service.MediaBillingKindGrokVideo }},
		{name: "reserve", mutate: func(job *service.MediaBillingJob) { job.ReservedAmount = 0.5 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			existing := &service.MediaBillingJob{
				ID: "duplicate-conflict", Kind: service.MediaBillingKindImage,
				UserID: 42, APIKeyID: 7, AccountID: 9, GroupID: 11,
				Status: service.MediaBillingReady, Intent: testMediaIntent(1.25),
			}
			requested := *existing
			test.mutate(&requested)
			require.False(t, sameMediaBillingCreate(existing, &requested))
		})
	}
}

func TestCreateMediaBillingJob_InsufficientBalanceRollsBackEntry(t *testing.T) {
	repo, mock := newMediaBillingSQLMock(t)
	job := service.MediaBillingJob{
		ID: "video-no-balance", Kind: service.MediaBillingKindGrokVideo,
		UserID: 42, APIKeyID: 7, AccountID: 9, GroupID: 11,
		Status: service.MediaBillingCreating, ReservedAmount: 5,
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)INSERT INTO media_billing_jobs.*RETURNING`).
		WillReturnRows(mediaBillingTestRow(job))
	mock.ExpectQuery(reserveBatchImageHoldSQL).
		WithArgs(5.0, int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(userExistsForBillingSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))
	mock.ExpectRollback()

	_, err := repo.CreateMediaBillingJob(context.Background(), &job)
	require.ErrorIs(t, err, service.ErrBalanceWithholdingFailed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateMediaBillingJob_SubscriptionIncludesOutstandingReservations(t *testing.T) {
	repo, mock := newMediaBillingSQLMock(t)
	subscriptionID := int64(13)
	job := service.MediaBillingJob{
		ID: "video-sub-limit", Kind: service.MediaBillingKindGrokVideo,
		UserID: 42, APIKeyID: 7, AccountID: 9, GroupID: 11, SubscriptionID: &subscriptionID,
		Status: service.MediaBillingCreating, ReservedAmount: 2,
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)INSERT INTO media_billing_jobs.*RETURNING`).
		WillReturnRows(mediaBillingTestRow(job))
	mock.ExpectQuery(`(?s)SELECT us.user_id.*FOR UPDATE OF us`).
		WithArgs(subscriptionID).
		WillReturnRows(sqlmock.NewRows([]string{
			"user_id", "group_id", "status", "starts_at", "expires_at",
			"daily_usage_usd", "weekly_usage_usd", "monthly_usage_usd",
			"daily_limit_usd", "weekly_limit_usd", "monthly_limit_usd",
		}).AddRow(
			int64(42), int64(11), service.SubscriptionStatusActive, time.Now().Add(-time.Hour), time.Now().Add(time.Hour),
			9.0, 0.0, 0.0, 10.0, nil, nil,
		))
	mock.ExpectQuery(`(?s)SELECT COALESCE\(SUM\(reserved_amount\), 0\).*subscription_id`).
		WithArgs(subscriptionID).
		WillReturnRows(sqlmock.NewRows([]string{"reserved"}).AddRow(2.0))
	mock.ExpectRollback()

	_, err := repo.CreateMediaBillingJob(context.Background(), &job)
	require.ErrorIs(t, err, service.ErrDailyLimitExceeded)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPrepareMediaBillingIntent_FirstWriterFreezesPrice(t *testing.T) {
	repo, mock := newMediaBillingSQLMock(t)
	existing := service.MediaBillingJob{
		ID: "video-frozen", Kind: service.MediaBillingKindGrokVideo,
		UserID: 42, APIKeyID: 7, AccountID: 9, GroupID: 11,
		Status: service.MediaBillingReady, ReservedAmount: 2, Intent: testMediaIntent(1.5),
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT.*FROM media_billing_jobs WHERE id = \$1 FOR UPDATE`).
		WithArgs(existing.ID).
		WillReturnRows(mediaBillingTestRow(existing))
	mock.ExpectCommit()

	got, err := repo.PrepareMediaBillingIntent(context.Background(), existing.ID, testMediaIntent(8.5))
	require.NoError(t, err)
	require.Equal(t, 1.5, got.Command.BalanceCost)
	require.Equal(t, [5]float64{1.5, 0.75}, got.RawAmounts)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyMediaBillingJob_ExistingClaimReleasesJobHoldWithoutCharging(t *testing.T) {
	repo, mock := newMediaBillingSQLMock(t)
	intent := testMediaIntent(0.5)
	intent.Command.Normalize()
	job := service.MediaBillingJob{
		ID: "video-legacy-claimed", Kind: service.MediaBillingKindGrokVideo,
		UserID: 42, APIKeyID: 7, AccountID: 9, GroupID: 11,
		Status: service.MediaBillingReady, ReservedAmount: 1, Intent: intent,
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT.*FROM media_billing_jobs WHERE id = \$1 FOR UPDATE`).
		WithArgs(job.ID).
		WillReturnRows(mediaBillingTestRow(job))
	mock.ExpectQuery(`(?s)INSERT INTO usage_billing_dedup.*ON CONFLICT.*RETURNING id`).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`(?s)SELECT request_fingerprint.*FROM usage_billing_dedup.*WHERE request_id`).
		WithArgs(intent.Command.RequestID, intent.Command.APIKeyID).
		WillReturnRows(sqlmock.NewRows([]string{"request_fingerprint"}).AddRow(intent.Command.RequestFingerprint))
	mock.ExpectQuery(captureBatchImageHoldSQL).
		WithArgs(1.0, 0.0, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(10.0, 0.0))
	mock.ExpectExec(`(?s)UPDATE media_billing_jobs.*SET status = \$2.*WHERE id = \$1`).
		WithArgs(job.ID, service.MediaBillingBilled, service.MediaBillingReady).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	result, err := repo.ApplyMediaBillingJob(context.Background(), job.ID)
	require.NoError(t, err)
	require.False(t, result.Applied)
	require.NotNil(t, result.NewBalance)
	require.Equal(t, 10.0, *result.NewBalance)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyMediaBillingJob_CaptureFailureRollsBackBeforeBilled(t *testing.T) {
	repo, mock := newMediaBillingSQLMock(t)
	intent := testMediaIntent(0.5)
	intent.Command.Normalize()
	job := service.MediaBillingJob{
		ID: "video-capture-fails", Kind: service.MediaBillingKindGrokVideo,
		UserID: 42, APIKeyID: 7, AccountID: 9, GroupID: 11,
		Status: service.MediaBillingReady, ReservedAmount: 1, Intent: intent,
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT.*FROM media_billing_jobs WHERE id = \$1 FOR UPDATE`).
		WithArgs(job.ID).
		WillReturnRows(mediaBillingTestRow(job))
	mock.ExpectQuery(`(?s)INSERT INTO usage_billing_dedup.*RETURNING id`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(101))
	mock.ExpectQuery(`(?s)SELECT request_fingerprint.*FROM usage_billing_dedup_archive`).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(captureBatchImageHoldSQL).
		WithArgs(1.0, 0.5, int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(userExistsForBillingSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))
	mock.ExpectRollback()

	result, err := repo.ApplyMediaBillingJob(context.Background(), job.ID)
	require.Nil(t, result)
	require.Error(t, err)
	require.False(t, errors.Is(err, service.ErrUsageBillingRequestConflict))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyMediaBillingJob_TopUpFailureRollsBackClaimAndKeepsHold(t *testing.T) {
	repo, mock := newMediaBillingSQLMock(t)
	intent := testMediaIntent(1.5)
	intent.Command.Normalize()
	job := service.MediaBillingJob{
		ID: "video-topup-fails", Kind: service.MediaBillingKindGrokVideo,
		UserID: 42, APIKeyID: 7, AccountID: 9, GroupID: 11,
		Status: service.MediaBillingReady, ReservedAmount: 1, Intent: intent,
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT.*FROM media_billing_jobs WHERE id = \$1 FOR UPDATE`).
		WithArgs(job.ID).
		WillReturnRows(mediaBillingTestRow(job))
	mock.ExpectQuery(`(?s)INSERT INTO usage_billing_dedup.*RETURNING id`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(102))
	mock.ExpectQuery(`(?s)SELECT request_fingerprint.*FROM usage_billing_dedup_archive`).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(reserveBatchImageHoldSQL).
		WithArgs(0.5, int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(userExistsForBillingSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))
	mock.ExpectRollback()

	result, err := repo.ApplyMediaBillingJob(context.Background(), job.ID)
	require.Nil(t, result)
	require.ErrorIs(t, err, service.ErrBalanceWithholdingFailed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListMediaBillingJobs_CapsBatchAndLeasesForSixtySeconds(t *testing.T) {
	repo, mock := newMediaBillingSQLMock(t)
	job := service.MediaBillingJob{
		ID: "leased-job", Kind: service.MediaBillingKindGrokVideo,
		UserID: 42, APIKeyID: 7, AccountID: 9, GroupID: 11,
		Status: service.MediaBillingSubmitted, ReservedAmount: 1,
	}
	mock.ExpectQuery(`(?s)WITH picked AS.*FOR UPDATE SKIP LOCKED.*LIMIT \$1.*UPDATE media_billing_jobs AS jobs.*available_at = NOW\(\) \+ \(\$2::bigint \* INTERVAL '1 millisecond'\).*RETURNING jobs.id`).
		WithArgs(32, int64(60000)).
		WillReturnRows(mediaBillingTestRow(job))

	jobs, err := repo.ListMediaBillingJobs(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, job.ID, jobs[0].ID)
	require.NoError(t, mock.ExpectationsWereMet())
}
