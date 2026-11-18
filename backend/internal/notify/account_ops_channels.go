package notify

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// AccountOpsAdminChannels resolves only the primary administrator; it never
// falls back to an observer or copies profile credentials into settings.
type AccountOpsAdminChannels struct {
	users service.UserRepository
	bark  *UserBarkService
	owner *OwnerPushAdapter
}

func NewAccountOpsAdminChannels(users service.UserRepository, bark *UserBarkService) *AccountOpsAdminChannels {
	return &AccountOpsAdminChannels{users: users, bark: bark, owner: NewOwnerPushAdapter(bark, 1)}
}

func (a *AccountOpsAdminChannels) PrimaryAdmin(ctx context.Context) (*service.User, error) {
	return a.users.GetByID(ctx, 1)
}

func (a *AccountOpsAdminChannels) BarkStatus(ctx context.Context) (bool, bool, error) {
	if a.bark == nil {
		return false, false, ErrBarkNotConfigured
	}
	return a.bark.Status(ctx, 1)
}

func (a *AccountOpsAdminChannels) PushToAdmins(ctx context.Context, title, body, group string) error {
	return a.owner.PushToAdmins(ctx, title, body, group)
}
