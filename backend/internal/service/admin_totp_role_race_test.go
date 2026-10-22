//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

type totpDisableRoleRaceRepo struct {
	UserRepository
	user           User
	currentRole    string
	legacyCalls    int
	guardedCalls   int
	guardedRole    string
	disableApplied bool
}

type adminRoleWriteGuardRepo struct {
	*roleGuardUserRepoStub
	currentRole string
	lastFields  UserUpdateFields
	updateCalls int
}

func (r *adminRoleWriteGuardRepo) Update(_ context.Context, user *User, fields UserUpdateFields) error {
	r.updateCalls++
	r.lastFields = fields
	if fields.ExpectedRole != "" && fields.ExpectedRole != r.currentRole {
		return ErrUserSecurityStateChanged
	}
	if fields.Role {
		r.currentRole = user.Role
	}
	return nil
}

func TestAdminService_UpdateUser_DemotionCannotBypassTotpOrConcurrentRoleGuard(t *testing.T) {
	for _, tc := range []struct {
		name, readRole, currentRole, requestedRole string
		approved                                   bool
		wantErr                                    error
		wantStepUp                                 bool
	}{
		{name: "admin_without_actor_approval", readRole: RoleAdmin, currentRole: RoleAdmin, requestedRole: RoleUser, wantStepUp: true},
		{name: "handler_saw_user_service_saw_admin", readRole: RoleAdmin, currentRole: RoleAdmin, requestedRole: RoleUser, wantStepUp: true},
		{name: "promotion_after_service_read", readRole: RoleUser, currentRole: RoleAdmin, requestedRole: RoleUser, wantErr: ErrUserSecurityStateChanged},
		{name: "approved_admin_demotion", readRole: RoleAdmin, currentRole: RoleAdmin, requestedRole: RoleUser, approved: true},
		{name: "regular_user_same_role", readRole: RoleUser, currentRole: RoleUser, requestedRole: RoleUser},
		{name: "omitted_role_keeps_admin", readRole: RoleAdmin, currentRole: RoleAdmin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &adminRoleWriteGuardRepo{
				roleGuardUserRepoStub: &roleGuardUserRepoStub{
					rpmUserRepoStub: &rpmUserRepoStub{userRepoStub: &userRepoStub{user: &User{ID: 42, Role: tc.readRole, Status: StatusActive}}},
					adminTotal:      2,
				},
				currentRole: tc.currentRole,
			}
			svc := &adminServiceImpl{userRepo: repo, redeemCodeRepo: &redeemRepoStub{}}
			_, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{Role: tc.requestedRole, TotpDemotionApproved: tc.approved})
			if tc.wantStepUp {
				require.Error(t, err)
				require.Contains(t, err.Error(), "recent two-factor verification")
				require.Zero(t, repo.updateCalls)
				return
			}
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Equal(t, RoleAdmin, repo.currentRole)
			} else {
				require.NoError(t, err)
			}
			if tc.requestedRole != "" {
				require.Equal(t, tc.readRole, repo.lastFields.ExpectedRole)
			}
		})
	}
}

func TestUpdateUserInputDemotionApprovalCannotBeBoundFromJSON(t *testing.T) {
	var input UpdateUserInput
	require.NoError(t, json.Unmarshal([]byte(`{"Role":"user","TotpDemotionApproved":true,"totp_demotion_approved":true}`), &input))
	require.Equal(t, RoleUser, input.Role)
	require.False(t, input.TotpDemotionApproved)
}

func (r *totpDisableRoleRaceRepo) GetByID(context.Context, int64) (*User, error) {
	snapshot := r.user
	return &snapshot, nil
}

func (r *totpDisableRoleRaceRepo) DisableTotp(context.Context, int64) error {
	r.legacyCalls++
	return nil
}

func (r *totpDisableRoleRaceRepo) DisableTotpWithExpectedRole(_ context.Context, _ int64, role string, _ *string) error {
	r.guardedCalls++
	r.guardedRole = role
	if r.currentRole != role {
		return ErrUserSecurityStateChanged
	}
	r.disableApplied = true
	return nil
}

func TestTotpDisablePasswordCannotRaceAdminPromotion(t *testing.T) {
	for _, promoted := range []bool{false, true} {
		t.Run(map[bool]string{false: "regular_user", true: "promoted_after_read"}[promoted], func(t *testing.T) {
			repo := &totpDisableRoleRaceRepo{user: User{ID: 42, Role: RoleUser, Status: StatusActive, TotpEnabled: true}, currentRole: RoleUser}
			require.NoError(t, repo.user.SetPassword("correct-password"))
			if promoted {
				repo.currentRole = RoleAdmin
			}
			settings := NewSettingService(&totpVMSettingRepoStub{values: map[string]string{}}, nil)
			svc := NewTotpService(repo, nil, nil, settings, nil, nil)
			err := svc.Disable(context.Background(), 42, "", "correct-password")
			if promoted {
				require.ErrorIs(t, err, ErrUserSecurityStateChanged)
				require.False(t, repo.disableApplied)
			} else {
				require.NoError(t, err)
				require.True(t, repo.disableApplied)
			}
			require.Equal(t, 1, repo.guardedCalls)
			require.Equal(t, RoleUser, repo.guardedRole)
			require.Zero(t, repo.legacyCalls)
		})
	}
}
