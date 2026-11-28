package dto

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUserSubscriptionDTO_DailyResetModeAndNextDailyResetAt(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("Asia/Shanghai", 8*3600)
	day1Noon := time.Date(2026, 8, 6, 12, 0, 0, 0, loc)
	day2Noon := day1Noon.Add(24 * time.Hour)
	day3Noon := day1Noon.Add(48 * time.Hour)

	group := &service.Group{
		ID:               10,
		Name:             "Anchored 24h",
		Platform:         service.PlatformAnthropic,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeSubscription,
		DailyResetMode:   service.DailyResetModeAnchored24h,
	}

	// Window 1: next reset is Day 2 12:00 (< ExpiresAt).
	subWin1 := &service.UserSubscription{
		ID:               1,
		UserID:           2,
		GroupID:          10,
		StartsAt:         day1Noon,
		ExpiresAt:        day3Noon,
		Status:           service.SubscriptionStatusActive,
		DailyWindowStart: &day1Noon,
		Group:            group,
	}
	dto1 := UserSubscriptionFromService(subWin1)
	require.NotNil(t, dto1.Group)
	require.Equal(t, service.DailyResetModeAnchored24h, dto1.Group.DailyResetMode)
	require.NotNil(t, dto1.NextDailyResetAt)
	require.True(t, day2Noon.Equal(*dto1.NextDailyResetAt))

	// Window 2: next boundary == ExpiresAt -> NextDailyResetAt must be omitted (nil).
	subWin2 := &service.UserSubscription{
		ID:               2,
		UserID:           2,
		GroupID:          10,
		StartsAt:         day1Noon,
		ExpiresAt:        day3Noon,
		Status:           service.SubscriptionStatusActive,
		DailyWindowStart: &day2Noon,
		Group:            group,
	}
	dto2 := UserSubscriptionFromService(subWin2)
	require.Nil(t, dto2.NextDailyResetAt)

	raw, err := json.Marshal(dto2)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "next_daily_reset_at")

	// Admin DTO preserves the same fields.
	adminDTO1 := UserSubscriptionFromServiceAdmin(subWin1)
	require.NotNil(t, adminDTO1.NextDailyResetAt)
	require.True(t, day2Noon.Equal(*adminDTO1.NextDailyResetAt))
	adminDTO2 := UserSubscriptionFromServiceAdmin(subWin2)
	require.Nil(t, adminDTO2.NextDailyResetAt)
}
