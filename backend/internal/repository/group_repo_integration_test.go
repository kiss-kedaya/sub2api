//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/suite"
)

type GroupRepoSuite struct {
	suite.Suite
	ctx  context.Context
	tx   *sql.Tx
	repo *groupRepository
}

func (s *GroupRepoSuite) SetupTest() {
	s.ctx = context.Background()
	entClient, tx := testEntSQLTx(s.T())
	s.tx = tx
	s.repo = newGroupRepositoryWithSQL(entClient, tx)
}

func TestGroupRepoSuite(t *testing.T) {
	suite.Run(t, new(GroupRepoSuite))
}

// --- Create / GetByID / Update / Delete ---

func (s *GroupRepoSuite) TestCreate() {
	group := &service.Group{
		Name:             "test-create",
		Platform:         service.PlatformAnthropic,
		RateMultiplier:   1.0,
		IsExclusive:      false,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	}

	err := s.repo.Create(s.ctx, group)
	s.Require().NoError(err, "Create")
	s.Require().NotZero(group.ID, "expected ID to be set")

	got, err := s.repo.GetByID(s.ctx, group.ID)
	s.Require().NoError(err, "GetByID")
	s.Require().Equal("test-create", got.Name)
}

func (s *GroupRepoSuite) TestGetByID_NotFound() {
	_, err := s.repo.GetByID(s.ctx, 999999)
	s.Require().Error(err, "expected error for non-existent ID")
	s.Require().ErrorIs(err, service.ErrGroupNotFound)
}

func (s *GroupRepoSuite) TestUpdate() {
	group := &service.Group{
		Name:             "original",
		Platform:         service.PlatformAnthropic,
		RateMultiplier:   1.0,
		IsExclusive:      false,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	}
	s.Require().NoError(s.repo.Create(s.ctx, group))

	group.Name = "updated"
	err := s.repo.Update(s.ctx, group)
	s.Require().NoError(err, "Update")

	got, err := s.repo.GetByID(s.ctx, group.ID)
	s.Require().NoError(err, "GetByID after update")
	s.Require().Equal("updated", got.Name)
}

func (s *GroupRepoSuite) TestDelete() {
	group := &service.Group{
		Name:             "to-delete",
		Platform:         service.PlatformAnthropic,
		RateMultiplier:   1.0,
		IsExclusive:      false,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	}
	s.Require().NoError(s.repo.Create(s.ctx, group))

	err := s.repo.Delete(s.ctx, group.ID)
	s.Require().NoError(err, "Delete")

	_, err = s.repo.GetByID(s.ctx, group.ID)
	s.Require().Error(err, "expected error after delete")
	s.Require().ErrorIs(err, service.ErrGroupNotFound)
}

// --- List / ListWithFilters ---

func (s *GroupRepoSuite) TestList() {
	s.Require().NoError(s.repo.Create(s.ctx, &service.Group{
		Name:             "g1",
		Platform:         service.PlatformAnthropic,
		RateMultiplier:   1.0,
		IsExclusive:      false,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	}))
	s.Require().NoError(s.repo.Create(s.ctx, &service.Group{
		Name:             "g2",
		Platform:         service.PlatformAnthropic,
		RateMultiplier:   1.0,
		IsExclusive:      false,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	}))

	groups, page, err := s.repo.List(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10})
	s.Require().NoError(err, "List")
	// 3 default groups + 2 test groups = 5 total
	s.Require().Len(groups, 5)
	s.Require().Equal(int64(5), page.Total)
}

func (s *GroupRepoSuite) TestListWithFilters_Platform() {
	s.Require().NoError(s.repo.Create(s.ctx, &service.Group{
		Name:             "g1",
		Platform:         service.PlatformAnthropic,
		RateMultiplier:   1.0,
		IsExclusive:      false,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	}))
	s.Require().NoError(s.repo.Create(s.ctx, &service.Group{
		Name:             "g2",
		Platform:         service.PlatformOpenAI,
		RateMultiplier:   1.0,
		IsExclusive:      false,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	}))

	groups, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, service.PlatformOpenAI, "", nil)
	s.Require().NoError(err)
	// 1 default openai group + 1 test openai group = 2 total
	s.Require().Len(groups, 2)
	// Verify all groups are OpenAI platform
	for _, g := range groups {
		s.Require().Equal(service.PlatformOpenAI, g.Platform)
	}
}

func (s *GroupRepoSuite) TestListWithFilters_Status() {
	s.Require().NoError(s.repo.Create(s.ctx, &service.Group{
		Name:             "g1",
		Platform:         service.PlatformAnthropic,
		RateMultiplier:   1.0,
		IsExclusive:      false,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	}))
	s.Require().NoError(s.repo.Create(s.ctx, &service.Group{
		Name:             "g2",
		Platform:         service.PlatformAnthropic,
		RateMultiplier:   1.0,
		IsExclusive:      false,
		Status:           service.StatusDisabled,
		SubscriptionType: service.SubscriptionTypeStandard,
	}))

	groups, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, "", service.StatusDisabled, nil)
	s.Require().NoError(err)
	s.Require().Len(groups, 1)
	s.Require().Equal(service.StatusDisabled, groups[0].Status)
}

func (s *GroupRepoSuite) TestListWithFilters_IsExclusive() {
	s.Require().NoError(s.repo.Create(s.ctx, &service.Group{
		Name:             "g1",
		Platform:         service.PlatformAnthropic,
		RateMultiplier:   1.0,
		IsExclusive:      false,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	}))
	s.Require().NoError(s.repo.Create(s.ctx, &service.Group{
		Name:             "g2",
		Platform:         service.PlatformAnthropic,
		RateMultiplier:   1.0,
		IsExclusive:      true,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	}))

	isExclusive := true
	groups, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, "", "", &isExclusive)
	s.Require().NoError(err)
	s.Require().Len(groups, 1)
	s.Require().True(groups[0].IsExclusive)
}

func (s *GroupRepoSuite) TestListWithFilters_AccountCount() {
	g1 := &service.Group{
		Name:             "g1",
		Platform:         service.PlatformAnthropic,
		RateMultiplier:   1.0,
		IsExclusive:      false,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	}
	g2 := &service.Group{
		Name:             "g2",
		Platform:         service.PlatformAnthropic,
		RateMultiplier:   1.0,
		IsExclusive:      true,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	}
	s.Require().NoError(s.repo.Create(s.ctx, g1))
	s.Require().NoError(s.repo.Create(s.ctx, g2))

	var accountID int64
	s.Require().NoError(s.tx.QueryRowContext(
		s.ctx,
		"INSERT INTO accounts (name, platform, type) VALUES ($1, $2, $3) RETURNING id",
		"acc1", service.PlatformAnthropic, service.AccountTypeOAuth,
	).Scan(&accountID))
	_, err := s.tx.ExecContext(s.ctx, "INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, $3, NOW())", accountID, g1.ID, 1)
	s.Require().NoError(err)
	_, err = s.tx.ExecContext(s.ctx, "INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, $3, NOW())", accountID, g2.ID, 1)
	s.Require().NoError(err)

	isExclusive := true
	groups, page, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, service.PlatformAnthropic, service.StatusActive, &isExclusive)
	s.Require().NoError(err, "ListWithFilters")
	s.Require().Equal(int64(1), page.Total)
	s.Require().Len(groups, 1)
	s.Require().Equal(g2.ID, groups[0].ID, "ListWithFilters returned wrong group")
	s.Require().Equal(int64(1), groups[0].AccountCount, "AccountCount mismatch")
}

// --- ListActive / ListActiveByPlatform ---

func (s *GroupRepoSuite) TestListActive() {
	s.Require().NoError(s.repo.Create(s.ctx, &service.Group{
		Name:             "active1",
		Platform:         service.PlatformAnthropic,
		RateMultiplier:   1.0,
		IsExclusive:      false,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	}))
	s.Require().NoError(s.repo.Create(s.ctx, &service.Group{
		Name:             "inactive1",
		Platform:         service.PlatformAnthropic,
		RateMultiplier:   1.0,
		IsExclusive:      false,
		Status:           service.StatusDisabled,
		SubscriptionType: service.SubscriptionTypeStandard,
	}))

	groups, err := s.repo.ListActive(s.ctx)
	s.Require().NoError(err, "ListActive")
	// 3 default groups (all active) + 1 test active group = 4 total
	s.Require().Len(groups, 4)
	// Verify our test group is in the results
	var found bool
	for _, g := range groups {
		if g.Name == "active1" {
			found = true
			break
		}
	}
	s.Require().True(found, "active1 group should be in results")
}

func (s *GroupRepoSuite) TestListActiveByPlatform() {
	s.Require().NoError(s.repo.Create(s.ctx, &service.Group{
		Name:             "g1",
		Platform:         service.PlatformAnthropic,
		RateMultiplier:   1.0,
		IsExclusive:      false,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	}))
	s.Require().NoError(s.repo.Create(s.ctx, &service.Group{
		Name:             "g2",
		Platform:         service.PlatformOpenAI,
		RateMultiplier:   1.0,
		IsExclusive:      false,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	}))
	s.Require().NoError(s.repo.Create(s.ctx, &service.Group{
		Name:             "g3",
		Platform:         service.PlatformAnthropic,
		RateMultiplier:   1.0,
		IsExclusive:      false,
		Status:           service.StatusDisabled,
		SubscriptionType: service.SubscriptionTypeStandard,
	}))

	groups, err := s.repo.ListActiveByPlatform(s.ctx, service.PlatformAnthropic)
	s.Require().NoError(err, "ListActiveByPlatform")
	// 1 default anthropic group + 1 test active anthropic group = 2 total
	s.Require().Len(groups, 2)
	// Verify our test group is in the results
	var found bool
	for _, g := range groups {
		if g.Name == "g1" {
			found = true
			break
		}
	}
	s.Require().True(found, "g1 group should be in results")
}

// --- ExistsByName ---

func (s *GroupRepoSuite) TestExistsByName() {
	s.Require().NoError(s.repo.Create(s.ctx, &service.Group{
		Name:             "existing-group",
		Platform:         service.PlatformAnthropic,
		RateMultiplier:   1.0,
		IsExclusive:      false,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	}))

	exists, err := s.repo.ExistsByName(s.ctx, "existing-group")
	s.Require().NoError(err, "ExistsByName")
	s.Require().True(exists)

	notExists, err := s.repo.ExistsByName(s.ctx, "non-existing")
	s.Require().NoError(err)
	s.Require().False(notExists)
}

// --- GetAccountCount ---

func (s *GroupRepoSuite) TestGetAccountCount() {
	group := &service.Group{
		Name:             "g-count",
		Platform:         service.PlatformAnthropic,
		RateMultiplier:   1.0,
		IsExclusive:      false,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	}
	s.Require().NoError(s.repo.Create(s.ctx, group))

	var a1 int64
	s.Require().NoError(s.tx.QueryRowContext(
		s.ctx,
		"INSERT INTO accounts (name, platform, type) VALUES ($1, $2, $3) RETURNING id",
		"a1", service.PlatformAnthropic, service.AccountTypeOAuth,
	).Scan(&a1))
	var a2 int64
	s.Require().NoError(s.tx.QueryRowContext(
		s.ctx,
		"INSERT INTO accounts (name, platform, type) VALUES ($1, $2, $3) RETURNING id",
		"a2", service.PlatformAnthropic, service.AccountTypeOAuth,
	).Scan(&a2))

	_, err := s.tx.ExecContext(s.ctx, "INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, $3, NOW())", a1, group.ID, 1)
	s.Require().NoError(err)
	_, err = s.tx.ExecContext(s.ctx, "INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, $3, NOW())", a2, group.ID, 2)
	s.Require().NoError(err)

	count, err := s.repo.GetAccountCount(s.ctx, group.ID)
	s.Require().NoError(err, "GetAccountCount")
	s.Require().Equal(int64(2), count)
}

func (s *GroupRepoSuite) TestGetAccountCount_Empty() {
	group := &service.Group{
		Name:             "g-empty",
		Platform:         service.PlatformAnthropic,
		RateMultiplier:   1.0,
		IsExclusive:      false,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	}
	s.Require().NoError(s.repo.Create(s.ctx, group))

	count, err := s.repo.GetAccountCount(s.ctx, group.ID)
	s.Require().NoError(err)
	s.Require().Zero(count)
}

// --- DeleteAccountGroupsByGroupID ---

func (s *GroupRepoSuite) TestDeleteAccountGroupsByGroupID() {
	g := &service.Group{
		Name:             "g-del",
		Platform:         service.PlatformAnthropic,
		RateMultiplier:   1.0,
		IsExclusive:      false,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	}
	s.Require().NoError(s.repo.Create(s.ctx, g))
	var accountID int64
	s.Require().NoError(s.tx.QueryRowContext(
		s.ctx,
		"INSERT INTO accounts (name, platform, type) VALUES ($1, $2, $3) RETURNING id",
		"acc-del", service.PlatformAnthropic, service.AccountTypeOAuth,
	).Scan(&accountID))
	_, err := s.tx.ExecContext(s.ctx, "INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, $3, NOW())", accountID, g.ID, 1)
	s.Require().NoError(err)

	affected, err := s.repo.DeleteAccountGroupsByGroupID(s.ctx, g.ID)
	s.Require().NoError(err, "DeleteAccountGroupsByGroupID")
	s.Require().Equal(int64(1), affected, "expected 1 affected row")

	count, err := s.repo.GetAccountCount(s.ctx, g.ID)
	s.Require().NoError(err, "GetAccountCount")
	s.Require().Equal(int64(0), count, "expected 0 account groups")
}

func (s *GroupRepoSuite) TestDeleteAccountGroupsByGroupID_MultipleAccounts() {
	g := &service.Group{
		Name:             "g-multi",
		Platform:         service.PlatformAnthropic,
		RateMultiplier:   1.0,
		IsExclusive:      false,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	}
	s.Require().NoError(s.repo.Create(s.ctx, g))

	insertAccount := func(name string) int64 {
		var id int64
		s.Require().NoError(s.tx.QueryRowContext(
			s.ctx,
			"INSERT INTO accounts (name, platform, type) VALUES ($1, $2, $3) RETURNING id",
			name, service.PlatformAnthropic, service.AccountTypeOAuth,
		).Scan(&id))
		return id
	}
	a1 := insertAccount("a1")
	a2 := insertAccount("a2")
	a3 := insertAccount("a3")
	_, err := s.tx.ExecContext(s.ctx, "INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, $3, NOW())", a1, g.ID, 1)
	s.Require().NoError(err)
	_, err = s.tx.ExecContext(s.ctx, "INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, $3, NOW())", a2, g.ID, 2)
	s.Require().NoError(err)
	_, err = s.tx.ExecContext(s.ctx, "INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, $3, NOW())", a3, g.ID, 3)
	s.Require().NoError(err)

	affected, err := s.repo.DeleteAccountGroupsByGroupID(s.ctx, g.ID)
	s.Require().NoError(err)
	s.Require().Equal(int64(3), affected)

	count, _ := s.repo.GetAccountCount(s.ctx, g.ID)
	s.Require().Zero(count)
}
