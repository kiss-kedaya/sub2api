package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type peakRateGroupRepoStub struct {
	getByID *Group
	updated *Group
}

func (s *peakRateGroupRepoStub) Create(context.Context, *Group) error {
	panic("unexpected Create call")
}

func (s *peakRateGroupRepoStub) GetByID(context.Context, int64) (*Group, error) {
	return s.getByID, nil
}

func (s *peakRateGroupRepoStub) GetByIDLite(context.Context, int64) (*Group, error) {
	return s.getByID, nil
}

func (s *peakRateGroupRepoStub) Update(_ context.Context, group *Group) error {
	s.updated = group
	return nil
}

func (s *peakRateGroupRepoStub) Delete(context.Context, int64) error {
	panic("unexpected Delete call")
}

func (s *peakRateGroupRepoStub) DeleteCascade(context.Context, int64) ([]int64, error) {
	panic("unexpected DeleteCascade call")
}

func (s *peakRateGroupRepoStub) List(context.Context, pagination.PaginationParams) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *peakRateGroupRepoStub) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string, *bool) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *peakRateGroupRepoStub) ListActive(context.Context) ([]Group, error) {
	panic("unexpected ListActive call")
}

func (s *peakRateGroupRepoStub) ListActiveByPlatform(context.Context, string) ([]Group, error) {
	panic("unexpected ListActiveByPlatform call")
}

func (s *peakRateGroupRepoStub) ExistsByName(context.Context, string) (bool, error) {
	panic("unexpected ExistsByName call")
}

func (s *peakRateGroupRepoStub) GetAccountCount(context.Context, int64) (int64, int64, error) {
	panic("unexpected GetAccountCount call")
}

func (s *peakRateGroupRepoStub) DeleteAccountGroupsByGroupID(context.Context, int64) (int64, error) {
	panic("unexpected DeleteAccountGroupsByGroupID call")
}

func (s *peakRateGroupRepoStub) GetAccountIDsByGroupIDs(context.Context, []int64) ([]int64, error) {
	panic("unexpected GetAccountIDsByGroupIDs call")
}

func (s *peakRateGroupRepoStub) BindAccountsToGroup(context.Context, int64, []int64) error {
	panic("unexpected BindAccountsToGroup call")
}

func (s *peakRateGroupRepoStub) UpdateSortOrders(context.Context, []GroupSortOrderUpdate) error {
	panic("unexpected UpdateSortOrders call")
}

func TestAdminService_UpdateGroup_ClearsPeakRateWhenChangingToStandardDefault(t *testing.T) {
	repo := &peakRateGroupRepoStub{getByID: &Group{
		ID:                 1,
		Name:               "existing-group",
		Platform:           PlatformOpenAI,
		Status:             StatusActive,
		SubscriptionType:   SubscriptionTypeSubscription,
		PeakRateEnabled:    true,
		PeakStart:          "14:00",
		PeakEnd:            "18:00",
		PeakRateMultiplier: 3,
	}}
	svc := &adminServiceImpl{groupRepo: repo}

	group, err := svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{
		SubscriptionType: SubscriptionTypeStandard,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.updated)
	require.Equal(t, SubscriptionTypeStandard, repo.updated.SubscriptionType)
	require.False(t, repo.updated.PeakRateEnabled)
	require.Equal(t, "", repo.updated.PeakStart)
	require.Equal(t, "", repo.updated.PeakEnd)
	require.Equal(t, 1.0, repo.updated.PeakRateMultiplier)
}
