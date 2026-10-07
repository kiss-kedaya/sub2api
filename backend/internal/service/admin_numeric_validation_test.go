package service

import (
	"context"
	"math"
	"net/http"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestAdminUserNumericLimitsRejectBeforeRepositoryAccess(t *testing.T) {
	svc := &adminServiceImpl{}
	ctx := context.Background()
	negative, zero := -1, 0
	for _, tc := range []struct{ concurrency, rpmLimit int }{{negative, zero}, {zero, negative}} {
		_, err := svc.CreateUser(ctx, &CreateUserInput{Concurrency: tc.concurrency, RPMLimit: tc.rpmLimit})
		require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
		_, err = svc.UpdateUser(ctx, 1, &UpdateUserInput{Concurrency: &tc.concurrency, RPMLimit: &tc.rpmLimit})
		require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
		_, err = svc.BatchUpdateLimits(ctx, []int64{1}, &tc.concurrency, &tc.rpmLimit)
		require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	}
	_, err := svc.BatchUpdateConcurrency(ctx, []int64{1}, negative, "set")
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	require.NoError(t, validateUserSchedulingLimits(nil, nil))
	require.NoError(t, validateUserSchedulingLimits(&zero, &zero))
	// add mode still accepts a negative delta; the repository clamps the result to zero.
	_, err = svc.BatchUpdateConcurrency(ctx, nil, negative, "add")
	require.NoError(t, err)
}

func TestAdminGroupNonFiniteLimitsRejectBeforeRepositoryAccess(t *testing.T) {
	svc := &adminServiceImpl{}
	ctx := context.Background()
	for _, invalid := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		for _, field := range []string{"daily", "weekly", "monthly"} {
			create, update := &CreateGroupInput{}, &UpdateGroupInput{}
			switch field {
			case "daily":
				create.DailyLimitUSD, update.DailyLimitUSD = &invalid, &invalid
			case "weekly":
				create.WeeklyLimitUSD, update.WeeklyLimitUSD = &invalid, &invalid
			case "monthly":
				create.MonthlyLimitUSD, update.MonthlyLimitUSD = &invalid, &invalid
			}
			_, err := svc.CreateGroup(ctx, create)
			require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
			_, err = svc.UpdateGroup(ctx, 1, update)
			require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
		}
	}
	zero, unlimited, positive := 0.0, -1.0, 42.5
	require.NoError(t, validateGroupLimitValues(nil, &zero, &unlimited, &positive))
	require.Nil(t, normalizeLimit(&unlimited))
	require.Zero(t, *normalizeLimit(&zero))
}
