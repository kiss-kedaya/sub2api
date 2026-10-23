package notify

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/notify/bark"
	"github.com/stretchr/testify/require"
)

func TestBarkSettingsNotifyOriginalDeviceBeforeReplacingDisablingOrClearing(t *testing.T) {
	for _, name := range []string{"replace", "disable", "enable", "clear", "replace disabled device", "first setup", "delivery failed"} {
		t.Run(name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			svc := NewUserBarkService(db, passthroughEncryptor{})
			key, oldKey, enabled, wasEnabled := "new-fixture-device", "original-fixture-device", true, true
			submitted := &key
			nextEncrypted := "enc:" + key
			switch name {
			case "disable":
				submitted = nil
				enabled = false
				nextEncrypted = oldKey
			case "enable":
				submitted = nil
				wasEnabled = false
				nextEncrypted = oldKey
			case "clear":
				key = ""
				nextEncrypted = ""
				enabled = false
			case "replace disabled device":
				wasEnabled = false
			case "first setup":
				oldKey = ""
				wasEnabled = false
			}
			mock.ExpectBegin()
			mock.ExpectQuery(`(?s)SELECT bark_device_key_encrypted,bark_notify_enabled FROM users.*FOR UPDATE`).WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"key", "enabled"}).AddRow(oldKey, wasEnabled))
			sent := &securityBarkSender{}
			svc.newSender = func(actualKey string) bark.Sender {
				require.Equal(t, "original-fixture-device", actualKey, "only the pre-update recipient can receive a change alert")
				return sent
			}
			if name == "delivery failed" {
				sent.err = errors.New("offline device")
				mock.ExpectRollback()
			} else {
				mock.ExpectExec(`UPDATE users SET bark_device_key_encrypted`).WithArgs(nextEncrypted, enabled, int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			err = svc.UpdateSettings(context.Background(), 1, submitted, enabled)
			if name == "delivery failed" {
				require.ErrorIs(t, err, ErrBarkOriginalNotificationFailed)
			} else {
				require.NoError(t, err)
			}
			if name == "first setup" {
				require.Zero(t, sent.calls)
			} else {
				require.Equal(t, 1, sent.calls)
				require.NotContains(t, sent.last.Body, "new-fixture-device")
				require.NotContains(t, sent.last.Body, "original-fixture-device")
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestWalletOperationNotificationIsOptionalUntilConfiguredAndEnabled(t *testing.T) {
	for _, tt := range []struct {
		key     string
		enabled bool
		want    bool
	}{{"", false, false}, {"", true, false}, {"fixture", false, false}, {"fixture", true, true}} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		mock.ExpectQuery(`SELECT bark_device_key_encrypted, bark_notify_enabled FROM users WHERE id = \$1`).WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"key", "enabled"}).AddRow(tt.key, tt.enabled))
		got, err := NewOwnerPushAdapter(NewUserBarkService(db, passthroughEncryptor{}), 1).WalletOperationNotificationsEnabled(context.Background())
		require.NoError(t, err)
		require.Equal(t, tt.want, got)
		require.NoError(t, mock.ExpectationsWereMet())
		mock.ExpectClose()
		require.NoError(t, db.Close())
	}
}
