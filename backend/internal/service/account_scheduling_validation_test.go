package service

import (
	"context"
	"net/http"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestValidateAccountSchedulingFields(t *testing.T) {
	zero, positive, negative := 0, 10, -1
	for _, tc := range []struct {
		name        string
		concurrency *int
		priority    *int
		reason      string
	}{
		{name: "omitted"},
		{name: "zero", concurrency: &zero, priority: &zero},
		{name: "positive", concurrency: &positive, priority: &positive},
		{name: "negative concurrency", concurrency: &negative, reason: "INVALID_ACCOUNT_CONCURRENCY"},
		{name: "negative priority", priority: &negative, reason: "INVALID_ACCOUNT_PRIORITY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAccountSchedulingFields(tc.concurrency, tc.priority)
			if tc.reason == "" {
				require.NoError(t, err)
				return
			}
			require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
			require.Equal(t, tc.reason, infraerrors.Reason(err))
		})
	}
}

func TestAccountSchedulingInvalidFieldsRejectedBeforeRepositoryAccess(t *testing.T) {
	ctx := context.Background()
	adminSvc := &adminServiceImpl{}
	accountSvc := NewAccountService(nil, nil)
	for _, tc := range []struct {
		name        string
		concurrency int
		priority    int
	}{
		{name: "priority", concurrency: 1, priority: -1},
		{name: "concurrency", concurrency: -1, priority: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := adminSvc.CreateAccount(ctx, &CreateAccountInput{Concurrency: tc.concurrency, Priority: tc.priority})
			require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
			_, err = buildAccountForCreate(&CreateAccountInput{Concurrency: tc.concurrency, Priority: tc.priority}, nil)
			require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
			_, err = adminSvc.UpdateAccount(ctx, 1, &UpdateAccountInput{Concurrency: &tc.concurrency, Priority: &tc.priority})
			require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
			_, err = adminSvc.BulkUpdateAccounts(ctx, &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Concurrency: &tc.concurrency, Priority: &tc.priority})
			require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
			_, err = accountSvc.Create(ctx, CreateAccountRequest{Concurrency: tc.concurrency, Priority: tc.priority})
			require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
			_, err = accountSvc.Update(ctx, 1, UpdateAccountRequest{Concurrency: &tc.concurrency, Priority: &tc.priority})
			require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
		})
	}
}

func TestAccountSchedulingZeroAndOmittedValues(t *testing.T) {
	ctx := context.Background()
	repo := &upstreamBillingProbeAccountRepo{}
	svc := &adminServiceImpl{accountRepo: repo}
	created, err := svc.CreateAccount(ctx, &CreateAccountInput{
		Name: "zero-priority", Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
		Concurrency: 1, Priority: 0, SkipDefaultGroupBind: true,
	})
	require.NoError(t, err)
	require.Zero(t, created.Priority)
	require.Zero(t, repo.accounts[created.ID].Priority)

	priority := 7
	updated, err := svc.UpdateAccount(ctx, created.ID, &UpdateAccountInput{Priority: &priority})
	require.NoError(t, err)
	require.Equal(t, priority, updated.Priority)
	updated, err = svc.UpdateAccount(ctx, created.ID, &UpdateAccountInput{Name: "keep-priority"})
	require.NoError(t, err)
	require.Equal(t, priority, updated.Priority)

	zero := 0
	updated, err = svc.UpdateAccount(ctx, created.ID, &UpdateAccountInput{Priority: &zero, Concurrency: &zero})
	require.NoError(t, err)
	require.Zero(t, updated.Priority)
	require.Zero(t, updated.Concurrency)
	_, err = svc.BulkUpdateAccounts(ctx, &BulkUpdateAccountsInput{AccountIDs: []int64{created.ID}, Priority: &zero, Concurrency: &zero})
	require.NoError(t, err)
	require.Len(t, repo.bulkUpdates, 1)
	require.NotNil(t, repo.bulkUpdates[0].Priority)
	require.Zero(t, *repo.bulkUpdates[0].Priority)
	require.NotNil(t, repo.bulkUpdates[0].Concurrency)
	require.Zero(t, *repo.bulkUpdates[0].Concurrency)
}
