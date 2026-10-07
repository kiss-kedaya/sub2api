package service

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

type ownerScopeAdminRepo struct {
	AccountRepository
	accounts                          map[int64]*Account
	bulkCalls, bindCalls, shadowCalls int
}

func (r *ownerScopeAdminRepo) GetByID(ctx context.Context, id int64) (*Account, error) {
	a := r.accounts[id]
	adminID, all, scoped := AccountOwnerScopeDetail(ctx)
	if a == nil || (scoped && !AccountVisibleToOwner(a.CreatedBy, adminID, all)) {
		return nil, ErrAccountNotFound
	}
	return a, nil
}

func (r *ownerScopeAdminRepo) GetByIDs(ctx context.Context, ids []int64) ([]*Account, error) {
	var out []*Account
	for _, id := range ids {
		if a, err := r.GetByID(ctx, id); err == nil {
			out = append(out, a)
		}
	}
	return out, nil
}

func (r *ownerScopeAdminRepo) BulkUpdate(context.Context, []int64, AccountBulkUpdate) (int64, error) {
	r.bulkCalls++
	return 1, nil
}

func (r *ownerScopeAdminRepo) BindGroups(context.Context, int64, []int64) error {
	r.bindCalls++
	return nil
}

func (r *ownerScopeAdminRepo) ListShadowsByParent(context.Context, int64) ([]*Account, error) {
	r.shadowCalls++
	return nil, nil
}

func newOwnerScopeAdminRepo() *ownerScopeAdminRepo {
	owner, other := int64(7), int64(8)
	return &ownerScopeAdminRepo{accounts: map[int64]*Account{
		11: {ID: 11, CreatedBy: &owner},
		12: {ID: 12, CreatedBy: &other},
		13: {ID: 13},
	}}
}

func TestAdminBulkUpdateRejectsInaccessibleTargetBeforeEveryWrite(t *testing.T) {
	for _, inaccessible := range []int64{12, 13, 404} {
		for _, field := range []string{"priority", "status", "credentials", "clear groups", "proxy"} {
			t.Run(field+"/"+strconv.FormatInt(inaccessible, 10), func(t *testing.T) {
				repo := newOwnerScopeAdminRepo()
				input := &BulkUpdateAccountsInput{AccountIDs: []int64{11, inaccessible}, SkipMixedChannelCheck: true}
				priority, proxyID, groups := 3, int64(0), []int64{}
				switch field {
				case "priority":
					input.Priority = &priority
				case "status":
					input.Status = StatusDisabled
				case "credentials":
					input.Credentials = map[string]any{"api_key": "new-test-value"}
				case "clear groups":
					input.GroupIDs = &groups
				case "proxy":
					input.ProxyID = &proxyID
				}
				result, err := (&adminServiceImpl{accountRepo: repo}).BulkUpdateAccounts(WithAccountOwnerScope(context.Background(), 7, 0), input)
				require.ErrorIs(t, err, ErrAccountNotFound)
				require.Nil(t, result)
				require.Zero(t, repo.bulkCalls)
				require.Zero(t, repo.bindCalls)
				require.Zero(t, repo.shadowCalls)
			})
		}
	}
}

func TestAdminBulkUpdateOwnerAndOrdinaryOwnUploadRemainAllowed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ownerID int64
		ids     []int64
	}{
		{"own uploads", 0, []int64{11, 11}},
		{"pool owner all uploads", 7, []int64{11, 12, 13}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newOwnerScopeAdminRepo()
			result, err := (&adminServiceImpl{accountRepo: repo}).BulkUpdateAccounts(WithAccountOwnerScope(context.Background(), 7, tc.ownerID), &BulkUpdateAccountsInput{AccountIDs: tc.ids, Name: "rename"})
			require.NoError(t, err)
			require.Equal(t, len(tc.ids), result.Success)
			require.Equal(t, 1, repo.bulkCalls)
		})
	}
}

func TestAdminDirectAccountOperationsRejectOtherAndLegacyBeforeSideEffects(t *testing.T) {
	for _, inaccessible := range []int64{12, 13, 404} {
		for _, tc := range []struct {
			name string
			call func(*adminServiceImpl, context.Context, int64) error
		}{
			{"delete", func(s *adminServiceImpl, ctx context.Context, id int64) error { return s.DeleteAccount(ctx, id) }},
			{"clear error", func(s *adminServiceImpl, ctx context.Context, id int64) error {
				_, err := s.ClearAccountError(ctx, id)
				return err
			}},
			{"set error", func(s *adminServiceImpl, ctx context.Context, id int64) error {
				return s.SetAccountError(ctx, id, "test")
			}},
			{"schedulable", func(s *adminServiceImpl, ctx context.Context, id int64) error {
				_, err := s.SetAccountSchedulable(ctx, id, true)
				return err
			}},
			{"revert proxy", func(s *adminServiceImpl, ctx context.Context, id int64) error {
				return s.RevertAccountProxyFallback(ctx, id)
			}},
			{"extra", func(s *adminServiceImpl, ctx context.Context, id int64) error {
				return s.UpdateAccountExtra(ctx, id, map[string]any{"label": "test"})
			}},
			{"duplicate", func(s *adminServiceImpl, ctx context.Context, id int64) error {
				_, err := s.DuplicateAccount(ctx, id, "admin:7", "same-key")
				return err
			}},
			{"recover duplicate", func(s *adminServiceImpl, ctx context.Context, id int64) error {
				_, err := s.RecoverDuplicateAccount(ctx, id, "admin:7", "same-key")
				return err
			}},
		} {
			t.Run(tc.name+"/"+strconv.FormatInt(inaccessible, 10), func(t *testing.T) {
				repo := newOwnerScopeAdminRepo()
				// The embedded repository is deliberately nil: reaching any direct
				// mutation (including plans/cache operations in Delete) fails the test.
				err := tc.call(&adminServiceImpl{accountRepo: repo}, WithAccountOwnerScope(context.Background(), 7, 0), inaccessible)
				require.ErrorIs(t, err, ErrAccountNotFound)
				require.Zero(t, repo.shadowCalls)
				require.Zero(t, repo.bulkCalls)
			})
		}
	}
}
