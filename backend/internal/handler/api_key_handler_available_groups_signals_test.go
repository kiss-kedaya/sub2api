package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type availableGroupsUserRepoStub struct{ service.UserRepository }

func (availableGroupsUserRepoStub) GetByID(_ context.Context, id int64) (*service.User, error) {
	return &service.User{ID: id}, nil
}

type availableGroupsSubRepoStub struct {
	service.UserSubscriptionRepository
}

func (availableGroupsSubRepoStub) ListActiveByUserID(context.Context, int64) ([]service.UserSubscription, error) {
	return nil, nil
}

type availableGroupsRateRepoStub struct {
	service.UserGroupRateRepository
	rates map[int64]float64
}

func (r availableGroupsRateRepoStub) GetByUserID(context.Context, int64) (map[int64]float64, error) {
	return r.rates, nil
}

type availableGroupsRepoStub struct {
	service.GroupRepository
	groups   []service.Group
	history  []service.GroupRateChange
	histErr  error
	gotSince time.Time
}

func (r *availableGroupsRepoStub) ListActive(context.Context) ([]service.Group, error) {
	return append([]service.Group(nil), r.groups...), nil
}

func (r *availableGroupsRepoStub) ListGroupRateChangesSince(_ context.Context, _ []int64, since time.Time) ([]service.GroupRateChange, error) {
	r.gotSince = since
	return r.history, r.histErr
}

// availableGroupsFixture: group 1 is old and was repriced 1h ago, group 2 is
// two hours old and never repriced, group 3 is old and untouched.
func availableGroupsFixture(now time.Time) (*availableGroupsRepoStub, time.Time) {
	newCreatedAt := now.Add(-2 * time.Hour)
	return &availableGroupsRepoStub{
		groups: []service.Group{
			{ID: 1, Name: "repriced", Platform: service.PlatformAnthropic, Status: service.StatusActive, RateMultiplier: 0.08, CreatedAt: now.Add(-30 * 24 * time.Hour)},
			{ID: 2, Name: "fresh", Platform: service.PlatformAnthropic, Status: service.StatusActive, RateMultiplier: 0.05, CreatedAt: newCreatedAt},
			{ID: 3, Name: "quiet", Platform: service.PlatformAnthropic, Status: service.StatusActive, RateMultiplier: 1, CreatedAt: now.Add(-30 * 24 * time.Hour)},
		},
		history: []service.GroupRateChange{
			{ID: 1, GroupID: 1, OldRate: 0.06, NewRate: 0.08, ChangedAt: now.Add(-time.Hour)},
		},
	}, newCreatedAt
}

func getAvailableGroupsPayload(t *testing.T, repo *availableGroupsRepoStub, rates map[int64]float64) []map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)

	var rateRepo service.UserGroupRateRepository
	if rates != nil {
		rateRepo = availableGroupsRateRepoStub{rates: rates}
	}
	svc := service.NewAPIKeyService(nil, availableGroupsUserRepoStub{}, repo, availableGroupsSubRepoStub{}, rateRepo, nil, nil)
	h := NewAPIKeyHandler(svc)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 9})
		c.Next()
	})
	router.GET("/groups/available", h.GetAvailableGroups)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/groups/available", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Code int              `json:"code"`
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Zero(t, body.Code)
	require.Len(t, body.Data, 3)
	return body.Data
}

func TestGetAvailableGroupsAddsRateChangeAndNewHints(t *testing.T) {
	repo, createdAt := availableGroupsFixture(time.Now())
	data := getAvailableGroupsPayload(t, repo, nil)

	require.WithinDuration(t, time.Now().Add(-service.GroupRateChangeWindow), repo.gotSince, 5*time.Second,
		"history is read for the 48h window")

	repriced := data[0]
	change, ok := repriced["recent_rate_change"].(map[string]any)
	require.True(t, ok, "repriced group carries recent_rate_change")
	require.Equal(t, "up", change["direction"])
	require.InDelta(t, 0.02, change["delta"], 1e-9)
	require.InDelta(t, 0.06, change["previous_rate"], 1e-9)
	require.Contains(t, change, "changed_at")
	require.NotContains(t, repriced, "is_new")

	fresh := data[1]
	require.Equal(t, true, fresh["is_new"])
	require.InDelta(t, 0.05, fresh["launch_rate"], 1e-9)
	launchedAtRaw, ok := fresh["launched_at"].(string)
	require.True(t, ok, "launched_at is a string timestamp")
	launchedAt, err := time.Parse(time.RFC3339Nano, launchedAtRaw)
	require.NoError(t, err)
	require.WithinDuration(t, createdAt, launchedAt, time.Second)
	require.NotContains(t, fresh, "recent_rate_change")

	quiet := data[2]
	for _, key := range []string{"recent_rate_change", "is_new", "launched_at", "launch_rate"} {
		require.NotContains(t, quiet, key)
	}
}

func TestGetAvailableGroupsHidesBaseRateHintsFromViewersWithAPersonalRate(t *testing.T) {
	repo, _ := availableGroupsFixture(time.Now())
	data := getAvailableGroupsPayload(t, repo, map[int64]float64{1: 0.05, 2: 0.02})

	require.NotContains(t, data[0], "recent_rate_change", "a fixed personal rate does not follow the base rate")
	require.Equal(t, true, data[1]["is_new"], "the group is still new for this viewer")
	require.NotContains(t, data[1], "launch_rate", "the base launch rate is not what this viewer pays")
	require.Contains(t, data[1], "launched_at")
}

func TestGetAvailableGroupsKeepsTheListWhenHistoryFails(t *testing.T) {
	repo, _ := availableGroupsFixture(time.Now())
	repo.histErr = errors.New("history unavailable")
	data := getAvailableGroupsPayload(t, repo, nil)

	require.NotContains(t, data[0], "recent_rate_change", "an unreadable history must not invent a change")
	require.Equal(t, true, data[1]["is_new"])
	require.NotContains(t, data[1], "launch_rate")
	require.Equal(t, "repriced", data[0]["name"], "the list itself is intact")
}
