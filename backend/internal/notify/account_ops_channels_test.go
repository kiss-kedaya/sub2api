package notify

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/notify/bark"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type opsOwnerUsers struct {
	service.UserRepository
	ids []int64
}

func (u *opsOwnerUsers) GetByID(_ context.Context, id int64) (*service.User, error) {
	u.ids = append(u.ids, id)
	return &service.User{ID: id, Role: service.RoleAdmin, Status: service.StatusActive}, nil
}

func TestAccountOpsOwnerAdapterUsesCurrentKeyAndOnlyPrimaryAdmin(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	users := &opsOwnerUsers{}
	barkSvc := NewUserBarkService(db, passthroughEncryptor{})
	var keys []string
	sender := &securityBarkSender{}
	barkSvc.newSender = func(key string) bark.Sender { keys = append(keys, key); return sender }
	adapter := NewAccountOpsAdminChannels(users, barkSvc)
	ctx := context.Background()
	_, err = adapter.PrimaryAdmin(ctx)
	require.NoError(t, err)
	require.Equal(t, []int64{1}, users.ids)
	for _, key := range []string{"synthetic-one", "synthetic-two"} {
		mock.ExpectQuery(`(?s)SELECT bark_device_key_encrypted, bark_notify_enabled FROM users.*WHERE id = \$1 AND role = 'admin' AND status = 'active' AND deleted_at IS NULL`).WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"bark_device_key_encrypted", "bark_notify_enabled"}).AddRow(key, true))
		require.NoError(t, adapter.PushToAdmins(ctx, "test", "body", "account-ops"))
	}
	require.Equal(t, []string{"synthetic-one", "synthetic-two"}, keys)
	require.Equal(t, "account-ops", sender.last.Group)
	mock.ExpectQuery(`SELECT bark_device_key_encrypted, bark_notify_enabled FROM users WHERE id = \$1`).WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"bark_device_key_encrypted", "bark_notify_enabled"}).AddRow("", false))
	configured, enabled, err := adapter.BarkStatus(ctx)
	require.NoError(t, err)
	require.False(t, configured)
	require.False(t, enabled)
	require.NoError(t, mock.ExpectationsWereMet())
	mock.ExpectClose()
}
