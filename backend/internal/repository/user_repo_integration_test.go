//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/suite"
	"gorm.io/gorm"
)

type UserRepoSuite struct {
	suite.Suite
	ctx  context.Context
	db   *gorm.DB
	repo *userRepository
}

func (s *UserRepoSuite) SetupTest() {
	s.ctx = context.Background()
	s.db = testTx(s.T())
	s.repo = NewUserRepository(s.db).(*userRepository)
}

func TestUserRepoSuite(t *testing.T) {
	suite.Run(t, new(UserRepoSuite))
}

// --- Create / GetByID / GetByEmail / Update / Delete ---

func (s *UserRepoSuite) TestCreate() {
	user := &service.User{
		Email:        "create@test.com",
		Username:     "testuser",
		PasswordHash: "test-password-hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
	}

	err := s.repo.Create(s.ctx, user)
	s.Require().NoError(err, "Create")
	s.Require().NotZero(user.ID, "expected ID to be set")

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err, "GetByID")
	s.Require().Equal("create@test.com", got.Email)
}

func (s *UserRepoSuite) TestGetByID_NotFound() {
	_, err := s.repo.GetByID(s.ctx, 999999)
	s.Require().Error(err, "expected error for non-existent ID")
}

func (s *UserRepoSuite) TestGetByEmail() {
	user := mustCreateUser(s.T(), s.db, &userModel{Email: "byemail@test.com"})

	got, err := s.repo.GetByEmail(s.ctx, user.Email)
	s.Require().NoError(err, "GetByEmail")
	s.Require().Equal(user.ID, got.ID)
}

func (s *UserRepoSuite) TestGetByEmail_NotFound() {
	_, err := s.repo.GetByEmail(s.ctx, "nonexistent@test.com")
	s.Require().Error(err, "expected error for non-existent email")
}

func (s *UserRepoSuite) TestUpdate() {
	user := userModelToService(mustCreateUser(s.T(), s.db, &userModel{Email: "update@test.com", Username: "original"}))

	user.Username = "updated"
	err := s.repo.Update(s.ctx, user)
	s.Require().NoError(err, "Update")

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err, "GetByID after update")
	s.Require().Equal("updated", got.Username)
}

func (s *UserRepoSuite) TestDelete() {
	user := mustCreateUser(s.T(), s.db, &userModel{Email: "delete@test.com"})

	err := s.repo.Delete(s.ctx, user.ID)
	s.Require().NoError(err, "Delete")

	_, err = s.repo.GetByID(s.ctx, user.ID)
	s.Require().Error(err, "expected error after delete")
}

// --- List / ListWithFilters ---

func (s *UserRepoSuite) TestList() {
	mustCreateUser(s.T(), s.db, &userModel{Email: "list1@test.com"})
	mustCreateUser(s.T(), s.db, &userModel{Email: "list2@test.com"})

	users, page, err := s.repo.List(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10})
	s.Require().NoError(err, "List")
	s.Require().Len(users, 2)
	s.Require().Equal(int64(2), page.Total)
}

func (s *UserRepoSuite) TestListWithFilters_Status() {
	mustCreateUser(s.T(), s.db, &userModel{Email: "active@test.com", Status: service.StatusActive})
	mustCreateUser(s.T(), s.db, &userModel{Email: "disabled@test.com", Status: service.StatusDisabled})

	users, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, service.StatusActive, "", "")
	s.Require().NoError(err)
	s.Require().Len(users, 1)
	s.Require().Equal(service.StatusActive, users[0].Status)
}

func (s *UserRepoSuite) TestListWithFilters_Role() {
	mustCreateUser(s.T(), s.db, &userModel{Email: "user@test.com", Role: service.RoleUser})
	mustCreateUser(s.T(), s.db, &userModel{Email: "admin@test.com", Role: service.RoleAdmin})

	users, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, "", service.RoleAdmin, "")
	s.Require().NoError(err)
	s.Require().Len(users, 1)
	s.Require().Equal(service.RoleAdmin, users[0].Role)
}

func (s *UserRepoSuite) TestListWithFilters_Search() {
	mustCreateUser(s.T(), s.db, &userModel{Email: "alice@test.com", Username: "Alice"})
	mustCreateUser(s.T(), s.db, &userModel{Email: "bob@test.com", Username: "Bob"})

	users, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, "", "", "alice")
	s.Require().NoError(err)
	s.Require().Len(users, 1)
	s.Require().Contains(users[0].Email, "alice")
}

func (s *UserRepoSuite) TestListWithFilters_SearchByUsername() {
	mustCreateUser(s.T(), s.db, &userModel{Email: "u1@test.com", Username: "JohnDoe"})
	mustCreateUser(s.T(), s.db, &userModel{Email: "u2@test.com", Username: "JaneSmith"})

	users, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, "", "", "john")
	s.Require().NoError(err)
	s.Require().Len(users, 1)
	s.Require().Equal("JohnDoe", users[0].Username)
}

func (s *UserRepoSuite) TestListWithFilters_SearchByWechat() {
	mustCreateUser(s.T(), s.db, &userModel{Email: "w1@test.com", Wechat: "wx_hello"})
	mustCreateUser(s.T(), s.db, &userModel{Email: "w2@test.com", Wechat: "wx_world"})

	users, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, "", "", "wx_hello")
	s.Require().NoError(err)
	s.Require().Len(users, 1)
	s.Require().Equal("wx_hello", users[0].Wechat)
}

func (s *UserRepoSuite) TestListWithFilters_LoadsActiveSubscriptions() {
	user := mustCreateUser(s.T(), s.db, &userModel{Email: "sub@test.com", Status: service.StatusActive})
	group := mustCreateGroup(s.T(), s.db, &groupModel{Name: "g-sub"})

	_ = mustCreateSubscription(s.T(), s.db, &userSubscriptionModel{
		UserID:    user.ID,
		GroupID:   group.ID,
		Status:    service.SubscriptionStatusActive,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})
	_ = mustCreateSubscription(s.T(), s.db, &userSubscriptionModel{
		UserID:    user.ID,
		GroupID:   group.ID,
		Status:    service.SubscriptionStatusExpired,
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	})

	users, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, "", "", "sub@")
	s.Require().NoError(err, "ListWithFilters")
	s.Require().Len(users, 1, "expected 1 user")
	s.Require().Len(users[0].Subscriptions, 1, "expected 1 active subscription")
	s.Require().NotNil(users[0].Subscriptions[0].Group, "expected subscription group preload")
	s.Require().Equal(group.ID, users[0].Subscriptions[0].Group.ID, "group ID mismatch")
}

func (s *UserRepoSuite) TestListWithFilters_CombinedFilters() {
	mustCreateUser(s.T(), s.db, &userModel{
		Email:    "a@example.com",
		Username: "Alice",
		Wechat:   "wx_a",
		Role:     service.RoleUser,
		Status:   service.StatusActive,
		Balance:  10,
	})
	target := mustCreateUser(s.T(), s.db, &userModel{
		Email:    "b@example.com",
		Username: "Bob",
		Wechat:   "wx_b",
		Role:     service.RoleAdmin,
		Status:   service.StatusActive,
		Balance:  1,
	})
	mustCreateUser(s.T(), s.db, &userModel{
		Email:  "c@example.com",
		Role:   service.RoleAdmin,
		Status: service.StatusDisabled,
	})

	users, page, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, service.StatusActive, service.RoleAdmin, "b@")
	s.Require().NoError(err, "ListWithFilters")
	s.Require().Equal(int64(1), page.Total, "ListWithFilters total mismatch")
	s.Require().Len(users, 1, "ListWithFilters len mismatch")
	s.Require().Equal(target.ID, users[0].ID, "ListWithFilters result mismatch")
}

// --- Balance operations ---

func (s *UserRepoSuite) TestUpdateBalance() {
	user := mustCreateUser(s.T(), s.db, &userModel{Email: "bal@test.com", Balance: 10})

	err := s.repo.UpdateBalance(s.ctx, user.ID, 2.5)
	s.Require().NoError(err, "UpdateBalance")

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().Equal(12.5, got.Balance)
}

func (s *UserRepoSuite) TestUpdateBalance_Negative() {
	user := mustCreateUser(s.T(), s.db, &userModel{Email: "balneg@test.com", Balance: 10})

	err := s.repo.UpdateBalance(s.ctx, user.ID, -3)
	s.Require().NoError(err, "UpdateBalance with negative")

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().Equal(7.0, got.Balance)
}

func (s *UserRepoSuite) TestDeductBalance() {
	user := mustCreateUser(s.T(), s.db, &userModel{Email: "deduct@test.com", Balance: 10})

	err := s.repo.DeductBalance(s.ctx, user.ID, 5)
	s.Require().NoError(err, "DeductBalance")

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().Equal(5.0, got.Balance)
}

func (s *UserRepoSuite) TestDeductBalance_InsufficientFunds() {
	user := mustCreateUser(s.T(), s.db, &userModel{Email: "insuf@test.com", Balance: 5})

	err := s.repo.DeductBalance(s.ctx, user.ID, 999)
	s.Require().Error(err, "expected error for insufficient balance")
	s.Require().ErrorIs(err, service.ErrInsufficientBalance)
}

func (s *UserRepoSuite) TestDeductBalance_ExactAmount() {
	user := mustCreateUser(s.T(), s.db, &userModel{Email: "exact@test.com", Balance: 10})

	err := s.repo.DeductBalance(s.ctx, user.ID, 10)
	s.Require().NoError(err, "DeductBalance exact amount")

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().Zero(got.Balance)
}

// --- Concurrency ---

func (s *UserRepoSuite) TestUpdateConcurrency() {
	user := mustCreateUser(s.T(), s.db, &userModel{Email: "conc@test.com", Concurrency: 5})

	err := s.repo.UpdateConcurrency(s.ctx, user.ID, 3)
	s.Require().NoError(err, "UpdateConcurrency")

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().Equal(8, got.Concurrency)
}

func (s *UserRepoSuite) TestUpdateConcurrency_Negative() {
	user := mustCreateUser(s.T(), s.db, &userModel{Email: "concneg@test.com", Concurrency: 5})

	err := s.repo.UpdateConcurrency(s.ctx, user.ID, -2)
	s.Require().NoError(err, "UpdateConcurrency negative")

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().Equal(3, got.Concurrency)
}

// --- ExistsByEmail ---

func (s *UserRepoSuite) TestExistsByEmail() {
	mustCreateUser(s.T(), s.db, &userModel{Email: "exists@test.com"})

	exists, err := s.repo.ExistsByEmail(s.ctx, "exists@test.com")
	s.Require().NoError(err, "ExistsByEmail")
	s.Require().True(exists)

	notExists, err := s.repo.ExistsByEmail(s.ctx, "notexists@test.com")
	s.Require().NoError(err)
	s.Require().False(notExists)
}

// --- RemoveGroupFromAllowedGroups ---

func (s *UserRepoSuite) TestRemoveGroupFromAllowedGroups() {
	groupID := int64(42)
	userA := mustCreateUser(s.T(), s.db, &userModel{
		Email:         "a1@example.com",
		AllowedGroups: pq.Int64Array{groupID, 7},
	})
	mustCreateUser(s.T(), s.db, &userModel{
		Email:         "a2@example.com",
		AllowedGroups: pq.Int64Array{7},
	})

	affected, err := s.repo.RemoveGroupFromAllowedGroups(s.ctx, groupID)
	s.Require().NoError(err, "RemoveGroupFromAllowedGroups")
	s.Require().Equal(int64(1), affected, "expected 1 affected row")

	got, err := s.repo.GetByID(s.ctx, userA.ID)
	s.Require().NoError(err, "GetByID")
	for _, id := range got.AllowedGroups {
		s.Require().NotEqual(groupID, id, "expected groupID to be removed from allowed_groups")
	}
}

func (s *UserRepoSuite) TestRemoveGroupFromAllowedGroups_NoMatch() {
	mustCreateUser(s.T(), s.db, &userModel{
		Email:         "nomatch@test.com",
		AllowedGroups: pq.Int64Array{1, 2, 3},
	})

	affected, err := s.repo.RemoveGroupFromAllowedGroups(s.ctx, 999)
	s.Require().NoError(err)
	s.Require().Zero(affected, "expected no affected rows")
}

// --- GetFirstAdmin ---

func (s *UserRepoSuite) TestGetFirstAdmin() {
	admin1 := mustCreateUser(s.T(), s.db, &userModel{
		Email:  "admin1@example.com",
		Role:   service.RoleAdmin,
		Status: service.StatusActive,
	})
	mustCreateUser(s.T(), s.db, &userModel{
		Email:  "admin2@example.com",
		Role:   service.RoleAdmin,
		Status: service.StatusActive,
	})

	got, err := s.repo.GetFirstAdmin(s.ctx)
	s.Require().NoError(err, "GetFirstAdmin")
	s.Require().Equal(admin1.ID, got.ID, "GetFirstAdmin mismatch")
}

func (s *UserRepoSuite) TestGetFirstAdmin_NoAdmin() {
	mustCreateUser(s.T(), s.db, &userModel{
		Email:  "user@example.com",
		Role:   service.RoleUser,
		Status: service.StatusActive,
	})

	_, err := s.repo.GetFirstAdmin(s.ctx)
	s.Require().Error(err, "expected error when no admin exists")
}

func (s *UserRepoSuite) TestGetFirstAdmin_DisabledAdminIgnored() {
	mustCreateUser(s.T(), s.db, &userModel{
		Email:  "disabled@example.com",
		Role:   service.RoleAdmin,
		Status: service.StatusDisabled,
	})
	activeAdmin := mustCreateUser(s.T(), s.db, &userModel{
		Email:  "active@example.com",
		Role:   service.RoleAdmin,
		Status: service.StatusActive,
	})

	got, err := s.repo.GetFirstAdmin(s.ctx)
	s.Require().NoError(err, "GetFirstAdmin")
	s.Require().Equal(activeAdmin.ID, got.ID, "should return only active admin")
}

// --- Combined original test ---

func (s *UserRepoSuite) TestCRUD_And_Filters_And_AtomicUpdates() {
	user1 := mustCreateUser(s.T(), s.db, &userModel{
		Email:    "a@example.com",
		Username: "Alice",
		Wechat:   "wx_a",
		Role:     service.RoleUser,
		Status:   service.StatusActive,
		Balance:  10,
	})
	user2 := mustCreateUser(s.T(), s.db, &userModel{
		Email:    "b@example.com",
		Username: "Bob",
		Wechat:   "wx_b",
		Role:     service.RoleAdmin,
		Status:   service.StatusActive,
		Balance:  1,
	})
	_ = mustCreateUser(s.T(), s.db, &userModel{
		Email:  "c@example.com",
		Role:   service.RoleAdmin,
		Status: service.StatusDisabled,
	})

	got, err := s.repo.GetByID(s.ctx, user1.ID)
	s.Require().NoError(err, "GetByID")
	s.Require().Equal(user1.Email, got.Email, "GetByID email mismatch")

	gotByEmail, err := s.repo.GetByEmail(s.ctx, user2.Email)
	s.Require().NoError(err, "GetByEmail")
	s.Require().Equal(user2.ID, gotByEmail.ID, "GetByEmail ID mismatch")

	got.Username = "Alice2"
	s.Require().NoError(s.repo.Update(s.ctx, got), "Update")
	got2, err := s.repo.GetByID(s.ctx, user1.ID)
	s.Require().NoError(err, "GetByID after update")
	s.Require().Equal("Alice2", got2.Username, "Update did not persist")

	s.Require().NoError(s.repo.UpdateBalance(s.ctx, user1.ID, 2.5), "UpdateBalance")
	got3, err := s.repo.GetByID(s.ctx, user1.ID)
	s.Require().NoError(err, "GetByID after UpdateBalance")
	s.Require().Equal(12.5, got3.Balance, "UpdateBalance mismatch")

	s.Require().NoError(s.repo.DeductBalance(s.ctx, user1.ID, 5), "DeductBalance")
	got4, err := s.repo.GetByID(s.ctx, user1.ID)
	s.Require().NoError(err, "GetByID after DeductBalance")
	s.Require().Equal(7.5, got4.Balance, "DeductBalance mismatch")

	err = s.repo.DeductBalance(s.ctx, user1.ID, 999)
	s.Require().Error(err, "DeductBalance expected error for insufficient balance")
	s.Require().ErrorIs(err, service.ErrInsufficientBalance, "DeductBalance unexpected error")

	s.Require().NoError(s.repo.UpdateConcurrency(s.ctx, user1.ID, 3), "UpdateConcurrency")
	got5, err := s.repo.GetByID(s.ctx, user1.ID)
	s.Require().NoError(err, "GetByID after UpdateConcurrency")
	s.Require().Equal(user1.Concurrency+3, got5.Concurrency, "UpdateConcurrency mismatch")

	params := pagination.PaginationParams{Page: 1, PageSize: 10}
	users, page, err := s.repo.ListWithFilters(s.ctx, params, service.StatusActive, service.RoleAdmin, "b@")
	s.Require().NoError(err, "ListWithFilters")
	s.Require().Equal(int64(1), page.Total, "ListWithFilters total mismatch")
	s.Require().Len(users, 1, "ListWithFilters len mismatch")
	s.Require().Equal(user2.ID, users[0].ID, "ListWithFilters result mismatch")
}
