//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

type balanceUserRepoStub struct {
	*userRepoStub
	adjustErr error
	// changes 记录每次原子余额变更，顺序与调用顺序一致。
	changes []BalanceChange
}

func (s *balanceUserRepoStub) AdjustBalance(ctx context.Context, id int64, delta float64) (BalanceChange, error) {
	return s.apply(func(current float64) float64 { return current + delta })
}

func (s *balanceUserRepoStub) SetBalance(ctx context.Context, id int64, value float64) (BalanceChange, error) {
	return s.apply(func(float64) float64 { return value })
}

func (s *balanceUserRepoStub) apply(next func(current float64) float64) (BalanceChange, error) {
	if s.adjustErr != nil {
		return BalanceChange{}, s.adjustErr
	}
	if s.userRepoStub == nil || s.userRepoStub.user == nil {
		return BalanceChange{}, ErrUserNotFound
	}
	change := BalanceChange{Old: s.userRepoStub.user.Balance}
	change.New = next(change.Old)
	if change.New < 0 {
		return change, ErrBalanceNegative
	}
	s.userRepoStub.user.Balance = change.New
	s.changes = append(s.changes, change)
	return change, nil
}

type balanceRedeemRepoStub struct {
	*redeemRepoStub
	created   []*RedeemCode
	createErr error
}

func (s *balanceRedeemRepoStub) Create(ctx context.Context, code *RedeemCode) error {
	if s.createErr != nil {
		return s.createErr
	}
	if code == nil {
		return nil
	}
	clone := *code
	s.created = append(s.created, &clone)
	return nil
}

type transactionalBalanceUserRepo struct {
	*userRepoStub
	client *dbent.Client
}

func (r *transactionalBalanceUserRepo) clientFor(ctx context.Context) *dbent.Client {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return tx.Client()
	}
	return r.client
}

func (r *transactionalBalanceUserRepo) GetByID(ctx context.Context, id int64) (*User, error) {
	stored, err := r.clientFor(ctx).User.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &User{ID: stored.ID, Email: stored.Email, Username: stored.Username, Balance: stored.Balance}, nil
}

func (r *transactionalBalanceUserRepo) AdjustBalance(ctx context.Context, id int64, delta float64) (BalanceChange, error) {
	client := r.clientFor(ctx)
	current, err := client.User.Get(ctx, id)
	if err != nil {
		return BalanceChange{}, err
	}
	change := BalanceChange{Old: current.Balance, New: current.Balance + delta}
	if change.New < 0 {
		return change, ErrBalanceNegative
	}
	updated, err := client.User.UpdateOneID(id).AddBalance(delta).Save(ctx)
	if err != nil {
		return BalanceChange{}, err
	}
	change.New = updated.Balance
	return change, nil
}

func (r *transactionalBalanceUserRepo) SetBalance(ctx context.Context, id int64, value float64) (BalanceChange, error) {
	client := r.clientFor(ctx)
	current, err := client.User.Get(ctx, id)
	if err != nil {
		return BalanceChange{}, err
	}
	change := BalanceChange{Old: current.Balance, New: value}
	if value < 0 {
		return change, ErrBalanceNegative
	}
	updated, err := client.User.UpdateOneID(id).SetBalance(value).Save(ctx)
	if err != nil {
		return BalanceChange{}, err
	}
	change.New = updated.Balance
	return change, nil
}

func (r *transactionalBalanceUserRepo) UpdateBalance(ctx context.Context, id int64, amount float64) error {
	_, err := r.clientFor(ctx).User.UpdateOneID(id).AddBalance(amount).Save(ctx)
	return err
}

type transactionalBalanceRedeemRepo struct {
	*redeemRepoStub
	client *dbent.Client
	fail   error
}

func (r *transactionalBalanceRedeemRepo) Create(ctx context.Context, code *RedeemCode) error {
	if r.fail != nil {
		return r.fail
	}
	client := r.client
	if tx := dbent.TxFromContext(ctx); tx != nil {
		client = tx.Client()
	}
	_, err := client.RedeemCode.Create().
		SetCode(code.Code).
		SetType(code.Type).
		SetValue(code.Value).
		SetStatus(code.Status).
		SetNotes(code.Notes).
		SetValidityDays(code.ValidityDays).
		SetNillableExpiresAt(code.ExpiresAt).
		SetNillableUsedBy(code.UsedBy).
		SetNillableUsedAt(code.UsedAt).
		SetNillableGroupID(code.GroupID).
		Save(ctx)
	return err
}

func newTransactionalBalanceUser(t *testing.T, client *dbent.Client, balance float64) *User {
	t.Helper()
	stored, err := client.User.Create().
		SetEmail("balance-provenance@example.test").
		SetPasswordHash("test-hash").
		SetRole(RoleUser).
		SetStatus(StatusActive).
		SetBalance(balance).
		SetConcurrency(1).
		Save(context.Background())
	require.NoError(t, err)
	return &User{ID: stored.ID, Email: stored.Email, Username: stored.Username, Balance: stored.Balance}
}

type authCacheInvalidatorStub struct {
	userIDs  []int64
	groupIDs []int64
	keys     []string
}

type adminRechargeAffiliateAccruerStub struct {
	calls  []adminRechargeAffiliateAccrual
	rebate float64
	err    error
}

type adminRechargeAffiliateAccrual struct {
	userID int64
	amount float64
}

func (s *adminRechargeAffiliateAccruerStub) AccrueInviteRebate(_ context.Context, userID int64, amount float64) (float64, error) {
	s.calls = append(s.calls, adminRechargeAffiliateAccrual{userID: userID, amount: amount})
	return s.rebate, s.err
}

func adminRechargeSettingService(enabled bool) *SettingService {
	values := map[string]string{}
	if enabled {
		values[SettingKeyAffiliateAdminRechargeEnabled] = "true"
	}
	return NewSettingService(&settingRepoStub{values: values}, nil)
}

func (s *authCacheInvalidatorStub) InvalidateAuthCacheByKey(ctx context.Context, key string) {
	s.keys = append(s.keys, key)
}

func (s *authCacheInvalidatorStub) InvalidateAuthCacheByUserID(ctx context.Context, userID int64) {
	s.userIDs = append(s.userIDs, userID)
}

func (s *authCacheInvalidatorStub) InvalidateAuthCacheByGroupID(ctx context.Context, groupID int64) {
	s.groupIDs = append(s.groupIDs, groupID)
}

// 管理员调账必须走原子的 AdjustBalance/SetBalance，而不是"读余额→算新值→整行写回"，
// 后者会把并发的计费扣款覆盖掉。userRepoStub.Update 对未预期的调用会 panic，
// 因此这里同时证明它没被走到。
func TestAdminService_UpdateUserBalance_UsesAtomicPrimitives(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		amount    float64
		want      BalanceChange
	}{
		{name: "add", operation: "add", amount: 5, want: BalanceChange{Old: 10, New: 15}},
		{name: "subtract", operation: "subtract", amount: 4, want: BalanceChange{Old: 10, New: 6}},
		{name: "set", operation: "set", amount: 2, want: BalanceChange{Old: 10, New: 2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &balanceUserRepoStub{userRepoStub: &userRepoStub{user: &User{ID: 7, Balance: 10}}}
			svc := &adminServiceImpl{
				userRepo:       repo,
				redeemCodeRepo: &balanceRedeemRepoStub{redeemRepoStub: &redeemRepoStub{}},
				entClient:      newAdminServiceAuthIdentityBindingTestClient(t),
			}

			user, err := svc.UpdateUserBalance(context.Background(), 7, tt.amount, tt.operation, "")
			require.NoError(t, err)
			require.Equal(t, []BalanceChange{tt.want}, repo.changes)
			require.Equal(t, tt.want.New, user.Balance)
		})
	}
}

func TestAdminService_UpdateUserBalance_RejectsNegativeResult(t *testing.T) {
	repo := &balanceUserRepoStub{userRepoStub: &userRepoStub{user: &User{ID: 7, Balance: 3}}}
	svc := &adminServiceImpl{
		userRepo:       repo,
		redeemCodeRepo: &balanceRedeemRepoStub{redeemRepoStub: &redeemRepoStub{}},
		entClient:      newAdminServiceAuthIdentityBindingTestClient(t),
	}

	_, err := svc.UpdateUserBalance(context.Background(), 7, 4, "subtract", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "balance cannot be negative")
	require.Empty(t, repo.changes, "refused adjustment must not be applied")
	require.Equal(t, 3.0, repo.userRepoStub.user.Balance)
}

func TestAdminService_UpdateUserBalance_RejectsUnknownOperation(t *testing.T) {
	repo := &balanceUserRepoStub{userRepoStub: &userRepoStub{user: &User{ID: 7, Balance: 10}}}
	svc := &adminServiceImpl{
		userRepo:       repo,
		redeemCodeRepo: &balanceRedeemRepoStub{redeemRepoStub: &redeemRepoStub{}},
		entClient:      newAdminServiceAuthIdentityBindingTestClient(t),
	}

	_, err := svc.UpdateUserBalance(context.Background(), 7, 1, "multiply", "")
	require.Error(t, err)
	require.Empty(t, repo.changes)
}

func TestAdminService_UpdateUserBalance_InvalidatesAuthCache(t *testing.T) {
	baseRepo := &userRepoStub{user: &User{ID: 7, Balance: 10}}
	repo := &balanceUserRepoStub{userRepoStub: baseRepo}
	redeemRepo := &balanceRedeemRepoStub{redeemRepoStub: &redeemRepoStub{}}
	invalidator := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{
		userRepo:             repo,
		redeemCodeRepo:       redeemRepo,
		authCacheInvalidator: invalidator,
		entClient:            newAdminServiceAuthIdentityBindingTestClient(t),
	}

	_, err := svc.UpdateUserBalance(context.Background(), 7, 5, "add", "")
	require.NoError(t, err)
	require.Equal(t, []int64{7}, invalidator.userIDs)
	require.Len(t, redeemRepo.created, 1)
}

func TestAdminService_UpdateUserBalance_NoChangeNoInvalidate(t *testing.T) {
	baseRepo := &userRepoStub{user: &User{ID: 7, Balance: 10}}
	repo := &balanceUserRepoStub{userRepoStub: baseRepo}
	redeemRepo := &balanceRedeemRepoStub{redeemRepoStub: &redeemRepoStub{}}
	invalidator := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{
		userRepo:             repo,
		redeemCodeRepo:       redeemRepo,
		authCacheInvalidator: invalidator,
		entClient:            newAdminServiceAuthIdentityBindingTestClient(t),
	}

	_, err := svc.UpdateUserBalance(context.Background(), 7, 10, "set", "")
	require.NoError(t, err)
	require.Empty(t, invalidator.userIDs)
	require.Empty(t, redeemRepo.created)
}

func TestAdminService_UpdateUserBalance_AdminRechargeAffiliateRebate(t *testing.T) {
	tests := []struct {
		name      string
		enabled   bool
		operation string
		amount    float64
		wantCalls []adminRechargeAffiliateAccrual
	}{
		{
			name:      "disabled by default",
			operation: "add",
			amount:    5,
		},
		{
			name:      "enabled add",
			enabled:   true,
			operation: "add",
			amount:    0.1,
			wantCalls: []adminRechargeAffiliateAccrual{{userID: 7, amount: 0.1}},
		},
		{
			name:      "enabled set increase",
			enabled:   true,
			operation: "set",
			amount:    15,
		},
		{
			name:      "enabled subtract",
			enabled:   true,
			operation: "subtract",
			amount:    5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseRepo := &userRepoStub{user: &User{ID: 7, Balance: 10}}
			repo := &balanceUserRepoStub{userRepoStub: baseRepo}
			redeemRepo := &balanceRedeemRepoStub{redeemRepoStub: &redeemRepoStub{}}
			affiliate := &adminRechargeAffiliateAccruerStub{}
			svc := &adminServiceImpl{
				userRepo:         repo,
				redeemCodeRepo:   redeemRepo,
				settingService:   adminRechargeSettingService(tt.enabled),
				affiliateService: affiliate,
				entClient:        newAdminServiceAuthIdentityBindingTestClient(t),
			}

			_, err := svc.UpdateUserBalance(context.Background(), 7, tt.amount, tt.operation, "")
			require.NoError(t, err)
			require.Equal(t, tt.wantCalls, affiliate.calls)
		})
	}
}

func TestAdminService_UpdateUserBalance_AffiliateFailureDoesNotRollbackRecharge(t *testing.T) {
	baseRepo := &userRepoStub{user: &User{ID: 7, Balance: 10}}
	repo := &balanceUserRepoStub{userRepoStub: baseRepo}
	redeemRepo := &balanceRedeemRepoStub{redeemRepoStub: &redeemRepoStub{}}
	affiliate := &adminRechargeAffiliateAccruerStub{err: errors.New("affiliate unavailable")}
	svc := &adminServiceImpl{
		userRepo:         repo,
		redeemCodeRepo:   redeemRepo,
		settingService:   adminRechargeSettingService(true),
		affiliateService: affiliate,
		entClient:        newAdminServiceAuthIdentityBindingTestClient(t),
	}

	user, err := svc.UpdateUserBalance(context.Background(), 7, 5, "add", "")
	require.NoError(t, err)
	require.Equal(t, 15.0, user.Balance)
	require.Equal(t, []adminRechargeAffiliateAccrual{{userID: 7, amount: 5}}, affiliate.calls)
	require.Len(t, redeemRepo.created, 1)
}

func TestAdminService_UpdateUserBalance_SourceFailureRollsBackBalance(t *testing.T) {
	client := newAdminServiceAuthIdentityBindingTestClient(t)
	stored := newTransactionalBalanceUser(t, client, 10)
	repo := &transactionalBalanceUserRepo{client: client}
	svc := &adminServiceImpl{
		userRepo:       repo,
		redeemCodeRepo: &transactionalBalanceRedeemRepo{redeemRepoStub: &redeemRepoStub{}, client: client, fail: errors.New("source store unavailable")},
		entClient:      client,
	}

	_, err := svc.UpdateUserBalance(context.Background(), stored.ID, 5, "add", "incident adjustment")
	require.ErrorContains(t, err, "create balance adjustment source record")

	current, err := client.User.Get(context.Background(), stored.ID)
	require.NoError(t, err)
	require.Equal(t, 10.0, current.Balance)
	count, err := client.RedeemCode.Query().Count(context.Background())
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestAdminService_UpdateUserBalance_CommitsBalanceWithSource(t *testing.T) {
	client := newAdminServiceAuthIdentityBindingTestClient(t)
	stored := newTransactionalBalanceUser(t, client, 10)
	svc := &adminServiceImpl{
		userRepo:       &transactionalBalanceUserRepo{client: client},
		redeemCodeRepo: &transactionalBalanceRedeemRepo{redeemRepoStub: &redeemRepoStub{}, client: client},
		entClient:      client,
	}

	user, err := svc.UpdateUserBalance(context.Background(), stored.ID, 5, "add", "incident adjustment")
	require.NoError(t, err)
	require.Equal(t, 15.0, user.Balance)

	current, err := client.User.Get(context.Background(), stored.ID)
	require.NoError(t, err)
	require.Equal(t, 15.0, current.Balance)
	code, err := client.RedeemCode.Query().Only(context.Background())
	require.NoError(t, err)
	require.Equal(t, AdjustmentTypeAdminBalance, code.Type)
	require.Equal(t, 5.0, code.Value)
	require.Equal(t, StatusUsed, code.Status)
	require.Equal(t, stored.ID, *code.UsedBy)
	require.NotNil(t, code.Notes)
	require.Equal(t, "incident adjustment", *code.Notes)
}
