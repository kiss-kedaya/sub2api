package notify

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/notify/bark"
	"github.com/stretchr/testify/require"
)

type securityBarkSender struct {
	calls int
	err   error
	last  bark.Notification
}

func (s *securityBarkSender) Send(_ context.Context, n bark.Notification) error {
	s.calls++
	s.last = n
	return s.err
}

func TestWalletOwnerNotificationsRequireActualRecipientAndDelivery(t *testing.T) {
	for _, tt := range []struct {
		name, key                            string
		enabled, missing, offline, wantError bool
	}{
		{name: "not configured", enabled: true, wantError: true},
		{name: "disabled", key: "fixture-key", wantError: true},
		{name: "deleted or non-admin", missing: true, wantError: true},
		{name: "transport error", key: "fixture-key", enabled: true, offline: true, wantError: true},
		{name: "delivered", key: "fixture-key", enabled: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { require.NoError(t, db.Close()) }()
			rows := sqlmock.NewRows([]string{"bark_device_key_encrypted", "bark_notify_enabled"})
			if !tt.missing {
				rows.AddRow(tt.key, tt.enabled)
			}
			mock.ExpectQuery(`(?s)SELECT bark_device_key_encrypted, bark_notify_enabled FROM users.*WHERE id = \$1 AND role = 'admin' AND status = 'active' AND deleted_at IS NULL`).WithArgs(int64(1)).WillReturnRows(rows)
			svc := NewUserBarkService(db, passthroughEncryptor{})
			sender := &securityBarkSender{}
			if tt.offline {
				sender.err = errors.New("offline")
			}
			svc.newSender = func(key string) bark.Sender { require.Equal(t, tt.key, key); return sender }
			err = NewOwnerPushAdapter(svc, 1).PushToAdmins(context.Background(), "wallet alert", "fixed operation metadata", "crypto-security")
			if tt.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if tt.key != "" && tt.enabled {
				require.Equal(t, 1, sender.calls)
			} else {
				require.Zero(t, sender.calls)
			}
			require.NoError(t, mock.ExpectationsWereMet())
			mock.ExpectClose()
		})
	}
}

func TestBarkTogglePreservesCiphertextAndRequiresKeyWhenEnabling(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disable", true: "enable"}[enabled], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			mock.ExpectExec(`(?s)UPDATE users SET bark_notify_enabled = \$1 WHERE id = \$2.*COALESCE\(bark_device_key_encrypted, ''\) <> ''`).WithArgs(enabled, int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
			require.NoError(t, NewUserBarkService(db, passthroughEncryptor{}).SetEnabled(context.Background(), 1, enabled))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectExec(`UPDATE users SET bark_notify_enabled`).WithArgs(true, int64(1)).WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, NewUserBarkService(db, passthroughEncryptor{}).SetEnabled(context.Background(), 1, true), ErrBarkKeyRequired)
	require.NoError(t, mock.ExpectationsWereMet())
}
